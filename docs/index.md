# dnsync documentation

`dnsync` is a small Go daemon that watches your Docker containers,
turns a Godoxy-style label scheme into DNS records, and keeps those records in
sync with one or more DNS providers. Out of the box it talks to AdGuard Home
rewrites; Cloudflare DNS and an opt-in Cloudflare Tunnel sidecar are also
supported.

You give it a TOML config and a Docker endpoint. It does the rest.

## If you are new here

Start with **[Getting started](getting-started.md)**. It walks you through
picking a deployment style, writing a working config, and confirming that the
daemon is actually syncing records.

That page is enough for most operators. Read further only when you need to.

## If you just want to know what's possible

Scan the headings below. Each link points at the single page that owns that
topic.

| You want to… | Go to |
| --- | --- |
| Install and run the daemon for the first time | [Getting started](getting-started.md) |
| Look up a config key, label, or default | [Configuration](configuration.md) |
| Understand how reconcile decides create/update/delete | [Architecture](architecture.md) |
| See what each provider actually calls | [Integrations](integrations.md) |
| Ship a systemd unit or a container image | [Deployment](deployment.md) |
| Run the unit tests or the live smoke test | [Testing](testing.md) |
| Tweak the codebase or build a release | [Development](development.md) |
| Inspect the persisted ownership snapshot | [Data model](data-model.md) |
| Recover from a startup or runtime failure | [Troubleshooting](troubleshooting.md) |
| Handle secrets, the Docker socket, or runtime hardening | [Security](security.md) |

## Things the daemon will not do

These are deliberate limits in the current codebase, not gaps in the docs:

- It will not start without a `-config` path. There are no built-in defaults.
- It will not invent an answer target for a local Unix Docker socket. Set
  `sources[].host_ip` or label each container with `proxy.<alias>.host`.
- It deletes one stale AdGuard rewrite matching the hostname/answer saved in
  daemon state. AdGuard exposes no stable ID, so an indistinguishable manual
  replacement can be deleted; duplicate same-key entries remain blocked.
