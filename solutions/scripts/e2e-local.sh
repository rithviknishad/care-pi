#!/usr/bin/env bash
# e2e-local.sh: the local end-to-end test of Phase 4 (docs/solutions.md, section 17). It makes two solution locks
# with make-lock.sh (offline, fake digests): 2026.11.0, and 2026.11.1 with care_fasterappointments_fe on. Then it
# runs the update machine against their index.json on the fake host: N → N+1 → rollback with a PITR offer
# (agent/internal/update, TestMakeLockDist). It needs no root, no podman, and no network.
# Usage: solutions/scripts/e2e-local.sh [WORKDIR]   (default ./e2e-local; the script deletes it first)
set -euo pipefail

cd "$(dirname "$0")/../.."
WORK="${1:-./e2e-local}"
rm -rf "$WORK"
mkdir -p "$WORK/fe"
WORK="$(cd "$WORK" && pwd)"

go build -o "$WORK/care-pi" ./agent/cmd/care-pi
printf '<!doctype html><title>care</title>\n' > "$WORK/fe/index.html"
tar -czf "$WORK/care_fe.tar.gz" -C "$WORK/fe" index.html

fake() { printf '%s' "$1" | shasum -a 256 | cut -d' ' -f1; }
digest="sha256:$(fake digest)"
sha1="$(fake sha1 | cut -c1-40)"
sha2="$(fake sha2 | cut -c1-40)"
sed '/- id: care_fasterappointments_fe/,/enabled:/ s/enabled: false/enabled: true/' \
  solutions/care-clinic/solution.yaml > "$WORK/solution-n1.yaml"

common=(--solution care-clinic --out "$WORK/dist" --offline --care-pi "$WORK/care-pi"
  --care-digest "$digest" --care-git-sha "$sha1" --solution-image "ghcr.io/rithviknishad/care-pi/care@$digest"
  --care-fe-sha "$sha2" --fe-bundle "$WORK/care_fe.tar.gz"
  --plug "care_multihost_files_be=$sha1" --plug "care_filly=$sha2"
  --image "postgres=$digest" --image "caddy=$digest" --image "redis=$digest" --image "s3=$digest" --image "node=$digest")
solutions/scripts/make-lock.sh "${common[@]}" --version 2026.11.0 --notes "Lock N."
solutions/scripts/make-lock.sh "${common[@]}" --solution-file "$WORK/solution-n1.yaml" --version 2026.11.1 --notes "Lock N+1."
for f in "$WORK"/dist/lock-*/*.lock.yaml "$WORK/dist/index/index.json"; do
  "$WORK/care-pi" solution validate "$f"
done

CARE_PI_E2E_INDEX="file://$WORK/dist/index/index.json" go test -count=1 -run TestMakeLockDist -v ./agent/internal/update/
