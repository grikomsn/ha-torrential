#!/bin/sh
# Joins the tailnet as its own node and publishes torrential with Tailscale
# Serve:
#   http://<TS_HOSTNAME>.<tailnet>.ts.net:47919   always, tailnet only
#   https://<TS_HOSTNAME>.<tailnet>.ts.net        when the `domain` option is
#                                                 that name; with Funnel, also
#                                                 from the public internet
# Exits 0 once serving; run.sh retries it on failure.
set -u

SOCKET=/tmp/ha/tailscaled.sock
AUTHKEY_FILE=/tmp/ha/tailscale.authkey
PORT=47919
TARGET="http://127.0.0.1:$PORT"
ts() { /usr/local/bin/tailscale --socket="$SOCKET" "$@"; }
log() { echo "[tailscale] $*"; }

i=0
until [ -S "$SOCKET" ] && ts version >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -ge 60 ] && { log "tailscaled did not start; turn on 'debug' to see its log"; exit 1; }
  sleep 1
done

set -- up --reset --hostname="$TS_HOSTNAME" --accept-dns=false --accept-routes=false
if [ -s "$AUTHKEY_FILE" ]; then
  set -- "$@" --auth-key="file:$AUTHKEY_FILE"
fi

state="$(ts status --json 2>/dev/null | yq -p json -o yaml '.BackendState // ""')"
if [ "$state" != "Running" ] && [ ! -s "$AUTHKEY_FILE" ]; then
  log "Not logged in and no tailscale_auth_key set: open the login URL below to add this node."
fi
# Blocks until logged in; prints a login URL when interactive login is needed.
ts "$@" || { log "tailscale up failed"; exit 1; }

# Plain HTTP on the tailnet, on torrential's own port. This is more than a
# convenience: in userspace mode tailscaled hands a tailnet connection to a
# port Serve doesn't own straight to 127.0.0.1, and torrential takes what
# comes from 127.0.0.1 for the machine itself, which needs no pairing. Serve
# adds X-Forwarded-For, so torrential sees a device that has to pair.
# It is set again at every start rather than reset first, so the port is never
# left without it.
ts serve --bg --yes --http="$PORT" "$TARGET" >/dev/null || { log "tailscale serve failed"; exit 1; }

dns_name="$(ts status --json | yq -p json -o yaml '.Self.DNSName // ""' | sed 's/\.$//')"
[ -n "$dns_name" ] || { log "No MagicDNS name: enable MagicDNS for the tailnet"; exit 1; }

# torrential answers HTTPS only at its `public_address`, so port 443 is served
# only when that is this node's name.
if [ "${PUBLIC_HOST:-}" = "$dns_name" ]; then
  if [ "$TS_FUNNEL" = "true" ]; then
    ts funnel --bg --yes --https=443 "$TARGET" >/dev/null || { log "tailscale funnel failed (is Funnel allowed for this node in the tailnet policy?)"; exit 1; }
    log "Reachable at https://$dns_name, from the public internet too (Funnel)"
  else
    ts serve --bg --yes --https=443 "$TARGET" >/dev/null || { log "tailscale serve failed (are HTTPS certificates enabled for the tailnet?)"; exit 1; }
    log "Reachable on your tailnet at https://$dns_name"
  fi
else
  ts serve --yes --https=443 off >/dev/null 2>&1 || true
  log "Reachable on your tailnet at http://$dns_name:$PORT"
  if [ "$TS_FUNNEL" = "true" ]; then
    log "Funnel is NOT on: it needs HTTPS, so set the 'domain' option to $dns_name and restart the app."
  elif [ -z "${PUBLIC_HOST:-}" ]; then
    log "For HTTPS on this name, set the 'domain' option to $dns_name and restart the app."
  fi
fi
