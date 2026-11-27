# Images are pinned by tag and multi-arch index digest; Renovate updates both
# (and `version` in config.yaml with the torrential tag). Any change here
# needs a version bump and a CHANGELOG.md entry: see RELEASING.md.

FROM docker.io/tailscale/tailscale:v1.104.7@sha256:72c1e19e0aedca96b8533bbb0b4368564410ff20b17e53b804eb6c1f04a31be4 AS tailscale
FROM docker.io/cloudflare/cloudflared:2026.11.2@sha256:55c7e3e8d89d5e8d20b75a82f3602cbf98e06db406e73e282073cfdc5a9a3af6 AS cloudflared
FROM docker.io/mikefarah/yq:4.54.3@sha256:7834187596ad56fd5a81436114e0b0b9dd54c91bb43946d1f7145b2be5f9c3d2 AS yq

# The mDNS responder behind the `mdns_hostname` option: this repository's own
# code, compiled on the build machine for the target architecture.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS mdns-alias
ARG TARGETARCH
WORKDIR /src
COPY mdns-alias/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /mdns-alias .

# Official torrential image (multi-arch: amd64, arm64), on Debian slim.
FROM ghcr.io/nexxeln/torrential:0.7.0@sha256:7a04f43ecf7feeea05d81b5b05daa121a684305758dab442d0c2b79d1a5859f6

# Upstream runs as user 1000. Home Assistant apps run as root, which is also
# what lets torrential write to network storage mounted under /media or /share.
USER root

COPY --from=tailscale /usr/local/bin/tailscale /usr/local/bin/tailscaled /usr/local/bin/
COPY --from=cloudflared /usr/local/bin/cloudflared /usr/local/bin/
COPY --from=yq /usr/bin/yq /usr/local/bin/yq
COPY --from=mdns-alias /mdns-alias /usr/local/bin/mdns-alias
COPY rootfs/ /
RUN chmod 0755 /ha/*.sh

# Wraps the upstream `torrential engine run --server`: writes the app options
# into torrential's config.yaml, starts the optional tunnels, then hands over.
ENTRYPOINT ["/ha/run.sh"]
