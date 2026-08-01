package state

import (
	"time"

	"github.com/aaronflorey/docker-dns-sync/internal/contracts"
)

const SnapshotVersion = 2

type Snapshot struct {
	Version        int             `json:"version"`
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
