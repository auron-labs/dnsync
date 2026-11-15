package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/auron-labs/dnsync/internal/contracts"
	"github.com/auron-labs/dnsync/internal/state"
)

func TestCloudflareMarkerAdoptsVisibleRecordWithoutPerRecordState(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	fakeOutput := &reconcileFakeOutput{provider: provider}

	result, err := ReconcileOutput(context.Background(), ReconcileInput{
		Output:  fakeOutput,
		Desired: []contracts.DesiredRecord{{Hostname: "app.example.com", Answer: "10.0.0.10", Source: source}},
		Visible: []contracts.VisibleRecord{{Output: provider, Hostname: "app.example.com", Answer: "10.0.0.10", Provenance: &contracts.RecordProvenance{RemoteID: "rec-owned", InstallationID: installationID}}},
		Owned:   state.Snapshot{InstallationID: installationID},
	})
	if err != nil {
		t.Fatalf("reconcile output: %v", err)
	}
	if len(fakeOutput.created) != 0 || len(fakeOutput.updated) != 0 || len(fakeOutput.deleted) != 0 {
		t.Fatalf("marker adoption must not mutate the record: creates=%d updates=%d deletes=%d", len(fakeOutput.created), len(fakeOutput.updated), len(fakeOutput.deleted))
	}
	if len(result.Next.ManagedRecords) != 1 {
		t.Fatalf("expected adopted managed record, got %+v", result.Next.ManagedRecords)
	}
	managed := result.Next.ManagedRecords[0]
	if managed.Source != source || managed.Provenance == nil || managed.Provenance.RemoteID != "rec-owned" || managed.Provenance.InstallationID != installationID {
		t.Fatalf("unexpected adopted record: %+v", managed)
	}
}

func TestCloudflareMarkerDeletesStaleVisibleRecordWithoutPerRecordState(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	fakeOutput := &reconcileFakeOutput{provider: provider}

	result, err := ReconcileOutput(context.Background(), ReconcileInput{
		Output:  fakeOutput,
		Visible: []contracts.VisibleRecord{{Output: provider, Hostname: "stale.example.com", Answer: "10.0.0.12", Provenance: &contracts.RecordProvenance{RemoteID: "rec-stale", InstallationID: installationID}}},
		Owned:   state.Snapshot{InstallationID: installationID},
	})
	if err != nil {
		t.Fatalf("reconcile output: %v", err)
	}
	if len(fakeOutput.deleted) != 1 || fakeOutput.deleted[0].Provenance == nil || fakeOutput.deleted[0].Provenance.RemoteID != "rec-stale" {
		t.Fatalf("expected marker-owned stale record to be deleted, got %+v", fakeOutput.deleted)
	}
	if len(result.Next.ManagedRecords) != 0 {
		t.Fatalf("expected no managed state after marker-owned stale deletion, got %+v", result.Next.ManagedRecords)
	}
}

func TestCloudflareMarkerDoesNotOverrideStalePersistedRemoteIDForCleanup(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	managed := state.ManagedRecord{
		Output:     provider,
		Source:     contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"},
		Hostname:   "stale.example.com",
		Answer:     "10.0.0.12",
		Provenance: &contracts.RecordProvenance{RemoteID: "rec-old"},
	}
	fakeOutput := &reconcileFakeOutput{provider: provider}

	result, err := ReconcileOutput(context.Background(), ReconcileInput{
		Output:  fakeOutput,
		Visible: []contracts.VisibleRecord{{Output: provider, Hostname: managed.Hostname, Answer: managed.Answer, Provenance: &contracts.RecordProvenance{RemoteID: "rec-replaced", InstallationID: installationID}}},
		Owned:   state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{managed}},
	})
	if err != nil {
		t.Fatalf("reconcile output: %v", err)
	}
	if len(fakeOutput.deleted) != 0 || len(result.Next.ManagedRecords) != 1 {
		t.Fatalf("expected stale persisted remote ID to block marker-only cleanup, deletes=%d next=%+v", len(fakeOutput.deleted), result.Next.ManagedRecords)
	}
}

func TestCloudflareMarkerDeletesStaleRecordAfterVisibleAnswerDrift(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	managed := state.ManagedRecord{
		Output:     provider,
		Source:     contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"},
		Hostname:   "stale.example.com",
		Answer:     "10.0.0.12",
		Provenance: &contracts.RecordProvenance{RemoteID: "rec-stale", InstallationID: installationID},
	}

	for _, test := range []struct {
		name        string
		remoteID    string
		wantDeletes int
	}{
		{name: "matching remote ID", remoteID: "rec-stale", wantDeletes: 1},
		{name: "differing remote ID", remoteID: "rec-replaced", wantDeletes: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeOutput := &reconcileFakeOutput{provider: provider}
			result, err := ReconcileOutput(context.Background(), ReconcileInput{
				Output: fakeOutput,
				Visible: []contracts.VisibleRecord{{
					Output:     provider,
					Hostname:   managed.Hostname,
					Answer:     "10.0.0.99",
					Provenance: &contracts.RecordProvenance{RemoteID: test.remoteID, InstallationID: installationID},
				}},
				Owned: state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{managed}},
			})
			if err != nil {
				t.Fatalf("reconcile output: %v", err)
			}
			if got := len(fakeOutput.deleted); got != test.wantDeletes {
				t.Fatalf("expected %d delete calls, got %d", test.wantDeletes, got)
			}
			if test.wantDeletes == 1 && (fakeOutput.deleted[0].Answer != "10.0.0.99" || len(result.Next.ManagedRecords) != 0) {
				t.Fatalf("expected drifted visible record to be deleted and lineage removed, deletes=%+v next=%+v", fakeOutput.deleted, result.Next.ManagedRecords)
			}
			if test.wantDeletes == 0 && len(result.Next.ManagedRecords) != 1 {
				t.Fatalf("expected mismatched remote ID to retain managed lineage, got %+v", result.Next.ManagedRecords)
			}
		})
	}
}

func TestCloudflareMarkedStateRequiresCurrentMarkerAndRemoteID(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"}
	managed := state.ManagedRecord{
		Output:     provider,
		Source:     source,
		Hostname:   "app.example.com",
		Answer:     "10.0.0.10",
		Provenance: &contracts.RecordProvenance{RemoteID: "rec-owned", InstallationID: installationID},
	}

	for _, test := range []struct {
		name     string
		marker   string
		remoteID string
	}{
		{name: "removed marker", remoteID: "rec-owned"},
		{name: "foreign marker", marker: "123e4567-e89b-42d3-a456-426614174001", remoteID: "rec-owned"},
		{name: "mismatched remote ID", marker: installationID, remoteID: "rec-replaced"},
	} {
		t.Run(test.name, func(t *testing.T) {
			visible := contracts.VisibleRecord{
				Output:     provider,
				Hostname:   managed.Hostname,
				Answer:     managed.Answer,
				Provenance: &contracts.RecordProvenance{RemoteID: test.remoteID, InstallationID: test.marker},
			}

			updateOutput := &reconcileFakeOutput{provider: provider}
			updateResult, err := ReconcileOutput(context.Background(), ReconcileInput{
				Output:  updateOutput,
				Desired: []contracts.DesiredRecord{{Hostname: managed.Hostname, Answer: "10.0.0.11", Source: source}},
				Visible: []contracts.VisibleRecord{visible},
				Owned:   state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{managed}},
			})
			if err != nil {
				t.Fatalf("update reconcile: %v", err)
			}
			if len(updateOutput.updated) != 0 || len(updateResult.Next.ManagedRecords) != 1 || updateResult.Next.ManagedRecords[0].Provenance.RemoteID != managed.Provenance.RemoteID || updateResult.Next.ManagedRecords[0].Provenance.InstallationID != installationID {
				t.Fatalf("expected marker removal/foreign marker to block update and preserve state, updates=%d next=%+v", len(updateOutput.updated), updateResult.Next.ManagedRecords)
			}

			deleteOutput := &reconcileFakeOutput{provider: provider}
			deleteResult, err := ReconcileOutput(context.Background(), ReconcileInput{
				Output:  deleteOutput,
				Visible: []contracts.VisibleRecord{visible},
				Owned:   state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{managed}},
			})
			if err != nil {
				t.Fatalf("delete reconcile: %v", err)
			}
			if len(deleteOutput.deleted) != 0 || len(deleteResult.Next.ManagedRecords) != 1 || deleteResult.Next.ManagedRecords[0].Provenance.RemoteID != managed.Provenance.RemoteID || deleteResult.Next.ManagedRecords[0].Provenance.InstallationID != installationID {
				t.Fatalf("expected marker removal/foreign marker to block delete and preserve state, deletes=%d next=%+v", len(deleteOutput.deleted), deleteResult.Next.ManagedRecords)
			}
		})
	}

	visible := contracts.VisibleRecord{
		Output:     provider,
		Hostname:   managed.Hostname,
		Answer:     managed.Answer,
		Provenance: &contracts.RecordProvenance{RemoteID: "rec-replaced", InstallationID: installationID},
	}
	result, err := ReconcileOutput(context.Background(), ReconcileInput{
		Output:  &reconcileFakeOutput{provider: provider},
		Desired: []contracts.DesiredRecord{{Hostname: managed.Hostname, Answer: managed.Answer, Source: source}},
		Visible: []contracts.VisibleRecord{visible},
		Owned:   state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{managed}},
	})
	if err != nil {
		t.Fatalf("exact-record reconcile: %v", err)
	}
	if len(result.Next.ManagedRecords) != 1 || result.Next.ManagedRecords[0].Provenance.RemoteID != managed.Provenance.RemoteID {
		t.Fatalf("expected mismatched remote ID to preserve managed provenance, got %+v", result.Next.ManagedRecords)
	}
}

func TestCloudflareCleanupRequiresMatchingPersistedRemoteIDWithoutMarker(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	managed := state.ManagedRecord{
		Output:     provider,
		Source:     contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"},
		Hostname:   "stale.example.com",
		Answer:     "10.0.0.12",
		Provenance: &contracts.RecordProvenance{RemoteID: "rec-owned"},
	}
	for _, remoteID := range []string{"rec-manual", "rec-owned"} {
		t.Run(remoteID, func(t *testing.T) {
			fakeOutput := &reconcileFakeOutput{provider: provider}
			result, err := ReconcileOutput(context.Background(), ReconcileInput{
				Output:  fakeOutput,
				Visible: []contracts.VisibleRecord{{Output: provider, Hostname: managed.Hostname, Answer: managed.Answer, Provenance: &contracts.RecordProvenance{RemoteID: remoteID}}},
				Owned:   state.Snapshot{InstallationID: "123e4567-e89b-42d3-a456-426614174000", ManagedRecords: []state.ManagedRecord{managed}},
			})
			if err != nil {
				t.Fatalf("reconcile output: %v", err)
			}
			if len(fakeOutput.deleted) != 0 || len(result.Next.ManagedRecords) != 1 {
				t.Fatalf("expected unmarked record to remain untouched, deletes=%d next=%+v", len(fakeOutput.deleted), result.Next.ManagedRecords)
			}
		})
	}
}

func TestCloudflareMarkerFlowDoesNotClaimForeignOrUnmarkedRecords(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	foreignID := "123e4567-e89b-42d3-a456-426614174001"

	for _, test := range []struct {
		name  string
		owner string
	}{
		{name: "foreign marker", owner: foreignID},
		{name: "no marker", owner: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeOutput := &reconcileFakeOutput{provider: provider}
			visible := contracts.VisibleRecord{Output: provider, Hostname: "app.example.com", Answer: "10.0.0.10", Provenance: &contracts.RecordProvenance{RemoteID: "manual-rec", InstallationID: test.owner}}
			result, err := ReconcileOutput(context.Background(), ReconcileInput{
				Output:  fakeOutput,
				Desired: []contracts.DesiredRecord{{Hostname: visible.Hostname, Answer: visible.Answer, Source: source}},
				Visible: []contracts.VisibleRecord{visible},
				Owned:   state.Snapshot{InstallationID: installationID},
			})
			if err != nil {
				t.Fatalf("reconcile output: %v", err)
			}
			if len(fakeOutput.created) != 0 || len(fakeOutput.updated) != 0 || len(fakeOutput.deleted) != 0 || len(result.Next.ManagedRecords) != 0 {
				t.Fatalf("unowned record was claimed or mutated: creates=%d updates=%d deletes=%d next=%+v", len(fakeOutput.created), len(fakeOutput.updated), len(fakeOutput.deleted), result.Next.ManagedRecords)
			}

			fakeOutput = &reconcileFakeOutput{provider: provider}
			result, err = ReconcileOutput(context.Background(), ReconcileInput{
				Output:  fakeOutput,
				Visible: []contracts.VisibleRecord{visible},
				Owned:   state.Snapshot{InstallationID: installationID},
			})
			if err != nil {
				t.Fatalf("stale reconcile output: %v", err)
			}
			if len(fakeOutput.deleted) != 0 || len(result.Next.ManagedRecords) != 0 {
				t.Fatalf("unowned stale record was deleted or persisted: deletes=%d next=%+v", len(fakeOutput.deleted), result.Next.ManagedRecords)
			}
		})
	}
}

func TestCloudflareMarkerFlowRejectsAmbiguousDuplicates(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	fakeOutput := &reconcileFakeOutput{provider: provider}
	visible := []contracts.VisibleRecord{
		{Output: provider, Hostname: "stale.example.com", Answer: "10.0.0.12", Provenance: &contracts.RecordProvenance{RemoteID: "rec-a", InstallationID: installationID}},
		{Output: provider, Hostname: "STALE.example.com", Answer: "10.0.0.12", Provenance: &contracts.RecordProvenance{RemoteID: "rec-b", InstallationID: installationID}},
	}

	_, err := ReconcileOutput(context.Background(), ReconcileInput{Output: fakeOutput, Visible: visible, Owned: state.Snapshot{InstallationID: installationID}})
	if err == nil {
		t.Fatal("expected ambiguous marker-owned records to fail closed")
	}
	var ambiguityErr *ErrVisibleRecordAmbiguous
	if !errors.As(err, &ambiguityErr) {
		t.Fatalf("expected ErrVisibleRecordAmbiguous, got %T", err)
	}
	if len(fakeOutput.deleted) != 0 {
		t.Fatalf("expected no delete for ambiguous marker-owned records, got %d", len(fakeOutput.deleted))
	}
}

func TestCloudflareLegacyStateTagsOnlyMatchingRemoteRecord(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1"}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	desired := contracts.DesiredRecord{Hostname: "app.example.com", Answer: "10.0.0.10", Source: source}

	t.Run("matching remote ID", func(t *testing.T) {
		output := &reconcileFakeOutput{provider: provider, updateProvenance: &contracts.RecordProvenance{RemoteID: "rec-legacy", InstallationID: installationID}}
		result, err := ReconcileOutput(context.Background(), ReconcileInput{
			Output:  output,
			Desired: []contracts.DesiredRecord{desired},
			Visible: []contracts.VisibleRecord{{Output: provider, Hostname: desired.Hostname, Answer: desired.Answer, Provenance: &contracts.RecordProvenance{RemoteID: "rec-legacy"}}},
			Owned: state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{{
				Output: provider, Source: source, Hostname: desired.Hostname, Answer: desired.Answer, Provenance: &contracts.RecordProvenance{RemoteID: "rec-legacy"},
			}}},
		})
		if err != nil {
			t.Fatalf("reconcile output: %v", err)
		}
		if len(output.updated) != 1 || len(result.Next.ManagedRecords) != 1 || result.Next.ManagedRecords[0].Provenance.InstallationID != installationID {
			t.Fatalf("expected matching legacy record to be tagged, updates=%+v next=%+v", output.updated, result.Next.ManagedRecords)
		}
	})

	t.Run("matching remote ID with desired content and proxy drift", func(t *testing.T) {
		drifted := desired
		drifted.Answer = "10.0.0.11"
		drifted.Proxied = true
		output := &reconcileFakeOutput{provider: provider, updateProvenance: &contracts.RecordProvenance{RemoteID: "rec-legacy", InstallationID: installationID}}
		result, err := ReconcileOutput(context.Background(), ReconcileInput{
			Output:  output,
			Desired: []contracts.DesiredRecord{drifted},
			Visible: []contracts.VisibleRecord{{Output: provider, Hostname: desired.Hostname, Answer: desired.Answer, Provenance: &contracts.RecordProvenance{RemoteID: "rec-legacy"}}},
			Owned: state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{{
				Output: provider, Source: source, Hostname: desired.Hostname, Answer: desired.Answer, Provenance: &contracts.RecordProvenance{RemoteID: "rec-legacy"},
			}}},
		})
		if err != nil {
			t.Fatalf("reconcile output: %v", err)
		}
		if len(output.updated) != 1 || output.updated[0].To.Answer != drifted.Answer || !output.updated[0].To.Proxied || len(result.Next.ManagedRecords) != 1 || result.Next.ManagedRecords[0].Provenance.InstallationID != installationID {
			t.Fatalf("expected matching legacy record with drift to be tagged and updated, updates=%+v next=%+v", output.updated, result.Next.ManagedRecords)
		}
	})

	t.Run("mismatched remote ID", func(t *testing.T) {
		output := &reconcileFakeOutput{provider: provider}
		result, err := ReconcileOutput(context.Background(), ReconcileInput{
			Output:  output,
			Desired: []contracts.DesiredRecord{desired},
			Visible: []contracts.VisibleRecord{{Output: provider, Hostname: desired.Hostname, Answer: desired.Answer, Provenance: &contracts.RecordProvenance{RemoteID: "rec-other"}}},
			Owned: state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{{
				Output: provider, Source: source, Hostname: desired.Hostname, Answer: desired.Answer, Provenance: &contracts.RecordProvenance{RemoteID: "rec-legacy"},
			}}},
		})
		if err != nil {
			t.Fatalf("reconcile output: %v", err)
		}
		if len(output.updated) != 0 || len(output.deleted) != 0 || len(result.Next.ManagedRecords) != 1 {
			t.Fatalf("mismatched legacy record was mutated: updates=%d deletes=%d next=%+v", len(output.updated), len(output.deleted), result.Next.ManagedRecords)
		}
	})
}

func TestNonAdGuardStaleOwnedSameKeyWithoutOwnershipProofRetainsState(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1", DisplayName: "svc"}
	fakeOutput := &reconcileFakeOutput{provider: provider}
	owned := state.Snapshot{ManagedRecords: []state.ManagedRecord{{
		Output:   provider,
		Source:   source,
		Hostname: "old.local",
		Answer:   "10.0.0.12",
	}}}

	result, err := ReconcileOutput(context.Background(), ReconcileInput{
		Output:  fakeOutput,
		Desired: nil,
		Visible: []contracts.VisibleRecord{
			{Output: provider, Hostname: "OLD.local", Answer: "10.0.0.12", Provenance: &contracts.RecordProvenance{RemoteID: "rec-replaced"}},
			{Output: provider, Hostname: "manual.local", Answer: "10.0.0.50"},
		},
		Owned: owned,
	})
	if err != nil {
		t.Fatalf("reconcile output: %v", err)
	}

	if got := len(fakeOutput.deleted); got != 0 {
		t.Fatalf("expected no non-AdGuard delete calls without ownership proof, got %d", got)
	}
	if got := len(result.Next.ManagedRecords); got != 1 {
		t.Fatalf("expected stale owned state to be retained without ownership proof, got %d managed records", got)
	}
	if result.Next.ManagedRecords[0].Hostname != "old.local" || result.Next.ManagedRecords[0].Answer != "10.0.0.12" {
		t.Fatalf("expected retained stale owned record to stay unchanged, got %+v", result.Next.ManagedRecords[0])
	}
}

func TestRetainedStaleOwnedStateAllowsLaterDisplayLineageUpdateWithInstallationMarker(t *testing.T) {
	t.Parallel()

	provider := contracts.ProviderRef{Type: "cloudflare", Name: "primary"}
	source := contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1", DisplayName: "svc"}
	nextSource := contracts.SourceObjectRef{Provider: source.Provider, ID: "ctr-2", DisplayName: source.DisplayName}
	installationID := "123e4567-e89b-42d3-a456-426614174000"
	fakeOutput := &reconcileFakeOutput{provider: provider}
	owned := state.Snapshot{InstallationID: installationID, ManagedRecords: []state.ManagedRecord{{
		Output:     provider,
		Source:     source,
		Hostname:   "whoami.test",
		Answer:     "127.0.0.1",
		Provenance: &contracts.RecordProvenance{RemoteID: "rec-whoami"},
	}}}

	first, err := ReconcileOutput(context.Background(), ReconcileInput{
		Output:  fakeOutput,
		Desired: nil,
		Visible: []contracts.VisibleRecord{{Output: provider, Hostname: "whoami.test", Answer: "127.0.0.1", Provenance: &contracts.RecordProvenance{RemoteID: "rec-whoami", InstallationID: installationID}}},
		Owned:   owned,
		Grace:   time.Hour,
	})
	if err != nil {
		t.Fatalf("first reconcile output: %v", err)
	}
	if got := len(first.Next.ManagedRecords); got != 1 {
		t.Fatalf("expected retained managed state across recreate gap, got %d managed records", got)
	}

	second, err := ReconcileOutput(context.Background(), ReconcileInput{
		Output:  fakeOutput,
		Desired: []contracts.DesiredRecord{{Hostname: "whoami.test", Answer: "127.0.0.2", Source: nextSource}},
		Visible: []contracts.VisibleRecord{{Output: provider, Hostname: "whoami.test", Answer: "127.0.0.1", Provenance: &contracts.RecordProvenance{RemoteID: "rec-whoami", InstallationID: installationID}}},
		Owned:   first.Next,
	})
	if err != nil {
		t.Fatalf("second reconcile output: %v", err)
	}
	if got := len(fakeOutput.created); got != 0 {
		t.Fatalf("expected no create calls once managed state is retained, got %d", got)
	}
	if got := len(fakeOutput.updated); got != 1 {
		t.Fatalf("expected 1 update call after recreate gap, got %d", got)
	}
	if got := len(second.Next.ManagedRecords); got != 1 {
		t.Fatalf("expected one updated managed record, got %d", got)
	}
	if second.Next.ManagedRecords[0].Source.ID != "ctr-2" || second.Next.ManagedRecords[0].Answer != "127.0.0.2" {
		t.Fatalf("expected managed state to roll forward after update, got %+v", second.Next.ManagedRecords[0])
	}
}
