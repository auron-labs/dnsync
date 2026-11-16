# Deployment

dnsync runs as a long-lived process that needs:

- read access to Docker, through the local socket or a socket proxy
- network access to your DNS servers
- a config file and a writable, persistent state file

Pick the setup that fits your host. Each one starts dnsync at boot and
restarts it if it stops.

## Run with Docker Compose

[Getting started](getting-started.md) walks through a Compose setup. In short:

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
```

Pin a release tag, `ghcr.io/auron-labs/dnsync:<version>`, if you want to
choose when to upgrade.

## Run with `docker run`

```bash
docker run -d \
  --name dnsync \
  --restart unless-stopped \
  -e ADGUARD_PASSWORD='your-adguard-password' \
  -v /opt/dnsync/config.toml:/etc/dnsync/config.toml:ro \
  -v /opt/dnsync/state:/var/lib/dnsync \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/auron-labs/dnsync:latest \
  -config /etc/dnsync/config.toml
```

The image runs as root so it can use the Docker socket with Docker's default
permissions. With rootless Docker or a socket owned by another user, adjust the
container user or the socket permissions to match.

Inside a container, `localhost` is the container itself. Use addresses for
your DNS servers that the container can reach.

## Run as a systemd service

1. Install the `dnsync` binary using one of the methods in the
   [README](../README.md#install), and make sure it's at
   `/usr/local/bin/dnsync`.

2. Create a system user for dnsync:

   ```bash
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin dnsync
   ```

3. Create the config and a file for secrets:

   ```bash
   sudo mkdir -p /etc/dnsync
   sudoedit /etc/dnsync/config.toml
   sudo install -m 0600 /dev/null /etc/dnsync/dnsync.env
   sudoedit /etc/dnsync/dnsync.env
   ```

   Put one `NAME=value` line in `dnsync.env` for each `ENV:` reference in
   your config, such as `ADGUARD_PASSWORD=your-adguard-password`. Set
   `state.path` to `/var/lib/dnsync/state.json`.

4. Install the service unit. To use the local Docker socket, uncomment its
   `SupplementaryGroups=docker` line. If a different group owns your socket,
   use that group's name:

   ```bash
   sudo curl -fsSL -o /etc/systemd/system/dnsync.service \
     https://raw.githubusercontent.com/auron-labs/dnsync/main/deploy/systemd/dnsync.service
   sudoedit /etc/systemd/system/dnsync.service
   ```

5. Start it:

   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now dnsync.service
   journalctl -u dnsync.service -n 50 --no-pager
   ```

   Look for `reconcile completed` in the log.

The unit runs dnsync as the `dnsync` user, creates `/var/lib/dnsync` for the
state file, and restarts dnsync if it fails. It doesn't depend on
`docker.service`, so it also works with a remote Docker endpoint.

## Use a Docker socket proxy

Access to the Docker socket is equivalent to root on the host. A socket proxy
lets dnsync reach Docker with only the permissions it needs.

For DNS records alone, dnsync needs to list containers and read events. With
[`tecnativa/docker-socket-proxy`](https://github.com/Tecnativa/docker-socket-proxy):

```yaml
services:
  docker-socket-proxy:
    image: tecnativa/docker-socket-proxy
    restart: unless-stopped
    environment:
      CONTAINERS: 1
      EVENTS: 1
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

Then point the source at the proxy, and set `host_ip`. Without it, records
would point at the proxy's hostname:

```toml
[[sources]]
type = "docker"
name = "docker"
endpoint = "tcp://docker-socket-proxy:2375"
host_ip = "192.168.1.50"
```

The [Cloudflare Tunnel](configuration.md#cloudflare-tunnel) needs more access,
because dnsync runs the `cloudflared` container itself. Also allow `POST`,
`IMAGES`, and `NETWORKS`.

## Build from source

You need Git and [mise](https://mise.jdx.dev/), which installs the right Go
version:

```bash
git clone https://github.com/auron-labs/dnsync.git
cd dnsync
mise install
mise run build
```

The binary is `bin/dnsync`. To build a container image instead, run
`docker build -t dnsync .` in the same directory.

## Upgrading

Replace the binary or pull the new image, then restart dnsync. Keep the same
state file; dnsync upgrades its format automatically. Check the
[release notes](https://github.com/auron-labs/dnsync/releases) first.
