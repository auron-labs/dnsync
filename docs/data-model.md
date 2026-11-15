# Data model

## State snapshot

The persisted snapshot lives at `state.path` and is JSON encoded.

### Schema

```json
{
  "version": 3,
  "installation_id": "123e4567-e89b-42d3-a456-426614174000",
  "managed_records": [
    {
      "output": { "Type": "adguard", "Name": "primary-adguard" },
      "source": {
        "Provider": { "Type": "docker", "Name": "local-docker" },
        "ID": "...",
        "DisplayName": "whoami"
      },
      "hostname": "whoami.test",
      "answer": "127.0.0.1",
      "proxied": false,
      "provenance": { "RemoteID": "...", "InstallationID": "..." },
      "last_applied_at": "2026-05-23T00:00:00Z",
      "remove_after": null
    }
  ],
  "managed_tunnels": [
    {
      "output": { "Type": "cloudflare", "Name": "primary-cloudflare" },
      "source": { "Type": "docker", "Name": "local-docker" },
      "account_id": "...",
      "name": "dnsync",
      "tunnel_id": "...",
      "sidecar_id": "...",
      "image": "cloudflare/cloudflared:2026.7.3",
      "routes": [
        {
          "hostname": "whoami.test",
          "service": "http://whoami:80",
          "source": {
            "Provider": { "Type": "docker", "Name": "local-docker" },
            "ID": "...",
            "DisplayName": "whoami"
          },
          "network_ids": ["..."]
        }
      ],
      "last_applied_at": "2026-05-23T00:00:00Z",
      "remove_after": null
    }
  ]
}
```

The top-level snapshot fields and the `ManagedRecord` / `ManagedTunnel` /
`ManagedTunnelRoute` fields are snake_case because they define explicit JSON
tags. The nested provider/source reference structs use Go's default exported
field names because they do not define JSON tags.

### Fields

| Field | Notes |
| --- | --- |
| `version` | Snapshot format version. Current value: `3`; version 1 and 2 snapshots migrate automatically. |
| `installation_id` | Cryptographically random UUID v4 identifying this state installation. It is generated once and must remain with the state file. |
| `managed_records` | Managed output records owned by the daemon. |
| `managed_tunnels` | Dedicated Cloudflare tunnel and connector ownership, including route deadlines and network identity. |
| `output` | Output provider reference. |
| `source` | Source object reference (Docker container ID and display name). |
| `hostname` | Managed hostname. |
| `answer` | Managed answer target. |
| `proxied` | `true` when Cloudflare proxy is enabled (tunnel routes publish a proxied CNAME instead of an ordinary DNS record). |
| `provenance` | Provider-specific remote reference (`RemoteID`) returned from the last successful `Create` / `Update`. Cloudflare also exposes its installation comment as `InstallationID`; the planner uses the remote ID from state or the current installation marker as positive ownership evidence. |
| `last_applied_at` | Time the record was last applied. |
| `remove_after` | Set when the record is pending deletion; the planner deletes the remote record and drops the state entry once this time is in the past. |
| `routes` | Per-hostname tunnel routes for a managed Cloudflare tunnel. |
| `service` | HTTP origin (`http://container:port`) used by the connector. |
| `network_ids` | Docker networks the connector attaches to, kept so reconnect and cleanup stay consistent. |
| `tunnel_id`, `sidecar_id`, `account_id`, `image` | Identity of the remotely managed tunnel and its connector sidecar. |

## Persistence behavior

- The store creates the file if it does not exist.
- Writes are atomic.
- The file is saved with `0600` permissions where supported.
- Version 1 and 2 snapshots migrate automatically to version 3 and receive one persisted installation UUID. Keep that ID with the state backup so Cloudflare ownership comments continue to identify this installation after restart or restore. Tunnel tokens are never persisted; the daemon re-fetches a fresh token on every reconcile when the tunnel is still required.

## Where it is used

- `internal/state/model.go`
- `internal/state/store.go`
- `internal/runtime/reconcile.go`
- `internal/runtime/reconcile_apply.go`
