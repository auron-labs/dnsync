# Development

## Setup

```bash
mise install
```

`mise.toml` pins Go 1.27.4, and `go.mod` pins the Go toolchain to `go1.27.4`.

## Common local commands

```bash
mise run setup   # modules and Git hooks
mise run build
mise exec -- go vet ./...
mise run test
mise run check   # hk lint and format checks
mise run fix     # hk automatic fixes
docker build -t dnsync .
```

## Repository layout

- `cmd/dnsync` — CLI entrypoint.
- `internal/config` — config parsing and validation.
- `internal/providers` — Docker, AdGuard, and Cloudflare integrations.
- `internal/runtime` — reconciliation orchestration.
- `internal/state` — persisted snapshot handling.
- `testdata/config` — sample configs used in tests and docs.

## CI checks

The CI workflow runs:

- `go vet ./...`
- `go test ./...`
- `goreleaser ... check`
- `docker build -t dnsync-ci .`

The root `Dockerfile` stays optimized for local `docker build ...` workflows. `Dockerfile.release` is the release-only image definition used by GoReleaser's `dockers_v2` pipeline, which copies prebuilt artifacts from the GoReleaser build context.

See `.github/workflows/ci.yaml`.

## Notes

- Lint and format checks run through `hk` (`mise run check`); configuration lives in `hk.pkl`, `.golangci.yml`, and the other dot-files at the repository root.
- Keep config examples in sync with `testdata/config/` and `config.example.toml`.
