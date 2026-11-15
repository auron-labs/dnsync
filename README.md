# dnsync

[![License](https://img.shields.io/github/license/auron-labs/dnsync?style=flat-square)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/auron-labs/dnsync/ci.yaml?branch=main&style=flat-square&label=ci)](https://github.com/auron-labs/dnsync/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/auron-labs/dnsync?display_name=tag&style=flat-square)](https://github.com/auron-labs/dnsync/releases)

`dnsync` is a Go daemon that watches Godoxy-labeled Docker workloads and keeps daemon-owned AdGuard Home, TelltaleDNS, Pi-hole, and Cloudflare DNS records in sync. An optional label-driven Cloudflare Tunnel lifecycle creates a dedicated remote tunnel and managed `cloudflared` sidecar.

## Installation

- Install from Homebrew with `brew install --cask auron-labs/tap/dnsync`.
- Install the release binary with [`bin`](https://github.com/aaronflorey/bin): `bin install github.com/auron-labs/dnsync`.
- Download the latest release archive for your platform from GitHub Releases and place `dnsync` on your `PATH`.
- Build from source with `mise install && mise run build`.
- Pull the release container image from `ghcr.io/auron-labs/dnsync:<tag>`.

## What It Does

- Performs a full reconcile on startup using Docker snapshot state and the persisted ownership file.
- Watches Docker for container lifecycle hints and reruns reconciliation after changes.
- Retries temporary AdGuard failures with bounded backoff before giving up.
- Deletes a stale AdGuard rewrite when exactly one visible record matches its daemon state entry's hostname and answer. This state-backed cleanup can delete an indistinguishable manual replacement; ambiguous duplicates remain blocked.
- A global removal grace period defaults to ten minutes and applies consistently to daemon-owned DNS records and tunnel resources; updates remain immediate.
- Logs are structured on stdout and can be duplicated to a concurrency-safe rotating `LOG_FILE` sink.

## Configuration

The binary contract is:

```text
dnsync -config /etc/dnsync/config.toml
```

Start from `testdata/config/example.toml` for a host-binary deployment with a local Docker socket, `config.example.toml` or `testdata/config/docker-container.toml` for a Docker container deployment that mounts `/var/run/docker.sock`, or `testdata/config/socket-proxy.toml` for a TCP proxy or remote Docker source.

Required config sections:

- `[[sources]]` for Docker connectivity.
- `[[outputs]]` for AdGuard Home connectivity.
- `[state]` for the writable ownership snapshot path.
- `[logging]` for log level and format.
- `[runtime]` for per-operation timeouts on provider reads and writes.
- `[retry]` for bounded backoff settings.

`[runtime] operation_timeout` defaults to `10s`. It applies per source/output read or write call (`ListDesired`, `ListVisible`, `Create`, `Update`, `Delete`), not to `Watch(ctx)` streams and not to the full reconcile pass. Retry backoff starts only after an operation returns or times out.

For Docker sources, set `sources[].host_ip` to the host-reachable IP address that unlabeled records should answer with. Explicit `proxy.<alias>.host`, `proxy.#<n>.host`, and `proxy.*.host` labels still override that shared source IP.

Use `password_ref = "ENV:ADGUARD_PASSWORD"` instead of embedding credentials in committed config.

For Cloudflare Tunnel configuration, use `api_key_ref = "ENV:CLOUDFLARE_API_KEY"`
and the optional `[outputs.tunnel]` block described in
[configuration](docs/configuration.md). The API token needs Zone DNS write and
Account Tunnel write permissions. The Docker socket (or proxy) additionally
needs container, image, and network write access so the daemon can manage the
connector sidecar. Never grant or document real tokens inline.

### Error reporting

Set `SENTRY_DSN` to report errors to Sentry. Error-level log records and fatal startup or runtime errors are sent after secret redaction; info and debug logs are not. Leave it unset to disable reporting. `SENTRY_ENVIRONMENT` optionally sets the environment name.

## Development

```bash
mise install
mise run setup   # download modules and install the Git hooks
mise run build   # build bin/dnsync
mise run test    # unit tests (Docker-free)
mise run check   # lint and format checks via hk
mise run fix     # apply automatic fixes
mise run live-test  # opt-in Docker/AdGuard smoke test
```

- `mise run live-test` requires `docker`, `docker compose`, and `curl`; `dig` is optional because the script falls back to containerized `nslookup`. It cleans up with `docker compose ... down -v` plus temporary runtime directory removal unless `--keep-running` or `KEEP_RUNNING=1` is used; keep-running mode prints the manual cleanup commands you must run after inspection.
- Keep secrets out of logs and shell history: use env references, do not paste real credentials inline, and avoid dumping compose logs by default.
- Stale AdGuard cleanup is state-backed: one exact visible hostname/answer match is deleted after its workload is removed. Because AdGuard exposes no stable rewrite ID, do not manually replace a daemon-managed rewrite with the same values if you need it preserved.
- Renovate is configured through `renovate.json` for Go modules, GitHub Actions, and container image references.

See [docs/development.md](docs/development.md) for repository layout and CI details.

## Release Automation

- Pushes to `main` run CI and update the release-please PR.
- Merging the release PR creates a `vX.Y.Z` tag and GitHub release.
- Release publishing uploads macOS and Linux archives plus checksums.
- The same tagged GoReleaser run publishes multi-arch container images to GHCR.
- The same tagged GoReleaser run updates the Homebrew cask in `auron-labs/homebrew-tap`.
- The release workflow requires a `HOMEBREW_TAP_GITHUB_TOKEN` secret with push access to `auron-labs/homebrew-tap`.

## Host Binary With systemd

1. Build the binary with `mise exec -- go build -o bin/dnsync ./cmd/dnsync`.
2. Install the binary to `/usr/local/bin/dnsync`.
3. Copy `testdata/config/example.toml` to `/etc/dnsync/config.toml` for a local Docker socket source, or start from `testdata/config/socket-proxy.toml` if Docker is reachable through a proxy or remote endpoint. Keep a writable state path such as `/var/lib/dnsync/state.json`.
4. Create an environment file at `/etc/dnsync/dnsync.env` with `ADGUARD_PASSWORD=...` (and optionally `SENTRY_DSN=...`).
5. Install `deploy/systemd/dnsync.service` to `/etc/systemd/system/dnsync.service`.
6. Run `sudo systemctl daemon-reload && sudo systemctl enable --now dnsync.service`.

Notes:

- The sample unit intentionally does not require `docker.service`, so it also works when the Docker source uses `tcp://...` instead of the local socket.
- If you use `/var/run/docker.sock`, the service user must be able to read and write that socket. A common host setup is to add the service user to the group that owns the socket and uncomment `SupplementaryGroups=docker` in the sample unit if that group name is `docker`.
- The state file path must be writable by the service user.
- Restart behavior is explicit in the unit file: `Restart=on-failure`.

## Docker Deployment

1. Pull a release image with `docker pull ghcr.io/auron-labs/dnsync:<tag>`, or build locally with `docker build -t dnsync .`.
2. Use the root `docker-compose.yaml` plus `config.example.toml` for the simplest container deployment, or start from `testdata/config/docker-container.toml` / `testdata/config/socket-proxy.toml` for a custom deployment.
3. Create a writable host directory for state such as `/opt/dnsync/state`.
4. Provide the AdGuard password through an environment variable.
5. Run the container:

```bash
docker run -d \
  --name dnsync \
  --restart unless-stopped \
  -e ADGUARD_PASSWORD=replace-me \
  -v /opt/dnsync/config.toml:/etc/dnsync/config.toml:ro \
  -v /opt/dnsync/state:/var/lib/dnsync \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/auron-labs/dnsync:<tag> \
  -config /etc/dnsync/config.toml
```

Notes:

- The container image runs as root on purpose so the documented `/var/run/docker.sock` mount works with the default host socket ownership and mode used by Docker installations.
- For local Docker socket sources, set `host_ip` in the source config to the host-reachable address you want all derived records to answer with. Use `proxy.<alias>.host`, `proxy.#<n>.host`, or `proxy.*.host` only when a specific container needs a different answer target.
- `testdata/config/example.toml` is intentionally host-oriented and leaves AdGuard at `127.0.0.1`; do not reuse it unchanged inside the container unless AdGuard is reachable at container-local localhost.
- Mounting `/var/run/docker.sock` gives the container broad Docker control. If you do not want to grant that access, use a `tcp://...` endpoint such as `testdata/config/socket-proxy.toml` and give the container only the network path to that proxy or remote daemon.
- If your host uses rootless Docker or a non-standard socket owner, adjust the container user or socket permissions to match that environment before mounting the socket.
- Keep the state directory persistent across restarts so ownership recovery works.
- The container image runs the real CLI contract with `-config` and does not carry built-in config defaults.

## Example Files

- `testdata/config/example.toml` - env-ref based example config.
- `config.example.toml` - root-level compose-oriented example config.
- `docker-compose.yaml` - root-level container deployment example.
- `testdata/config/docker-container.toml` - env-ref based Docker container config for `/var/run/docker.sock` plus network-reachable AdGuard.
- `testdata/config/socket-proxy.toml` - env-ref based TCP proxy or remote Docker example config.
- `deploy/systemd/dnsync.service` - starting point for host deployment.
- `Dockerfile` - local container build for Docker deployment.
- `Dockerfile.release` - GoReleaser container build for published multi-arch images.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for local setup and pull request expectations. Report vulnerabilities as described in [SECURITY.md](SECURITY.md).

## License

Released under the MIT License. See [LICENSE](LICENSE).
