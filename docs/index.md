# dnsync documentation

dnsync gives your Docker containers DNS names automatically. You label a
container with the names it should answer to, and dnsync creates and updates
the matching records on your DNS server. It supports AdGuard Home, Pi-hole,
Technitium DNS Server, TelltaleDNS, and Cloudflare, including Cloudflare
Tunnel.

## Where to start

New to dnsync? Follow [Getting started](getting-started.md). It sets up dnsync
with Docker Compose and checks that a test container resolves.

| You want to… | Go to |
| --- | --- |
| Set up dnsync for the first time | [Getting started](getting-started.md) |
| Run it as a systemd service, with `docker run`, or from source | [Deployment](deployment.md) |
| Look up a setting, label, or default | [Configuration](configuration.md) |
| Connect a specific DNS server or Cloudflare | [DNS servers](integrations.md) |
| Understand which records dnsync changes and when | [How dnsync works](architecture.md) |
| Back up or inspect the state file | [State file](data-model.md) |
| Protect credentials and Docker access | [Security](security.md) |
| Fix an error | [Troubleshooting](troubleshooting.md) |
| Work on dnsync itself | [Development](development.md) and [Testing](testing.md) |

## Good to know

- dnsync needs a config file. It has no built-in defaults and won't start
  without `-config`.
- When dnsync reads Docker through the local socket, set `host_ip` in your
  source. dnsync can't tell which address your clients should use.
- dnsync only changes records it created. Records you added by hand are never
  modified.
- On AdGuard Home, Pi-hole, and TelltaleDNS, dnsync doesn't delete records for
  removed containers. It logs a warning so you can delete them.
