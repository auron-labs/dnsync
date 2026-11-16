# dnsync

[![License](https://img.shields.io/github/license/auron-labs/dnsync?style=flat-square)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/auron-labs/dnsync/ci.yaml?branch=main&style=flat-square&label=ci)](https://github.com/auron-labs/dnsync/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/auron-labs/dnsync?display_name=tag&style=flat-square)](https://github.com/auron-labs/dnsync/releases)

dnsync gives your Docker containers DNS names automatically. Add a label to a
container, and dnsync creates a matching record on your DNS server. Change or
remove the label, and dnsync updates the record. Records you created yourself
are left alone.

dnsync reads the same `proxy.*` labels as [GoDoxy](https://github.com/yusing/godoxy),
so containers already labelled for GoDoxy get DNS records with no extra work.
It can also publish containers through a Cloudflare Tunnel and run the
`cloudflared` connector for you.

## Supported DNS servers

| DNS server | What dnsync manages | Cleans up after removed containers |
| --- | --- | --- |
| AdGuard Home | DNS rewrites | No; it logs a warning so you can remove the record |
| Pi-hole 6 or later | Local DNS records and CNAMEs | No; it logs a warning so you can remove the record |
| Technitium DNS Server 15.0 or later | A, AAAA, and CNAME records in one zone | Yes |
| TelltaleDNS | Local names | No; it logs a warning so you can remove the record |
| Cloudflare | DNS records in one zone, plus an optional Tunnel | Yes |

[DNS servers](docs/integrations.md) explains the cleanup difference and what
each server needs.

Pi-hole, Technitium DNS Server, and TelltaleDNS support is newer than the
latest release, v0.3.0. To use them before the next release,
[build from source](docs/deployment.md#build-from-source).

## Quick start

This example runs dnsync with Docker Compose and publishes a test container to
AdGuard Home. You need a Docker host and an AdGuard Home server that the Docker
host can reach.

1. Create `config.toml`. Replace the IP addresses, domain, and username with
   your own:

   ```toml
   [[sources]]
   type = "docker"
   name = "local-docker"
   endpoint = "unix:///var/run/docker.sock"
   # Names are created under this domain: "whoami" becomes "whoami.home.arpa".
   base_domain = "home.arpa"
   # The address your DNS records point to, usually the Docker host.
   host_ip = "192.168.1.50"

   [[outputs]]
   type = "adguard"
   name = "adguard"
   url = "http://192.168.1.2:3000"
   username = "admin"
   password_ref = "ENV:ADGUARD_PASSWORD"

   [state]
   path = "/var/lib/dnsync/state.json"

   [logging]
   level = "info"
   format = "text"

   [retry]
   initial_interval = "1s"
   max_interval = "30s"
   max_elapsed_time = "5m"
   ```

2. Create `compose.yaml` next to it. The `whoami` service is a test container
   with the two labels dnsync needs:

   ```yaml
   services:
     dnsync:
       image: ghcr.io/auron-labs/dnsync:latest
       command: ["-config", "/etc/dnsync/config.toml"]
       restart: unless-stopped
       environment:
         ADGUARD_PASSWORD: ${ADGUARD_PASSWORD:?set ADGUARD_PASSWORD}
       volumes:
         - ./config.toml:/etc/dnsync/config.toml:ro
         - ./state:/var/lib/dnsync
         - /var/run/docker.sock:/var/run/docker.sock

     whoami:
       image: traefik/whoami
       labels:
         proxy.aliases: whoami
         proxy.whoami.port: "80"
   ```

   Mounting the Docker socket gives dnsync full control of Docker. To limit
   that, use a [Docker socket proxy](docs/deployment.md#use-a-docker-socket-proxy).

3. Start both containers:

   ```bash
   export ADGUARD_PASSWORD='your-adguard-password'
   docker compose up -d
   ```

4. Check the new record. The answer should be your `host_ip`:

   ```bash
   dig +short whoami.home.arpa @192.168.1.2
   ```

   You can also open **Filters → DNS rewrites** in AdGuard Home and look for
   `whoami.home.arpa`.

If the record doesn't appear, run `docker compose logs dnsync` and see
[Troubleshooting](docs/troubleshooting.md).

## Install

- **Container image:** `ghcr.io/auron-labs/dnsync:latest`, or pin a release tag
  such as `ghcr.io/auron-labs/dnsync:v0.3.0`.
- **Homebrew:** `brew install --cask auron-labs/tap/dnsync`
- **[bin](https://github.com/aaronflorey/bin):** `bin install github.com/auron-labs/dnsync`
- **Release archive:** download a Linux or macOS archive from
  [GitHub Releases](https://github.com/auron-labs/dnsync/releases) and put
  `dnsync` on your `PATH`.

[Deployment](docs/deployment.md) covers running dnsync as a systemd service,
with `docker run`, behind a socket proxy, and from source.

## Documentation

- [Getting started](docs/getting-started.md): a guided first setup.
- [Configuration](docs/configuration.md): every setting and container label.
- [DNS servers](docs/integrations.md): setup and behaviour for each DNS server.
- [How dnsync works](docs/architecture.md): ownership, cleanup, and retries.
- [Troubleshooting](docs/troubleshooting.md): common errors and fixes.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [Development](docs/development.md).
Report vulnerabilities as described in [SECURITY.md](SECURITY.md).

## License

Released under the MIT License. See [LICENSE](LICENSE).
