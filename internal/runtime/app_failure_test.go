package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
	"github.com/auron-labs/dnsync/internal/state"
)

// scriptedOutputStub is an in-memory output whose failures are all permanent,
// the kind a DNS server returns for a record it will never accept.
type scriptedOutputStub struct {
	provider       contracts.ProviderRef
	visible        []contracts.VisibleRecord
	failLists      int
	failCreateHost string
	failDelete     bool
	listCalls      int
	createCalls    int
	deleteCalls    int
	mu             sync.Mutex
}

func (o *scriptedOutputStub) Provider() contracts.ProviderRef { return o.provider }

func (o *scriptedOutputStub) ListVisible(context.Context) ([]contracts.VisibleRecord, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.listCalls++
	if o.listCalls <= o.failLists {
		return nil, errors.New("list refused")
	}
	return append([]contracts.VisibleRecord(nil), o.visible...), nil
}

func (o *scriptedOutputStub) Create(_ context.Context, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.createCalls++
	if desired.Hostname == o.failCreateHost {
		return nil, errors.New("create refused")
	}
	o.visible = append(o.visible, contracts.VisibleRecord{Output: o.provider, Hostname: desired.Hostname, Answer: desired.Answer})
	return nil, nil
}

func (o *scriptedOutputStub) Update(context.Context, contracts.VisibleRecord, contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	return nil, nil
}

func (o *scriptedOutputStub) Delete(context.Context, contracts.VisibleRecord) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleteCalls++
	if o.failDelete {
		return errors.New("delete refused")
	}
	return nil
}

func (o *scriptedOutputStub) counts() (creates, deletes int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.createCalls, o.deleteCalls
}

var failureTestSource = contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1", DisplayName: "svc"}

func desiredFor(hostnames ...string) []contracts.DesiredRecord {
	records := make([]contracts.DesiredRecord, 0, len(hostnames))
	for _, hostname := range hostnames {
		records = append(records, contracts.DesiredRecord{Hostname: hostname, Answer: "10.0.0.10", Source: failureTestSource})
	}
	return records
}

func newFailureTestApp(t *testing.T, deps Deps, source contracts.Source, outputs ...contracts.Output) (*App, *state.Store) {
	t.Helper()
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	app := New(testRuntimeConfig(store.Path()))
	app.store = store
	setTestProviders(app, deps, []contracts.Source{source}, outputs)
	return app, store
}

func managedHostnames(t *testing.T, store *state.Store, output contracts.ProviderRef) []string {
	t.Helper()
	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	var hostnames []string
	for _, record := range snapshot.ManagedRecords {
		if record.Output == output {
			hostnames = append(hostnames, record.Hostname)
		}
	}
	return hostnames
}

func TestAppReconcileOnceIsolatesFailingRecordsAndOutputs(t *testing.T) {
	t.Parallel()

	refusesRecord := &scriptedOutputStub{provider: contracts.ProviderRef{Type: "adguard", Name: "refuses-record"}, failCreateHost: "bad.local"}
	refusesList := &scriptedOutputStub{provider: contracts.ProviderRef{Type: "adguard", Name: "refuses-list"}, failLists: 1}
	healthy := &scriptedOutputStub{provider: contracts.ProviderRef{Type: "adguard", Name: "healthy"}}
	source := &startupSourceStub{provider: failureTestSource.Provider, desired: desiredFor("bad.local", "good.local")}
	app, store := newFailureTestApp(t, testRuntimeDeps(RetryPolicy{}, 0), source, refusesRecord, refusesList, healthy)

	err := app.reconcileOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "create refused") || !strings.Contains(err.Error(), "list refused") {
		t.Fatalf("expected both failures to be reported, got %v", err)
	}

	if got := managedHostnames(t, store, refusesRecord.provider); len(got) != 1 || got[0] != "good.local" {
		t.Fatalf("expected the refused record not to block the other record on its output, got %v", got)
	}
	if got := managedHostnames(t, store, healthy.provider); len(got) != 2 {
		t.Fatalf("expected the failing outputs not to block the healthy one, got %v", got)
	}
}

func TestAppReconcileOncePersistsRemovalDeadlineWhenEveryMutationFails(t *testing.T) {
	t.Parallel()

	output := &scriptedOutputStub{
		provider:       contracts.ProviderRef{Type: "adguard", Name: "primary"},
		failCreateHost: "bad.local",
	}
	output.visible = []contracts.VisibleRecord{{Output: output.provider, Hostname: "gone.local", Answer: "10.0.0.10"}}
	deps := testRuntimeDeps(RetryPolicy{}, 0)
	deps.RemovalGrace = 10 * time.Minute
	source := &startupSourceStub{provider: failureTestSource.Provider, desired: desiredFor("bad.local")}
	app, store := newFailureTestApp(t, deps, source, output)
	gone := contracts.SourceObjectRef{Provider: failureTestSource.Provider, ID: "ctr-gone", DisplayName: "gone"}
	if err := store.Save(state.Snapshot{ManagedRecords: []state.ManagedRecord{{Output: output.provider, Source: gone, Hostname: "gone.local", Answer: "10.0.0.10"}}}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := app.reconcileOnce(context.Background()); err == nil {
		t.Fatal("expected the refused create to be reported")
	}

	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	// Without the deadline, every later pass would start a fresh grace period
	// and the record would never be removed.
	if len(snapshot.ManagedRecords) != 1 || snapshot.ManagedRecords[0].RemoveAfter == nil {
		t.Fatalf("expected the removal deadline to be saved despite the failed create, got %+v", snapshot.ManagedRecords)
	}
}

func TestAppReconcileOnceDoesNotDeleteWhenSourceListingFails(t *testing.T) {
	t.Parallel()

	output := &scriptedOutputStub{provider: contracts.ProviderRef{Type: "adguard", Name: "primary"}}
	output.visible = []contracts.VisibleRecord{{Output: output.provider, Hostname: "app.local", Answer: "10.0.0.10"}}
	source := &errorSourceStub{provider: failureTestSource.Provider, err: errors.New("docker refused")}
	app, store := newFailureTestApp(t, testRuntimeDeps(RetryPolicy{}, 0), source, output)
	if err := store.Save(state.Snapshot{ManagedRecords: []state.ManagedRecord{{Output: output.provider, Source: failureTestSource, Hostname: "app.local", Answer: "10.0.0.10"}}}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := app.reconcileOnce(context.Background()); err == nil {
		t.Fatal("expected the source failure to be reported")
	}
	if _, deletes := output.counts(); deletes != 0 {
		t.Fatalf("expected no deletes without a complete desired set, got %d", deletes)
	}
}

func startFailureTestRun(t *testing.T, source contracts.Source, output contracts.Output, statePath string, retry RetryPolicy) (context.CancelFunc, <-chan error) {
	t.Helper()
	app := New(testRuntimeConfig(statePath))
	app.registry = testRegistry(stubSourceFactory{source: source}, stubOutputFactory{output: output})
	app.newDeps = func(config.Config) (Deps, error) {
		return testRuntimeDeps(retry, shortWatchHintDebounce), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, done
}

func TestAppRunRecoversFromFailedStartupWithoutDockerEvent(t *testing.T) {
	t.Parallel()

	// Startup and the watch handoff both fail; only the retry timer can recover.
	output := &scriptedOutputStub{provider: contracts.ProviderRef{Type: "adguard", Name: "primary"}, failLists: 2}
	source := &watchSourceStub{
		startupSourceStub: startupSourceStub{provider: failureTestSource.Provider, desired: desiredFor("app.local")},
		sessions:          []*watchSession{newWatchSession()},
	}
	cancel, done := startFailureTestRun(t, source, output, filepath.Join(t.TempDir(), "state.json"), RetryPolicy{InitialInterval: 5 * time.Millisecond, MaxInterval: 10 * time.Millisecond, MaxElapsedTime: 50 * time.Millisecond})

	waitForCondition(t, func() bool {
		creates, _ := output.counts()
		return creates == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("expected clean shutdown, got %v", err)
	}
}

func TestAppRunStopsWhenStateFileFails(t *testing.T) {
	t.Parallel()

	output := &scriptedOutputStub{provider: contracts.ProviderRef{Type: "adguard", Name: "primary"}}
	session := newWatchSession()
	source := &watchSourceStub{startupSourceStub: startupSourceStub{provider: failureTestSource.Provider}, sessions: []*watchSession{session}}
	stateDir := t.TempDir()
	_, done := startFailureTestRun(t, source, output, filepath.Join(stateDir, "state.json"), RetryPolicy{InitialInterval: 5 * time.Millisecond, MaxInterval: 10 * time.Millisecond, MaxElapsedTime: 50 * time.Millisecond})

	waitForCondition(t, func() bool { return source.listDesiredCallCount() == 2 })
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatalf("remove state directory: %v", err)
	}
	session.hints <- struct{}{}

	select {
	case err := <-done:
		var stateErr *state.Error
		if !errors.As(err, &stateErr) {
			t.Fatalf("expected a state file error to stop the runtime, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the runtime to stop when the state file fails")
	}
}

// A due removal deadline whose delete keeps failing must wait for the retry
// backoff; re-arming the removal timer would retry it in a tight loop.
func TestAppRunDoesNotSpinOnFailingDueRemoval(t *testing.T) {
	t.Parallel()

	sources := map[string]func() contracts.Source{
		"watching source": func() contracts.Source {
			return &watchSourceStub{startupSourceStub: startupSourceStub{provider: failureTestSource.Provider}, sessions: []*watchSession{newWatchSession()}}
		},
		"non-watching source": func() contracts.Source {
			return &startupSourceStub{provider: failureTestSource.Provider}
		},
	}
	for name, newSource := range sources {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatalf("create state store: %v", err)
			}
			seeded, err := store.Load()
			if err != nil {
				t.Fatalf("load state: %v", err)
			}
			provider := contracts.ProviderRef{Type: "adguard", Name: "primary"}
			provenance := &contracts.RecordProvenance{RemoteID: "rec-1", InstallationID: seeded.InstallationID}
			past := time.Now().Add(-time.Minute)
			seeded.ManagedRecords = []state.ManagedRecord{{Output: provider, Source: failureTestSource, Hostname: "gone.local", Answer: "10.0.0.10", Provenance: provenance, RemoveAfter: &past}}
			if err := store.Save(seeded); err != nil {
				t.Fatalf("seed state: %v", err)
			}
			output := &scriptedOutputStub{provider: provider, failDelete: true}
			output.visible = []contracts.VisibleRecord{{Output: provider, Hostname: "gone.local", Answer: "10.0.0.10", Provenance: provenance}}

			retry := 20 * time.Millisecond
			cancel, done := startFailureTestRun(t, newSource(), output, store.Path(), RetryPolicy{InitialInterval: retry, MaxInterval: retry, MaxElapsedTime: retry})
			time.Sleep(200 * time.Millisecond)
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("expected clean shutdown, got %v", err)
			}

			if _, deletes := output.counts(); deletes == 0 || deletes > 20 {
				t.Fatalf("expected the failing delete to be retried on the backoff, got %d attempts in 200ms", deletes)
			}
		})
	}
}
