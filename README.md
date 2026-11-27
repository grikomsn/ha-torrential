<img src="assets/icon.svg" width="64" height="64" alt="">

# ha-torrential

A Home Assistant app repository for self-hosting
**[torrential](https://github.com/nexxeln/torrential)**, which finds films and
shows and plays them on your phone, computer or TV while they download, on
Home Assistant OS, with optional:

- **Cloudflare Tunnel** to serve it on your own domain or subdomain, and
- **Tailscale Serve/Funnel** to serve it on your tailnet or the internet.

No router ports need to be opened for either.

## Install

[![Add repository to Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fgrikomsn%2Fha-torrential)

Or add `https://github.com/grikomsn/ha-torrential` under **Settings → Apps →
Install app → ⋮ → Repositories**, then install **torrential**.

## Apps

| App | Description |
| --- | --- |
| [torrential](torrential/) | torrential 0.7.0 with built-in cloudflared and Tailscale. [Docs](torrential/DOCS.md) |

## How it fits together

```text
Browser ─────────HTTPS──> Cloudflare edge ──tunnel──> cloudflared ─────┐
Tailnet devices ─HTTP(S)─> Tailscale Serve (torrential.<tailnet>.ts.net)┼─> torrential :47919 ─> /data, /media
Home network ────HTTP────> homeassistant.local:47919 ──────────────────┘   (one app container, on the host's network)
```

The app's Dockerfile starts from the official `ghcr.io/nexxeln/torrential`
image, adds the `tailscale`, `cloudflared` and `yq` binaries from their
official images, and wraps the upstream `torrential engine run --server` with
[`run.sh`](torrential/rootfs/ha/run.sh), which writes the app's options into
torrential's `config.yaml`.

torrential has no accounts: a device gets in by pairing with a code that only
the server can make. [`pair.sh`](torrential/rootfs/ha/pair.sh) prints that
code in the app's Log tab, and the tunnels are set up so that torrential sees
their visitors as devices that have to pair, not as the server itself. The
[smoke test](.github/scripts/smoke_test.sh) checks that on every build.

## Releases

Versions follow torrential (`0.7.0`), with a fourth number for packaging-only
changes (`0.7.0.1`). Each release publishes Cosign-signed `amd64`/`aarch64`
images to `ghcr.io/grikomsn/ha-torrential` and a
[GitHub release](https://github.com/grikomsn/ha-torrential/releases).
Dependencies are kept current by Renovate. See [RELEASING.md](RELEASING.md).

## License and trademarks

The packaging in this repository (Dockerfile, scripts, configuration and docs)
is [MIT licensed](LICENSE). The image it builds contains torrential (MIT,
source at <https://github.com/nexxeln/torrential>), plus Tailscale
(BSD-3-Clause), cloudflared (Apache-2.0) and yq (MIT).

This is an unofficial community project. It is not affiliated with or endorsed
by the torrential project, Tailscale Inc., Cloudflare, Inc. or the Home
Assistant project. Download only what you have the right to.

Security issues: see [SECURITY.md](SECURITY.md).
