# Changelog

## 0.7.0

- First release, based on the official torrential 0.7.0 image, run as a server
  on the host's network.
- A pairing code is printed in the Log tab while no device is paired, and at
  every start with `pair_on_start`.
- Built-in cloudflared 2026.11.2 (Cloudflare Tunnel, token mode).
- Built-in Tailscale v1.104.7 (separate node; plain HTTP on the tailnet, and
  Serve or Funnel over HTTPS).
- Options for the public address, the downloads folder and the space for
  plays. Removed downloads go to a Trash folder in the downloads folder.
- Base images pinned by digest.
