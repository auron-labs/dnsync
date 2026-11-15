package contracts

import "context"

type RecordProvenance struct {
	RemoteID       string `json:"RemoteID"`
	InstallationID string `json:"InstallationID,omitempty"`
}

type VisibleRecord struct {
	Output     ProviderRef
	Hostname   string
	Answer     string
	Provenance *RecordProvenance
	Proxied    bool
}

type Output interface {
	Provider() ProviderRef
	ListVisible(context.Context) ([]VisibleRecord, error)
	Create(context.Context, DesiredRecord) (*RecordProvenance, error)
	Update(context.Context, VisibleRecord, DesiredRecord) (*RecordProvenance, error)
	Delete(context.Context, VisibleRecord) error
}

// ClosableOutput is implemented by outputs that hold remote resources, such as
// API sessions, which the runtime releases when it stops.
type ClosableOutput interface {
	Close(context.Context) error
}

type TunnelInfo struct {
	ID   string
	Name string
}

type TunnelOutput interface {
	EnsureTunnel(context.Context, string, string, string) (TunnelInfo, error)
	ConfigureTunnel(context.Context, string, string, []DesiredTunnelRoute) error
	TunnelToken(context.Context, string, string) (string, error)
	DeleteTunnel(context.Context, string, string) error
}
