#!/bin/sh
# Home Assistant wrapper for the official torrential image.
#
# Reads the app options written by the Supervisor, writes the ones torrential
# takes from its config.yaml, starts the optional cloudflared / Tailscale
# helpers in the background, then execs the upstream engine unchanged.
set -eu

OPTIONS=/data/options.json
CONFIG=/data/config/config.yaml
RUN_DIR=/tmp/ha
PORT=47919

log() { echo "[ha-torrential] $*"; }
die() { echo "[ha-torrential] ERROR: $*" >&2; exit 1; }

# opt NAME -> value as text; empty when unset, null or false.
opt() { k="$1" yq -p json -o yaml '.[strenv(k)] // ""' "$OPTIONS"; }

# set_config PATH VALUE / del_config PATH -> edit one key of config.yaml.
set_config() { v="$2" yq -i "$1 = strenv(v)" "$CONFIG" || die "Could not update $CONFIG. Fix or delete it, then start the app again."; }
del_config() { yq -i "del($1)" "$CONFIG" || die "Could not update $CONFIG. Fix or delete it, then start the app again."; }

# forever NAME COMMAND... -> run COMMAND in the background, restarting it
# whenever it exits. The delay doubles (10s up to 5 min) while it keeps failing
# quickly, e.g. with a bad token or no network yet. torrential itself runs in
# the foreground, so the app stops (and the watchdog restarts it) only when
# torrential does.
forever() {
  name="$1"
  shift
  (
    delay=10
    while :; do
      started="$(date +%s)"
      "$@" || true
      [ "$(($(date +%s) - started))" -ge 60 ] && delay=10
      log "$name exited; restarting in ${delay}s"
      sleep "$delay"
      delay=$((delay * 2))
      [ "$delay" -gt 300 ] && delay=300
    done
  ) &
}

[ -f "$OPTIONS" ] || die "$OPTIONS not found. Is this running as a Home Assistant app?"

# --- Public address (optional) -------------------------------------------------
# torrential answers one HTTPS address: the one in `public_address`. Pairing
# links use it, and a browser on any other HTTPS name is refused.
domain="$(opt domain)"
url=""
host=""
if [ -n "$domain" ]; then
  case "$domain" in
    http://* | https://*) url="$domain" ;;
    *) url="https://$domain" ;;
  esac
  url="$(printf '%s' "${url%/}" | tr '[:upper:]' '[:lower:]')"
  host="${url#*://}"
  case "$host" in
    "" | *[!a-z0-9.:-]*) die "'domain' must be a hostname such as tv.example.com or torrential.tail1234.ts.net, without a path." ;;
  esac
fi

tunnel_token="$(opt cloudflare_tunnel_token)"
if [ -n "$tunnel_token" ] && [ -z "$url" ]; then
  die "Set 'domain' to the public hostname of your Cloudflare Tunnel, e.g. tv.example.com. Without it torrential refuses to pair devices there."
fi

# --- Folders -------------------------------------------------------------------
downloads="$(opt downloads_dir)"
downloads="${downloads:-/media/torrential}"
downloads="${downloads%/}"
case "$downloads" in
  */.. | */../* | */. | */./*) die "'downloads_dir' must not contain . or .. parts." ;;
  /media/?* | /share/?*) ;;
  *) die "'downloads_dir' must be a folder inside /media or /share, e.g. /media/torrential." ;;
esac
mkdir -p "$downloads" "$(dirname "$CONFIG")"

# --- config.yaml ---------------------------------------------------------------
# torrential writes this file too (indexers, preferences), so only the keys
# the app options own are touched and the rest is left as it is.
if [ ! -s "$CONFIG" ]; then
  (umask 077 && v="$downloads" yq -n '.downloads.dir = strenv(v)' > "$CONFIG")
fi
set_config .downloads.dir "$downloads"
if [ -n "$url" ]; then set_config .public_address "$url"; else del_config .public_address; fi
plays_max="$(opt plays_max)"
if [ -n "$plays_max" ]; then set_config .plays.max "$plays_max"; else del_config .plays.max; fi
chmod 0600 "$CONFIG"

# A download you remove goes to the freedesktop Trash under XDG_DATA_HOME, in
# one rename. /data and the downloads folder are separate mounts, so the Trash
# has to be on the downloads side: <downloads_dir>/Trash.
export XDG_DATA_HOME="$downloads"

# --- Extra environment variables -----------------------------------------------
count="$(yq -p json -o yaml '(.env_vars // []) | length' "$OPTIONS")"
i=0
while [ "$i" -lt "$count" ]; do
  name="$(yq -p json -o yaml ".env_vars[$i].name" "$OPTIONS")"
  value="$(yq -p json -o yaml ".env_vars[$i].value" "$OPTIONS")"
  case "$name" in
    "" | [0-9]* | *[!A-Za-z0-9_]*) die "env_vars: invalid variable name '$name'" ;;
    HOME | TORRENTIAL_HOME | TORRENTIAL_IMAGE | XDG_DATA_HOME) die "env_vars: $name is set by the app and can't be changed." ;;
  esac
  export "$name=$value"
  log "env_vars: set $name"
  i=$((i + 1))
done

# --- Optional helper programs ----------------------------------------------------
mkdir -p "$RUN_DIR"
chmod 0700 "$RUN_DIR"
rm -f "$RUN_DIR"/cloudflared.token "$RUN_DIR"/tailscale.authkey

if [ -n "$tunnel_token" ]; then
  (umask 077 && printf '%s' "$tunnel_token" > "$RUN_DIR/cloudflared.token")
  # In a container cloudflared serves its metrics on every address, which on
  # the host's network would be the whole home network. Keep them on loopback.
  forever cloudflared /usr/local/bin/cloudflared tunnel --no-autoupdate --metrics 127.0.0.1:0 run --token-file "$RUN_DIR/cloudflared.token"
  log "Cloudflare Tunnel: enabled (point the tunnel's public hostname at http://localhost:$PORT)"
else
  log "Cloudflare Tunnel: disabled"
fi

if [ "$(opt tailscale)" = "true" ]; then
  mkdir -p /data/tailscale
  ts_auth_key="$(opt tailscale_auth_key)"
  if [ -n "$ts_auth_key" ]; then
    (umask 077 && printf '%s' "$ts_auth_key" > "$RUN_DIR/tailscale.authkey")
  fi
  ts_hostname="$(opt tailscale_hostname)"
  ts_funnel="$(opt tailscale_funnel)"
  # tailscaled is chatty; its own log only goes to the Log tab when debugging.
  if [ "$(opt debug)" = "true" ]; then ts_log=/proc/self/fd/1; else ts_log=/dev/null; fi
  # Userspace networking: no TUN device or routes, so it doesn't get in the
  # way of the host's own network or of the Tailscale app, if that runs too.
  # shellcheck disable=SC2016 # $1 is expanded by the inner sh
  forever tailscaled sh -c 'exec /usr/local/bin/tailscaled --tun=userspace-networking --statedir=/data/tailscale --socket=/tmp/ha/tailscaled.sock >"$1" 2>&1' tailscaled "$ts_log"
  (
    until TS_HOSTNAME="${ts_hostname:-torrential}" TS_FUNNEL="${ts_funnel:-false}" PUBLIC_HOST="$host" /ha/tailscale-up.sh; do
      log "Tailscale setup failed; retrying in 30s"
      sleep 30
    done
  ) &
  log "Tailscale: enabled as '${ts_hostname:-torrential}' (funnel: ${ts_funnel:-false})"
else
  log "Tailscale: disabled"
fi

# Shows a pairing code in the Log tab once the engine is up.
PAIR_ON_START="$(opt pair_on_start)" /ha/pair.sh &

log "Downloads: $downloads (removed downloads go to $downloads/Trash)"
if [ -n "$url" ]; then
  log "Starting torrential for $url, and on the home network on port $PORT"
else
  log "Starting torrential on the home network on port $PORT"
fi
exec torrential engine run --server
