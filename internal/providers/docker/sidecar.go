package docker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/aaronflorey/docker-dns-sync/internal/contracts"
	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"
)

const (
	sidecarManagedLabel = "com.aaronflorey.docker-dns-sync.managed"
	sidecarOutputLabel  = "com.aaronflorey.docker-dns-sync.output"
	sidecarSourceLabel  = "com.aaronflorey.docker-dns-sync.source"
	sidecarTunnelLabel  = "com.aaronflorey.docker-dns-sync.tunnel-id"
	sidecarNameLabel    = "com.aaronflorey.docker-dns-sync.tunnel-name"
)

var errSidecarNotFound = errors.New("tunnel sidecar not found")

type tunnelSidecarContainer struct {
	ID       string
	Name     string
	Image    string
	Token    string
	Labels   map[string]string
	Running  bool
	Networks []string
}

type tunnelSidecarClient interface {
	InspectContainer(context.Context, string) (tunnelSidecarContainer, error)
	ImageExists(context.Context, string) (bool, error)
	PullImage(context.Context, string) error
	CreateContainer(context.Context, string, *containertypes.Config, *containertypes.HostConfig) (string, error)
	StartContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
	ConnectNetwork(context.Context, string, string) error
	DisconnectNetwork(context.Context, string, string) error
}

func (c mobyTunnelSidecarClient) ImageExists(ctx context.Context, image string) (bool, error) {
	_, err := c.client.ImageInspect(ctx, image)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

type mobyTunnelSidecarClient struct {
	client *mobyclient.Client
}

func (c mobyTunnelSidecarClient) InspectContainer(ctx context.Context, name string) (tunnelSidecarContainer, error) {
	result, err := c.client.ContainerInspect(ctx, name, mobyclient.ContainerInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return tunnelSidecarContainer{}, errSidecarNotFound
		}
		return tunnelSidecarContainer{}, err
	}
	container := result.Container
	item := tunnelSidecarContainer{ID: container.ID, Name: strings.TrimPrefix(container.Name, "/")}
	if container.Config != nil {
		item.Image = container.Config.Image
		item.Labels = container.Config.Labels
		for _, value := range container.Config.Env {
			if token, ok := strings.CutPrefix(value, "TUNNEL_TOKEN="); ok {
				item.Token = token
				break
			}
		}
		if item.Token == "" {
			for index, value := range container.Config.Cmd {
				if value == "--token" && index+1 < len(container.Config.Cmd) {
					item.Token = container.Config.Cmd[index+1]
					break
				}
			}
		}
	}
	if container.State != nil {
		item.Running = container.State.Running
	}
	if container.NetworkSettings != nil {
		for networkName, endpoint := range container.NetworkSettings.Networks {
			if endpoint == nil {
				continue
			}
			networkID := strings.TrimSpace(endpoint.NetworkID)
			if networkID == "" {
				networkID = networkName
			}
			item.Networks = append(item.Networks, networkID)
		}
		sort.Strings(item.Networks)
	}
	return item, nil
}

func (c mobyTunnelSidecarClient) PullImage(ctx context.Context, image string) error {
	response, err := c.client.ImagePull(ctx, image, mobyclient.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer response.Close()
	return response.Wait(ctx)
}

func (c mobyTunnelSidecarClient) CreateContainer(ctx context.Context, name string, cfg *containertypes.Config, hostCfg *containertypes.HostConfig) (string, error) {
	result, err := c.client.ContainerCreate(ctx, mobyclient.ContainerCreateOptions{Name: name, Config: cfg, HostConfig: hostCfg})
	return result.ID, err
}

func (c mobyTunnelSidecarClient) StartContainer(ctx context.Context, id string) error {
	_, err := c.client.ContainerStart(ctx, id, mobyclient.ContainerStartOptions{})
	return err
}

func (c mobyTunnelSidecarClient) RemoveContainer(ctx context.Context, id string) error {
	_, err := c.client.ContainerRemove(ctx, id, mobyclient.ContainerRemoveOptions{Force: true})
	return err
}

func (c mobyTunnelSidecarClient) ConnectNetwork(ctx context.Context, networkID, containerID string) error {
	_, err := c.client.NetworkConnect(ctx, networkID, mobyclient.NetworkConnectOptions{Container: containerID, EndpointConfig: &network.EndpointSettings{}})
	return err
}

func (c mobyTunnelSidecarClient) DisconnectNetwork(ctx context.Context, networkID, containerID string) error {
	_, err := c.client.NetworkDisconnect(ctx, networkID, mobyclient.NetworkDisconnectOptions{Container: containerID, Force: true})
	return err
}

func (p *Provider) EnsureTunnelSidecar(ctx context.Context, spec contracts.TunnelSidecarSpec) (string, error) {
	if p.sidecars == nil {
		return "", errors.New("docker tunnel sidecar client is unavailable")
	}
	if strings.TrimSpace(spec.TunnelID) == "" || strings.TrimSpace(spec.Image) == "" || strings.TrimSpace(spec.Token) == "" {
		return "", errors.New("docker tunnel sidecar tunnel ID, image, and token are required")
	}

	name := tunnelSidecarName(spec.Output, p.ref, spec.Name)
	labels := tunnelSidecarLabels(spec.Output, p.ref, spec.TunnelID, spec.Name)
	existing, err := p.sidecars.InspectContainer(ctx, name)
	switch {
	case err == nil:
		if existing.Labels[sidecarManagedLabel] != "true" {
			return "", fmt.Errorf("docker container name %q is already used by an unowned container", name)
		}
		if !sidecarIdentityMatches(existing.Labels, labels) {
			return "", fmt.Errorf("docker container name %q is owned by a different tunnel", name)
		}
		if tunnelSidecarDrifted(existing, spec, labels) {
			if err := p.ensureTunnelSidecarImage(ctx, spec.Image); err != nil {
				return "", err
			}
			if err := p.sidecars.RemoveContainer(ctx, existing.ID); err != nil {
				return "", fmt.Errorf("remove drifted cloudflared sidecar: %w", err)
			}
			return p.createTunnelSidecar(ctx, name, spec, labels)
		}
		if err := p.reconcileSidecarNetworks(ctx, existing.ID, existing.Networks, spec.NetworkIDs); err != nil {
			return "", err
		}
		if !existing.Running {
			if err := p.sidecars.StartContainer(ctx, existing.ID); err != nil {
				return "", fmt.Errorf("start cloudflared sidecar: %w", err)
			}
		}
		return existing.ID, nil
	case errors.Is(err, errSidecarNotFound):
		if err := p.ensureTunnelSidecarImage(ctx, spec.Image); err != nil {
			return "", err
		}
		return p.createTunnelSidecar(ctx, name, spec, labels)
	default:
		return "", fmt.Errorf("inspect cloudflared sidecar: %w", err)
	}
}

func (p *Provider) ensureTunnelSidecarImage(ctx context.Context, image string) error {
	present, err := p.sidecars.ImageExists(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect cloudflared image: %w", err)
	}
	if present {
		return nil
	}
	if err := p.sidecars.PullImage(ctx, image); err != nil {
		return fmt.Errorf("pull cloudflared image: %w", err)
	}
	return nil
}

func (p *Provider) createTunnelSidecar(ctx context.Context, name string, spec contracts.TunnelSidecarSpec, labels map[string]string) (string, error) {
	id, err := p.sidecars.CreateContainer(ctx, name, &containertypes.Config{
		Image:  spec.Image,
		Cmd:    []string{"tunnel", "--no-autoupdate", "run", "--token", spec.Token},
		Labels: labels,
	}, &containertypes.HostConfig{
		NetworkMode: containertypes.NetworkMode("none"),
		RestartPolicy: containertypes.RestartPolicy{
			Name: containertypes.RestartPolicyUnlessStopped,
		},
	})
	if err != nil {
		return "", fmt.Errorf("create cloudflared sidecar: %w", err)
	}
	if err := p.reconcileSidecarNetworks(ctx, id, nil, spec.NetworkIDs); err != nil {
		_ = p.sidecars.RemoveContainer(ctx, id)
		return "", err
	}
	if err := p.sidecars.StartContainer(ctx, id); err != nil {
		_ = p.sidecars.RemoveContainer(ctx, id)
		return "", fmt.Errorf("start cloudflared sidecar: %w", err)
	}
	return id, nil
}

func (p *Provider) reconcileSidecarNetworks(ctx context.Context, containerID string, current, desired []string) error {
	currentSet := stringSet(current)
	desiredSet := stringSet(desired)
	for _, networkID := range sortedSetKeys(desiredSet) {
		if _, ok := currentSet[networkID]; ok {
			continue
		}
		if err := p.sidecars.ConnectNetwork(ctx, networkID, containerID); err != nil {
			return fmt.Errorf("connect cloudflared sidecar to network %s: %w", networkID, err)
		}
	}
	for _, networkID := range sortedSetKeys(currentSet) {
		if _, ok := desiredSet[networkID]; ok || networkID == "none" {
			continue
		}
		if err := p.sidecars.DisconnectNetwork(ctx, networkID, containerID); err != nil {
			return fmt.Errorf("disconnect cloudflared sidecar from network %s: %w", networkID, err)
		}
	}
	return nil
}

func (p *Provider) DeleteTunnelSidecar(ctx context.Context, containerID string, expected contracts.TunnelSidecarSpec) error {
	if p.sidecars == nil {
		return errors.New("docker tunnel sidecar client is unavailable")
	}
	lookup := strings.TrimSpace(containerID)
	if lookup == "" {
		lookup = tunnelSidecarName(expected.Output, p.ref, expected.Name)
	}
	existing, err := p.sidecars.InspectContainer(ctx, lookup)
	if errors.Is(err, errSidecarNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect cloudflared sidecar for removal: %w", err)
	}
	if existing.Labels[sidecarManagedLabel] != "true" {
		return fmt.Errorf("refusing to remove unowned docker container %q", existing.Name)
	}
	for key, value := range tunnelSidecarLabels(expected.Output, p.ref, expected.TunnelID, expected.Name) {
		if existing.Labels[key] != value {
			return fmt.Errorf("refusing to remove cloudflared sidecar with mismatched %s label", key)
		}
	}
	if err := p.sidecars.RemoveContainer(ctx, existing.ID); err != nil {
		return fmt.Errorf("remove cloudflared sidecar: %w", err)
	}
	return nil
}

func tunnelSidecarDrifted(existing tunnelSidecarContainer, spec contracts.TunnelSidecarSpec, labels map[string]string) bool {
	if existing.Image != spec.Image || existing.Token != spec.Token {
		return true
	}
	for key, value := range labels {
		if existing.Labels[key] != value {
			return true
		}
	}
	return false
}

func sidecarIdentityMatches(existing, expected map[string]string) bool {
	for _, key := range []string{sidecarOutputLabel, sidecarSourceLabel, sidecarTunnelLabel, sidecarNameLabel} {
		if existing[key] != expected[key] {
			return false
		}
	}
	return true
}

func tunnelSidecarLabels(output, source contracts.ProviderRef, tunnelID, tunnelName string) map[string]string {
	return map[string]string{
		sidecarManagedLabel: "true",
		sidecarOutputLabel:  output.Type + "/" + output.Name,
		sidecarSourceLabel:  source.Type + "/" + source.Name,
		sidecarTunnelLabel:  tunnelID,
		sidecarNameLabel:    tunnelName,
	}
}

func tunnelSidecarName(output, source contracts.ProviderRef, tunnelName string) string {
	identity := output.Type + "/" + output.Name + "|" + source.Type + "/" + source.Name + "|" + tunnelName
	sum := sha256.Sum256([]byte(identity))
	readable := sanitizeContainerName(output.Name)
	if readable == "" {
		readable = "tunnel"
	}
	return fmt.Sprintf("docker-dns-sync-cloudflared-%s-%x", readable, sum[:5])
}

func sanitizeContainerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			result.WriteRune(r)
		} else if result.Len() > 0 {
			result.WriteByte('-')
		}
	}
	return strings.Trim(result.String(), "-_.")
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func sortedSetKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
