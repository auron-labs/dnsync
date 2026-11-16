# Troubleshooting

Start with dnsync's log. With Docker Compose, run `docker compose logs dnsync`.
With systemd, run `journalctl -u dnsync.service`. For more detail, set the
environment variable `LOG_LEVEL=debug` and restart dnsync.

## A container doesn't get a record

Set `LOG_LEVEL=debug` and look for `skipped docker DNS record derivation`
with your container's name. The `reason` and `hint` fields say what's wrong.

| Reason | Fix |
| --- | --- |
| `no supported alias/port` | Give the container a name with a port, such as `proxy.aliases: app` and `proxy.app.port: "80"`. See [Container labels](configuration.md#container-labels). |
| `empty_answer_target` | dnsync has no address for the record. Set `host_ip` on the source, or add a `proxy.<name>.host` label. |
| `proxy.dns=false` | The container opted out of DNS. Remove the label or set it to `true`. |
| `proxy.exclude` | The container opted out of dnsync. Remove `proxy.exclude=true`. |

If no such line appears, check these:

- **The name already has a record.** dnsync never replaces a record you
  created. Delete yours, then restart dnsync or the container, and dnsync
  creates its own.
- **`proxy.dns` names another output type.** The record only goes to outputs
  of that type.
- **The container belongs to an excluded Compose project.** Check
  `exclude_compose_projects` in your source.
- **The DNS server has two identical records for the name.** dnsync logs
  `skipping duplicate visible records` and won't pick one. Delete the
  duplicate.

## Records for removed containers aren't deleted

This is expected in two cases.

**The grace period hasn't ended.** dnsync waits 10 minutes by default before
removing a record. Change this with
[`removal_grace_period`](configuration.md#runtime).

**The DNS server is AdGuard Home, Pi-hole, or TelltaleDNS.** dnsync can't
prove a record on these servers is still its own, so it keeps the record and
logs `blocked managed record cleanup without provenance`. Delete the record on
the DNS server. dnsync then stops tracking it.
[Removing records](architecture.md#removing-records) explains why.

On Technitium DNS Server and Cloudflare, dnsync only deletes records that still
carry its `dnsync:managed-by=` comment. If someone removed the comment, delete
the record yourself.

## dnsync exits at startup

dnsync prints the reason as `error: …` and exits with status 1.

| Message contains | Cause and fix |
| --- | --- |
| `-config is required` | Start dnsync with `-config /path/to/config.toml`. |
| `stat config file` | The config file doesn't exist, or dnsync's user can't read it. |
| `decode config file` | The file isn't valid TOML, or has a setting dnsync doesn't recognise. The message names the line or key. Check spelling against [Configuration](configuration.md). |
| A setting name, such as `outputs[0].url is required` | That setting is missing or invalid. `outputs[0]` is the first `[[outputs]]` block, counting from 0. |
| `references an unset environment variable` | An `ENV:` reference points to a variable that isn't set or is empty. Set it where dnsync runs: the Compose `environment`, or the systemd environment file. |
| `must set exactly one of` | Set either the plain setting or its `*_ref` version, not both. |
| `decode state file` | The state file is damaged. Restore it from a backup. See [If you lose the state file](integrations.md#if-you-lose-the-state-file). |
| `unsupported state snapshot version` | The state file was written by a newer dnsync. Upgrade dnsync, or restore an older backup. |
| `open LOG_FILE` | dnsync can't create the file named in `LOG_FILE`. Check the path and permissions. |

## dnsync logs `reconcile failed; retrying later`

A DNS server or Docker returned an error. dnsync keeps running, makes every
other change it can, and retries until the failing one succeeds. The `error`
field names the output and the record.

**Wrong password or token.** The DNS server returns `401` or `403`. Check the
credential and its permissions, then restart dnsync. It reads secrets only at
startup. See [DNS servers](integrations.md).

**The DNS server is unreachable.** Check the `url` from where dnsync runs. In a
container, `localhost` means the dnsync container itself.

**Technitium: `zone "…" does not exist`.** Create the zone in Technitium, or
fix `zone` in your config and restart dnsync.

**Technitium: `has no "dnsync:managed-by=…" comment; leaving it unchanged`.**
A record dnsync created lost its comment, or was replaced by one you made.
Delete the record, or add the comment back.

**Pi-hole refuses changes.** Your local DNS records are set through `FTLCONF_`
environment variables, or Pi-hole is in read-only mode. See
[Pi-hole](integrations.md#pi-hole).

**Pi-hole: `also defines other names`.** A Pi-hole entry lists several names,
and dnsync needs to change one of them. Split the entry into one name per line
in Pi-hole.

**TelltaleDNS returns `409`.** The name is defined in TelltaleDNS's config
files, which dnsync can't change. Remove it from the config file, or stop
giving the name to a container.

**Cloudflare Tunnel: `tunnel derivation failed`.** A container routed through
the tunnel has no numeric port, or isn't attached to a network. Fix the
container's labels or networks.

## dnsync stops with a state file error

dnsync stops if it can't read or write its state file while running, for
example because the disk is full or the directory was removed. It won't change
records it can't record. Fix the file or its directory, then start dnsync
again.

## dnsync lost its connection to Docker

dnsync logs `source watch disconnected` and keeps trying to reconnect. Once
it's back, it runs a full sync, so no changes are missed. If it doesn't
reconnect, check that Docker or your socket proxy is running, and that dnsync
can still reach it.

## Check what's on the DNS server

To confirm what clients see, ask the DNS server directly:

```bash
dig +short app.home.arpa @192.168.1.2
```

Replace the name and the server's address with your own.
