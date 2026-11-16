# Configuration

dnsync reads one TOML file. This page lists every setting, the environment
variables dnsync uses, and the container labels it understands.

## Command line

```bash
dnsync -config /etc/dnsync/config.toml
```

`-config` is the only flag, and it's required. dnsync has no built-in
defaults.

- Without `-config`, dnsync prints `error: -config is required` and exits
  with status 1.
- If the file can't be read or contains a mistake, dnsync prints the cause
  and exits with status 1. See [Troubleshooting](troubleshooting.md).
- Otherwise dnsync runs until it receives `SIGINT` or `SIGTERM`, then stops
  cleanly.

## File layout

A config file has these sections:

| Section | Required | Purpose |
| --- | --- | --- |
| `[[sources]]` | yes, at least one | Where to find containers. |
| `[[outputs]]` | yes, at least one | DNS servers to write records to. |
| `[state]` | yes | Where dnsync records which DNS records it owns. |
| `[logging]` | yes | Log level and format. |
| `[retry]` | yes | How long to keep retrying a failing DNS server. |
| `[runtime]` | no | Timeouts and the removal grace period. |

dnsync rejects keys it doesn't recognise. A misspelled setting stops startup
instead of being silently ignored.

### Secrets

Keep passwords and tokens out of the file with a reference to an environment
variable:

```toml
password_ref = "ENV:ADGUARD_PASSWORD"
```

dnsync reads the variable once at startup. If it's unset or empty, dnsync
won't start. Each `*_ref` setting below has a plain counterpart, such as
`password`, for testing. Set only one of the two. Pi-hole accepts only
`password_ref`.

## Sources

Each `[[sources]]` block is a Docker host to watch.

| Key | Required | Description |
| --- | --- | --- |
| `type` | yes | Always `docker`. |
| `name` | yes | A name for this source, used in logs and the state file. |
| `endpoint` | yes | How to reach Docker: `unix:///var/run/docker.sock` for the local socket, or `tcp://host:port` for a socket proxy or remote daemon. |
| `host_ip` | no | The IP address records point to, unless a container label overrides it. |
| `base_domain` | no | Domain added to container names. With `home.arpa`, the name `whoami` becomes `whoami.home.arpa`. Names that already contain a dot are used as they are. |
| `exclude_compose_projects` | no | Compose project names to ignore, such as `["test-stack"]`. Matching ignores case. |

### Which address records point to

dnsync picks the answer for each record in this order:

1. A `proxy.<name>.host`, `proxy.#<n>.host`, or `proxy.*.host` label on the
   container. See [Container labels](#container-labels).
2. The source's `host_ip`.
3. For a `tcp://` endpoint, the endpoint's host.

With the local socket and no `host_ip`, there's no address to use. dnsync
skips those records and logs why.

An answer that's an IP address becomes an A or AAAA record. Anything else,
such as another hostname, becomes a CNAME.

## Outputs

Each `[[outputs]]` block is a DNS server to write to. These keys apply to all
of them:

| Key | Required | Description |
| --- | --- | --- |
| `type` | yes | `adguard`, `pihole`, `technitium`, `telltaledns`, or `cloudflare`. |
| `name` | yes | A name for this output, used in logs and the state file. Don't rename an output after it has created records; dnsync tracks ownership by name. |
| `enabled` | no | Set to `false` to stop writing to this output. Defaults to `true`. |

The remaining keys depend on the type. [DNS servers](integrations.md) explains
how to create the credentials each one needs.

### AdGuard Home

| Key | Required | Description |
| --- | --- | --- |
| `url` | yes | AdGuard Home's web address, such as `http://192.168.1.2:3000`. |
| `username` | yes | An AdGuard Home admin user. |
| `password_ref` or `password` | one of them | That user's password. |

### Pi-hole

| Key | Required | Description |
| --- | --- | --- |
| `url` | yes | Pi-hole's web address, such as `http://pihole`. |
| `password_ref` | yes | The web interface password, or an app password. Inline `password` isn't accepted. |

Requires Pi-hole 6 or later.

### Technitium DNS Server

| Key | Required | Description |
| --- | --- | --- |
| `url` | yes | Technitium's web address, such as `http://technitium:5380`. |
| `zone` | yes | An existing zone to manage, such as `home.arpa`. |
| `api_token_ref` or `api_token` | one of them | An API token that can view, modify, and delete records in the zone. |

Requires Technitium DNS Server 15.0 or later. dnsync doesn't create the zone.
Every record must fall inside it, so set the source's `base_domain` to the zone
or a name under it. To manage several zones, add one output per zone and use
the `proxy.dns` label to choose between them.

### TelltaleDNS

| Key | Required | Description |
| --- | --- | --- |
| `url` | yes | The TelltaleDNS API address, such as `http://telltale:8053`. |
| `api_key_ref` or `api_key` | one of them | An API token with the `write` scope. |

### Cloudflare

| Key | Required | Description |
| --- | --- | --- |
| `zone_id` | yes | The ID of the Cloudflare zone to manage. |
| `api_key_ref` or `api_key` | one of them | An API token with Zone DNS edit permission. |

#### Cloudflare Tunnel

Add an `[outputs.tunnel]` block to one Cloudflare output to publish containers
through a Cloudflare Tunnel. dnsync creates the tunnel, runs a `cloudflared`
connector container, and routes each opted-in container through it.

```toml
[[outputs]]
type = "cloudflare"
name = "cloudflare"
zone_id = "your-zone-id"
api_key_ref = "ENV:CLOUDFLARE_API_KEY"

[outputs.tunnel]
account_id = "your-account-id"
name = "dnsync"
source = "local-docker"
```

| Key | Required | Description |
| --- | --- | --- |
| `account_id` | yes | The Cloudflare account that owns the tunnel. |
| `name` | yes | The tunnel's name in Cloudflare. |
| `source` | yes | The `name` of the Docker source whose containers use the tunnel. The connector runs on that Docker host. |
| `connector_image` | no | The `cloudflared` image to run. Defaults to `cloudflare/cloudflared:2026.7.3`. |

Only one enabled output can have a tunnel. The API token also needs Account
Cloudflare Tunnel edit permission. dnsync's Docker access must allow creating
containers, pulling images, and connecting networks.

dnsync only manages a tunnel and connector it created. If a tunnel or
container with the same name already exists, dnsync reports a conflict instead
of taking it over.

To route a container through the tunnel, add `proxy.cloudflare.tunnel=true`
to it. See [Container labels](#container-labels).

## State

| Key | Required | Description |
| --- | --- | --- |
| `path` | yes | A writable file path, such as `/var/lib/dnsync/state.json`. dnsync creates the file if it's missing. |

The state file is how dnsync knows which records are its own. Keep it on
persistent storage and include it in backups. See [State file](data-model.md).

## Logging

| Key | Required | Description |
| --- | --- | --- |
| `level` | yes | `trace`, `debug`, `info`, `warn`, or `error`. |
| `format` | yes | `json` or `text`. |
| `max_size_mb` | no | Size at which the log file set by `LOG_FILE` rotates. Defaults to 10. |
| `max_backups` | no | Rotated log files to keep. Defaults to 5. |

Logs always go to standard output. Startup errors go to standard error.

## Runtime

| Key | Default | Description |
| --- | --- | --- |
| `removal_grace_period` | `10m` | How long dnsync waits after a container disappears before removing its records. A container that comes back in time keeps its records. `0s` removes them immediately. Changes to existing records aren't delayed. |
| `operation_timeout` | `10s` | Time limit for each individual request to Docker or a DNS server. |

Durations use Go syntax, such as `30s`, `10m`, or `1h30m`.

## Retry

When Docker or a DNS server fails, dnsync retries with increasing delays until
the change succeeds. See [When something fails](architecture.md#when-something-fails).

| Key | Required | Description |
| --- | --- | --- |
| `initial_interval` | yes | The first delay, such as `1s`. |
| `max_interval` | yes | The longest delay between attempts, such as `30s`. |
| `max_elapsed_time` | yes | How long dnsync retries a temporary problem, such as a timeout, before it logs an error, such as `5m`. It keeps trying after that, `max_interval` apart. |

## Environment variables

| Variable | Description |
| --- | --- |
| Any `ENV:` reference | Supplies a secret, as described in [Secrets](#secrets). |
| `LOG_LEVEL` | Overrides `logging.level`. |
| `LOG_FILE` | Also writes logs to this file, with `0600` permissions. dnsync creates missing directories and rotates the file using `max_size_mb` and `max_backups`. If the file can't be opened, dnsync doesn't start. |
| `SENTRY_DSN` | Sends errors to Sentry. Secrets are removed first. Leave unset to disable. |
| `SENTRY_ENVIRONMENT` | The environment name shown in Sentry. |

## Container labels

dnsync reads `proxy.*` labels on your containers. They're compatible with
GoDoxy's labels.

A container needs at least one name with a port:

```yaml
labels:
  proxy.aliases: whoami
  proxy.whoami.port: "80"
```

| Label | Description |
| --- | --- |
| `proxy.aliases` | Comma-separated names for the container, such as `app,app-admin`. Each name needs a matching port label. |
| `proxy.<name>.port` | The port for a name. A name with only this label, and no `proxy.aliases` entry, still counts. |
| `proxy.#<n>.port` | The port for the nth name in `proxy.aliases`, counting from 1. |
| `proxy.*.port` | A port for every name that doesn't have its own. |
| `proxy.<name>.host`, `proxy.#<n>.host`, `proxy.*.host` | The address a record points to instead of `host_ip`. Checked in this order. |
| `proxy.dns` | `false` gives the container no DNS records. An output type, such as `adguard` or `cloudflare`, writes its records only to outputs of that type. Unset or `true` writes to every output. |
| `proxy.exclude` | `true` makes dnsync ignore the container completely, including for the tunnel. |
| `proxy.cloudflare.tunnel` | `true` routes each of the container's names through the Cloudflare Tunnel. |

Names are lowercased. A name without a dot gets the source's `base_domain`.

For tunnel routes, ports must be numbers from 1 to 65535. dnsync connects to
the container over HTTP on its first network address, preferring IPv4. A
tunnelled name gets a proxied CNAME to the tunnel on the tunnel's Cloudflare
output, instead of an ordinary record there. Other outputs still get their
usual records unless `proxy.dns` says otherwise.

## Example files

The repository includes complete examples:

- [`config.example.toml`](../config.example.toml): runs in a container next
  to AdGuard Home, with every other DNS server shown commented out. The
  repository's [`docker-compose.yaml`](../docker-compose.yaml) uses it.
- [`testdata/config/example.toml`](../testdata/config/example.toml): dnsync
  installed directly on the Docker host.
- [`testdata/config/docker-container.toml`](../testdata/config/docker-container.toml):
  dnsync in a container.
- [`testdata/config/socket-proxy.toml`](../testdata/config/socket-proxy.toml):
  Docker reached through a socket proxy or another host.
