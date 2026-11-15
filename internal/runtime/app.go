package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
	"github.com/auron-labs/dnsync/internal/state"
)

var errSourceWatchClosed = errors.New("source watch closed")

type App struct {
	cfg      config.Config
	registry *FactoryRegistry
	newDeps  func(config.Config) (RuntimeDeps, error)
	deps     RuntimeDeps
	store    *state.Store
	sources  []contracts.Source
	outputs  []contracts.Output
}

func New(cfg config.Config) *App {
	return &App{
		cfg:      cfg,
		registry: NewDefaultFactoryRegistry(),
		newDeps:  NewRuntimeDeps,
	}
}

func (a *App) Run(ctx context.Context) error {
	if err := config.Validate(a.cfg); err != nil {
		return err
	}

	resolved, err := config.ResolveSecrets(a.cfg, os.LookupEnv)
	if err != nil {
		return err
	}

	deps, err := a.newDeps(resolved)
	if err != nil {
		return err
	}
	if deps.LogCloser != nil {
		defer deps.LogCloser.Close()
	}

	store, err := state.NewStore(resolved.State.Path)
	if err != nil {
		return err
	}
	owned, err := store.Load()
	if err != nil {
		return fmt.Errorf("load state before provider setup: %w", err)
	}
	deps.InstallationID = owned.InstallationID

	sources, outputs, err := a.registry.BuildProviders(resolved, deps)
	if err != nil {
		return err
	}

	a.cfg = resolved
	a.deps = deps
	a.store = store
	a.setProviders(sources, outputs, deps)

	deps.Logger.Info("starting dnsync runtime", "sources", len(sources), "outputs", len(outputs), "state_path", resolved.State.Path, "log_level", resolved.Logging.Level)
	if err := a.reconcile(ctx, "startup"); err != nil {
		if errors.Is(err, context.Canceled) {
			deps.Logger.Info("runtime cancelled", "reason", ctx.Err())
			return nil
		}
		return err
	}
	if err := a.runSteadyState(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			deps.Logger.Info("runtime cancelled", "reason", ctx.Err())
			return nil
		}
		return err
	}
	return nil
}

func (a *App) reconcile(ctx context.Context, reason string) error {
	a.deps.Logger.Info("starting reconcile", "reason", reason)

	attempt := 0
	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		attempt++
		err := a.reconcileOnce(ctx)
		if err == nil {
			return struct{}{}, nil
		}
		if ctx.Err() != nil {
			return struct{}{}, backoff.Permanent(ctx.Err())
		}
		if _, _, ok := retryableReconcileFailure(err); !ok {
			return struct{}{}, backoff.Permanent(err)
		}
		return struct{}{}, err
	},
		backoff.WithBackOff(a.deps.Retry.newBackOff()),
		backoff.WithMaxElapsedTime(a.deps.Retry.MaxElapsedTime),
		backoff.WithNotify(func(err error, delay time.Duration) {
			description, provider, _ := retryableReconcileFailure(err)
			a.deps.Logger.Warn(description, "reason", reason, provider.kind, providerKey(provider.ref), "attempt", attempt, "next_delay", delay, "error", err)
		}),
	)
	if err != nil {
		return err
	}
	a.deps.Logger.Info("reconcile completed", "reason", reason)
	return nil
}

type failedProvider struct {
	kind string
	ref  contracts.ProviderRef
}

// retryableReconcileFailure reports whether err is a temporary provider failure that a full reconcile pass may retry.
func retryableReconcileFailure(err error) (string, failedProvider, bool) {
	if !isTemporaryError(err) {
		return "", failedProvider{}, false
	}
	var sourceErr sourceListError
	if errors.As(err, &sourceErr) {
		return "retrying full reconcile after temporary source read failure", failedProvider{"source", sourceErr.provider}, true
	}
	var outputErr outputListError
	if errors.As(err, &outputErr) {
		return "retrying full reconcile after temporary output read failure", failedProvider{"output", outputErr.provider}, true
	}
	var mutationErr outputMutationError
	if errors.As(err, &mutationErr) {
		return "retrying full reconcile after temporary output write failure", failedProvider{"output", mutationErr.provider}, true
	}
	return "", failedProvider{}, false
}

func (a *App) reconcileOnce(ctx context.Context) error {
	owned, err := a.store.Load()
	if err != nil {
		return fmt.Errorf("load owned state: %w", err)
	}
	logDebug(ctx, a.deps.Logger, "loaded owned state snapshot", "records", len(owned.ManagedRecords))

	desired := make([]contracts.DesiredRecord, 0)
	tunnelRoutes := make([]contracts.DesiredTunnelRoute, 0)
	for i, source := range a.sources {
		snapshot, err := source.ListDesired(ctx)
		if err != nil {
			return sourceListError{sourceIndex: i, provider: source.Provider(), err: err}
		}
		logDebug(ctx, a.deps.Logger, "listed desired records from source", "source", providerKey(source.Provider()), "records", len(snapshot.Records), "tunnel_routes", len(snapshot.TunnelRoutes))
		for _, record := range snapshot.Records {
			logTrace(ctx, a.deps.Logger, "desired record discovered", "source", providerKey(source.Provider()), "source_id", record.Source.ID, "display_name", record.Source.DisplayName, "hostname", record.Hostname, "answer", record.Answer)
		}
		desired = append(desired, snapshot.Records...)
		tunnelRoutes = append(tunnelRoutes, snapshot.TunnelRoutes...)
	}
	now := time.Now().UTC()
	owned, tunnel, err := a.prepareTunnel(ctx, owned, tunnelRoutes, now)
	if err != nil {
		return err
	}
	logDebug(ctx, a.deps.Logger, "collected desired records across sources", "records", len(desired), "tunnel_routes", len(tunnelRoutes))

	tunnelHostnames := make(map[string]struct{})
	if tunnel != nil && tunnel.managed != nil {
		for _, route := range activeTunnelRoutes(tunnel.routes, now) {
			tunnelHostnames[normalizeHostname(route.Hostname)] = struct{}{}
			desired = append(desired, contracts.DesiredRecord{
				Hostname: route.Hostname,
				Answer:   tunnel.managed.TunnelID + ".cfargotunnel.com",
				Source:   route.Source,
				Output:   "cloudflare",
				Proxied:  true,
			})
		}
	}

	for i, output := range a.outputs {
		outputDesired := filterDesiredForOutputWithTunnelPriority(desired, output.Provider(), tunnelHostnames, tunnel)
		visible, err := output.ListVisible(ctx)
		if err != nil {
			return outputListError{outputIndex: i, provider: output.Provider(), err: err}
		}
		logDebug(ctx, a.deps.Logger, "listed visible records from output", "output", providerKey(output.Provider()), "records", len(visible))
		for _, record := range visible {
			logTrace(ctx, a.deps.Logger, "visible record discovered", "output", providerKey(output.Provider()), "hostname", record.Hostname, "answer", record.Answer)
		}

		result, err := ReconcileAndPersist(ctx, a.store, ReconcileInput{
			Output:  output,
			Desired: outputDesired,
			Visible: visible,
			Owned:   owned,
			Now: func() time.Time {
				return now
			},
			Grace:  a.deps.RemovalGrace,
			Logger: a.deps.Logger,
		})
		if err != nil {
			return fmt.Errorf("reconcile output %d (%s/%s): %w", i, output.Provider().Type, output.Provider().Name, err)
		}

		owned = result.Next
	}
	_, err = a.finishTunnel(ctx, owned, tunnel, now)
	return err
}

func filterDesiredForOutput(desired []contracts.DesiredRecord, output contracts.ProviderRef) []contracts.DesiredRecord {
	return filterDesiredForOutputWithTunnelPriority(desired, output, nil, nil)
}

func filterDesiredForOutputWithTunnelPriority(desired []contracts.DesiredRecord, output contracts.ProviderRef, tunnelHostnames map[string]struct{}, tunnel *tunnelPhase) []contracts.DesiredRecord {
	if len(desired) == 0 {
		return nil
	}

	filtered := make([]contracts.DesiredRecord, 0, len(desired))
	outputType := strings.TrimSpace(strings.ToLower(output.Type))
	for _, record := range desired {
		if record.Output != "" && strings.TrimSpace(strings.ToLower(record.Output)) != outputType {
			continue
		}
		if outputType == "cloudflare" {
			_, tunnelHostname := tunnelHostnames[normalizeHostname(record.Hostname)]
			isSelectedTunnelCNAME := tunnel != nil && tunnel.managed != nil && sameProvider(output, tunnel.binding.output.Provider()) && record.Proxied && strings.EqualFold(strings.TrimSpace(record.Answer), strings.TrimSpace(tunnel.managed.TunnelID+".cfargotunnel.com"))
			if tunnelHostname && !isSelectedTunnelCNAME {
				continue
			}
			if isSelectedTunnelCNAME && !sameProvider(output, tunnel.binding.output.Provider()) {
				continue
			}
		}
		filtered = append(filtered, record)
	}

	return filtered
}

type sourceWatchEvent struct {
	sourceIndex int
	err         error
	hint        bool
	reconnect   bool
	deadline    bool
}

type sourceListError struct {
	sourceIndex int
	provider    contracts.ProviderRef
	err         error
}

type outputListError struct {
	outputIndex int
	provider    contracts.ProviderRef
	err         error
}

type temporaryError interface {
	Temporary() bool
}

func (e sourceListError) Error() string {
	return fmt.Sprintf("list desired records from source %d (%s/%s): %v", e.sourceIndex, e.provider.Type, e.provider.Name, e.err)
}

func (e sourceListError) Unwrap() error {
	return e.err
}

func (e outputListError) Error() string {
	return fmt.Sprintf("list visible records from output %d (%s/%s): %v", e.outputIndex, e.provider.Type, e.provider.Name, e.err)
}

func (e outputListError) Unwrap() error {
	return e.err
}

func (a *App) runSteadyState(ctx context.Context) error {
	watchers := make(map[int]contracts.WatchableSource)
	for i, source := range a.sources {
		watchable, ok := source.(contracts.WatchableSource)
		if ok {
			watchers[i] = watchable
		}
	}

	if len(watchers) == 0 {
		return a.runRemovalDeadlineOnly(ctx)
	}

	events := make(chan sourceWatchEvent, len(watchers)+1)
	reconnectBackoffs := make(map[int]*backoff.ExponentialBackOff, len(watchers))
	reconnectPending := make(map[int]bool, len(watchers))
	watchHintDebounce := a.deps.WatchHintDebounce
	var watchHintTimer *time.Timer
	var watchHintTimerC <-chan time.Time
	pendingWatchHintCount := 0
	for i, watchable := range watchers {
		reconnectBackoffs[i] = a.deps.Retry.newBackOff()
		a.startSourceWatch(ctx, i, watchable, events)
	}
	if err := a.reconcile(ctx, "watch_startup_handoff"); err != nil {
		return err
	}
	var removalTimer *time.Timer
	var removalTimerC <-chan time.Time
	refreshRemovalTimer := func() {
		stopTimer(removalTimer)
		removalTimer = nil
		removalTimerC = nil
		deadline, err := a.nextRemovalDeadline()
		if err != nil || deadline.IsZero() {
			if err != nil {
				a.deps.Logger.Warn("could not schedule removal grace deadline", "error", err)
			}
			return
		}
		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		removalTimer = time.NewTimer(delay)
		removalTimerC = removalTimer.C
	}
	refreshRemovalTimer()
	for _, reconnectBackoff := range reconnectBackoffs {
		reconnectBackoff.Reset()
	}

	for {
		select {
		case <-ctx.Done():
			stopTimer(watchHintTimer)
			stopTimer(removalTimer)
			return ctx.Err()
		case <-removalTimerC:
			removalTimer = nil
			removalTimerC = nil
			if err := a.reconcile(ctx, "removal_deadline"); err != nil {
				return err
			}
			refreshRemovalTimer()
		case <-watchHintTimerC:
			watchHintTimer = nil
			watchHintTimerC = nil
			coalescedHintCount := pendingWatchHintCount
			pendingWatchHintCount = 0
			logDebug(ctx, a.deps.Logger, "running coalesced watch hint reconcile", "debounce", watchHintDebounce, "coalesced_hint_count", coalescedHintCount)
			if err := a.reconcile(ctx, "watch_hint"); err != nil {
				return err
			}
			refreshRemovalTimer()
		case event := <-events:
			if event.hint {
				provider := a.sources[event.sourceIndex].Provider()
				reconnectBackoffs[event.sourceIndex].Reset()
				if watchHintDebounce <= 0 {
					logDebug(ctx, a.deps.Logger, "source watch emitted reconcile hint", "source", providerKey(provider), "debounce", watchHintDebounce, "coalesced_hint_count", 1)
					if err := a.reconcile(ctx, "watch_hint"); err != nil {
						return err
					}
					refreshRemovalTimer()
					continue
				}

				if watchHintTimer == nil {
					pendingWatchHintCount = 1
					watchHintTimer = time.NewTimer(watchHintDebounce)
					watchHintTimerC = watchHintTimer.C
					logDebug(ctx, a.deps.Logger, "started watch hint debounce", "source", providerKey(provider), "debounce", watchHintDebounce, "coalesced_hint_count", pendingWatchHintCount)
					continue
				}

				pendingWatchHintCount++
				logDebug(ctx, a.deps.Logger, "coalesced watch hint while debounce pending", "source", providerKey(provider), "debounce", watchHintDebounce, "coalesced_hint_count", pendingWatchHintCount)
				continue
			}

			if event.reconnect {
				reconnectPending[event.sourceIndex] = false
				logDebug(ctx, a.deps.Logger, "restarting source watch after backoff", "source", providerKey(a.sources[event.sourceIndex].Provider()))
				a.startSourceWatch(ctx, event.sourceIndex, watchers[event.sourceIndex], events)
				if watchHintTimer != nil {
					logDebug(ctx, a.deps.Logger, "clearing pending watch hint debounce after reconnect repair", "source", providerKey(a.sources[event.sourceIndex].Provider()), "debounce", watchHintDebounce, "coalesced_hint_count", pendingWatchHintCount)
					stopTimer(watchHintTimer)
					watchHintTimer = nil
					watchHintTimerC = nil
					pendingWatchHintCount = 0
				}
				if err := a.reconcile(ctx, "watch_reconnect_repair"); err != nil {
					return err
				}
				refreshRemovalTimer()
				reconnectBackoffs[event.sourceIndex].Reset()
				continue
			}

			provider := a.sources[event.sourceIndex].Provider()
			if reconnectPending[event.sourceIndex] {
				continue
			}
			delay := reconnectBackoffs[event.sourceIndex].NextBackOff()
			a.deps.Logger.Warn("source watch disconnected", "source", providerKey(provider), "reconnect_delay", delay, "error", event.err)
			reconnectPending[event.sourceIndex] = true
			a.scheduleSourceReconnect(ctx, event.sourceIndex, delay, events)
		}
	}
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (a *App) scheduleSourceReconnect(ctx context.Context, sourceIndex int, delay time.Duration, events chan<- sourceWatchEvent) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		select {
		case events <- sourceWatchEvent{sourceIndex: sourceIndex, reconnect: true}:
		case <-ctx.Done():
		}
	}()
}

func (a *App) startSourceWatch(ctx context.Context, sourceIndex int, source contracts.WatchableSource, events chan<- sourceWatchEvent) {
	provider := a.sources[sourceIndex].Provider()
	a.deps.Logger.Info("starting source watch", "source", providerKey(provider))
	stream := source.Watch(ctx)

	go func() {
		for stream.Hints != nil || stream.Err != nil {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-stream.Hints:
				if !ok {
					stream.Hints = nil
					continue
				}
				logTrace(ctx, a.deps.Logger, "source watch hint received", "source", providerKey(provider))
				select {
				case events <- sourceWatchEvent{sourceIndex: sourceIndex, hint: true}:
				case <-ctx.Done():
				}
			case err, ok := <-stream.Err:
				if !ok {
					stream.Err = nil
					continue
				}
				if err == nil || errors.Is(err, context.Canceled) {
					return
				}
				select {
				case events <- sourceWatchEvent{sourceIndex: sourceIndex, err: err}:
				case <-ctx.Done():
				}
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case events <- sourceWatchEvent{sourceIndex: sourceIndex, err: errSourceWatchClosed}:
		case <-ctx.Done():
		}
	}()
}

type loggingOutput struct {
	base   contracts.Output
	logger *slog.Logger
}

type timeoutSource struct {
	base    contracts.Source
	timeout time.Duration
}

type timeoutWatchableSource struct {
	timeoutSource
	watchable contracts.WatchableSource
}

type timeoutOutput struct {
	base    contracts.Output
	timeout time.Duration
}

func wrapSources(sources []contracts.Source, deps RuntimeDeps) []contracts.Source {
	wrapped := make([]contracts.Source, 0, len(sources))
	for _, source := range sources {
		wrappedSource := timeoutSource{base: source, timeout: deps.OperationTimeout}
		watchable, ok := source.(contracts.WatchableSource)
		if ok {
			wrapped = append(wrapped, timeoutWatchableSource{timeoutSource: wrappedSource, watchable: watchable})
			continue
		}
		wrapped = append(wrapped, wrappedSource)
	}
	return wrapped
}

func wrapOutputs(outputs []contracts.Output, deps RuntimeDeps) []contracts.Output {
	wrapped := make([]contracts.Output, 0, len(outputs))
	for _, output := range outputs {
		wrapped = append(wrapped, loggingOutput{base: timeoutOutput{base: output, timeout: deps.OperationTimeout}, logger: deps.Logger})
	}
	return wrapped
}

func (a *App) setProviders(sources []contracts.Source, outputs []contracts.Output, deps RuntimeDeps) {
	a.sources = wrapSources(sources, deps)
	a.outputs = wrapOutputs(outputs, deps)
}

func (s timeoutSource) Provider() contracts.ProviderRef {
	return s.base.Provider()
}

func (s timeoutSource) ListDesired(ctx context.Context) (contracts.DesiredSnapshot, error) {
	ctx, cancel := withOperationTimeout(ctx, s.timeout)
	defer cancel()
	return s.base.ListDesired(ctx)
}

func (s timeoutWatchableSource) Watch(ctx context.Context) contracts.SourceWatch {
	return s.watchable.Watch(ctx)
}

func (o timeoutOutput) Provider() contracts.ProviderRef {
	return o.base.Provider()
}

func (o timeoutOutput) ListVisible(ctx context.Context) ([]contracts.VisibleRecord, error) {
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return o.base.ListVisible(ctx)
}

func (o timeoutOutput) Create(ctx context.Context, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return o.base.Create(ctx, desired)
}

func (o timeoutOutput) Update(ctx context.Context, visible contracts.VisibleRecord, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return o.base.Update(ctx, visible, desired)
}

func (o timeoutOutput) Delete(ctx context.Context, visible contracts.VisibleRecord) error {
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return o.base.Delete(ctx, visible)
}

func (o timeoutOutput) EnsureTunnel(ctx context.Context, accountID, name, managedID string) (contracts.TunnelInfo, error) {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return contracts.TunnelInfo{}, fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return tunnel.EnsureTunnel(ctx, accountID, name, managedID)
}

func (o timeoutOutput) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return "", fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return tunnel.TunnelToken(ctx, accountID, tunnelID)
}

func (o timeoutOutput) ConfigureTunnel(ctx context.Context, accountID, tunnelID string, routes []contracts.DesiredTunnelRoute) error {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return tunnel.ConfigureTunnel(ctx, accountID, tunnelID, routes)
}

func (o timeoutOutput) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	ctx, cancel := withOperationTimeout(ctx, o.timeout)
	defer cancel()
	return tunnel.DeleteTunnel(ctx, accountID, tunnelID)
}

func (o loggingOutput) Provider() contracts.ProviderRef {
	return o.base.Provider()
}

func (o loggingOutput) ListVisible(ctx context.Context) ([]contracts.VisibleRecord, error) {
	return o.base.ListVisible(ctx)
}

func (o loggingOutput) Create(ctx context.Context, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	provenance, err := o.base.Create(ctx, desired)
	if err == nil {
		o.logger.Info("output mutation applied", "operation", "create", "provider", providerKey(o.Provider()), "hostname", desired.Hostname)
	}
	return provenance, err
}

func (o loggingOutput) Update(ctx context.Context, visible contracts.VisibleRecord, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	provenance, err := o.base.Update(ctx, visible, desired)
	if err == nil {
		o.logger.Info("output mutation applied", "operation", "update", "provider", providerKey(o.Provider()), "hostname", desired.Hostname)
	}
	return provenance, err
}

func (o loggingOutput) Delete(ctx context.Context, visible contracts.VisibleRecord) error {
	err := o.base.Delete(ctx, visible)
	if err == nil {
		o.logger.Info("output mutation applied", "operation", "delete", "provider", providerKey(o.Provider()), "hostname", visible.Hostname)
	}
	return err
}

func (o loggingOutput) EnsureTunnel(ctx context.Context, accountID, name, managedID string) (contracts.TunnelInfo, error) {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return contracts.TunnelInfo{}, fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	return tunnel.EnsureTunnel(ctx, accountID, name, managedID)
}

func (o loggingOutput) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return "", fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	return tunnel.TunnelToken(ctx, accountID, tunnelID)
}

func (o loggingOutput) ConfigureTunnel(ctx context.Context, accountID, tunnelID string, routes []contracts.DesiredTunnelRoute) error {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	return tunnel.ConfigureTunnel(ctx, accountID, tunnelID, routes)
}

func (o loggingOutput) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	tunnel, ok := o.base.(contracts.TunnelOutput)
	if !ok {
		return fmt.Errorf("output %s does not support tunnel control", providerKey(o.Provider()))
	}
	return tunnel.DeleteTunnel(ctx, accountID, tunnelID)
}

func providerKey(provider contracts.ProviderRef) string {
	return provider.Type + "/" + provider.Name
}

func logDebug(ctx context.Context, logger *slog.Logger, msg string, args ...any) {
	if logger == nil || !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	logger.DebugContext(ctx, msg, args...)
}

func logTrace(ctx context.Context, logger *slog.Logger, msg string, args ...any) {
	if logger == nil || !logger.Enabled(ctx, LevelTrace) {
		return
	}
	logger.Log(ctx, LevelTrace, msg, args...)
}

func (a *App) Deps() RuntimeDeps {
	return a.deps
}

func (a *App) LogLevel() slog.Level {
	return a.deps.LogLevel
}

func (a *App) StateStore() *state.Store {
	return a.store
}

func (a *App) SourceCount() int {
	return len(a.sources)
}

func (a *App) OutputCount() int {
	return len(a.outputs)
}
