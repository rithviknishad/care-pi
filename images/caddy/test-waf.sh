#!/bin/sh
# Test the Coraza WAF of the care-pi Caddy image (Phase 7, docs/console-server.md section 15) in Docker:
#   1. the image has the module http.handlers.waf;
#   2. "caddy validate" accepts the Caddyfile of the golden case "hardened" (the WAF in block mode, paranoia 2);
#   3. a live request with a classic SQL injection to /api/* gets 403 in block mode, and 200 (from a stub of
#      care-api) with an audit log line in detect mode; a normal request gets 200 in both modes;
#   4. the audit line has no request header: the bearer token and the console cookie of the request are not in the log.
# Usage: images/caddy/test-waf.sh [IMAGE]. Without IMAGE, the script builds images/caddy/Containerfile.
# Needs: docker. It changes nothing outside a temporary work directory of this repository (local/).
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
image=${1:-}
work="$root/local/waf-test"
golden="$root/agent/internal/render/testdata/golden/hardened/etc/care-pi/rendered/caddy"
name=care-pi-waf-test
upstream=care-pi-waf-upstream
curl=docker.io/curlimages/curl:8.17.0

cleanup() {
  docker rm -f "$name" "$upstream" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if [ -z "$image" ]; then
  image=care-pi-caddy:waf-test
  docker build -q -f "$root/images/caddy/Containerfile" -t "$image" "$root/images/caddy" >/dev/null
fi

echo "==> modules of $image"
docker run --rm "$image" caddy list-modules --skip-standard | grep -qx 'http.handlers.waf' || {
  echo "FAIL: no module http.handlers.waf" >&2
  exit 1
}
echo "ok: http.handlers.waf"

rm -rf "$work"
mkdir -p "$work/ca" "$work/run" "$work/fe" "$work/mode" "$work/plugs"
# The name-constrained root of the agent is mounted at /etc/care-pi-ca: a test root for the check.
docker run --rm -v "$work/ca:/ca" "$image" sh -c '
  apk add -q openssl >/dev/null 2>&1 || true
  openssl ecparam -name prime256v1 -genkey -noout -out /ca/root.key &&
  openssl req -x509 -new -key /ca/root.key -days 30 -subj "/CN=care-pi WAF test root" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" -out /ca/root.crt' >/dev/null 2>&1

status() { # $1: path and query, then more curl arguments; prints the HTTP status (curl in the network namespace of Caddy)
  url=$1
  shift
  docker run --rm --network "container:$name" "$curl" -sk -o /dev/null -w '%{http_code}' \
    --resolve care.local:443:127.0.0.1 "$@" "https://care.local$url" 2>/dev/null || true
}

run_caddy() { # $1: the Caddyfile directory
  docker rm -f "$name" "$upstream" >/dev/null 2>&1 || true
  docker run -d --name "$name" \
    -v "$1:/etc/caddy:ro" -v "$work/ca:/etc/care-pi-ca:ro" -v "$work/run:/run/care-pi" \
    -v "$work/fe:/srv/fe:ro" -v "$work/mode:/srv/mode:ro" -v "$work/plugs:/srv/plugs:ro" \
    "$image" caddy run --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null
  # A stub of care-api on localhost:9000 in the network namespace of Caddy.
  docker run -d --name "$upstream" --network "container:$name" "$image" \
    caddy respond --listen 127.0.0.1:9000 --body '{"ok":true}' >/dev/null
  i=0
  until [ "$(status /api/v1/ping/)" = 200 ]; do
    i=$((i + 1))
    if [ "$i" -gt 60 ]; then
      docker logs "$name" 2>&1 | tail -20 >&2
      echo "FAIL: Caddy does not answer" >&2
      exit 1
    fi
    sleep 1
  done
}


sqli="/api/v1/patient/?search=1%27%20OR%20%271%27%3D%271%27%20--%20"

echo "==> caddy validate (block mode, paranoia 2)"
cp -R "$golden" "$work/block"
docker run --rm -v "$work/block:/etc/caddy:ro" -v "$work/ca:/etc/care-pi-ca:ro" "$image" \
  caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null 2>&1 || {
  docker run --rm -v "$work/block:/etc/caddy:ro" -v "$work/ca:/etc/care-pi-ca:ro" "$image" \
    caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >&2 || true
  echo "FAIL: caddy validate" >&2
  exit 1
}
echo "ok: caddy validate"

echo "==> block mode"
run_caddy "$work/block"
got=$(status "/api/v1/ping/")
[ "$got" = 200 ] || { echo "FAIL: a normal request gave $got, not 200" >&2; exit 1; }
got=$(status "$sqli")
[ "$got" = 403 ] || { echo "FAIL: the SQL injection gave $got, not 403" >&2; exit 1; }
echo "ok: normal 200, SQL injection 403"
mem=$(docker stats --no-stream --format '{{.MemUsage}}' "$name")
echo "memory of Caddy with the CRS: $mem"

echo "==> detect mode"
mkdir -p "$work/detect"
cp -R "$golden/." "$work/detect/"
sed 's/SecRuleEngine On/SecRuleEngine DetectionOnly/' "$golden/Caddyfile" > "$work/detect/Caddyfile"
run_caddy "$work/detect"
got=$(status "$sqli" -H "Authorization: Bearer waf-test-token.secret" -H "Cookie: __Host-care_pi_session=waf-test-session")
[ "$got" = 200 ] || { echo "FAIL: detect mode gave $got, not 200" >&2; exit 1; }
sleep 1
if docker logs "$name" 2>&1 | grep -q '"942100"\|id "942100"\|942100'; then
  echo "ok: SQL injection 200 in detect mode, the audit log has rule 942100"
else
  docker logs "$name" 2>&1 | tail -5 >&2
  echo "FAIL: no audit line of the CRS rule 942100" >&2
  exit 1
fi
if docker logs "$name" 2>&1 | grep -q 'waf-test-token\|waf-test-session'; then
  echo "FAIL: the audit log has the bearer token or the console cookie of the request" >&2
  exit 1
fi
echo "ok: the audit log has no request header (no bearer token, no console cookie)"
echo "PASS"
