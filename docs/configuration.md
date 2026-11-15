# Configuration

## Entry point

The daemon requires a config file path:

```bash
dnsync -config /path/to/config.toml
```

`cmd/dnsync/main.go` rejects startup if `-config` is missing.

## Config file structure

The TOML file must define these top-level sections:

- `[[sources]]`
- `[[outputs]]`
- `[state]`
- `[logging]`
- `[runtime]`
- `[retry]`

The config is parsed first, then validated, then secret references are resolved from the process environment.

Startup is strict about unknown TOML keys. Misspelled fields, extra sections, and stale options are rejected during load instead of being ignored, so remove the unexpected key and use a documented field name before restarting.

## Sources

| Key | Required | Notes |
| --- | --- | --- |
| `type` | yes | Registered source type. The default registry currently supports `docker`. |
| `name` | yes | Human-readable provider name used in logs and state. |
| `endpoint` | yes | Must start with `unix://` or `tcp://`. |
| `host_ip` | no | Default answer target when labels do not override it. Must be a valid IP address if set. |
| `base_domain` | no | Appended to label-derived hostnames so `foo` becomes `foo.example.com`. Existing FQDN labels are left unchanged. |
| `exclude_compose_projects` | no | Compose project names whose containers are ignored, matched against the standard `com.docker.compose.project` label. |

### Docker source behavior

- `host_ip` is the preferred default answer target.
- `base_domain` makes derived hostnames canonical FQDNs before they reach any output.
- `exclude_compose_projects = ["dnsync-live-test"]` prevents a production source on the same Docker daemon from discovering the repository's live-test workload.
- If `host_ip` is empty and the endpoint is remote, the endpoint host is used as the default answer target.
- If the endpoint is a local socket (`unix://`), no default answer target is inferred.
- The source watches Docker container and network events and uses them as reconciliation hints.

## Outputs

| Key | Required | Notes |
| --- | --- | --- |
| `type` | yes | Supported values in the default registry: `adguard`, `cloudflare`, `pihole`, `telltaledns`. |
| `name` | yes | Human-readable provider name used in logs and state. |
| `enabled` | no | Defaults to `true` when omitted. Set `false` to skip the output. |

### AdGuard output

Required when `type = "adguard"` and enabled:

- `url`
- `username`
- exactly one of `password` or `password_ref`

`password_ref` must use the `ENV:` prefix, for example `ENV:ADGUARD_PASSWORD`.

### TelltaleDNS output

Required when `type = "telltaledns"` and enabled:

- `url`, the TelltaleDNS API base URL, for example `http://telltale:8053`
- exactly one of `api_key` or `api_key_ref`

The key is a TelltaleDNS API token with the `write` scope (`tt_<id>_<secret>`). `api_key_ref` must use the `ENV:` prefix, for example `ENV:TELLTALEDNS_API_KEY`.

### Pi-hole output

Required when `type = "pihole"` and enabled:

- `url`, the Pi-hole web server base URL, for example `http://pihole`
- `password_ref`, which must use the `ENV:` prefix, for example `ENV:PIHOLE_PASSWORD`. An inline `password` is rejected.

dnsync requires Pi-hole v6 or later. Use the web interface password, or an app password if two-factor authentication is enabled. An app password can change local DNS records only when Pi-hole's `webserver.api.app_sudo` setting is `true`.

### Cloudflare output

Required when `type = "cloudflare"` and enabled:

- `zone_id`
- exactly one of `api_key` or `api_key_ref`

`api_key_ref` must use the `ENV:` prefix, for example `ENV:CLOUDFLARE_API_KEY`.

#### Optional Cloudflare Tunnel

At most one enabled Cloudflare output may contain a tunnel configuration. It
must name exactly one configured Docker source. The API token needs Zone DNS
write and Account Tunnel write permissions.

```toml
[[outputs]]
type = "cloudflare"
name = "primary-cloudflare"
zone_id = "..."
api_key_ref = "ENV:CLOUDFLARE_API_KEY"

[outputs.tunnel]
account_id = "..."
name = "dnsync"
source = "local-docker"
connector_image = "cloudflare/cloudflared:2026.7.3"
```

Set `proxy.cloudflare.tunnel=true` on a running workload alongside its usual
aliases and ports. Tunnel routes use HTTP origins and the selected Docker
network endpoint. `proxy.exclude=true` remains the highest-priority opt-out;
tunnel opt-in is independent of `proxy.dns`. A tunnel route publishes a
proxied CNAME to `<tunnel-id>.cfargotunnel.com` on the selected Cloudflare
output and suppresses that hostname's ordinary Cloudflare record. AdGuard
records retain their existing behavior.

The daemon creates and owns a dedicated remotely managed tunnel and a managed
`cloudflared` sidecar. The sidecar requires Docker container, image, and
network write operations. A same-name unmanaged tunnel or sidecar is an
ownership conflict and is never adopted.

## State

| Key | Required | Notes |
| --- | --- | --- |
| `path` | yes | Writable path for the owned-record snapshot. The store creates the file if it does not exist. |

State is written atomically with JSON encoding and `0600` permissions where supported.

## Logging

| Key | Allowed values | Notes |
| --- | --- | --- |
| `level` | `debug`, `info`, `warn`, `error` | Invalid values fail validation. |
| `format` | `json`, `text` | Structured records are written to stdout. |
| `max_size_mb` | positive integer | Maximum active log size before rotation; defaults to 10 MiB. |
| `max_backups` | positive integer | Number of numbered backups retained; defaults to 5. |

Logs always go to stdout. A nonblank `LOG_FILE` adds an identical rotating
file sink, creates parent directories, and uses `0600` permissions. `LOG_LEVEL`
overrides `logging.level`; a file-open failure stops startup. `LOG_FILE.1`
through `LOG_FILE.5` are retained by default. CLI parsing and fatal errors stay
on stderr.

## Runtime

| Key | Notes |
| --- | --- |
| `operation_timeout` | Optional Go duration string. Defaults to `10s`. Applied per source/output read or write operation (`ListDesired`, `ListVisible`, `Create`, `Update`, `Delete`). |
| `removal_grace_period` | Optional non-negative Go duration. Defaults to `10m`; `0s` removes immediately. It applies globally to daemon-owned AdGuard records, Cloudflare records, tunnel CNAMEs, and tunnel routes. |

This timeout is not a global reconcile deadline and does not wrap long-lived `Watch(ctx)` streams. Retry handling starts only after an operation returns successfully or fails, including when it fails by timing out.

## Retry

| Key | Notes |
| --- | --- |
| `initial_interval` | Required Go duration string, greater than zero. |
| `max_interval` | Required Go duration string, greater than zero. |
| `max_elapsed_time` | Required Go duration string, greater than zero. |

These values control bounded backoff during reconcile and watch-reconnect recovery. They do not shorten in-flight provider calls; retries begin only after the per-operation runtime timeout fires or the operation returns on its own.

## Label-driven record derivation

Docker sources interpret these labels:

- `proxy.exclude=true` disables the container.
- `proxy.dns` controls DNS participation: unset or `true` applies to every configured output, `false` opts the container out of DNS, and a value like `adguard`, `cloudflare`, `pihole`, or `telltaledns` limits the record to that output type.
- `proxy.aliases` is a comma-separated alias list.
- `proxy.<alias>.port` defines a named alias.
- `proxy.#<n>.port` and `proxy.*.port` provide indexed and wildcard ports.
- `proxy.<alias>.host`, `proxy.#<n>.host`, and `proxy.*.host` override the answer target.
- `proxy.cloudflare.tunnel=true` enables a Cloudflare Tunnel route for each
  existing alias/port. Only the case-insensitive value `true` enables it;
  other nonblank values are reported as safe derivation diagnostics. Tunnel
  ports must be numeric from 1 through 65535. The first non-empty network
  endpoint is selected with IPv4 preferred over IPv6.

When a container stops producing a desired record because labels changed, AdGuard cleanup deletes exactly one visible hostname/answer match recorded in daemon state. AdGuard does not expose a stable rewrite ID, so an indistinguishable manual replacement can be deleted. Ambiguous duplicate matches remain blocked. Other outputs still require non-empty daemon-owned provenance before stale cleanup.

Lookup precedence for host overrides is:

1. `proxy.<alias>.host`
2. `proxy.#<n>.host`
3. `proxy.*.host`

## Example starter configs

- `testdata/config/example.toml` — host-oriented example.
- `config.example.toml` — compose-oriented example.
- `testdata/config/docker-container.toml` — container deployment example.
- `testdata/config/socket-proxy.toml` — TCP proxy / remote Docker example.
