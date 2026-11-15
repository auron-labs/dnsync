package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aaronflorey/dnsync/internal/config"
	"github.com/aaronflorey/dnsync/internal/contracts"
	"github.com/aaronflorey/dnsync/internal/state"
)

// tunnelBinding ties the one configured tunnel to the concrete providers that
// own its control plane and connector lifecycle.
type tunnelBinding struct {
	output       contracts.Output
	tunnelOutput contracts.TunnelOutput
	source       contracts.Source
	sidecars     contracts.TunnelSidecarSource
	config       config.TunnelConfig
}

type tunnelPhase struct {
	binding  tunnelBinding
	managed  *state.ManagedTunnel
	routes   []state.ManagedTunnelRoute
	expiring bool
}

func (a *App) configuredTunnelBinding() (*tunnelBinding, error) {
	var configured *config.OutputConfig
	for i := range a.cfg.Outputs {
		output := &a.cfg.Outputs[i]
		if output.IsEnabled() && output.Tunnel != nil {
			configured = output
			break
		}
	}
	if configured == nil {
		return nil, nil
	}

	outputRef := contracts.ProviderRef{Type: configured.Type, Name: configured.Name}
	var output contracts.Output
	for _, candidate := range a.outputs {
		if sameProvider(candidate.Provider(), outputRef) {
			output = candidate
			break
		}
	}
	if output == nil {
		return nil, fmt.Errorf("configured tunnel output %s is unavailable", providerKey(outputRef))
	}
	tunnelOutput, ok := output.(contracts.TunnelOutput)
	if !ok {
		return nil, fmt.Errorf("configured tunnel output %s does not support tunnel control", providerKey(outputRef))
	}

	var source contracts.Source
	for _, candidate := range a.sources {
		ref := candidate.Provider()
		if strings.EqualFold(strings.TrimSpace(ref.Type), "docker") && ref.Name == configured.Tunnel.Source {
			source = candidate
			break
		}
	}
	if source == nil {
		return nil, fmt.Errorf("configured tunnel source %q is unavailable", configured.Tunnel.Source)
	}
	sidecars, ok := unwrapTunnelSidecarSource(source)
	if !ok {
		return nil, fmt.Errorf("configured tunnel source %s does not support sidecar management", providerKey(source.Provider()))
	}

	return &tunnelBinding{
		output:       output,
		tunnelOutput: tunnelOutput,
		source:       source,
		sidecars:     sidecars,
		config:       *configured.Tunnel,
	}, nil
}

func unwrapTunnelSidecarSource(source contracts.Source) (contracts.TunnelSidecarSource, bool) {
	if sidecars, ok := source.(contracts.TunnelSidecarSource); ok {
		return sidecars, true
	}
	if wrapped, ok := source.(timeoutSource); ok {
		return unwrapTunnelSidecarSource(wrapped.base)
	}
	if wrapped, ok := source.(timeoutWatchableSource); ok {
		return unwrapTunnelSidecarSource(wrapped.base)
	}
	return nil, false
}

func (a *App) prepareTunnel(ctx context.Context, owned state.Snapshot, routes []contracts.DesiredTunnelRoute, now time.Time) (state.Snapshot, *tunnelPhase, error) {
	binding, err := a.configuredTunnelBinding()
	if err != nil {
		return owned, nil, err
	}
	if binding == nil {
		if len(routes) != 0 {
			return owned, nil, errors.New("received Cloudflare Tunnel routes without a configured tunnel output")
		}
		return owned, nil, nil
	}

	for _, route := range routes {
		if !sameProvider(route.Source.Provider, binding.source.Provider()) {
			return owned, nil, fmt.Errorf("Cloudflare Tunnel route %q came from non-selected source %s", route.Hostname, providerKey(route.Source.Provider))
		}
	}
	if err := validateTunnelRouteHostnames(routes); err != nil {
		return owned, nil, err
	}

	managed, err := ownedTunnel(owned, binding)
	if err != nil {
		return owned, nil, err
	}
	existingManaged := managed != nil
	routeState, expiring := mergeTunnelRoutes(managed, routes, now, a.deps.RemovalGrace)
	if managed != nil {
		if len(routes) > 0 {
			managed.RemoveAfter = nil
		} else {
			deadline := now.Add(a.deps.RemovalGrace)
			if managed.RemoveAfter == nil || deadline.Before(*managed.RemoveAfter) {
				managed.RemoveAfter = &deadline
			}
		}
	}
	phase := &tunnelPhase{binding: *binding, managed: managed, routes: routeState, expiring: expiring}

	if managed == nil && len(routeState) == 0 {
		return owned, phase, nil
	}
	if managed == nil {
		created, err := binding.tunnelOutput.EnsureTunnel(ctx, binding.config.AccountID, binding.config.Name, "")
		if err != nil {
			return owned, nil, outputMutationError{provider: binding.output.Provider(), err: fmt.Errorf("ensure Cloudflare tunnel: %w", err)}
		}
		managed = &state.ManagedTunnel{
			Output:        binding.output.Provider(),
			Source:        binding.source.Provider(),
			AccountID:     binding.config.AccountID,
			Name:          created.Name,
			TunnelID:      created.ID,
			Image:         binding.config.CloudflaredImage(),
			Routes:        routeState,
			LastAppliedAt: now,
		}
		owned = replaceOwnedTunnel(owned, binding, managed)
		if err := a.store.Save(owned); err != nil {
			return owned, nil, fmt.Errorf("persist created Cloudflare tunnel state: %w", err)
		}
		phase.managed = managed
		logDebug(ctx, a.deps.Logger, "created managed Cloudflare tunnel", "tunnel_id", managed.TunnelID, "output", providerKey(managed.Output))
	} else {
		managed.Routes = routeState
		owned = replaceOwnedTunnel(owned, binding, managed)
		if err := a.store.Save(owned); err != nil {
			return owned, nil, fmt.Errorf("persist Cloudflare tunnel route state: %w", err)
		}
	}

	if phase.expiring && len(activeTunnelRoutes(routeState, now)) == 0 {
		return owned, phase, nil
	}
	// A state-owned tunnel can disappear outside the daemon. Revalidate it on
	// every active or grace-period reconcile before retrieving a token or
	// touching the connector. EnsureTunnel refuses a conflicting same-name
	// tunnel, and creates only when no such conflict exists.
	if existingManaged && phase.managed != nil && phase.managed.TunnelID != "" {
		ensured, err := binding.tunnelOutput.EnsureTunnel(ctx, managed.AccountID, managed.Name, managed.TunnelID)
		if err != nil {
			return owned, nil, outputMutationError{provider: binding.output.Provider(), err: fmt.Errorf("revalidate Cloudflare tunnel: %w", err)}
		}
		if ensured.ID != managed.TunnelID {
			// The old connector is daemon-owned by persisted ID/name. Remove it
			// before changing state so the stable name can never be used to adopt
			// a connector belonging to the old tunnel identity.
			if managed.SidecarID != "" {
				if err := binding.sidecars.DeleteTunnelSidecar(ctx, managed.SidecarID, tunnelSidecarSpec(managed, nil)); err != nil {
					return owned, nil, outputMutationError{provider: binding.output.Provider(), err: fmt.Errorf("remove stale Cloudflare connector: %w", err)}
				}
				managed.SidecarID = ""
				owned = replaceOwnedTunnel(owned, binding, managed)
				if err := a.store.Save(owned); err != nil {
					return owned, nil, fmt.Errorf("persist removed stale Cloudflare connector state: %w", err)
				}
			}
			managed.TunnelID = ensured.ID
			managed.Name = ensured.Name
			owned = replaceOwnedTunnel(owned, binding, managed)
			if err := a.store.Save(owned); err != nil {
				return owned, nil, fmt.Errorf("persist recreated Cloudflare tunnel state: %w", err)
			}
			logDebug(ctx, a.deps.Logger, "recreated managed Cloudflare tunnel", "tunnel_id", managed.TunnelID, "output", providerKey(managed.Output))
		}
	}

	token, err := binding.tunnelOutput.TunnelToken(ctx, managed.AccountID, managed.TunnelID)
	if err != nil {
		return owned, nil, outputMutationError{provider: binding.output.Provider(), err: fmt.Errorf("get Cloudflare tunnel token: %w", err)}
	}
	sidecarID, err := binding.sidecars.EnsureTunnelSidecar(ctx, contracts.TunnelSidecarSpec{
		Output:     managed.Output,
		TunnelID:   managed.TunnelID,
		Name:       managed.Name,
		Image:      managed.Image,
		Token:      token,
		NetworkIDs: tunnelRouteNetworkIDs(routeState),
	})
	if err != nil {
		return owned, nil, outputMutationError{provider: binding.output.Provider(), err: fmt.Errorf("ensure Cloudflare connector: %w", err)}
	}
	if managed.SidecarID != sidecarID {
		managed.SidecarID = sidecarID
		owned = replaceOwnedTunnel(owned, binding, managed)
		if err := a.store.Save(owned); err != nil {
			return owned, nil, fmt.Errorf("persist Cloudflare connector state: %w", err)
		}
	}
	if err := binding.tunnelOutput.ConfigureTunnel(ctx, managed.AccountID, managed.TunnelID, desiredRoutes(routeState)); err != nil {
		return owned, nil, outputMutationError{provider: binding.output.Provider(), err: fmt.Errorf("configure Cloudflare tunnel ingress: %w", err)}
	}
	owned = replaceOwnedTunnel(owned, binding, managed)
	if err := a.store.Save(owned); err != nil {
		return owned, nil, fmt.Errorf("persist Cloudflare tunnel ingress state: %w", err)
	}
	return owned, phase, nil
}

// finishTunnel performs only the mutations that must happen after DNS has
// successfully removed expired CNAMEs: ingress/network first, then connector,
// then the remotely-managed tunnel.
func (a *App) finishTunnel(ctx context.Context, owned state.Snapshot, phase *tunnelPhase, now time.Time) (state.Snapshot, error) {
	if phase == nil || phase.managed == nil || !phase.expiring {
		return owned, nil
	}
	managed := phase.managed
	live := activeTunnelRoutes(phase.routes, now)
	// An empty persisted route set proves ingress cleanup already completed.
	// Skipping the redundant configuration call lets teardown recover when the
	// remote delete succeeded but the following state save failed: the remote is
	// already gone, so configuration would otherwise fail before idempotent
	// deletion can confirm completion.
	if len(phase.routes) > 0 {
		if err := phase.binding.tunnelOutput.ConfigureTunnel(ctx, managed.AccountID, managed.TunnelID, desiredRoutes(live)); err != nil {
			return owned, outputMutationError{provider: managed.Output, err: fmt.Errorf("remove expired Cloudflare tunnel ingress: %w", err)}
		}
		managed.Routes = live
		owned = replaceOwnedTunnel(owned, &phase.binding, managed)
		if err := a.store.Save(owned); err != nil {
			return owned, fmt.Errorf("persist Cloudflare tunnel route cleanup: %w", err)
		}
	}

	if len(live) > 0 {
		token, err := phase.binding.tunnelOutput.TunnelToken(ctx, managed.AccountID, managed.TunnelID)
		if err != nil {
			return owned, outputMutationError{provider: managed.Output, err: fmt.Errorf("get Cloudflare tunnel token for network cleanup: %w", err)}
		}
		sidecarID, err := phase.binding.sidecars.EnsureTunnelSidecar(ctx, contracts.TunnelSidecarSpec{Output: managed.Output, TunnelID: managed.TunnelID, Name: managed.Name, Image: managed.Image, Token: token, NetworkIDs: tunnelRouteNetworkIDs(live)})
		if err != nil {
			return owned, outputMutationError{provider: managed.Output, err: fmt.Errorf("reconcile Cloudflare connector networks: %w", err)}
		}
		if managed.SidecarID != sidecarID {
			managed.SidecarID = sidecarID
			owned = replaceOwnedTunnel(owned, &phase.binding, managed)
			if err := a.store.Save(owned); err != nil {
				return owned, fmt.Errorf("persist Cloudflare connector network state: %w", err)
			}
		}
		return owned, nil
	}

	if managed.SidecarID != "" {
		if err := phase.binding.sidecars.DeleteTunnelSidecar(ctx, managed.SidecarID, tunnelSidecarSpec(managed, nil)); err != nil {
			return owned, outputMutationError{provider: managed.Output, err: fmt.Errorf("remove Cloudflare connector: %w", err)}
		}
	}
	managed.SidecarID = ""
	owned = replaceOwnedTunnel(owned, &phase.binding, managed)
	if err := a.store.Save(owned); err != nil {
		return owned, fmt.Errorf("persist removed Cloudflare connector state: %w", err)
	}
	if err := phase.binding.tunnelOutput.DeleteTunnel(ctx, managed.AccountID, managed.TunnelID); err != nil {
		return owned, outputMutationError{provider: managed.Output, err: fmt.Errorf("delete Cloudflare tunnel: %w", err)}
	}
	owned = removeOwnedTunnel(owned, phase.binding)
	if err := a.store.Save(owned); err != nil {
		return owned, fmt.Errorf("persist removed Cloudflare tunnel state: %w", err)
	}
	return owned, nil
}

func tunnelSidecarSpec(managed *state.ManagedTunnel, networkIDs []string) contracts.TunnelSidecarSpec {
	return contracts.TunnelSidecarSpec{Output: managed.Output, TunnelID: managed.TunnelID, Name: managed.Name, Image: managed.Image, NetworkIDs: networkIDs}
}

func ownedTunnel(owned state.Snapshot, binding *tunnelBinding) (*state.ManagedTunnel, error) {
	var matches []state.ManagedTunnel
	for _, tunnel := range owned.ManagedTunnels {
		if sameProvider(tunnel.Output, binding.output.Provider()) && sameProvider(tunnel.Source, binding.source.Provider()) {
			matches = append(matches, tunnel)
		}
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple managed tunnel snapshots exist for %s", providerKey(binding.output.Provider()))
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if matches[0].Name != binding.config.Name || matches[0].AccountID != binding.config.AccountID || strings.TrimSpace(matches[0].TunnelID) == "" {
		return nil, errors.New("managed tunnel state does not match configured output/source identity")
	}
	result := matches[0]
	if result.Image == "" {
		result.Image = binding.config.CloudflaredImage()
	}
	return &result, nil
}

func mergeTunnelRoutes(managed *state.ManagedTunnel, desired []contracts.DesiredTunnelRoute, now time.Time, grace time.Duration) ([]state.ManagedTunnelRoute, bool) {
	current := make(map[string]state.ManagedTunnelRoute)
	if managed != nil {
		for _, route := range managed.Routes {
			current[normalizeHostname(route.Hostname)] = route
		}
	}
	for _, route := range desired {
		current[normalizeHostname(route.Hostname)] = state.ManagedTunnelRoute{Hostname: route.Hostname, Service: route.Service, Source: route.Source, NetworkIDs: append([]string(nil), route.NetworkIDs...)}
	}
	desiredKeys := make(map[string]struct{}, len(desired))
	for _, route := range desired {
		desiredKeys[normalizeHostname(route.Hostname)] = struct{}{}
	}
	expiring := managed != nil && len(desired) == 0 && len(current) == 0 && managed.RemoveAfter != nil && !managed.RemoveAfter.After(now)
	for key, route := range current {
		if _, ok := desiredKeys[key]; ok {
			continue
		}
		if grace > 0 && (route.RemoveAfter == nil || route.RemoveAfter.After(now)) {
			if route.RemoveAfter == nil {
				deadline := now.Add(grace)
				route.RemoveAfter = &deadline
				current[key] = route
			}
			continue
		}
		expiring = true
	}
	routes := make([]state.ManagedTunnelRoute, 0, len(current))
	for _, route := range current {
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool {
		return normalizeHostname(routes[i].Hostname) < normalizeHostname(routes[j].Hostname)
	})
	return routes, expiring
}

func activeTunnelRoutes(routes []state.ManagedTunnelRoute, now time.Time) []state.ManagedTunnelRoute {
	active := make([]state.ManagedTunnelRoute, 0, len(routes))
	for _, route := range routes {
		if route.RemoveAfter != nil && !route.RemoveAfter.After(now) {
			continue
		}
		active = append(active, route)
	}
	return active
}

func desiredRoutes(routes []state.ManagedTunnelRoute) []contracts.DesiredTunnelRoute {
	result := make([]contracts.DesiredTunnelRoute, 0, len(routes))
	for _, route := range routes {
		result = append(result, contracts.DesiredTunnelRoute{Hostname: route.Hostname, Service: route.Service, Source: route.Source, NetworkIDs: append([]string(nil), route.NetworkIDs...)})
	}
	return result
}

func tunnelRouteNetworkIDs(routes []state.ManagedTunnelRoute) []string {
	set := make(map[string]struct{})
	for _, route := range routes {
		for _, networkID := range route.NetworkIDs {
			if networkID = strings.TrimSpace(networkID); networkID != "" {
				set[networkID] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for networkID := range set {
		result = append(result, networkID)
	}
	sort.Strings(result)
	return result
}

func validateTunnelRouteHostnames(routes []contracts.DesiredTunnelRoute) error {
	seen := make(map[string]contracts.DesiredTunnelRoute)
	for _, route := range routes {
		hostname := normalizeHostname(route.Hostname)
		if existing, ok := seen[hostname]; ok {
			return fmt.Errorf("duplicate Cloudflare Tunnel hostname %q from %s and %s", hostname, existing.Source.ID, route.Source.ID)
		}
		seen[hostname] = route
	}
	return nil
}

func replaceOwnedTunnel(owned state.Snapshot, binding *tunnelBinding, replacement *state.ManagedTunnel) state.Snapshot {
	next := owned
	next.ManagedTunnels = nil
	replaced := false
	for _, tunnel := range owned.ManagedTunnels {
		if sameProvider(tunnel.Output, binding.output.Provider()) && sameProvider(tunnel.Source, binding.source.Provider()) {
			if !replaced {
				next.ManagedTunnels = append(next.ManagedTunnels, *replacement)
				replaced = true
			}
			continue
		}
		next.ManagedTunnels = append(next.ManagedTunnels, tunnel)
	}
	if !replaced {
		next.ManagedTunnels = append(next.ManagedTunnels, *replacement)
	}
	return next
}

func removeOwnedTunnel(owned state.Snapshot, binding tunnelBinding) state.Snapshot {
	next := owned
	next.ManagedTunnels = nil
	for _, tunnel := range owned.ManagedTunnels {
		if sameProvider(tunnel.Output, binding.output.Provider()) && sameProvider(tunnel.Source, binding.source.Provider()) {
			continue
		}
		next.ManagedTunnels = append(next.ManagedTunnels, tunnel)
	}
	return next
}

func sameProvider(left, right contracts.ProviderRef) bool {
	return strings.EqualFold(strings.TrimSpace(left.Type), strings.TrimSpace(right.Type)) && left.Name == right.Name
}

func (a *App) nextRemovalDeadline() (time.Time, error) {
	owned, err := a.store.Load()
	if err != nil {
		return time.Time{}, fmt.Errorf("load state for removal deadline: %w", err)
	}
	var earliest time.Time
	consider := func(deadline *time.Time) {
		if deadline == nil || deadline.IsZero() {
			return
		}
		if earliest.IsZero() || deadline.Before(earliest) {
			earliest = *deadline
		}
	}
	for _, record := range owned.ManagedRecords {
		consider(record.RemoveAfter)
	}
	for _, tunnel := range owned.ManagedTunnels {
		consider(tunnel.RemoveAfter)
		for _, route := range tunnel.Routes {
			consider(route.RemoveAfter)
		}
	}
	return earliest, nil
}

func (a *App) runRemovalDeadlineOnly(ctx context.Context) error {
	for {
		deadline, err := a.nextRemovalDeadline()
		if err != nil {
			return err
		}
		if deadline.IsZero() {
			<-ctx.Done()
			return ctx.Err()
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return ctx.Err()
		case <-timer.C:
			if err := a.reconcile(ctx, "removal_deadline"); err != nil {
				return err
			}
		}
	}
}
