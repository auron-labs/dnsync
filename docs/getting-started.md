# Getting started

This guide takes you from nothing to a container that resolves by name through
your DNS server. It uses Docker Compose and AdGuard Home. The steps for other
DNS servers differ only in the `[[outputs]]` block; see
[DNS servers](integrations.md) for theirs.

By the end you will have:

- dnsync running as a container on your Docker host
- a test container named `whoami.home.arpa` in your DNS
- a feel for what happens when you change or remove a container

## Before you start

You need:

- A Docker host with Docker Compose.
- A DNS server that dnsync supports. This guide uses AdGuard Home.
- The AdGuard Home admin username and password.
- The IP address your DNS names should point to. This is usually the Docker
  host's LAN address, because that's where container ports are published.
- A domain for your containers, such as `home.arpa`. Every container name is
  created under it.

## 1. Write the configuration

Create a directory for dnsync and a `config.toml` inside it:

```toml
[[sources]]
type = "docker"
name = "local-docker"
endpoint = "unix:///var/run/docker.sock"
base_domain = "home.arpa"
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

What each part does:

- `[[sources]]` tells dnsync where to find containers. `base_domain` is added
  to each container name, and `host_ip` is the address every record points to.
- `[[outputs]]` is the DNS server to write to. `password_ref` reads the
  password from the `ADGUARD_PASSWORD` environment variable, so it never sits
  in the file. Use a URL the dnsync container can reach; `localhost` inside a
  container is the container itself.
- `[state]` is where dnsync remembers which records it created. Keep this file;
  without it, dnsync treats its old records as someone else's.
- `[logging]` and `[retry]` control log output and how long dnsync keeps
  retrying when the DNS server is unreachable.

[Configuration](configuration.md) lists every setting.

## 2. Add dnsync and a test container

Create `compose.yaml` in the same directory:

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

The two labels on `whoami` are what dnsync looks for:

- `proxy.aliases: whoami` names the container. With `base_domain`, it becomes
  `whoami.home.arpa`.
- `proxy.whoami.port: "80"` gives that name a port. dnsync ignores a name
  without one.

dnsync reads the Docker socket to see containers and their labels. The socket
gives full control of Docker, so treat the dnsync container as trusted. If you
want to limit that, see
[Use a Docker socket proxy](deployment.md#use-a-docker-socket-proxy).

## 3. Start it

```bash
export ADGUARD_PASSWORD='your-adguard-password'
docker compose up -d
docker compose logs dnsync
```

A healthy start logs `starting dnsync runtime`, then `reconcile completed`.
A reconcile is one pass in which dnsync compares your containers with the DNS
server and makes any changes needed.

If dnsync exits instead, the last log line names the problem. Common ones are
in [Troubleshooting](troubleshooting.md).

## 4. Check the record

Ask your DNS server for the new name:

```bash
dig +short whoami.home.arpa @192.168.1.2
```

The answer should be your `host_ip`, `192.168.1.50`. In AdGuard Home, the
record is listed under **Filters → DNS rewrites**.

## 5. Change and remove the container

dnsync watches Docker and reacts within moments of a change.

**Point it somewhere else.** Add a third label to `whoami`, then run
`docker compose up -d`:

```yaml
      proxy.whoami.host: "192.168.1.60"
```

dnsync updates `whoami.home.arpa` to answer `192.168.1.60` straight away.

**Remove it.** Stop the container with `docker compose stop whoami`. dnsync
waits 10 minutes before removing a record, so a quick restart doesn't make the
name disappear. You can change this with `removal_grace_period` in
[`[runtime]`](configuration.md#runtime).

What happens next depends on your DNS server:

- Cloudflare and Technitium DNS Server: dnsync deletes the record.
- AdGuard Home, Pi-hole, and TelltaleDNS: dnsync keeps the record and logs
  `blocked managed record cleanup without provenance`. These servers can't
  tell dnsync's records apart from ones you created, so dnsync won't delete
  them on its own. Delete the record on the DNS server and dnsync stops
  tracking it.

Renaming a container works the same way: dnsync creates the new name, and
the old name goes through the removal steps above.

[How dnsync works](architecture.md#removing-records) explains why.

## Next steps

- Label your real containers. [Container labels](configuration.md#container-labels)
  covers multiple names, per-name addresses, and opting out.
- Run dnsync as a systemd service instead of a container:
  [Deployment](deployment.md#run-as-a-systemd-service).
- Add another DNS server, or Cloudflare: [DNS servers](integrations.md).
