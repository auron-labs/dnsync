# Getting started

This page gets you to a running daemon and a verified record in the fewest
steps. Read it once top-to-bottom the first time; after that, treat it as a
cheat sheet for the three paths below.

## What you'll have when you're done

Pick whichever path matches your environment. All three end with the same
outcome: the `whoami` container in the live test stack resolves through
AdGuard to `127.0.0.1`.

| Path | Best when | Where the daemon runs | Where AdGuard lives |
| --- | --- | --- | --- |
| [Host binary](#host-binary-with-systemd) | You want a Linux service on a single host | A `systemd` unit on the host | On the same host, reachable on `127.0.0.1:3000` |
| [Container, root compose](#container-with-the-root-compose-file) | You already run a Docker host and want the simplest compose file | The published GHCR image, mounted `/var/run/docker.sock` | Inside the same compose project on the `adguardhome` host |
| [Container, manual run](#container-with-an-explicit-docker-run) | You want to see every flag once | The published GHCR image, mounted `/var/run/docker.sock` | Wherever you point `outputs[].url` |

If you only want to poke the daemon without touching your real DNS, the live
test stack under `deploy/compose/live-test/` runs the daemon, AdGuard, and a
sample workload together. See [Verify with the live stack](#verify-with-the-live-stack).

## Prereqs in common

- Go 1.26.3 via `mise.toml` for building from source (skip if you install
  from a release archive or container image).
- A reachable Docker daemon — local Unix socket, a TCP proxy, or a remote
  endpoint. The daemon speaks `unix://` and `tcp://` only.
- An AdGuard Home instance with admin access. Create a dedicated user/password
  pair; the daemon needs basic-auth API access.
- For Cloudflare output: an API token with Zone DNS write (and Account Tunnel
  write if you also enable the tunnel block).

## Host binary with systemd

1. Install the pinned toolchain (skip if you already have Go 1.26.3):

   ```bash
   mise install
   ```

2. Build and install the binary:

   ```bash
   mise exec -- go build -o bin/docker-dns-sync ./cmd/docker-dns-sync
   sudo install -m 0755 bin/docker-dns-sync /usr/local/bin/docker-dns-sync
   ```

3. Copy a starter config and edit it:

   ```bash
   sudo mkdir -p /etc/docker-dns-sync
   sudo install -m 0644 testdata/config/example.toml /etc/docker-dns-sync/config.toml
   ```

   At minimum, change `sources[].host_ip` to the IP your DNS clients should
   reach, and set `sources[].endpoint` to your Docker socket. For a remote
   Docker endpoint, start from `testdata/config/socket-proxy.toml` instead.

4. Create a writable state directory and an env file:

   ```bash
   sudo mkdir -p /var/lib/docker-dns-sync
   sudo tee /etc/docker-dns-sync/docker-dns-sync.env >/dev/null <<'EOF'
   ADGUARD_PASSWORD=replace-me
   EOF
   sudo chmod 0600 /etc/docker-dns-sync/docker-dns-sync.env
   ```

5. Install the systemd unit and start the service:

   ```bash
   sudo install -m 0644 deploy/systemd/docker-dns-sync.service \
     /etc/systemd/system/docker-dns-sync.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now docker-dns-sync.service
   sudo systemctl status docker-dns-sync.service
   ```

   If you mount `/var/run/docker.sock`, the service user must be able to read
   and write that socket. The sample unit has `SupplementaryGroups=docker`
   commented out for exactly that case.

6. Confirm the daemon started cleanly:

   ```bash
   sudo journalctl -u docker-dns-sync.service -n 50 --no-pager
   ```

   You should see a `reconcile completed` line with no error after it.

## Container with the root compose file

The repository ships `docker-compose.yaml` plus `config.example.toml` as the
shortest path to a working container deployment.

1. Export the AdGuard password in your shell, or add it to `.env`:

   ```bash
   echo 'ADGUARD_PASSWORD=replace-me' >> .env
   ```

   `config.example.toml` references it through `password_ref = "ENV:ADGUARD_PASSWORD"`,
   so the password never appears in the committed config.

2. Edit `config.example.toml` if needed:

   - `sources[].host_ip` is the IP your DNS clients should reach.
   - `sources[].base_domain` is appended to label-derived names (`foo`
     becomes `foo.bar.bz`).
   - `outputs[].url` must be reachable from inside the container. The default
     `http://adguardhome:3000` assumes AdGuard is on the same Docker network.

3. Bring the stack up:

   ```bash
   docker compose up -d
   docker compose logs -f docker-dns-sync
   ```

4. Confirm a reconcile happened and exited cleanly:

   ```bash
   docker compose logs docker-dns-sync | grep "reconcile completed"
   ```

   A clean start logs `reconcile completed` once on startup, then keeps
   streaming watch hints and per-event reconciles after that.

## Container with an explicit `docker run`

Use this when you want every flag visible at the command line and you are not
using Compose for the daemon itself.

```bash
docker pull ghcr.io/aaronflorey/docker-dns-sync:latest
docker run -d \
  --name docker-dns-sync \
  --restart unless-stopped \
  -e ADGUARD_PASSWORD=replace-me \
  -v /opt/docker-dns-sync/config.toml:/etc/docker-dns-sync/config.toml:ro \
  -v /opt/docker-dns-sync/state:/var/lib/docker-dns-sync \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/aaronflorey/docker-dns-sync:latest \
  -config /etc/docker-dns-sync/config.toml
```

The image runs as root on purpose so the documented `/var/run/docker.sock`
mount works with the default host socket ownership. If your host uses
rootless Docker, adjust the container user or socket permissions to match
before mounting the socket.

## Verify with the live stack

The `deploy/compose/live-test/` directory runs the daemon, AdGuard, and a
labeled `whoami` workload together. The automated smoke test is the easiest
end-to-end check the repo ships:

```bash
mise run live-test
```

What that covers:

- initial managed rewrite creation
- managed rewrite update and restore
- manual-record safety across daemon restart
- restart recovery
- persisted removal grace and cancellation when the workload returns
- dual stdout and file logging with `0600` permissions
- stale-delete verification is intentionally skipped in the default harness
  because AdGuard's rewrite listing does not expose proof-bearing provenance
  — same-key records are left in place rather than risked

The script tears the stack down with `docker compose ... down -v` and removes
the temporary runtime bind-mount directories by default. To inspect the stack
after it finishes, run `mise run live-test -- --keep-running` (or set
`KEEP_RUNNING=1`); the script then prints the exact cleanup commands.

## CLI exit behavior

The binary has one flag: `-config <path>`.

- Missing or empty `-config` exits with status 1 and writes `error: -config is required` to stderr.
- Successful parse, validate, and secret resolution run the daemon until it
  receives `SIGINT` or `SIGTERM`, at which point the context cancels and the
  process exits cleanly.
- All other startup failures (bad TOML, unknown keys, missing env vars, bad
  endpoint scheme) exit 1 with the cause on stderr. The matching list lives
  in [Troubleshooting](troubleshooting.md).

## What to read next

- Edit your config with confidence: [Configuration](configuration.md).
- Understand the safety rules before you trust label changes to delete
  records: [Architecture](architecture.md).
- Hit a problem you cannot decode: [Troubleshooting](troubleshooting.md).
- Add a new provider or change the daemon: [Development](development.md).