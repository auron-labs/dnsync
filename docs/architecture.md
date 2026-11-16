# How dnsync works

This page explains how dnsync decides which records to create, change, and
remove. It's useful when dnsync does something you didn't expect.

## Three sources of truth

Each time dnsync syncs, it compares three things:

- **Your containers.** The names and addresses your container labels ask for.
- **The DNS server.** The records that exist there right now.
- **The state file.** The records dnsync created earlier.

From these, it works out the changes needed. This pass is called a reconcile.

## When dnsync syncs

- At startup, in full.
- When Docker reports a container or network change. Changes that arrive
  together are handled in one pass.
- When a record's removal grace period ends.
- After reconnecting to Docker, if the connection dropped.

## Creating and updating records

dnsync creates a record when a container asks for a name that has no record
on the DNS server.

If the name already has an address or CNAME record that dnsync didn't create,
dnsync leaves the name alone. Your own records always win.

When a container's address changes, dnsync updates its record straight away.

## Removing records

When a container stops asking for a name, because it was removed, stopped, or
relabelled, dnsync waits for the removal grace period. The default is 10
minutes. If the container comes back in that time, nothing changes.

After the grace period, dnsync deletes the record only if it can prove the
record is still its own:

- On **Technitium DNS Server** and **Cloudflare**, dnsync tags each record with
  a comment. A record that still carries its comment, and matches the state
  file, is deleted.
- On **AdGuard Home**, **Pi-hole**, and **TelltaleDNS**, records have no IDs or
  comments. A record dnsync created looks identical to one you added by hand
  with the same name and address. So dnsync keeps it and logs
  `blocked managed record cleanup without provenance`.

When you delete a blocked record yourself, dnsync notices it's gone and stops
tracking it.

## Records dnsync won't touch

- Records you created. dnsync never changes them, even if a container asks
  for the same name.
- Records with another dnsync installation's comment, on Technitium and
  Cloudflare.
- Duplicates. If the DNS server has two identical records, dnsync can't tell
  which one is its own, so it changes neither.

## When something fails

dnsync retries temporary problems with increasing delays: timeouts, network
errors, rate limits, and server errors. The `[retry]` settings control how long
it keeps trying.

If the problem lasts longer than `max_elapsed_time`, or can't be fixed by
retrying, dnsync exits with the error. A wrong password or a missing zone are
examples. Run dnsync under systemd or a Docker restart policy so it starts
again. Each start begins with a full sync.

dnsync saves the state file after every change it makes. If it stops partway
through, it still knows which records it created.

## Related

- [DNS servers](integrations.md): how each server behaves.
- [State file](data-model.md): what dnsync stores.
- [Configuration](configuration.md#runtime): the grace period and timeouts.
