# State file

The state file is how dnsync remembers which DNS records it created. Its
location is set by [`state.path`](configuration.md#state).

## Look after it

- **Keep it on persistent storage.** In a container, mount a volume for its
  directory.
- **Back it up.** If you lose it, dnsync treats its old records as yours and
  stops managing them. See
  [If you lose the state file](integrations.md#if-you-lose-the-state-file).
- **Don't edit it.** dnsync rewrites it after every change.

dnsync creates the file if it doesn't exist, with permissions `0600`. It
replaces the file in one step, so a crash can't leave it half-written.

## Contents

The file is JSON. You can read it to see what dnsync is tracking:

```json
{
  "version": 3,
  "installation_id": "123e4567-e89b-42d3-a456-426614174000",
  "managed_records": [
    {
      "output": { "Type": "adguard", "Name": "adguard" },
      "source": {
        "Provider": { "Type": "docker", "Name": "local-docker" },
        "ID": "4f1c…",
        "DisplayName": "whoami"
      },
      "hostname": "whoami.home.arpa",
      "answer": "192.168.1.50",
      "proxied": false,
      "provenance": null,
      "last_applied_at": "2026-05-23T00:00:00Z",
      "remove_after": null
    }
  ],
  "managed_tunnels": []
}
```

| Field | Meaning |
| --- | --- |
| `version` | File format version. dnsync upgrades older versions automatically. |
| `installation_id` | A random ID for this dnsync installation. It's written into record comments on Technitium and Cloudflare, so it must stay with the file. |
| `managed_records` | The records dnsync created, one entry per output and name. |
| `output` | The output that holds the record, by type and name. |
| `source` | The container that asked for it. |
| `hostname`, `answer` | The record's name and address. |
| `proxied` | Whether the record is proxied through Cloudflare. |
| `provenance` | The server's ID for the record, plus the installation ID from its comment. Only Technitium and Cloudflare fill this in. |
| `last_applied_at` | When dnsync last wrote the record. |
| `remove_after` | Set while the container is gone. dnsync removes the record after this time. |
| `managed_tunnels` | The Cloudflare Tunnel and `cloudflared` container dnsync runs, and the containers routed through it. |

dnsync never stores the tunnel's connector token. It fetches a fresh one from
Cloudflare when needed.
