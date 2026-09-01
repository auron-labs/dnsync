# Live Test Stack

This stack gives you a local AdGuard Home instance, the `docker-dns-sync` daemon under test, and one labeled sample workload.

The default config is AdGuard-only so the smoke test can run without any external DNS provider credentials.

## Services

- `adguardhome` exposes the admin/API UI on `http://127.0.0.1:13000`.
- `docker-dns-sync` watches the host Docker socket and syncs daemon-owned rewrites into AdGuard.
- `whoami` is a disposable labeled workload used only to drive managed rewrite creation in AdGuard; it is not published on a host port.
- `adguard-init` runs once to complete AdGuard's first-install flow automatically.

## Prerequisites

- `docker`
- `docker compose`
- `curl`
- Optional: `dig` for host-side DNS checks. If `dig` is missing, `deploy/compose/live-test/verify.sh` falls back to a containerized `nslookup` check.

## Automated run

Prefer the automated smoke test instead of manual `docker compose` steps:

```bash
mise run live-test
```

What it covers by default:

- initial managed rewrite creation
- managed rewrite update and restore
- manual-record safety across daemon restart
- restart recovery
- persisted removal grace and cancellation when the workload returns
- dual stdout/file logging, `0600` log permissions, and the configured rotation settings
- stale AdGuard rewrite deletion after the removal grace period

Cleanup defaults to `docker compose -f deploy/compose/live-test/compose.yaml down -v` on exit and removes the temporary runtime bind-mount directories. Keep the stack running only when you need to inspect it:

```bash
mise run live-test -- --keep-running
# or
KEEP_RUNNING=1 mise run live-test
```

In keep-running mode, the script preserves the temporary runtime directories that back the AdGuard and state bind mounts, then prints the exact `docker compose ... down -v` and `rm -rf ...` cleanup commands to run when you are done inspecting the stack.

Optional external outputs should be added only when you want to exercise them explicitly, and any secret must stay as an environment reference such as `api_key_ref = "ENV:CLOUDFLARE_API_KEY"`.

The fixture uses a short ten-second grace period and writes its second log sink
to the temporary state mount. The script reads protected state/log files via
Docker so the test can preserve the daemon's `0600` permissions.

## Credentials and log hygiene

- The test stack uses fixed local-only AdGuard credentials inside the compose fixture.
- Do not paste real credentials inline when extending the stack or copying commands into docs.
- Do not echo secret environment values, enable `set -x`, or dump compose logs by default.
- If you need extra diagnostics, inspect only the specific service or API response relevant to the failure and avoid sharing secret-bearing config.

## Scope note

The live harness verifies deletion of one stale AdGuard rewrite matching the hostname/answer in daemon state. AdGuard Home exposes no stable rewrite ID, so this policy can delete an indistinguishable manual replacement. Same-key duplicates remain blocked and are not covered by the harness.

## Why The Sample Labels Look Like This

The daemon does not derive a default answer target from a local Unix Docker socket endpoint. For local-socket testing, the sample workload needs an explicit host label, so the stack sets `proxy.whoami.test.host=127.0.0.1`.
