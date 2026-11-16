# Security

dnsync holds credentials for your DNS servers and needs access to Docker.
This page covers how to keep both under control.

## Credentials

- Use `*_ref = "ENV:NAME"` settings so passwords and tokens stay out of the
  config file. See [Secrets](configuration.md#secrets).
- Give each credential only what dnsync needs:
  - AdGuard Home: an admin user. AdGuard Home has no narrower role.
  - Pi-hole: an app password where possible, rather than your main password.
  - Technitium DNS Server: a token for a user that can only edit your
    container zone.
  - TelltaleDNS: a token with the `write` scope.
  - Cloudflare: a token limited to Zone → DNS → Edit on one zone, plus
    Account → Cloudflare Tunnel → Edit only if you use the tunnel.
- With systemd, keep secrets in an environment file readable only by root,
  such as `/etc/dnsync/dnsync.env` with permissions `0600`.
- dnsync removes secrets from its logs and from error reports sent to Sentry.

## Docker access

Access to the Docker socket is equivalent to root on the host. Anyone who can
control dnsync, or its config, can control Docker.

To limit this, connect dnsync to Docker through a socket proxy that allows only
the calls it needs. See
[Use a Docker socket proxy](deployment.md#use-a-docker-socket-proxy).

dnsync needs write access to Docker only for the Cloudflare Tunnel, where it
runs the `cloudflared` container.

## Files

- The state file is created with permissions `0600`. It holds no secrets, but
  it decides which records dnsync may delete, so protect it from changes.
- A log file set with `LOG_FILE` is also created with permissions `0600`.

## The systemd unit

The sample unit runs dnsync as an unprivileged `dnsync` user, with
`NoNewPrivileges=true`. It only gains Docker access if you add it to the
socket's group.

## The container image

The image runs as root so it can use the Docker socket with Docker's default
permissions. If you use a socket proxy instead, you can run the container as a
non-root user. That user must be able to write the state directory.

## Reporting a vulnerability

See [SECURITY.md](../SECURITY.md).
