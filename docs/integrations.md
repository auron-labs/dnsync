# Integrations

## Docker source

The Docker provider uses the Moby client with API version negotiation. It reads container snapshots and watches container and network events as hints for reconciliation.

### Label contract

- `proxy.aliases` defines aliases.
- `proxy.dns` gates DNS output selection: unset or `true` targets all outputs, `false` disables DNS for that container, and values like `adguard` or `cloudflare` target a single output type.
- `proxy.<alias>.port` declares a named alias.
- `proxy.#<n>.port` and `proxy.*.port` support indexed and wildcard ports.
- `proxy.<alias>.host`, `proxy.#<n>.host`, and `proxy.*.host` override the answer target.
- `proxy.exclude=true` opts a container out.
- If `base_domain` is configured on the source, bare aliases are expanded to FQDNs before reconciliation.
- `exclude_compose_projects` ignores every container whose standard Compose project label matches a configured project name. Matching is case-insensitive and surrounding whitespace in configuration is ignored.

When labels stop producing a record, stale cleanup normally requires exactly one same-key record with non-empty daemon-owned provenance. For AdGuard, dnsync instead deletes exactly one visible hostname/answer match from daemon state. This can delete an indistinguishable manual replacement because AdGuard exposes no stable rewrite ID; ambiguous duplicates remain blocked.

## AdGuard Home output

The AdGuard provider uses the control API under `/control/rewrite/*` with basic auth and JSON requests.

- List: `GET /control/rewrite/list`
- Add: `POST /control/rewrite/add`
- Update: `PUT /control/rewrite/update`
- Delete: `POST /control/rewrite/delete`

Temporary statuses treated as retryable include `408`, `429`, and `5xx` responses.

AdGuard's rewrite listing has no stable record ID. Stale cleanup therefore relies on the hostname/answer persisted by the daemon and is intentionally destructive for a unique exact match.

## TelltaleDNS output

The TelltaleDNS provider uses the local names API under `/api/v1/records` with a bearer API token.

- List: `GET /api/v1/records`
- Set a name's records: `PUT /api/v1/records/{name}`
- Remove a name: `DELETE /api/v1/records/{name}`

`PUT` replaces every API-defined record of a name, so each create, update, or delete reads the name's current records and writes back the full set. Records of types dnsync does not manage (`TXT`, `MX`, `SRV`, `PTR`) on the same name are preserved. Names defined in TelltaleDNS config files are read-only; they appear as visible records, and changes to them fail with `409`.

Answers that parse as IPv4 or IPv6 addresses become `A` or `AAAA` records; anything else becomes a `CNAME`. Created records use TelltaleDNS's default TTL.

Records carry no stable ID, so like AdGuard, stale cleanup relies on the hostname/answer persisted by the daemon and is destructive for a unique exact match. Temporary statuses treated as retryable include `408`, `429`, and `5xx` responses.

## Cloudflare output

The Cloudflare provider uses an API token and a zone ID.

- It lists DNS records for the zone and keeps hostnames as FQDNs in the daemon model.
- It caches record IDs for update/delete.
- It creates `A`, `AAAA`, or `CNAME` records based on the target value.
- It stamps created and updated records with `dnsync:managed-by=<installation UUID>` and reads that comment during recovery.
- It recovers duplicate-create error code `81058` by re-reading visible records.

## Upstream references

This repository's source tree is the best reference for supported behavior:

- `internal/providers/docker/*`
- `internal/providers/adguard/*`
- `internal/providers/telltaledns/*`
- `internal/providers/cloudflare/*`
