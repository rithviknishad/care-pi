#!/bin/sh
# Run "quadlet -dryrun" of podman 5.4 (Debian trixie, the podman of Raspberry Pi OS) on the quadlets of each golden
# case of agent/internal/render. Exit code 1 when quadlet reports an error or a warning for a case.
# Usage: quadlets/scripts/quadlet-dryrun.sh [CASE...]. Needs: docker.
# QUADLET_IMAGE=fedora:43 uses the podman of another distribution (apt-get or dnf; Phase 7: Fedora is experimental).
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
golden="$root/agent/internal/render/testdata/golden"
cases=${*:-$(cd "$golden" && ls)}

docker run --rm -v "$golden:/golden:ro" -e "CASES=$cases" "${QUADLET_IMAGE:-debian:trixie}" sh -c '
  if command -v apt-get >/dev/null; then
    apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq podman >/dev/null 2>&1
  else
    dnf -y -q --setopt=install_weak_deps=False install podman >/dev/null 2>&1
  fi
  echo "podman $(podman --version | cut -d" " -f3)"
  rc=0
  for c in $CASES; do
    # quadlet -dryrun logs each file it loads on stderr; only an error or a warning counts.
    out=$(QUADLET_UNIT_DIRS=/golden/$c/etc/containers/systemd /usr/libexec/podman/quadlet -dryrun 2>&1 >/dev/null | grep -iv "Loading source unit file") || true
    units=$(QUADLET_UNIT_DIRS=/golden/$c/etc/containers/systemd /usr/libexec/podman/quadlet -dryrun 2>/dev/null | grep -c "^---.*---$" || true)
    if [ -n "$out" ]; then
      echo "FAIL $c:"; echo "$out"; rc=1
    else
      echo "ok $c ($units units)"
    fi
  done
  exit $rc'
