# Deployment

## Host binary with systemd

`deploy/systemd/dnsync.service` is the sample unit file.

```bash
mise exec -- go build -o bin/dnsync ./cmd/dnsync
sudo install -m 0755 bin/dnsync /usr/local/bin/dnsync
sudo install -m 0644 deploy/systemd/dnsync.service /etc/systemd/system/dnsync.service
sudo systemctl daemon-reload && sudo systemctl enable --now dnsync.service
```

The sample unit reads `/etc/dnsync/dnsync.env` and starts the binary with `-config /etc/dnsync/config.toml`.

## Container deployment

### Root compose example

`docker-compose.yaml` mounts `config.example.toml`, `./state`, and `/var/run/docker.sock`.

```bash
docker compose up -d
```

The image defaults to `ghcr.io/aaronflorey/dnsync:latest` unless `DNSYNC_IMAGE` is set.

### Dockerfile

`Dockerfile` builds a static binary on Go 1.27.4 and runs it from `gcr.io/distroless/static-debian13`.

`Dockerfile.release` is the GoReleaser-only image definition for published multi-arch images. It copies the prebuilt binary from GoReleaser's per-platform build context instead of compiling inside the image build.

### Release images and archives

`.goreleaser.yaml` publishes:

- Linux and macOS tarballs
- multi-arch GHCR images
- a Homebrew cask update

See `.github/workflows/release.yaml` for the publishing workflow.

## Deployment notes

- Keep the state directory persistent across restarts.
- Use a writable config path and a writable state path.
- Grant socket access only as broadly as your deployment requires.
