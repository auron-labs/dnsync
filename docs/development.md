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

| Path | Responsibility |
| --- | --- |
| `cmd/dnsync` | CLI entrypoint and signal handling. |
| `internal/config` | TOML model, validation, and `ENV:` secret resolution. |
| `internal/contracts` | Source and output provider interfaces. |
| `internal/runtime` | Provider wiring, reconciliation, retries, and watch handling. |
| `internal/providers/docker` | Docker snapshot and event source. |
| `internal/providers/adguard` | AdGuard Home rewrites output. |
| `internal/providers/pihole` | Pi-hole local DNS output. |
| `internal/providers/technitium` | Technitium DNS Server zone records output. |
| `internal/providers/telltaledns` | TelltaleDNS local names output. |
| `internal/providers/cloudflare` | Cloudflare DNS and Tunnel output. |
| `internal/state` | Persisted ownership snapshot. |
| `testdata/config` | Sample configs used in tests and docs. |

Startup flow: load and validate the config, resolve secrets, build providers
from the registry in `internal/runtime/factories.go`, open the state store,
run a startup reconcile, then reconcile on watch hints and removal deadlines.

## CI checks

The CI workflow runs:

- `go vet ./...`
- `go test ./...`
- `goreleaser ... check`
- `docker build -t dnsync-ci .`

The root `Dockerfile` stays optimized for local `docker build ...` workflows. `Dockerfile.release` is the release-only image definition used by GoReleaser's `dockers_v2` pipeline, which copies prebuilt artifacts from the GoReleaser build context.

See `.github/workflows/ci.yaml`.

## Releases

- Pushes to `main` run CI and update the release-please pull request.
- Merging that pull request creates a `vX.Y.Z` tag and GitHub release.
- The tagged GoReleaser run uploads Linux and macOS archives with checksums,
  publishes multi-arch images to GHCR, and updates the Homebrew cask in
  `auron-labs/homebrew-tap`.
- Release workflows authenticate as a GitHub App using the `APP_ID` and
  `APP_PRIVATE_KEY` secrets. The app needs contents, issues, and pull request
  write access on this repository and contents write access on
  `auron-labs/homebrew-tap`.

## Notes

- Lint and format checks run through `hk` (`mise run check`); configuration lives in `hk.pkl`, `.golangci.yml`, and the other dot-files at the repository root.
- Keep config examples in sync with `testdata/config/` and `config.example.toml`.
