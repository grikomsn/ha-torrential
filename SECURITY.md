# Security policy

This repository packages torrential as a Home Assistant app. Please report
security problems privately.

## What to report here

Problems in this repository's own code: the [Dockerfile](torrential/Dockerfile),
the wrapper scripts in [`torrential/rootfs/ha/`](torrential/rootfs/ha/), and
the app configuration. For example:

- A way for a device to get past pairing through the built-in Cloudflare
  Tunnel or Tailscale node. torrential trusts a request from `127.0.0.1` as
  the server itself unless it carries `X-Forwarded-For` or names the public
  address, so the packaging has to make sure every outside request does.
- Secrets leaking into logs (tunnel token, Tailscale auth key, indexer keys),
  or pairing codes shown anywhere but the app's log.
- Wrong file permissions, or Tailscale or Cloudflare settings that expose more
  than the docs say.

Report them through
[GitHub private vulnerability reporting](https://github.com/grikomsn/ha-torrential/security/advisories/new).
Please don't open a public issue.

## What to report upstream

- torrential: <https://github.com/nexxeln/torrential/security>
- Tailscale: <https://tailscale.com/security>
- cloudflared: <https://www.cloudflare.com/disclosure/>

## Supported versions

Only the latest release of the app gets fixes. It tracks the latest
torrential release, so security fixes from upstream reach you through normal
app updates.
