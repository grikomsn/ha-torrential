# torrential

This app runs [torrential](https://github.com/nexxeln/torrential) as a server
on Home Assistant. You search for a film or a show in its web app, press Play,
and watch it on a phone, a computer or a TV while it downloads. torrential
never seeds: it uploads nothing, and a download stops when it finishes.
Download only what you have the right to.

On the home network it works with no setup at `http://homeassistant.local:47919`.
To watch away from home without opening router ports, the app can also run
either or both of:

- **Cloudflare Tunnel** on a domain or subdomain of your choice
  (`tv.example.com`).
- **Tailscale** as its own tailnet node (`torrential.tail1234.ts.net`), for
  tailnet devices only, or with Funnel for the public internet.

> This app is not made or supported by the torrential project. Report problems
> with the app here.

## Install

1. In Home Assistant go to **Settings → Apps → Install app**, open the
   three-dot menu, choose **Repositories** and add
   `https://github.com/grikomsn/ha-torrential`.
2. Install **torrential**. Home Assistant downloads the prebuilt image
   (`ghcr.io/grikomsn/ha-torrential`, signed with Cosign).
3. Start the app and open the **Log** tab.

## First run: pair a device

torrential has no accounts. A device gets in with a six-digit code, and holds
a cookie from then on. A device that isn't paired sees the pairing page and
nothing else.

Only the server itself can make a code, so this app prints one in the **Log**
tab at every start while no device is paired:

```text
[pairing] Pair a phone, computer or TV. The code works for 10 minutes:
[pairing]   open  http://homeassistant.local:47919/pair#482913
[pairing]   or    http://homeassistant.local:47919  and enter  482913
[pairing]   or    http://192.168.1.23:47919  on the home network
```

Open the link on the device, or open the address and type the code. A code
pairs one device.

To pair another device, turn on `pair_on_start`, restart the app and use the
new code from the log. Turn the option off afterwards. A paired device can't
add or remove devices. To list or remove them, see
[Running torrential commands](#running-torrential-commands).

## Options

| Option | Description |
| --- | --- |
| `domain` | The one public address, for example `tv.example.com`. `https://` is added for you. Pairing links use it. Needed for Cloudflare Tunnel and for HTTPS on Tailscale. Leave empty on the home network. |
| `downloads_dir` | Where downloads you keep are saved: a folder inside `/media` or `/share`. Default `/media/torrential`. |
| `plays_max` | The most space that what you watch may take up, such as `20GiB`, or `none`. Default `20GiB`. |
| `pair_on_start` | Print a pairing code at every start, not only while no device is paired. |
| `cloudflare_tunnel_token` | Turns on the built-in Cloudflare Tunnel. |
| `tailscale`, `tailscale_auth_key`, `tailscale_hostname`, `tailscale_funnel` | Turn on the built-in Tailscale node. |
| `debug` | Also show the Tailscale daemon's log. |
| `env_vars` | Extra environment variables for torrential, cloudflared and Tailscale. |

torrential answers HTTPS at **one** address only, the one in `domain`. A
browser that reaches it through any other HTTPS name gets the pairing page but
can't pair. Plain HTTP works at any name, which is how the home network and
the tailnet address below work next to a Cloudflare `domain`.

The app writes `domain`, `downloads_dir` and `plays_max` into torrential's
own `config.yaml` (`public_address`, `downloads.dir` and `plays.max`) at every
start. Everything else in that file is left alone.

## Option A: Cloudflare Tunnel on your own domain

Needs a domain whose DNS is on Cloudflare.

1. In the Cloudflare dashboard open **Zero Trust → Networks → Connectors
   (Tunnels) → Create a tunnel**, choose **Cloudflared**, and name it.
2. Copy the token from the install command (the long string after
   `--token`). Skip the install step; this app runs cloudflared itself.
3. Add a **published application** (public hostname) route:
   - Subdomain and domain: whatever you want, e.g. `tv` + `example.com`.
   - Service: `HTTP` and `localhost:47919`.
   - Leave **HTTP Host Header** empty. torrential tells a visitor from the
     tunnel apart from the server itself by the hostname it is asked for.
   Cloudflare creates the DNS record for you.
4. In the app, set `cloudflare_tunnel_token` to the token and `domain` to the
   hostname from step 3, then (re)start the app. The log should show
   `Registered tunnel connection`, and the pairing link now starts with
   `https://tv.example.com`.

Things to know:

- The hostname is public. Anyone can open its pairing page, and nobody gets
  further without a code from your log. A code stops working after five wrong
  tries or ten minutes.
- Each tunnel token should be used by one running app only.
- Cloudflare's [service-specific terms](https://www.cloudflare.com/service-specific-terms-application-services/)
  limit serving video through its network on the Free, Pro and Business
  plans. Read them before you watch films through a tunnel. Tailscale has no
  such limit.

## Option B: Tailscale

The app joins your tailnet as a separate machine (default name `torrential`).
This is independent of the Tailscale app you may already run for Home
Assistant.

1. In the Tailscale admin console under **DNS**, enable **MagicDNS**.
2. In the app, turn on `tailscale`. Either paste an auth key into
   `tailscale_auth_key`, or leave it empty and open the login URL that appears
   in the **Log** tab. Start the app.
3. The log prints the address, for example
   `Reachable on your tailnet at http://torrential.tail1234.ts.net:47919`.
   Open it on a tailnet device and pair. Use the name, not the node's
   `100.x` address, which Tailscale Serve doesn't answer for.
4. In the Tailscale admin console, disable key expiry for the new machine so
   it doesn't drop off the tailnet.

That address is plain HTTP inside Tailscale's encrypted connection, and it
works whatever `domain` is set to.

### HTTPS and Funnel

For `https://torrential.tail1234.ts.net`, or to reach it from the public
internet with Funnel:

1. In the Tailscale admin console under **DNS**, enable **HTTPS
   Certificates**.
2. For Funnel, allow it in the tailnet policy file, for example:

   ```json
   "nodeAttrs": [{ "target": ["autogroup:member"], "attr": ["funnel"] }]
   ```

3. Set `domain` to the full name the log printed
   (`torrential.tail1234.ts.net`), turn on `tailscale_funnel` if you want
   Funnel, and restart the app.

Because torrential answers HTTPS at one address, this only works while
`domain` is the node's name. With `domain` set to a Cloudflare hostname, the
tailnet keeps the `http://…:47919` address and Funnel stays off; the log says
so.

If a machine with the same name already exists in your tailnet, the new one
gets a suffix like `torrential-1`. Remove the old machine first to keep the
name. The first Funnel request can take a few minutes while Tailscale
provisions the certificate and public DNS.

### What else the node exposes

This app runs on the host's network (see below), so its Tailscale node is, in
effect, the Home Assistant host. Other tailnet devices that your tailnet
policy lets reach the node can also connect to other ports that are open on
the host, such as Home Assistant itself on 8123. With the default policy that
is every device in your tailnet. To limit the node to torrential, give it a
tag and grant only its two ports:

```json
"tagOwners": { "tag:torrential": ["autogroup:admin"] },
"grants": [
  { "src": ["autogroup:member"], "dst": ["tag:torrential"], "ip": ["tcp:443", "tcp:47919"] }
]
```

Funnel only ever exposes port 443.

## The host's network

torrential needs the host's network: it finds TVs for **Play on TV** by
multicast on the home network, the TV then fetches the film from it, and
pairing links name the host's own addresses. So:

- It listens on port **47919** on the Home Assistant host, which can't be
  changed, and on a port of its choosing for BitTorrent.
- Every device on the home network can open the pairing page. Without a code
  that is all it gets.
- torrential takes a request from `127.0.0.1` for the server itself, which
  needs no pairing, unless the request carries `X-Forwarded-For` or names the
  `domain`. The built-in tunnels both qualify. If you put **another** proxy
  on the Home Assistant host in front of it, make sure it sends
  `X-Forwarded-For`, or everyone it lets through skips pairing. The same goes
  for anything that passes outside connections to `127.0.0.1` as they are,
  which the Tailscale app for Home Assistant does in userspace networking
  mode.

## Downloads and disk space

- **Downloads**, what you choose to keep, are saved in `downloads_dir`. The
  default, `/media/torrential`, shows up in Home Assistant's media browser
  and in the Samba share. The folder can be on network storage you added to
  Home Assistant.
- **Removed downloads** are moved to `<downloads_dir>/Trash`, not deleted.
  Empty that folder yourself to get the space back.
- **Plays**, what you watch, go to the app's private data folder.
  When they take up more than `plays_max`, torrential deletes the ones
  watched longest ago, but never the film you watched last or a title you
  saved.
- torrential always leaves some of the disk free (2%, at least 2 GiB).
- The first time a file needs converting for the browser, torrential fetches
  ffmpeg into its cache.

## Running torrential commands

Some things have no option here and no button for a paired device: listing
and removing devices, and adding indexers. They take the `torrential` command
inside the app's container. You need a shell on the host with Docker access,
for example the *Advanced SSH & Web Terminal* app with protection mode off:

```sh
docker exec -it "$(docker ps -qf name=torrential)" torrential devices
docker exec -it "$(docker ps -qf name=torrential)" torrential devices remove 3f9a0c2e
docker exec -it "$(docker ps -qf name=torrential)" torrential devices add
```

`torrential setup` sets the screen and language that titles are ranked for,
and `torrential --help` lists the rest.

### Private and local trackers

To search trackers torrential lacks, run Prowlarr or Jackett and give
torrential its address and API key:

```sh
docker exec -it "$(docker ps -qf name=torrential)" \
  torrential indexers add http://homeassistant.local:9696 --key <api key>
```

The key is kept in torrential's `config.yaml`, in the app's data folder.

## Backups and updates

- Home Assistant backups include this app's data folder: torrential's
  `config.yaml` (with indexer keys), its database of paired devices and
  downloads, and the Tailscale state. The app is stopped briefly while it is
  backed up (`backup: cold`). Plays and the cache are left out.
- Downloads are in Home Assistant's media (or share) folder, so they are in a
  backup only when you include that folder.
- The app version follows the torrential release it is built on, with a
  fourth number for packaging-only changes (`0.7.0.1`). Updates download a
  new prebuilt image; torrential's own updater is off. Release notes:
  <https://github.com/grikomsn/ha-torrential/releases>.

## Troubleshooting

- **App stops right after start:** the log says which option is wrong, for
  example `plays.max "lots" is not a size`.
- **No pairing code in the log:** codes are printed while no device is
  paired, or with `pair_on_start`. Restart the app for a fresh one.
- **Pairing fails with "torrential answers only at its own address":** you
  opened it at an HTTPS name that isn't `domain`. Use the `domain` address,
  or a plain HTTP one.
- **Port 47919 is taken:** the port can't be changed, so stop whatever else
  uses it on the Home Assistant host.
- **Logs:** the Log tab shows torrential, cloudflared and the Tailscale
  login/serve steps. Turn on `debug` for the Tailscale daemon too.
- **`tailscale serve failed`:** enable MagicDNS, and HTTPS certificates when
  `domain` is the node's name. **`tailscale funnel failed`:** add the `funnel`
  node attribute.
- **Play on TV finds no TV:** the TV and Home Assistant must be on the same
  network segment; DLNA discovery doesn't cross VLANs or reach over Tailscale
  or Cloudflare.
