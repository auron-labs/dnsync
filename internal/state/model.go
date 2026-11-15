package state

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/auron-labs/dnsync/internal/contracts"
)

const SnapshotVersion = 3

type Snapshot struct {
	Version        int             `json:"version"`
	InstallationID string          `json:"installation_id"`
	ManagedRecords []ManagedRecord `json:"managed_records"`
	ManagedTunnels []ManagedTunnel `json:"managed_tunnels"`
}

type ManagedRecord struct {
	Output        contracts.ProviderRef       `json:"output"`
	Source        contracts.SourceObjectRef   `json:"source"`
	Hostname      string                      `json:"hostname"`
	Answer        string                      `json:"answer"`
	Proxied       bool                        `json:"proxied,omitempty"`
	Provenance    *contracts.RecordProvenance `json:"provenance,omitempty"`
	LastAppliedAt time.Time                   `json:"last_applied_at"`
	RemoveAfter   *time.Time                  `json:"remove_after,omitempty"`
}

type ManagedTunnel struct {
	Output        contracts.ProviderRef `json:"output"`
	Source        contracts.ProviderRef `json:"source"`
	AccountID     string                `json:"account_id"`
	Name          string                `json:"name"`
	TunnelID      string                `json:"tunnel_id"`
	SidecarID     string                `json:"sidecar_id,omitempty"`
	Image         string                `json:"image"`
	Routes        []ManagedTunnelRoute  `json:"routes"`
	LastAppliedAt time.Time             `json:"last_applied_at"`
	RemoveAfter   *time.Time            `json:"remove_after,omitempty"`
}

type ManagedTunnelRoute struct {
	Hostname    string                    `json:"hostname"`
	Service     string                    `json:"service"`
	Source      contracts.SourceObjectRef `json:"source"`
	NetworkIDs  []string                  `json:"network_ids,omitempty"`
	RemoveAfter *time.Time                `json:"remove_after,omitempty"`
}

func EmptySnapshot() Snapshot {
	return Snapshot{
		Version:        SnapshotVersion,
		ManagedRecords: []ManagedRecord{},
		ManagedTunnels: []ManagedTunnel{},
	}
}

func newInstallationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate installation ID: %w", err)
	}

	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return strings.Join([]string{
		encoded[:8],
		encoded[8:12],
		encoded[12:16],
		encoded[16:20],
		encoded[20:],
	}, "-"), nil
}

func validateInstallationID(id string) error {
	if id == "" {
		return errors.New("installation ID is required")
	}
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return fmt.Errorf("installation ID %q must be a canonical UUID v4", id)
	}
	for i, char := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !isHexDigit(char) {
			return fmt.Errorf("installation ID %q must be a canonical UUID v4", id)
		}
	}
	if id[14] != '4' || !strings.ContainsRune("89abAB", rune(id[19])) {
		return fmt.Errorf("installation ID %q must be a canonical UUID v4", id)
	}
	return nil
}

func isHexDigit(char rune) bool {
	return char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F'
}
