# DNS servers

dnsync writes records to one or more DNS servers, called outputs. This page
covers what each one needs and how dnsync behaves with it. For the exact
settings, see [Outputs](configuration.md#outputs).

## Comparison

| DNS server | What dnsync manages | How dnsync recognises its records | Removes records for removed containers |
| --- | --- | --- | --- |
| [AdGuard Home](#adguard-home) | DNS rewrites | State file only | No |
| [Pi-hole](#pi-hole) | Local DNS records and CNAMEs | State file only | No |
| [Technitium DNS Server](#technitium-dns-server) | A, AAAA, and CNAME records in one zone | State file and a record comment | Yes |
| [TelltaleDNS](#telltaledns) | Local names | State file only | No |
| [Cloudflare](#cloudflare) | A, AAAA, and CNAME records in one zone | State file and a record comment | Yes |

Where dnsync can only use its state file, it can't prove that a record is still
the one it created. You might have deleted it and added an identical one by
hand. So when a container goes away, dnsync keeps the record and logs
`blocked managed record cleanup without provenance` with the record's name.
Delete the record on the DNS server, and dnsync stops tracking it.

Where dnsync can tag records with a comment, it deletes them itself after the
[removal grace period](configuration.md#runtime). The comment is
`dnsync:managed-by=` followed by the installation ID from your
[state file](data-model.md).

## Using several DNS servers

Every container's records go to every enabled output. To send a container's
records to only one kind of server, label it with that output type, such as
`proxy.dns=cloudflare`. See [Container labels](configuration.md#container-labels).

## AdGuard Home

dnsync manages AdGuard Home's DNS rewrites, listed under
**Filters → DNS rewrites**.

**What you need:** an AdGuard Home admin username and password.

```toml
[[outputs]]
type = "adguard"
name = "adguard"
url = "http://192.168.1.2:3000"
username = "admin"
password_ref = "ENV:ADGUARD_PASSWORD"
```

**Good to know:**

- dnsync doesn't delete rewrites for removed containers. Delete them in
  AdGuard Home when you see the warning.
- If you switch off a rewrite that dnsync manages, dnsync leaves it switched
  off.
- If two identical rewrites exist, dnsync won't change either. Delete one to
  let dnsync manage the other.

## Pi-hole

dnsync manages Pi-hole's local DNS records and local CNAME records. It needs
Pi-hole 6 or later.

**What you need:** the password you use for Pi-hole's web interface. If you
use two-factor authentication, create an app password instead, and set
Pi-hole's `webserver.api.app_sudo` setting to `true` so the app password can
change DNS records.

```toml
[[outputs]]
type = "pihole"
name = "pihole"
url = "http://pihole"
password_ref = "ENV:PIHOLE_PASSWORD"
```

**Good to know:**

- dnsync doesn't delete records for removed containers. Delete them in Pi-hole
  when you see the warning.
- Pi-hole restarts its DNS service whenever a CNAME changes, so DNS is briefly
  unavailable. Address records change without a restart.
- Pi-hole won't accept changes to local DNS records while they're set through
  `FTLCONF_` environment variables, such as `FTLCONF_dns_hosts` in a Docker
  setup, or while Pi-hole is in read-only mode. Remove those variables to let
  dnsync manage the records.
- A Pi-hole entry can list several names, such as `192.168.1.50 app admin`.
  dnsync never rewrites those entries. If dnsync needs to change one, it
  reports an error, and you'll need to split the entry in Pi-hole.
- dnsync keeps one login session open while it runs and signs out when it
  stops. Pi-hole allows 16 sessions by default.

## Technitium DNS Server

dnsync manages A, AAAA, and CNAME records in one zone. It needs Technitium DNS
Server 15.0 or later.

**What you need:**

- A zone for your container names, such as `home.arpa`. dnsync doesn't create
  zones. Set your source's `base_domain` to this zone or a name under it.
- An API token whose user can view, modify, and delete records in that zone.
  You can create tokens in Technitium's administration settings.

```toml
[[outputs]]
type = "technitium"
name = "technitium"
url = "http://technitium:5380"
zone = "home.arpa"
api_token_ref = "ENV:TECHNITIUM_API_TOKEN"
```

To manage more than one zone, add an output for each zone and route containers
with `proxy.dns`.

**Good to know:**

- dnsync adds the comment `dnsync:managed-by=<installation ID>` to every
  record it creates. It never changes or deletes a record without that
  comment. Keep the comment if you edit one of these records; you can add your
  own notes around it.
- Records for removed containers are deleted after the removal grace period.
- When dnsync updates a record, it keeps the record's TTL, disabled state, and
  comments.
- Deleting an A or AAAA record also deletes the matching PTR record in a
  reverse zone on the same server. Technitium does this itself.
- If the zone doesn't exist, dnsync logs an error rather than writing to a
  parent zone. It starts using the zone once you create it.

## TelltaleDNS

dnsync manages TelltaleDNS local names.

**What you need:** a TelltaleDNS API token with the `write` scope.

```toml
[[outputs]]
type = "telltaledns"
name = "telltaledns"
url = "http://telltale:8053"
api_key_ref = "ENV:TELLTALEDNS_API_KEY"
```

**Good to know:**

- dnsync doesn't delete names for removed containers. Delete them in
  TelltaleDNS when you see the warning.
- Other record types on the same name, such as TXT or MX, are kept when dnsync
  changes the name's address.
- Names defined in TelltaleDNS's config files are read-only. dnsync can see
  them but can't change them.

## Cloudflare

dnsync manages DNS records in one Cloudflare zone and can also publish
containers through a [Cloudflare Tunnel](configuration.md#cloudflare-tunnel).

**What you need:**

- The zone ID, shown on the zone's overview page in the Cloudflare dashboard.
- An API token with **Zone → DNS → Edit** permission for that zone. For a
  tunnel, the token also needs **Account → Cloudflare Tunnel → Edit**.

```toml
[[outputs]]
type = "cloudflare"
name = "cloudflare"
zone_id = "your-zone-id"
api_key_ref = "ENV:CLOUDFLARE_API_KEY"
```

**Good to know:**

- dnsync adds the comment `dnsync:managed-by=<installation ID>` to every
  record it creates or updates. Records you created yourself are never
  changed.
- Records for removed containers are deleted after the removal grace period.
- dnsync's ordinary records are DNS only. If you switch on proxying for one,
  dnsync switches it back off. Tunnel records are always proxied.

## If you lose the state file

The state file is how dnsync knows which records it created. If you lose it,
dnsync treats all of its old records as ones you made. It won't change or
delete them, and it won't create records on names they occupy. This applies to
Technitium and Cloudflare too: the file holds the installation ID in their
record comments, and a new file gets a new ID.

To recover, restore the state file from a backup. If you have no backup,
delete dnsync's old records on each DNS server, then restart dnsync to
recreate them.
