#!/bin/sh
# Shows a pairing code in the Log tab.
#
# torrential lets a device in with a short code, and only the machine it runs
# on can make one: a paired device can't add another. On Home Assistant nobody
# sits at that machine's terminal, so the code is made here: at every start
# while no device is paired, and at every start when `pair_on_start` is on.
set -u

log() { echo "[pairing] $*"; }
# json EXPRESSION -> read a value from JSON on stdin; empty when missing.
json() { yq -p json -o yaml "$1" 2>/dev/null; }

# The command line starts an engine of its own when none runs, so wait for
# the one run.sh starts before asking it anything.
i=0
until [ "$(torrential engine status --json 2>/dev/null | json '.engine // ""')" = "running" ]; do
  i=$((i + 1))
  [ "$i" -ge 60 ] && { log "torrential did not start, so no pairing code was made."; exit 0; }
  sleep 2
done

devices="$(torrential devices --json 2>/dev/null | json '.devices | length')"
if [ "${PAIR_ON_START:-false}" != "true" ] && [ "${devices:-0}" != "0" ]; then
  log "$devices paired device(s). To add another, turn on 'pair_on_start' and restart the app."
  exit 0
fi

offer="$(torrential devices add --json 2>&1)" || { log "Could not make a pairing code: $offer"; exit 0; }
field() { printf '%s' "$offer" | json ".offer.$1 // \"\""; }
code="$(field code)"
[ -n "$code" ] || { log "Could not make a pairing code: $offer"; exit 0; }

log "Pair a phone, computer or TV. The code works for 10 minutes:"
log "  open  $(field url)"
log "  or    $(field address)  and enter  $code"
lan="$(field lan_address)"
if [ -n "$lan" ] && [ "$lan" != "$(field address)" ]; then
  log "  or    $lan  on the home network"
fi
if [ "${PAIR_ON_START:-false}" = "true" ]; then
  log "Turn 'pair_on_start' off again once your devices are paired."
fi
