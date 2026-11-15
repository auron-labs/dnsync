package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type Store struct {
	path           string
	installationID string
}

func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("state path is required")
	}

	store := &Store{path: path}
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat state file: %w", err)
		}

		snapshot := EmptySnapshot()
		var idErr error
		snapshot.InstallationID, idErr = newInstallationID()
		if idErr != nil {
			return nil, idErr
		}
		if err := store.Save(snapshot); err != nil {
			return nil, err
		}
	}

	if _, err := store.Load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load() (Snapshot, error) {
	snapshot, migrated, err := s.load()
	if err != nil {
		return Snapshot{}, err
	}
	if migrated {
		if err := s.Save(snapshot); err != nil {
			return Snapshot{}, fmt.Errorf("persist migrated state file: %w", err)
		}
	} else {
		s.installationID = snapshot.InstallationID
	}
	return snapshot, nil
}

func (s *Store) load() (Snapshot, bool, error) {
	payload, err := os.ReadFile(s.path)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("read state file: %w", err)
	}

	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return Snapshot{}, false, fmt.Errorf("decode state file: %w", err)
	}

	migrated := false
	switch snapshot.Version {
	case 1, 2:
		migrated = true
		snapshot.Version = SnapshotVersion
		if snapshot.InstallationID == "" {
			id, err := newInstallationID()
			if err != nil {
				return Snapshot{}, false, err
			}
			snapshot.InstallationID = id
		}
	case SnapshotVersion:
	default:
		return Snapshot{}, false, fmt.Errorf("unsupported state snapshot version %d", snapshot.Version)
	}
	if err := validateInstallationID(snapshot.InstallationID); err != nil {
		return Snapshot{}, false, fmt.Errorf("invalid state snapshot installation ID: %w", err)
	}

	if snapshot.ManagedRecords == nil {
		snapshot.ManagedRecords = []ManagedRecord{}
	}
	if snapshot.ManagedTunnels == nil {
		snapshot.ManagedTunnels = []ManagedTunnel{}
	}

	return snapshot, migrated, nil
}

func (s *Store) Save(snapshot Snapshot) error {
	// In-memory callers may start from EmptySnapshot; inherit the persisted
	// identity rather than generating or accepting a replacement identity.
	if snapshot.InstallationID == "" {
		snapshot.InstallationID = s.installationID
	}
	if err := validateInstallationID(snapshot.InstallationID); err != nil {
		return fmt.Errorf("invalid state snapshot installation ID: %w", err)
	}
	if s.installationID != "" && snapshot.InstallationID != s.installationID {
		return errors.New("state snapshot installation ID does not match store identity")
	}

	snapshot.Version = SnapshotVersion
	if snapshot.ManagedRecords == nil {
		snapshot.ManagedRecords = []ManagedRecord{}
	}
	if snapshot.ManagedTunnels == nil {
		snapshot.ManagedTunnels = []ManagedTunnel{}
	}

	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state snapshot: %w", err)
	}

	payload = append(payload, '\n')
	if err := atomicWriteFile(s.path, payload, 0o600); err != nil {
		return err
	}
	s.installationID = snapshot.InstallationID

	return nil
}
