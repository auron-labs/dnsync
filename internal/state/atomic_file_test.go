package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronflorey/dnsync/internal/contracts"
)

func TestAtomicWriteReplacesStateFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ownership.json")
	if err := os.WriteFile(path, []byte("old-state\n"), 0o600); err != nil {
		t.Fatalf("seed state file: %v", err)
	}

	if err := atomicWriteFile(path, []byte("new-state\n"), 0o600); err != nil {
		t.Fatalf("atomic write: %v", err)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	if string(payload) != "new-state\n" {
		t.Fatalf("expected replaced state contents, got %q", string(payload))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read state directory: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "ownership.json" {
		t.Fatalf("expected only ownership.json to remain, got %#v", entries)
	}
}

func TestAtomicWriteCreatesNestedStateDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "nested", "ownership.json")
	if err := atomicWriteFile(path, []byte("new-state\n"), 0o600); err != nil {
		t.Fatalf("atomic write: %v", err)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	if string(payload) != "new-state\n" {
		t.Fatalf("expected nested state contents, got %q", string(payload))
	}
}

func TestStoreLoadLegacySnapshotWithoutProvenance(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	payload := []byte(`{
  "version": 1,
  "managed_records": [
    {
      "output": {"type": "cloudflare", "name": "primary"},
      "source": {
        "provider": {"type": "docker", "name": "local"},
        "id": "ctr-1",
        "display_name": "svc"
      },
      "hostname": "app.example.com",
      "answer": "10.0.0.10",
      "last_applied_at": "2026-05-13T01:00:00Z"
    }
  ]
}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write legacy snapshot: %v", err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(snapshot.ManagedRecords) != 1 {
		t.Fatalf("expected 1 managed record, got %d", len(snapshot.ManagedRecords))
	}
	if snapshot.Version != SnapshotVersion {
		t.Fatalf("expected legacy snapshot to migrate to version %d, got %d", SnapshotVersion, snapshot.Version)
	}
	if err := validateInstallationID(snapshot.InstallationID); err != nil {
		t.Fatalf("expected migrated installation ID to be valid: %v", err)
	}
	if snapshot.ManagedRecords[0].Provenance != nil {
		t.Fatalf("expected legacy record provenance to remain nil, got %+v", snapshot.ManagedRecords[0].Provenance)
	}

	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated state: %v", err)
	}
	var migrated Snapshot
	if err := json.Unmarshal(persisted, &migrated); err != nil {
		t.Fatalf("decode migrated state: %v", err)
	}
	if migrated.Version != SnapshotVersion || migrated.InstallationID != snapshot.InstallationID {
		t.Fatalf("expected migrated identity to be persisted, got %+v", migrated)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	restarted, err := reopened.Load()
	if err != nil {
		t.Fatalf("load reopened store: %v", err)
	}
	if restarted.InstallationID != snapshot.InstallationID {
		t.Fatalf("expected installation ID to remain stable after restart: first=%q restarted=%q", snapshot.InstallationID, restarted.InstallationID)
	}
}

func TestNewStoreGeneratesStableUUIDv4InstallationID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	first, err := store.Load()
	if err != nil {
		t.Fatalf("load initial state: %v", err)
	}
	if err := validateInstallationID(first.InstallationID); err != nil {
		t.Fatalf("expected UUID v4 installation ID: %v", err)
	}
	if first.InstallationID[14] != '4' || !strings.ContainsRune("89ab", rune(first.InstallationID[19])) {
		t.Fatalf("expected UUID v4 version and variant bits, got %q", first.InstallationID)
	}

	restarted, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	second, err := restarted.Load()
	if err != nil {
		t.Fatalf("load restarted state: %v", err)
	}
	if second.InstallationID != first.InstallationID {
		t.Fatalf("expected installation ID to remain stable: first=%q second=%q", first.InstallationID, second.InstallationID)
	}
}

func TestStoreMigratesV2SnapshotWithStableInstallationID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"managed_records":[],"managed_tunnels":[]}`), 0o600); err != nil {
		t.Fatalf("write v2 snapshot: %v", err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	first, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if first.Version != SnapshotVersion || validateInstallationID(first.InstallationID) != nil {
		t.Fatalf("expected valid migrated v2 snapshot, got %+v", first)
	}

	if err := store.Save(first); err != nil {
		t.Fatalf("save migrated snapshot: %v", err)
	}
	second, err := store.Load()
	if err != nil {
		t.Fatalf("reload migrated snapshot: %v", err)
	}
	if second.InstallationID != first.InstallationID {
		t.Fatalf("expected v2 installation ID to remain stable: first=%q second=%q", first.InstallationID, second.InstallationID)
	}
}

func TestStoreRejectsInvalidNewSnapshotInstallationID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "not-a-uuid", "123e4567-e89b-12d3-a456-426614174000"} {
		id := id
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "state.json")
			payload, err := json.Marshal(Snapshot{Version: SnapshotVersion, InstallationID: id, ManagedRecords: []ManagedRecord{}, ManagedTunnels: []ManagedTunnel{}})
			if err != nil {
				t.Fatalf("marshal invalid snapshot: %v", err)
			}
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatalf("write invalid snapshot: %v", err)
			}

			if _, err := NewStore(path); err == nil || !strings.Contains(err.Error(), "installation ID") {
				t.Fatalf("expected clear installation ID error, got %v", err)
			}
		})
	}
}

func TestStoreSaveLoadRoundTripsTunnelStateWithoutToken(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	deadline := time.Date(2026, 8, 1, 5, 10, 0, 0, time.UTC)
	want := Snapshot{
		ManagedRecords: []ManagedRecord{{Hostname: "app.example.com", Answer: "tunnel-id.cfargotunnel.com", Proxied: true, RemoveAfter: &deadline}},
		ManagedTunnels: []ManagedTunnel{{Output: contracts.ProviderRef{Type: "cloudflare", Name: "primary"}, Source: contracts.ProviderRef{Type: "docker", Name: "local"}, AccountID: "account", Name: "dnsync", TunnelID: "tunnel-id", SidecarID: "container-id", Image: "cloudflare/cloudflared:2026.7.3", Routes: []ManagedTunnelRoute{{Hostname: "app.example.com", Service: "http://10.0.0.2:8080", NetworkIDs: []string{"network-id"}, RemoveAfter: &deadline}}}},
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(payload), "token") {
		t.Fatalf("state payload must not contain tunnel token material: %s", payload)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.Version != SnapshotVersion || len(got.ManagedTunnels) != 1 || !got.ManagedRecords[0].Proxied || got.ManagedTunnels[0].Routes[0].RemoveAfter == nil {
		t.Fatalf("unexpected tunnel state round trip: %+v", got)
	}
}

func TestStoreSaveLoadRoundTripsProvenance(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	now := time.Date(2026, 5, 13, 1, 0, 0, 0, time.UTC)
	want := Snapshot{ManagedRecords: []ManagedRecord{{
		Output:   contracts.ProviderRef{Type: "cloudflare", Name: "primary"},
		Source:   contracts.SourceObjectRef{Provider: contracts.ProviderRef{Type: "docker", Name: "local"}, ID: "ctr-1", DisplayName: "svc"},
		Hostname: "app.example.com",
		Answer:   "10.0.0.10",
		Provenance: &contracts.RecordProvenance{
			RemoteID: "rec-123",
		},
		LastAppliedAt: now,
	}}}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(got.ManagedRecords) != 1 {
		t.Fatalf("expected 1 managed record, got %d", len(got.ManagedRecords))
	}
	if got.ManagedRecords[0].Provenance == nil || got.ManagedRecords[0].Provenance.RemoteID != "rec-123" {
		t.Fatalf("expected provenance remote ID rec-123, got %+v", got.ManagedRecords[0].Provenance)
	}
}
