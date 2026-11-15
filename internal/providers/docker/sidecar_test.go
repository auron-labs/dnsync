package docker

import (
	"context"
	"reflect"
	"testing"

	"github.com/auron-labs/dnsync/internal/contracts"
	containertypes "github.com/moby/moby/api/types/container"
)

func TestEnsureTunnelSidecarCreatesOnlyAfterMissingImagePull(t *testing.T) {
	t.Parallel()
	client := &sidecarClientFake{inspectErr: errSidecarNotFound, imagePresent: false}
	p := &Provider{ref: contracts.ProviderRef{Type: "docker", Name: "local"}, sidecars: client}
	id, err := p.EnsureTunnelSidecar(context.Background(), testSidecarSpec())
	if err != nil {
		t.Fatal(err)
	}
	if id != "created" || client.pulls != 1 || client.creates != 1 || client.starts != 1 {
		t.Fatalf("unexpected lifecycle: %+v", client)
	}
	if !reflect.DeepEqual(client.connected, []string{"net-a:created", "net-b:created"}) {
		t.Fatalf("unexpected sidecar networks: %v", client.connected)
	}
}

func TestEnsureTunnelSidecarSkipsPullForLocalImageAndReconcilesNetworks(t *testing.T) {
	t.Parallel()
	spec := testSidecarSpec()
	labels := tunnelSidecarLabels(spec.Output, contracts.ProviderRef{Type: "docker", Name: "local"}, spec.TunnelID, spec.Name)
	client := &sidecarClientFake{imagePresent: true, container: tunnelSidecarContainer{ID: "existing", Image: spec.Image, Token: spec.Token, Labels: labels, Networks: []string{"net-a", "obsolete"}}}
	p := &Provider{ref: contracts.ProviderRef{Type: "docker", Name: "local"}, sidecars: client}
	if _, err := p.EnsureTunnelSidecar(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if client.pulls != 0 || client.creates != 0 || client.starts != 1 {
		t.Fatalf("local image sidecar was unexpectedly recreated: %+v", client)
	}
	if !reflect.DeepEqual(client.connected, []string{"net-b:existing"}) || !reflect.DeepEqual(client.disconnected, []string{"obsolete:existing"}) {
		t.Fatalf("unexpected network reconciliation: connects=%v disconnects=%v", client.connected, client.disconnected)
	}
}

func TestEnsureTunnelSidecarRecreatesOwnedDriftAndRejectsUnownedConflict(t *testing.T) {
	t.Parallel()
	spec := testSidecarSpec()
	labels := tunnelSidecarLabels(spec.Output, contracts.ProviderRef{Type: "docker", Name: "local"}, spec.TunnelID, spec.Name)
	client := &sidecarClientFake{imagePresent: true, container: tunnelSidecarContainer{ID: "old", Image: "old-image", Token: spec.Token, Labels: labels, Running: true}}
	p := &Provider{ref: contracts.ProviderRef{Type: "docker", Name: "local"}, sidecars: client}
	if _, err := p.EnsureTunnelSidecar(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.removed, []string{"old"}) || client.creates != 1 || client.pulls != 0 {
		t.Fatalf("owned drift was not safely recreated: %+v", client)
	}
	client.container = tunnelSidecarContainer{ID: "operator", Labels: map[string]string{}}
	client.inspectErr = nil
	if _, err := p.EnsureTunnelSidecar(context.Background(), spec); err == nil {
		t.Fatal("expected ownership conflict")
	}
	if len(client.removed) != 1 {
		t.Fatalf("unowned container was touched: %v", client.removed)
	}
}

func testSidecarSpec() contracts.TunnelSidecarSpec {
	return contracts.TunnelSidecarSpec{Output: contracts.ProviderRef{Type: "cloudflare", Name: "primary"}, TunnelID: "tunnel-1", Name: "main", Image: "cloudflared:test", Token: "secret", NetworkIDs: []string{"net-b", "net-a"}}
}

type sidecarClientFake struct {
	container               tunnelSidecarContainer
	inspectErr              error
	imagePresent            bool
	pulls, creates, starts  int
	connected, disconnected []string
	removed                 []string
}

func (f *sidecarClientFake) InspectContainer(context.Context, string) (tunnelSidecarContainer, error) {
	return f.container, f.inspectErr
}

func (f *sidecarClientFake) ImageExists(context.Context, string) (bool, error) {
	return f.imagePresent, nil
}
func (f *sidecarClientFake) PullImage(context.Context, string) error { f.pulls++; return nil }
func (f *sidecarClientFake) CreateContainer(_ context.Context, _ string, _ *containertypes.Config, _ *containertypes.HostConfig) (string, error) {
	f.creates++
	return "created", nil
}
func (f *sidecarClientFake) StartContainer(context.Context, string) error { f.starts++; return nil }
func (f *sidecarClientFake) RemoveContainer(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

func (f *sidecarClientFake) ConnectNetwork(_ context.Context, networkID, containerID string) error {
	f.connected = append(f.connected, networkID+":"+containerID)
	return nil
}

func (f *sidecarClientFake) DisconnectNetwork(_ context.Context, networkID, containerID string) error {
	f.disconnected = append(f.disconnected, networkID+":"+containerID)
	return nil
}

var _ tunnelSidecarClient = (*sidecarClientFake)(nil)
