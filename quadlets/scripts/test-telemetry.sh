#!/bin/sh
# Live test of the opt-in telemetry (Phase 7, docs/telemetry.md) in Docker, with the files of the golden case
# "hardened": node_exporter and vmagent with the Exec lines of their quadlets and the rendered scrape.yml, and a
# VictoriaMetrics single node with basic auth as the remote-write receiver. The script checks that the receiver
# gets the metrics of the host (node_load1) and of the agent (the text file), with instance=<host name> and the label
# care_pi_version.
# Usage: quadlets/scripts/test-telemetry.sh. Needs: docker. Work directory: local/telemetry-test (ignored by git).
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
golden="$root/agent/internal/render/testdata/golden/hardened"
work="$root/local/telemetry-test"
net=care-pi-telemetry-test
vm=care-pi-telemetry-vm
exporter=care-pi-telemetry-node-exporter
agent=care-pi-telemetry-vmagent
vmimage=docker.io/victoriametrics/victoria-metrics:v1.131.0
curl=docker.io/curlimages/curl:8.17.0

cleanup() {
  docker rm -f "$vm" "$exporter" "$agent" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM
cleanup

# The goldens have test digests: use the tags of agent/internal/render (ImageVMAgent, ImageNodeExporter).
image_of() {
  case $1 in
    care-telemetry) sed -n 's/^[[:space:]]*ImageVMAgent *= *"\(.*\)"$/\1/p' "$root/agent/internal/render/model.go" ;;
    care-node-exporter) sed -n 's/^[[:space:]]*ImageNodeExporter *= *"\(.*\)"$/\1/p' "$root/agent/internal/render/model.go" ;;
  esac
}
exec_of() { sed -n 's/^Exec=//p' "$golden/etc/containers/systemd/$1.container"; }

rm -rf "$work"
mkdir -p "$work/textfile" "$work/conf" "$work/queue" "$work/secrets"
# The scrape interval of the test is 5 s (the golden has 60 s).
sed 's/scrape_interval: .*/scrape_interval: 5s/' "$golden/etc/care-pi/rendered/telemetry/scrape.yml" > "$work/conf/scrape.yml"
printf 'secret-of-the-test' > "$work/secrets/care-pi-telemetry-password"
cat > "$work/textfile/care_pi.prom" <<'PROM'
# HELP care_pi_unit_active The unit is active.
# TYPE care_pi_unit_active gauge
care_pi_unit_active{unit="care-api.service"} 1
PROM

docker network create "$net" >/dev/null
docker run -d --name "$vm" --network "$net" "$vmimage" \
  -httpAuth.username=sunrise -httpAuth.password=secret-of-the-test -retentionPeriod=1d >/dev/null

# The quadlets use the host network; here node_exporter has its own network namespace and vmagent shares it, so
# 127.0.0.1:19100 is the same as on a Pi.
# shellcheck disable=SC2046
docker run -d --name "$exporter" --network "$net" -v /:/host:ro -v "$work/textfile:/textfile:ro" \
  "$(image_of care-node-exporter)" $(exec_of care-node-exporter) >/dev/null
url="http://$vm:8428/api/v1/write"
# shellcheck disable=SC2046
docker run -d --name "$agent" --network "container:$exporter" -v "$work/conf:/etc/vmagent:ro" \
  -v "$work/queue:/var/lib/vmagent" -v "$work/secrets:/run/secrets:ro" \
  "$(image_of care-telemetry)" $(exec_of care-telemetry | sed "s#-remoteWrite.url=[^ ]*#-remoteWrite.url=$url#") >/dev/null

query() {
  docker run --rm --network "$net" "$curl" -s -u sunrise:secret-of-the-test \
    "http://$vm:8428/api/v1/query" --data-urlencode "query=$1"
}

i=0
until query 'care_pi_unit_active' | grep -q '"instance":"care"'; do
  i=$((i + 1))
  if [ "$i" -gt 40 ]; then
    docker logs "$agent" 2>&1 | tail -10 >&2
    echo "FAIL: no care_pi_unit_active at the receiver" >&2
    exit 1
  fi
  sleep 3
done
echo "ok: the receiver has care_pi_unit_active: $(query 'care_pi_unit_active' | sed 's/.*"metric":\({[^}]*}\).*/\1/')"
query 'node_load1' | grep -q '"instance":"care"' || { echo "FAIL: no node_load1" >&2; exit 1; }
query 'node_load1' | grep -q '"care_pi_version":' || { echo "FAIL: no label care_pi_version" >&2; exit 1; }
echo "ok: node_load1 with instance=care and care_pi_version"
for c in "$exporter" "$agent"; do
  echo "memory of $c: $(docker stats --no-stream --format '{{.MemUsage}}' "$c")"
done
# A wrong password: the receiver refuses the samples (401).
docker rm -f "$agent" >/dev/null
printf 'wrong' > "$work/secrets/care-pi-telemetry-password"
# shellcheck disable=SC2046
docker run -d --name "$agent" --network "container:$exporter" -v "$work/conf:/etc/vmagent:ro" \
  -v "$work/queue:/var/lib/vmagent" -v "$work/secrets:/run/secrets:ro" \
  "$(image_of care-telemetry)" $(exec_of care-telemetry | sed "s#-remoteWrite.url=[^ ]*#-remoteWrite.url=$url#; s#-loggerLevel=WARN#-loggerLevel=INFO#") >/dev/null
sleep 12
if docker logs "$agent" 2>&1 | grep -q '401'; then
  echo "ok: a wrong password gives 401 at the receiver"
else
  docker logs "$agent" 2>&1 | tail -5 >&2
  echo "FAIL: no 401 with a wrong password" >&2
  exit 1
fi
echo PASS
