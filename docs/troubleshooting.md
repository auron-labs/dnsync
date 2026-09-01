# Troubleshooting

Every symptom on this page maps to a real error path in the codebase. The
exact wording depends on the inputs; match the leading token to the relevant
cause, not to a literal character-for-character line.

## The daemon exits before reaching steady state

### It complains that `-config` is required

**Cause:** The binary was started without the only flag it accepts, or with an
empty path.

**Fix:** Pass `-config /path/to/config.toml` on the command line.

### It mentions `stat config file`

**Cause:** The path you passed does not exist or is unreadable by the daemon's
user. The startup loader calls `os.Stat` on the path and wraps any error with
this prefix.

**Fix:** Confirm the file exists and is readable. The repository ships
`testdata/config/example.toml`, `testdata/config/docker-container.toml`, and
`testdata/config/socket-proxy.toml` as known-good starting points.

### It mentions `decode config file`

**Cause:** TOML parsing failed, or the loader found an unknown key. The loader
rejects misspelled fields, unexpected sections, and stale options instead of
silently ignoring them.

**Fix:** Read the error closely:

- For syntax errors, fix the TOML and try again. `testdata/config/malformed.toml`
  is a working parse-failure example to compare against.
- For unknown keys, rename or remove the listed field. See
  [Configuration](configuration.md) for the supported key list.

### It mentions `unix://` or `tcp://`

**Cause:** The `sources[].endpoint` field is missing or uses a scheme other
than the two supported transports.

**Fix:** Use a `unix://` socket path or a `tcp://` host:port endpoint.

### It mentions `exactly one of` or `references an unset environment variable`

**Cause:** The AdGuard or Cloudflare output is missing one of the credential
fields, set both an inline value and an `*_ref`, or set a `*_ref` whose
environment variable is unset or empty.

**Fix:**

- Pick exactly one of `password`/`password_ref` (AdGuard) and one of
  `api_key`/`api_key_ref` (Cloudflare).
- Export the referenced environment variable before starting the daemon.
- Restart after fixing; secret resolution happens once at startup.

### The state file looks stale or corrupted

**Cause:** The state file has a `version` newer than the running daemon, or
the JSON is malformed. The loader wraps JSON decode failures with a
`decode state file` prefix and rejects unknown versions with an
`unsupported state snapshot version` prefix.

**Fix:**

- If you just upgraded and the file is newer than the daemon supports, stop
  upgrading and roll back to a build that can read it.
- If the file is corrupted or hand-edited, point `state.path` at a fresh
  writable file. Deleting the old file is safe; the store creates a new empty
  snapshot on startup when the path is missing.

## Reconciliation is happening but produces nothing useful

### It logs a visible-record ambiguity error

**Cause:** An output already has more than one visible record for the same
hostname/answer pair. The reconciler refuses to guess which one belongs to
the daemon and skips it.

**Fix:** Remove the duplicate from the output (AdGuard rewrite list or
Cloudflare DNS list), then trigger another reconcile by restarting the
daemon or waiting for the next Docker event.

### Stale DNS records are not getting deleted

**Cause (almost always):** The output has an ambiguous duplicate hostname and
answer pair, so the reconciler refuses to select one. A unique stale AdGuard
hostname/answer match is deleted from daemon state after the removal grace
period. Other outputs still require visible daemon-owned provenance.

**Fix:**

- Confirm the record is actually stale (label removed, container gone).
- Remove duplicate hostname/answer entries from the output, then trigger
  another reconcile.
- See [Architecture → Reconciliation model](architecture.md#reconciliation-model)
  for the precise rule.

### The Docker event stream dropped

**Cause:** The Docker daemon, socket, or proxy became unavailable. The source
goroutine reports a `docker event stream ended` error on its error channel
and the runtime reconnects with bounded backoff.

**Fix:**

- Check the Docker daemon and socket permissions.
- For `tcp://` endpoints, confirm the proxy or remote daemon is still
  reachable.
- A full reconcile still happens on every reconnect, so manual recovery is
  usually unnecessary.

## AdGuard and Cloudflare lookups fail

### `adguard request` errors

**Cause:** The AdGuard API returned a non-2xx status, the connection failed,
or the request body could not be encoded. Temporary statuses (`408`, `429`,
`5xx`) are retried with bounded backoff; everything else surfaces.

**Fix:**

- Verify the URL, username, and password in your config.
- Confirm AdGuard Home is reachable from where the daemon runs.
- If you need a direct API check, run
  `curl -u user:pass <url>/control/rewrite/list` against the same URL the
  daemon uses.

### Cloudflare mutations report duplicate or unknown errors

**Cause:** Record creation may collide with a pre-existing record. The
provider recovers from the `81058` duplicate-create error by re-reading
visible records and trying again; other codes surface.

**Fix:** Inspect the relevant zone in the Cloudflare dashboard to confirm
the conflict is gone, then restart the daemon.

## Quick diagnostic commands

```bash
mise exec -- go test ./...
docker compose -f deploy/compose/live-test/compose.yaml logs -f docker-dns-sync
curl -u admin:adguard-test-password http://127.0.0.1:13000/control/rewrite/list
```

The AdGuard URL above matches the live test stack only; use your own URL
and credentials in production.
