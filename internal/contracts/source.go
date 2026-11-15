package contracts

import (
	"context"
	"sort"
)

type ProviderRef struct {
	Type string `json:"Type"`
	Name string `json:"Name"`
}

type SourceObjectRef struct {
	Provider    ProviderRef `json:"Provider"`
	ID          string      `json:"ID"`
	DisplayName string      `json:"DisplayName"`
}

type DesiredRecord struct {
	Hostname string
	Answer   string
	Source   SourceObjectRef
	Output   string
	Proxied  bool
}

type DesiredTunnelRoute struct {
	Hostname   string
	Service    string
	Source     SourceObjectRef
	NetworkIDs []string
	Output     string
}

type DesiredSnapshot struct {
	Records      []DesiredRecord
	TunnelRoutes []DesiredTunnelRoute
}

func (s DesiredSnapshot) Sorted() DesiredSnapshot {
	next := DesiredSnapshot{
		Records:      append([]DesiredRecord(nil), s.Records...),
		TunnelRoutes: append([]DesiredTunnelRoute(nil), s.TunnelRoutes...),
	}
	sort.Slice(next.Records, func(i, j int) bool {
		left, right := next.Records[i], next.Records[j]
		return left.Hostname+"|"+left.Answer+"|"+left.Source.ID < right.Hostname+"|"+right.Answer+"|"+right.Source.ID
	})
	sort.Slice(next.TunnelRoutes, func(i, j int) bool {
		left, right := next.TunnelRoutes[i], next.TunnelRoutes[j]
		return left.Hostname+"|"+left.Service+"|"+left.Source.ID < right.Hostname+"|"+right.Service+"|"+right.Source.ID
	})
	return next
}

type SourceWatch struct {
	Hints <-chan struct{}
	Err   <-chan error
}

type Source interface {
	Provider() ProviderRef
	ListDesired(context.Context) (DesiredSnapshot, error)
}

type TunnelSidecarSpec struct {
	Output     ProviderRef
	TunnelID   string
	Name       string
	Image      string
	Token      string
	NetworkIDs []string
}

type TunnelSidecarSource interface {
	EnsureTunnelSidecar(context.Context, TunnelSidecarSpec) (string, error)
	DeleteTunnelSidecar(context.Context, string, TunnelSidecarSpec) error
}

type WatchableSource interface {
	Watch(context.Context) SourceWatch
}
