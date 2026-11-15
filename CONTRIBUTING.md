# Contributing

## Development Setup

1. Install the pinned toolchain with `mise install`.
2. Run `mise run setup` to download modules and install the Git hooks.
3. Run the test suite with `mise run test` and static checks with `mise exec -- go vet ./...`.
4. Run `mise run check` for lint and format checks; `mise run fix` applies automatic fixes.

Commit messages follow Conventional Commits; the `commit-msg` hook enforces this and Release Please derives versions from them.

## Pull Requests

1. Keep changes focused on one concern.
2. Add or update tests when behavior changes.
3. Make sure `mise run test` passes before opening a pull request.

## Release Process

1. Push changes to `main`.
2. Release Please opens or updates a release PR.
3. Merging that PR creates a `vX.Y.Z` tag, updates release notes, and triggers artifact publishing.
