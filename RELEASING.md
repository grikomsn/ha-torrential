# Releasing

Home Assistant decides everything from `version` in
[`torrential/config.yaml`](torrential/config.yaml): the version users see,
the image tag the Supervisor pulls, and whether an update is offered.

## Version scheme

```text
<torrential tag>[.<revision>]
0.7.0      torrential 0.7.0
0.7.0.1    same torrential, first packaging change (scripts, mdns-alias, Tailscale, cloudflared, yq, options)
0.7.1      next torrential release (revision resets)
```

Don't use a suffix like `0.7.0-1`: the Supervisor compares versions with
[AwesomeVersion](https://github.com/ludeeus/awesomeversion), which reads `-1`
as a pre-release, so `0.7.0-1` sorts *below* `0.7.0` and users never see the
update. The four-part form sorts correctly (checked with awesomeversion
25.8.0, the version the Supervisor pins).

## What CI enforces

`Version and changelog` in [CI](.github/workflows/ci.yaml) runs
[`check_version.py`](.github/scripts/check_version.py) on every pull request:

- `version` is the torrential tag from the Dockerfile, optionally plus
  `.<revision>`.
- `torrential/CHANGELOG.md` has a `## <version>` section.
- If the Dockerfile instructions, `rootfs/`, or the options in `config.yaml`
  changed, `version` must be higher than on `main`. The error message names
  the version to use. Edits to comments, docs, translations and the `image`
  key don't need a bump.

The amd64 build also runs [`smoke_test.sh`](.github/scripts/smoke_test.sh),
which starts the image and checks that torrential answers, that the options
reach its `config.yaml`, that a request arriving through a proxy on
`127.0.0.1` has to pair, and that a device can pair through the `domain`.

To run it yourself: `docker build -t ha-torrential:dev torrential` and then
`.github/scripts/smoke_test.sh ha-torrential:dev`.

## Dependency updates (Renovate)

[Renovate](renovate.json) opens PRs on Monday mornings (UTC):

| PR | What it changes | What you do |
| --- | --- | --- |
| **GitHub Actions** | Action versions and commit SHAs | Nothing. It merges itself when CI passes. |
| **torrential** | Dockerfile tag and digest, and `version` in `config.yaml` | Read the [release notes](https://github.com/nexxeln/torrential/releases), add a `## <new version>` CHANGELOG section, then merge. |
| **Tailscale, cloudflared and yq** | Dockerfile tag and digest, including the Go image that builds `mdns-alias` | Bump `version` to the next revision (CI names it), add a CHANGELOG section, then merge. |

The packaging leans on three things in torrential that a release could
change, so look for them in its release notes:

- **Who counts as the server.** A request from `127.0.0.1` needs no pairing
  unless it carries `X-Forwarded-For` or names the `public_address`. Both
  tunnels depend on that. The smoke test fails if it changes.
- **`config.yaml` keys.** [`run.sh`](torrential/rootfs/ha/run.sh) writes
  `public_address`, `downloads.dir` and `plays.max`.
- **The command line's JSON.** [`pair.sh`](torrential/rootfs/ha/pair.sh)
  reads `torrential engine status --json` and `torrential devices --json`.

## Publishing

Merging to `main` runs the [Release workflow](.github/workflows/release.yaml)
when `version` has no `v<version>` tag yet. It:

1. Builds `amd64` and `aarch64` images natively with Home Assistant's
   [builder actions](https://github.com/home-assistant/builder) and pushes
   `ghcr.io/grikomsn/{amd64,aarch64}-ha-torrential:<version>`.
2. Publishes the multi-arch image `ghcr.io/grikomsn/ha-torrential:<version>`
   (and `latest`). Every image is signed with Cosign (keyless, GitHub OIDC).
3. Creates the `v<version>` tag and a GitHub release with the CHANGELOG section.

Tags are never overwritten: if a version is already published, the build is
skipped.

The Supervisor downloads images anonymously, so the
`ghcr.io/grikomsn/ha-torrential` package must be public (GitHub → package
settings → Change visibility), and so must this repository for Home Assistant
to add it.

Because `image:` is set in `config.yaml`, there is a window of a few minutes
between the merge and the end of the build where a Supervisor may already
offer the update but can't download it yet. Merge only when CI is green, so
that the release build doesn't fail.

To check a published image's signature (needs Cosign 3 or later; the builder
signs with Cosign 3, and Cosign 2 reports "no signatures found"):

```sh
cosign verify ghcr.io/grikomsn/ha-torrential:<version> \
  --certificate-identity-regexp '^https://github\.com/grikomsn/ha-torrential/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```
