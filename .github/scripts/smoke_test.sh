#!/bin/sh
# Starts the image the way the Supervisor does, with the options in
# /data/options.json, and checks what the packaging relies on: above all that
# torrential makes a request that came through a proxy on 127.0.0.1 (as
# cloudflared and Tailscale Serve send them) pair before it gets in.
#
# Usage: smoke_test.sh IMAGE
set -eu

image="$1"
name=ha-torrential-smoke
host=tv.example.test
proxied="X-Forwarded-For: 203.0.113.9"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm -f "$name" >/dev/null 2>&1 || true
}
fail() {
  echo "FAIL: $*" >&2
  docker logs "$name" 2>&1 | tail -n 60 >&2 || true
  cleanup
  exit 1
}

# send REQUEST -> the answer to a raw HTTP request (\r\n written out) sent over
# loopback from inside the container, which has bash but no curl.
send() {
  # shellcheck disable=SC2016 # expanded by the inner bash
  docker exec -e REQUEST="$1" "$name" bash -c 'exec 3<>/dev/tcp/127.0.0.1/47919 && printf "$REQUEST" >&3 && cat <&3' 2>/dev/null || true
}
# status PATH [HEADER...] -> the status code of a GET with exactly these headers.
status() {
  path="$1"
  shift
  headers=""
  for header in "$@"; do headers="$headers$header\r\n"; done
  send "GET $path HTTP/1.1\r\n${headers}Connection: close\r\n\r\n" | head -n 1 | cut -d ' ' -f 2
}
# expect CODE DESCRIPTION PATH [HEADER...]
expect() {
  want="$1"
  what="$2"
  shift 2
  got="$(status "$@")"
  [ "$got" = "$want" ] || fail "$what: got ${got:-no answer}, expected $want"
  echo "ok: $what ($got)"
}

cleanup
docker run --rm -i -v "$name:/data" --entrypoint sh "$image" -c 'cat > /data/options.json' <<EOF
{"domain":"$host","downloads_dir":"/media/torrential","plays_max":"5GiB","pair_on_start":false,"cloudflare_tunnel_token":"","tailscale":false,"tailscale_hostname":"torrential","tailscale_funnel":false,"debug":false,"env_vars":[]}
EOF
docker run -d --init --name "$name" -v "$name:/data" "$image" >/dev/null

i=0
until [ "$(status /.well-known/torrential "Host: 127.0.0.1:47919")" = "200" ]; do
  i=$((i + 1))
  [ "$i" -ge 45 ] && fail "torrential did not answer the watchdog URL"
  sleep 2
done
echo "ok: watchdog URL answers after ~$((i * 2))s"

docker exec "$name" grep -qx "public_address: https://$host" /data/config/config.yaml || fail "config.yaml has no public_address"
docker exec "$name" grep -q "dir: /media/torrential" /data/config/config.yaml || fail "config.yaml has no downloads.dir"
echo "ok: options are in config.yaml"

expect 200 "the machine itself needs no pairing" /api/devices "Host: 127.0.0.1:47919"
expect 401 "a proxied request has to pair" /api/devices "Host: $host" "$proxied"
expect 401 "a proxied request can't pass for the machine by its Host" /api/devices "Host: 127.0.0.1:47919" "$proxied"
expect 303 "a proxied visitor is sent to the pairing page" / "Host: $host" "$proxied"

i=0
until code="$(docker logs "$name" 2>&1 | sed -n "s#.*https://$host/pair\#\([0-9]\{6\}\)\$#\1#p" | tail -n 1)" && [ -n "$code" ]; do
  i=$((i + 1))
  [ "$i" -ge 30 ] && fail "no pairing code for https://$host in the log"
  sleep 2
done
echo "ok: the log shows a pairing link for https://$host"

body="{\"code\":\"$code\"}"
answer="$(send "POST /api/pair HTTP/1.1\r\nHost: $host\r\n$proxied\r\nOrigin: https://$host\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\nConnection: close\r\n\r\n$body")"
printf '%s' "$answer" | head -n 1 | grep -q " 200 " || fail "pairing through https://$host was refused: $(printf '%s' "$answer" | head -n 1)"
printf '%s' "$answer" | grep -qi '^set-cookie: torrential_device=.*; Secure' || fail "pairing through https://$host gave no Secure cookie"
echo "ok: a device pairs through https://$host and gets a Secure cookie"

docker restart "$name" >/dev/null
i=0
until docker logs "$name" 2>&1 | grep -q '^\[pairing\] 1 paired device'; do
  i=$((i + 1))
  [ "$i" -ge 45 ] && fail "after a restart the paired device is gone, or a new code was made"
  sleep 2
done
echo "ok: the device stays paired across a restart, and no new code is made"

cleanup
echo "All checks passed."
