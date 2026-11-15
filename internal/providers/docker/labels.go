package docker

import (
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/auron-labs/dnsync/internal/contracts"
	containertypes "github.com/moby/moby/api/types/container"
)

func deriveDesiredRecords(provider contracts.ProviderRef, defaultTarget, baseDomain string, container containertypes.Summary) []contracts.DesiredRecord {
	records, _, _ := deriveDesiredSnapshotDetailed(provider, defaultTarget, baseDomain, container)
	return records
}

type derivationDiagnostic struct {
	alias    string
	hostname string
	reason   string
	hint     string
}

func deriveDesiredRecordsDetailed(provider contracts.ProviderRef, defaultTarget, baseDomain string, container containertypes.Summary) ([]contracts.DesiredRecord, []derivationDiagnostic) {
	records, _, diagnostics := deriveDesiredSnapshotDetailed(provider, defaultTarget, baseDomain, container)
	return records, diagnostics
}

func deriveDesiredSnapshotDetailed(provider contracts.ProviderRef, defaultTarget, baseDomain string, container containertypes.Summary) ([]contracts.DesiredRecord, []contracts.DesiredTunnelRoute, []derivationDiagnostic) {
	if isExcluded(container.Labels) {
		return nil, nil, []derivationDiagnostic{{
			reason: "proxy.exclude",
			hint:   "remove proxy.exclude=true to let this container publish DNS records",
		}}
	}

	tunnelEnabled, tunnelInvalid := tunnelLabel(container.Labels)
	outputType, include := dnsOutputType(container.Labels)
	if !include && !tunnelEnabled {
		diagnostics := []derivationDiagnostic{{
			reason: "proxy.dns=false",
			hint:   "set proxy.dns=true or remove proxy.dns to enable DNS derivation",
		}}
		if tunnelInvalid {
			diagnostics = append(diagnostics, derivationDiagnostic{reason: "invalid proxy.cloudflare.tunnel", hint: "set proxy.cloudflare.tunnel=true to enable Cloudflare Tunnel routing"})
		}
		return nil, nil, diagnostics
	}

	diagnostics := make([]derivationDiagnostic, 0, 2)
	if tunnelInvalid {
		diagnostics = append(diagnostics, derivationDiagnostic{
			reason: "invalid proxy.cloudflare.tunnel",
			hint:   "set proxy.cloudflare.tunnel=true to enable Cloudflare Tunnel routing",
		})
	}

	aliases := collectAliases(container)
	if len(aliases) == 0 {
		return nil, nil, []derivationDiagnostic{{
			reason: "no supported alias/port",
			hint:   "set proxy.aliases with matching proxy.<alias>.port labels, or define proxy.*.port / proxy.#.port",
		}}
	}

	answer := normalizeAnswerTarget(defaultTarget)

	source := contracts.SourceObjectRef{
		Provider:    provider,
		ID:          strings.TrimSpace(container.ID),
		DisplayName: containerDisplayName(container),
	}

	records := make([]contracts.DesiredRecord, 0, len(aliases))
	routes := make([]contracts.DesiredTunnelRoute, 0, len(aliases))
	for _, alias := range aliases {
		aliasName := alias.name
		hostname := qualifyHostname(aliasName, baseDomain)
		if hostname == "" {
			diagnostics = append(diagnostics, derivationDiagnostic{
				alias:  aliasName,
				reason: "invalid/empty hostname",
				hint:   "set proxy.aliases to a valid hostname value",
			})
			continue
		}

		if tunnelEnabled {
			service, networkIDs, reason := tunnelService(container, alias)
			if reason != "" {
				diagnostics = append(diagnostics, derivationDiagnostic{alias: aliasName, hostname: hostname, reason: reason, hint: "set a numeric proxy port from 1 through 65535 and attach the container to a network with an IP endpoint"})
			}
			if service != "" {
				routes = append(routes, contracts.DesiredTunnelRoute{
					Hostname:   hostname,
					Service:    service,
					Source:     source,
					NetworkIDs: networkIDs,
					Output:     "cloudflare",
				})
			}
		}

		if include {
			target := explicitHostOverride(container.Labels, aliasName, alias.position)
			if target == "" {
				target = answer
			}
			if target == "" {
				diagnostics = append(diagnostics, derivationDiagnostic{
					alias:    aliasName,
					hostname: hostname,
					reason:   "empty_answer_target",
					hint:     "set sources[].host_ip or proxy.*.host / proxy.<alias>.host",
				})
				continue
			}

			records = append(records, contracts.DesiredRecord{
				Hostname: hostname,
				Answer:   normalizeAnswerTarget(target),
				Source:   source,
				Output:   outputType,
			})
		}
	}

	sort.Slice(records, func(i, j int) bool {
		return desiredRecordLess(records[i], records[j])
	})

	sort.Slice(routes, func(i, j int) bool {
		left := routes[i]
		right := routes[j]
		return left.Hostname+"|"+left.Service+"|"+left.Source.ID < right.Hostname+"|"+right.Service+"|"+right.Source.ID
	})

	return records, routes, diagnostics
}

func tunnelService(container containertypes.Summary, alias aliasRef) (string, []string, string) {
	port := portForAlias(container.Labels, alias.name, alias.position)
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", nil, "invalid tunnel port"
	}
	if container.NetworkSettings == nil {
		return "", nil, "missing container network endpoint"
	}

	type endpoint struct {
		name      string
		address   string
		networkID string
	}
	endpoints := make([]endpoint, 0, len(container.NetworkSettings.Networks))
	for name, settings := range container.NetworkSettings.Networks {
		if settings == nil || !settings.IPAddress.IsValid() || strings.TrimSpace(settings.NetworkID) == "" {
			continue
		}
		endpoints = append(endpoints, endpoint{name: name, address: settings.IPAddress.String(), networkID: strings.TrimSpace(settings.NetworkID)})
	}
	if len(endpoints) == 0 {
		return "", nil, "missing container network endpoint"
	}
	sort.Slice(endpoints, func(i, j int) bool {
		leftIPv4 := endpoints[i].address != "" && !strings.Contains(endpoints[i].address, ":")
		rightIPv4 := endpoints[j].address != "" && !strings.Contains(endpoints[j].address, ":")
		if leftIPv4 != rightIPv4 {
			return leftIPv4
		}
		return endpoints[i].name < endpoints[j].name
	})

	service := "http://" + net.JoinHostPort(endpoints[0].address, strconv.Itoa(portNumber))
	return service, []string{endpoints[0].networkID}, ""
}

func tunnelLabel(labels map[string]string) (enabled, invalid bool) {
	value := strings.TrimSpace(labels["proxy.cloudflare.tunnel"])
	if value == "" {
		return false, false
	}
	if strings.EqualFold(value, "true") {
		return true, false
	}
	return false, true
}

func portForAlias(labels map[string]string, alias string, index int) string {
	if value := normalizePortValue(labels["proxy."+alias+".port"]); value != "" {
		return value
	}
	if index > 0 {
		if value := normalizePortValue(labels["proxy.#"+itoa(index)+".port"]); value != "" {
			return value
		}
	}
	return normalizePortValue(labels["proxy.*.port"])
}

func schemeForAlias(labels map[string]string, alias string, index int) string {
	for _, key := range []string{"proxy." + alias + ".scheme", "proxy." + alias + ".protocol"} {
		if value := normalizeTunnelScheme(labels[key]); value != "" {
			return value
		}
	}
	if index > 0 {
		for _, suffix := range []string{".scheme", ".protocol"} {
			if value := normalizeTunnelScheme(labels["proxy.#"+itoa(index)+suffix]); value != "" {
				return value
			}
		}
	}
	for _, key := range []string{"proxy.*.scheme", "proxy.*.protocol"} {
		if value := normalizeTunnelScheme(labels[key]); value != "" {
			return value
		}
	}
	return "http"
}

func normalizeTunnelScheme(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "http", "https", "tcp", "ssh", "rdp", "smb":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func qualifyHostname(hostname, baseDomain string) string {
	hostname = normalizeName(hostname)
	if hostname == "" {
		return ""
	}
	if strings.Contains(hostname, ".") {
		return hostname
	}

	baseDomain = normalizeName(baseDomain)
	if baseDomain == "" || hostname == baseDomain || strings.HasSuffix(hostname, "."+baseDomain) {
		return hostname
	}

	return hostname + "." + baseDomain
}

type aliasRef struct {
	name     string
	position int
}

func collectAliases(container containertypes.Summary) []aliasRef {
	labels := container.Labels
	ordered := make([]aliasRef, 0)
	seen := make(map[string]struct{})

	for i, alias := range strings.Split(labels["proxy.aliases"], ",") {
		alias = normalizeName(alias)
		if alias == "" {
			continue
		}
		if !hasPortForAlias(labels, alias, i+1) {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		ordered = append(ordered, aliasRef{name: alias, position: i + 1})
	}

	discovered := make([]string, 0)
	for key := range labels {
		alias, ok := supportedNamedPortAlias(key)
		if !ok {
			continue
		}
		if normalizePortValue(labels[key]) == "" {
			continue
		}
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		discovered = append(discovered, alias)
	}
	sort.Strings(discovered)
	for _, alias := range discovered {
		ordered = append(ordered, aliasRef{name: alias})
	}

	if len(ordered) == 0 && hasWildcardOrIndexedPort(labels) {
		fallback := normalizeName(containerDisplayName(container))
		if fallback != "" {
			ordered = append(ordered, aliasRef{name: fallback})
		}
	}

	return ordered
}

func explicitHostOverride(labels map[string]string, alias string, index int) string {
	if value := normalizeAnswerTarget(labels["proxy."+alias+".host"]); value != "" {
		return value
	}
	if index > 0 {
		if value := normalizeAnswerTarget(labels["proxy.#"+itoa(index)+".host"]); value != "" {
			return value
		}
	}
	return normalizeAnswerTarget(labels["proxy.*.host"])
}

func hasPortForAlias(labels map[string]string, alias string, index int) bool {
	if normalizePortValue(labels["proxy."+alias+".port"]) != "" {
		return true
	}
	if index > 0 {
		if normalizePortValue(labels["proxy.#"+itoa(index)+".port"]) != "" {
			return true
		}
	}
	return normalizePortValue(labels["proxy.*.port"]) != ""
}

func hasWildcardOrIndexedPort(labels map[string]string) bool {
	for key := range labels {
		if strings.HasPrefix(key, "proxy.*.") || strings.HasPrefix(key, "proxy.#") {
			if strings.HasSuffix(key, ".port") && normalizePortValue(labels[key]) != "" {
				return true
			}
		}
	}

	return false
}

func supportedNamedPortAlias(key string) (string, bool) {
	if !strings.HasPrefix(key, "proxy.") {
		return "", false
	}

	parts := strings.Split(key, ".")
	if len(parts) != 3 || parts[0] != "proxy" {
		return "", false
	}
	if parts[1] == "" || parts[1] == "*" || strings.HasPrefix(parts[1], "#") {
		return "", false
	}
	if parts[2] != "port" {
		return "", false
	}

	return normalizeName(parts[1]), true
}

func isExcluded(labels map[string]string) bool {
	value := strings.TrimSpace(strings.ToLower(labels["proxy.exclude"]))
	return value == "true"
}

func dnsOutputType(labels map[string]string) (string, bool) {
	value := strings.TrimSpace(strings.ToLower(labels["proxy.dns"]))
	switch value {
	case "", "true":
		return "", true
	case "false":
		return "", false
	default:
		return value, true
	}
}

func containerDisplayName(container containertypes.Summary) string {
	for _, name := range container.Names {
		trimmed := strings.TrimSpace(strings.TrimPrefix(name, "/"))
		if trimmed != "" {
			return trimmed
		}
	}

	return strings.TrimSpace(container.ID)
}

func isLocalEndpoint(endpoint string) bool {
	return strings.HasPrefix(endpoint, "unix://") || strings.HasPrefix(endpoint, "npipe://") || endpoint == ""
}

func endpointHost(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}

	return normalizeAnswerTarget(parsed.Hostname())
}

func desiredRecordLess(left, right contracts.DesiredRecord) bool {
	leftKey := left.Hostname + "|" + left.Answer + "|" + left.Source.ID
	rightKey := right.Hostname + "|" + right.Answer + "|" + right.Source.ID
	return leftKey < rightKey
}

func normalizeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.TrimSuffix(value, ".")
}

func normalizeAnswerTarget(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.TrimSuffix(value, ".")
}

func normalizePortValue(value string) string {
	return strings.TrimSpace(value)
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
