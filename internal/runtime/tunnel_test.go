package runtime

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
	"github.com/auron-labs/dnsync/internal/state"
)

func TestMergeTunnelRoutesAppliesGraceAndCancelsOnReturn(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"}
	managed := &state.ManagedTunnel{Routes: []state.ManagedTunnelRoute{{Hostname: "app.example.test", Service: "http://10.0.0.2:8080", Source: source, NetworkIDs: []string{"network-a"}}}}

	pending, expiring := mergeTunnelRoutes(managed, nil, now, 10*time.Minute)
	if expiring || len(pending) != 1 || pending[0].RemoveAfter == nil {
		t.Fatalf("expected one grace-pending route, got routes=%+v expiring=%v", pending, expiring)
	}
	if want := now.Add(10 * time.Minute); !pending[0].RemoveAfter.Equal(want) {
		t.Fatalf("expected removal deadline %s, got %s", want, pending[0].RemoveAfter)
	}
	if got := activeTunnelRoutes(pending, now); len(got) != 1 {
		t.Fatalf("expected grace-pending route to remain active, got %+v", got)
	}

	reappeared, expiring := mergeTunnelRoutes(&state.ManagedTunnel{Routes: pending}, []contracts.DesiredTunnelRoute{{Hostname: "app.example.test", Service: "http://10.0.0.3:8080", Source: source, NetworkIDs: []string{"network-b"}}}, now.Add(time.Minute), 10*time.Minute)
	if expiring || len(reappeared) != 1 || reappeared[0].RemoveAfter != nil {
		t.Fatalf("expected reappearance to cancel grace, got routes=%+v expiring=%v", reappeared, expiring)
	}
	if reappeared[0].Service != "http://10.0.0.3:8080" || reappeared[0].NetworkIDs[0] != "network-b" {
		t.Fatalf("expected immediate route update, got %+v", reappeared[0])
	}

	_, expiring = mergeTunnelRoutes(&state.ManagedTunnel{Routes: pending}, nil, now.Add(10*time.Minute), 10*time.Minute)
	if !expiring {
		t.Fatal("expected route to expire at its persisted deadline")
	}
}

func TestFilterDesiredForOutputWithTunnelPriority(t *testing.T) {
	t.Parallel()

	selected := &tunnelTestOutput{ref: contracts.ProviderRef{Type: "cloudflare", Name: "primary"}}
	secondary := contracts.ProviderRef{Type: "cloudflare", Name: "secondary"}
	adguard := contracts.ProviderRef{Type: "adguard", Name: "local"}
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"}
	tunnel := &tunnelPhase{binding: tunnelBinding{output: selected}, managed: &state.ManagedTunnel{TunnelID: "tunnel-id"}}
	desired := []contracts.DesiredRecord{
		{Hostname: "app.example.test", Answer: "10.0.0.2", Source: source},
		{Hostname: "app.example.test", Answer: "tunnel-id.cfargotunnel.com", Source: source, Output: "cloudflare", Proxied: true},
	}
	hosts := map[string]struct{}{"app.example.test": {}}

	if got := filterDesiredForOutputWithTunnelPriority(desired, selected.ref, hosts, tunnel); len(got) != 1 || got[0].Answer != "tunnel-id.cfargotunnel.com" || !got[0].Proxied {
		t.Fatalf("expected selected Cloudflare output to receive only proxied tunnel CNAME, got %+v", got)
	}
	if got := filterDesiredForOutputWithTunnelPriority(desired, secondary, hosts, tunnel); len(got) != 0 {
		t.Fatalf("expected secondary Cloudflare output to be suppressed, got %+v", got)
	}
	if got := filterDesiredForOutputWithTunnelPriority(desired, adguard, hosts, tunnel); len(got) != 1 || got[0].Answer != "10.0.0.2" {
		t.Fatalf("expected AdGuard to retain ordinary desired record, got %+v", got)
	}
}

func TestValidateTunnelRouteHostnamesRejectsConflicts(t *testing.T) {
	t.Parallel()

	err := validateTunnelRouteHostnames([]contracts.DesiredTunnelRoute{
		{Hostname: "APP.example.test", Source: contracts.SourceObjectRef{ID: "one"}},
		{Hostname: "app.example.test", Source: contracts.SourceObjectRef{ID: "two"}},
	})
	if err == nil {
		t.Fatal("expected duplicate tunnel hostname to fail closed")
	}
}

func TestPrepareTunnelRevalidatesManagedTunnelAndReplacesConnectorOnIdentityDrift(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 6, 0, 0, 0, time.UTC)
	output := &tunnelLifecycleOutput{ref: contracts.ProviderRef{Type: "cloudflare", Name: "primary"}, ensured: contracts.TunnelInfo{ID: "new-tunnel", Name: "main"}}
	source := &tunnelLifecycleSource{ref: contracts.ProviderRef{Type: "docker", Name: "local"}, sidecarID: "new-sidecar"}
	app := newTunnelLifecycleApp(t, output, source)
	owned := state.EmptySnapshot()
	owned.ManagedTunnels = []state.ManagedTunnel{{Output: output.ref, Source: source.ref, AccountID: "account", Name: "main", TunnelID: "old-tunnel", SidecarID: "old-sidecar", Image: "cloudflared:test", Routes: []state.ManagedTunnelRoute{{Hostname: "app.example.test", Service: "http://10.0.0.2:8080", Source: contracts.SourceObjectRef{Provider: source.ref, ID: "workload"}, NetworkIDs: []string{"net"}}}}}
	routes := []contracts.DesiredTunnelRoute{{Hostname: "app.example.test", Service: "http://10.0.0.2:8080", Source: contracts.SourceObjectRef{Provider: source.ref, ID: "workload"}, NetworkIDs: []string{"net"}}}
	next, _, err := app.prepareTunnel(context.Background(), owned, routes, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := output.managedIDs; !reflectStrings(got, []string{"old-tunnel"}) {
		t.Fatalf("EnsureTunnel did not use state-owned ID: %v", got)
	}
	if !reflectStrings(source.deleted, []string{"old-sidecar"}) || !reflectStrings(source.ensuredTunnelIDs, []string{"new-tunnel"}) {
		t.Fatalf("connector replacement was unsafe: deletes=%v ensured=%v", source.deleted, source.ensuredTunnelIDs)
	}
	if next.ManagedTunnels[0].TunnelID != "new-tunnel" || next.ManagedTunnels[0].SidecarID != "new-sidecar" {
		t.Fatalf("recreated tunnel state not persisted: %+v", next.ManagedTunnels[0])
	}
}

func TestTunnelFinalTeardownRetriesWithoutRecreatingConnector(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 7, 0, 0, 0, time.UTC)
	output := &tunnelLifecycleOutput{ref: contracts.ProviderRef{Type: "cloudflare", Name: "primary"}, deleteErr: errors.New("temporary delete failure")}
	source := &tunnelLifecycleSource{ref: contracts.ProviderRef{Type: "docker", Name: "local"}}
	app := newTunnelLifecycleApp(t, output, source)
	deadline := now
	managed := &state.ManagedTunnel{Output: output.ref, Source: source.ref, AccountID: "account", Name: "main", TunnelID: "tunnel", SidecarID: "sidecar", Image: "cloudflared:test", RemoveAfter: &deadline, Routes: []state.ManagedTunnelRoute{{Hostname: "app.example.test", Service: "http://10.0.0.2:8080", RemoveAfter: &deadline}}}
	owned := state.EmptySnapshot()
	owned.ManagedTunnels = []state.ManagedTunnel{*managed}
	phase := &tunnelPhase{binding: tunnelBinding{output: output, tunnelOutput: output, source: source, sidecars: source, config: config.TunnelConfig{AccountID: "account", Name: "main", Source: "local"}}, managed: managed, routes: managed.Routes, expiring: true}
	next, err := app.finishTunnel(context.Background(), owned, phase, now)
	if err == nil {
		t.Fatal("expected delete failure")
	}
	if len(next.ManagedTunnels) != 1 || next.ManagedTunnels[0].SidecarID != "" || len(source.deleted) != 1 {
		t.Fatalf("partial teardown state was not persisted: %+v deletes=%v", next.ManagedTunnels, source.deleted)
	}
	_, retry, err := app.prepareTunnel(context.Background(), next, nil, now.Add(time.Second))
	if err != nil || retry == nil || !retry.expiring {
		t.Fatalf("teardown did not remain resumable: phase=%+v err=%v", retry, err)
	}
	if len(source.ensuredTunnelIDs) != 0 || len(output.managedIDs) != 0 {
		t.Fatalf("retry recreated active resources: connector=%v tunnel=%v", source.ensuredTunnelIDs, output.managedIDs)
	}
	output.deleteErr = nil
	next, err = app.finishTunnel(context.Background(), next, retry, now.Add(time.Second))
	if err != nil || len(next.ManagedTunnels) != 0 || output.deletes != 2 {
		t.Fatalf("teardown did not complete on retry: state=%+v deletes=%d err=%v", next.ManagedTunnels, output.deletes, err)
	}
}

func TestTunnelFinalTeardownRecoversAfterRemoteDeleteSucceededBeforeStateSave(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 7, 30, 0, 0, time.UTC)
	output := &tunnelLifecycleOutput{
		ref:          contracts.ProviderRef{Type: "cloudflare", Name: "primary"},
		configureErr: errors.New("remote tunnel no longer exists"),
	}
	source := &tunnelLifecycleSource{ref: contracts.ProviderRef{Type: "docker", Name: "local"}}
	app := newTunnelLifecycleApp(t, output, source)
	deadline := now.Add(-time.Second)
	managed := &state.ManagedTunnel{Output: output.ref, Source: source.ref, AccountID: "account", Name: "main", TunnelID: "already-removed", Image: "cloudflared:test", RemoveAfter: &deadline}
	owned := state.EmptySnapshot()
	owned.ManagedTunnels = []state.ManagedTunnel{*managed}
	phase := &tunnelPhase{
		binding:  tunnelBinding{output: output, tunnelOutput: output, source: source, sidecars: source, config: config.TunnelConfig{AccountID: "account", Name: "main", Source: "local"}},
		managed:  managed,
		expiring: true,
	}

	next, err := app.finishTunnel(context.Background(), owned, phase, now)
	if err != nil {
		t.Fatal(err)
	}
	if output.configureCalls != 0 {
		t.Fatalf("already-cleaned ingress was configured again %d times", output.configureCalls)
	}
	if output.deletes != 1 || len(next.ManagedTunnels) != 0 {
		t.Fatalf("idempotent teardown did not clear state: deletes=%d state=%+v", output.deletes, next.ManagedTunnels)
	}
}

func newTunnelLifecycleApp(t *testing.T, output *tunnelLifecycleOutput, source *tunnelLifecycleSource) *App {
	t.Helper()
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{cfg: config.Config{Outputs: []config.OutputConfig{{Type: "cloudflare", Name: "primary", Tunnel: &config.TunnelConfig{AccountID: "account", Name: "main", Source: "local", ConnectorImage: "cloudflared:test"}}}}, store: store, outputs: []contracts.Output{output}, sources: []contracts.Source{source}, deps: RuntimeDeps{RemovalGrace: 0, Logger: slog.New(slog.DiscardHandler)}}
	return app
}

type tunnelTestOutput struct{ ref contracts.ProviderRef }

func (o *tunnelTestOutput) Provider() contracts.ProviderRef { return o.ref }
func (*tunnelTestOutput) ListVisible(context.Context) ([]contracts.VisibleRecord, error) {
	return nil, nil
}

func (*tunnelTestOutput) Create(context.Context, contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	return nil, nil
}

func (*tunnelTestOutput) Update(context.Context, contracts.VisibleRecord, contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	return nil, nil
}
func (*tunnelTestOutput) Delete(context.Context, contracts.VisibleRecord) error { return nil }

type tunnelLifecycleOutput struct {
	ref            contracts.ProviderRef
	ensured        contracts.TunnelInfo
	managedIDs     []string
	configureErr   error
	configureCalls int
	deleteErr      error
	deletes        int
}

func (o *tunnelLifecycleOutput) Provider() contracts.ProviderRef { return o.ref }
func (*tunnelLifecycleOutput) ListVisible(context.Context) ([]contracts.VisibleRecord, error) {
	return nil, nil
}

func (*tunnelLifecycleOutput) Create(context.Context, contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	return nil, nil
}

func (*tunnelLifecycleOutput) Update(context.Context, contracts.VisibleRecord, contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	return nil, nil
}
func (*tunnelLifecycleOutput) Delete(context.Context, contracts.VisibleRecord) error { return nil }
func (o *tunnelLifecycleOutput) EnsureTunnel(_ context.Context, _, _ string, managedID string) (contracts.TunnelInfo, error) {
	o.managedIDs = append(o.managedIDs, managedID)
	return o.ensured, nil
}

func (*tunnelLifecycleOutput) TunnelToken(context.Context, string, string) (string, error) {
	return "secret", nil
}

func (o *tunnelLifecycleOutput) ConfigureTunnel(context.Context, string, string, []contracts.DesiredTunnelRoute) error {
	o.configureCalls++
	return o.configureErr
}

func (o *tunnelLifecycleOutput) DeleteTunnel(context.Context, string, string) error {
	o.deletes++
	return o.deleteErr
}

type tunnelLifecycleSource struct {
	ref              contracts.ProviderRef
	sidecarID        string
	ensuredTunnelIDs []string
	deleted          []string
}

func (s *tunnelLifecycleSource) Provider() contracts.ProviderRef { return s.ref }
func (*tunnelLifecycleSource) ListDesired(context.Context) (contracts.DesiredSnapshot, error) {
	return contracts.DesiredSnapshot{}, nil
}

func (s *tunnelLifecycleSource) EnsureTunnelSidecar(_ context.Context, spec contracts.TunnelSidecarSpec) (string, error) {
	s.ensuredTunnelIDs = append(s.ensuredTunnelIDs, spec.TunnelID)
	return s.sidecarID, nil
}

func (s *tunnelLifecycleSource) DeleteTunnelSidecar(_ context.Context, id string, _ contracts.TunnelSidecarSpec) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func reflectStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
