#!/bin/sh
# test-download.sh: checks the download path of install.sh against the release workflow, without a network and
# without root. It sources install.sh (CARE_PI_SOURCE_ONLY=1: nothing runs) and checks:
#   - the release URLs of agent_base_url (a tag, latest, CARE_PI_DOWNLOAD_BASE),
#   - the asset names: install.sh downloads care-pi_linux_<arch> and SHA256SUMS; .github/workflows/release.yml and
#     the Makefile make them,
#   - sums_lookup on a SHA256SUMS file in the format of the workflow (sha256sum),
#   - step_helper restarts a running helper after a new agent binary (it runs the binary of the agent).
# Run it from any directory: sh installer/test-download.sh
# The checks compare literal text of the files, with $ in single quotes:
# shellcheck disable=SC2016
set -eu

dir=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$dir/.." && pwd)
workflow="$root/.github/workflows/release.yml"
makefile="$root/Makefile"

fails=0
check() {
  # check NAME GOT WANT
  if [ "$2" = "$3" ]; then
    printf 'ok    %s\n' "$1"
  else
    printf 'FAIL  %s\n      got:  %s\n      want: %s\n' "$1" "$2" "$3"
    fails=$((fails + 1))
  fi
}
has() {
  # has NAME FILE FIXED-STRING
  if grep -qF -- "$3" "$2"; then
    printf 'ok    %s\n' "$1"
  else
    printf 'FAIL  %s: %s has no "%s"\n' "$1" "$2" "$3"
    fails=$((fails + 1))
  fi
}

CARE_PI_SOURCE_ONLY=1
# shellcheck source=installer/install.sh
. "$dir/install.sh"
set +e

unset CARE_PI_DOWNLOAD_BASE
AGENT_VERSION=latest
check "latest URL" "$(agent_base_url)" "https://github.com/rithviknishad/care-pi/releases/latest/download"
AGENT_VERSION=v0.1.0
check "tag URL" "$(agent_base_url)" "https://github.com/rithviknishad/care-pi/releases/download/v0.1.0"
CARE_PI_DOWNLOAD_BASE="http://192.168.1.5:8000/"
check "CARE_PI_DOWNLOAD_BASE" "$(agent_base_url)" "http://192.168.1.5:8000"
unset CARE_PI_DOWNLOAD_BASE

# The names that install.sh downloads.
has "install.sh: the binary name" "$dir/install.sh" 'ar_asset="care-pi_linux_$ARCH"'
has "install.sh: SHA256SUMS" "$dir/install.sh" 'fetch "$ar_base/SHA256SUMS"'

# The names that the Makefile and the release workflow make.
has "Makefile: both architectures" "$makefile" 'for arch in arm64 amd64'
has "Makefile: the binary name" "$makefile" '$(BIN_DIR)/care-pi_linux_$$arch'
for arch in arm64 amd64; do
  has "release.yml: copies care-pi_linux_$arch" "$workflow" "bin/care-pi_linux_$arch"
  has "release.yml: publishes care-pi_linux_$arch" "$workflow" "release/care-pi_linux_$arch"
done
has "release.yml: SHA256SUMS of the 4 files" "$workflow" 'sha256sum care-pi_linux_arm64 care-pi_linux_amd64 install.sh uninstall.sh > SHA256SUMS'
for f in SHA256SUMS install.sh uninstall.sh; do
  has "release.yml: publishes $f" "$workflow" "release/$f"
done
has "release.yml: the release is the latest" "$workflow" '--latest --title'
has "release.yml: the version of the binary" "$workflow" 'make build-linux VERSION="${TAG}"'

# sums_lookup reads the format of sha256sum (two spaces), the binary marker, and a directory part.
a=0000000000000000000000000000000000000000000000000000000000000a64
b=0000000000000000000000000000000000000000000000000000000000000a86
c=00000000000000000000000000000000000000000000000000000000000000c1
sums=$(printf '%s  care-pi_linux_arm64\n%s *care-pi_linux_amd64\n%s  release/install.sh\n' "$a" "$b" "$c")
check "sums_lookup arm64" "$(printf '%s\n' "$sums" | sums_lookup /dev/stdin care-pi_linux_arm64)" "$a"
check "sums_lookup amd64 (binary marker)" "$(printf '%s\n' "$sums" | sums_lookup /dev/stdin care-pi_linux_amd64)" "$b"
check "sums_lookup install.sh (directory)" "$(printf '%s\n' "$sums" | sums_lookup /dev/stdin install.sh)" "$c"
check "sums_lookup missing" "$(printf '%s\n' "$sums" | sums_lookup /dev/stdin care-pi_linux_riscv64)" ""

# step_helper with stubs (no file is written): a new agent binary (step 12) restarts a running helper, also when the
# units of the helper did not change; without a new binary and new units, nothing restarts.
RUNS=""
run() { RUNS="$RUNS|$*"; }
ensure_agent_user() { :; }
ensure_dir() { :; }
ensure_service() { :; }
write_file() { LAST_WRITE=0; }
SYSTEMD=1 IMAGE_BUILD=0 HELPER=0 HELPER_DROPIN=/nonexistent/60-helper.conf
AGENT_CHANGED=1
step_helper
check "step_helper: a new agent binary restarts the helper" "$RUNS" "|systemctl try-restart care-pi-helper.service"
RUNS="" AGENT_CHANGED=0
step_helper
check "step_helper: no change, no restart" "$RUNS" ""

if [ "$fails" -gt 0 ]; then
  printf '%d check(s) failed\n' "$fails"
  exit 1
fi
echo "all download checks passed"
