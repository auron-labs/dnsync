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

## Pi-hole output

The Pi-hole provider uses the Pi-hole v6 REST API. The Pi-hole v5 `admin/api.php` API is not supported.

- Log in: `POST /api/auth`, then send the session ID as `X-FTL-SID`
- List: `GET /api/config/dns`, reading `dns.hosts` and `dns.cnameRecords`
- Add an entry: `PUT /api/config/dns/{hosts|cnameRecords}/{entry}`
- Remove an entry: `DELETE /api/config/dns/{hosts|cnameRecords}/{entry}`
- Log out: `DELETE /api/auth` when dnsync stops

dnsync logs in on first use and reuses the session, which Pi-hole extends on every request. If Pi-hole answers `401` because the session expired or was invalidated, dnsync logs in again and retries the request once. Logging out on shutdown keeps the session from holding one of Pi-hole's `webserver.api.max_sessions` seats until it expires.

Answers that parse as IPv4 or IPv6 addresses become `dns.hosts` entries (`IP HOSTNAME`); anything else becomes a `dns.cnameRecords` entry (`HOSTNAME,TARGET`). Changing a CNAME on the same hostname removes the old entry before adding the new one, because Pi-hole rejects a second CNAME for a name. Each CNAME change restarts Pi-hole's DNS resolver; requests that fail during the restart are retried.

Entries that list several names, such as `IP NAME1 NAME2` or `NAME1,NAME2,TARGET`, appear as visible records but are never rewritten. A change that would remove one of their names fails, and the entry must be edited in Pi-hole.

Pi-hole refuses API changes to `dns.hosts` or `dns.cnameRecords` while they are set through `FTLCONF_` environment variables or `misc.readOnly` is enabled.

Entries carry no provenance, so like AdGuard, dnsync only changes entries backed by its own state and leaves manual entries alone. When labels stop producing a record, dnsync keeps the entry and logs a blocked-cleanup warning instead of deleting it; remove it in Pi-hole if it is no longer needed. Network failures and `408`, `429` (including exhausted API seats), and `5xx` responses are retryable.

## Technitium DNS Server output

The Technitium provider manages records in one authoritative zone through the HTTP API, sending form-encoded `POST` requests with the API token in the `Authorization: Bearer` header.

- List: `/api/zones/records/get` with `listZone=true`
- Add: `/api/zones/records/add`
- Update: `/api/zones/records/update`
- Delete: `/api/zones/records/delete`

Answers that parse as IPv4 or IPv6 addresses become `A` or `AAAA` records; anything else becomes a `CNAME`. Other record types in the zone are never changed. Created records use Technitium's default TTL. An update keeps the record's TTL, disabled state, expiry, and comments; a change between record types deletes the old record and adds a new one. Deleting an `A` or `AAAA` record also removes a matching `PTR` record from a primary reverse zone, which is Technitium's own behavior.

dnsync stamps every record it creates with the comment `dnsync:managed-by=<installation UUID>`. Technitium has no record IDs, so dnsync identifies a record by its type, name, and value, and treats that comment as ownership proof, as it does for Cloudflare. Stale cleanup deletes a record only when it matches daemon state and still carries this installation's comment. Records with the comment are also recovered without a state entry, for example after the state file is lost: when a commented record is the only record on its name, dnsync adopts it if it matches a desired record and deletes it otherwise. Records without the comment, or with another installation's, are never changed; updates and deletes re-check the comment and fail rather than touch them. Keep the comment when editing a managed record in the Technitium console; other notes can sit alongside it.

Technitium resolves a missing zone to its closest parent zone, so listing fails when the zone Technitium returns differs from the configured `zone`. API failures, including invalid tokens, arrive as HTTP `200` with a non-`ok` status and are not retried. Temporary statuses treated as retryable include `408`, `429`, and `5xx` responses, along with connection failures.

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
- `internal/providers/pihole/*`
- `internal/providers/technitium/*`
- `internal/providers/cloudflare/*`
