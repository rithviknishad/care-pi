#!/usr/bin/env bash
# image/build.sh: build the care-pi Raspberry Pi image (docs/image.md, "Build the image").
#
# It runs pi-gen (the generator of Raspberry Pi OS, branch arm64) in a privileged linux/arm64 container:
# Raspberry Pi OS Lite trixie (stages 0 to 2), then the stage image/stage-care: installer/install.sh
# --image-build in the chroot, the skopeo pre-pull of the solution lock, the pre-seed (dist snapshot,
# FE build, FE plug bundles), and care-firstboot.service. The outputs go to --out:
#   care-pi-<version>-<solution>.img.xz, .img.xz.sha256, os_list.json, <name>.rpi-imager-manifest,
#   <name>.image-release.json, <name>.install.log, <name>.pi-gen.log, <name>.info, <name>.verify.txt
#
# Needs: docker (or podman with "docker" as a name) that runs linux/arm64 containers natively (an arm64
# host: Apple silicon, a GitHub ubuntu-24.04-arm runner, a Pi 5). An x86 host needs qemu-user-static
# (binfmt) and takes hours.
set -euo pipefail

usage() {
	cat <<'EOF'
Usage: image/build.sh --dist DIR --lock-version YYYY.MM.N [options]

  --dist DIR             a dist directory with the release layout <tag>/<asset>: the lock
                         (lock-<solution>-<version>/<solution>-<version>.lock.yaml), its FE bundle, and
                         its built FE plug bundles (solutions/scripts/make-lock.sh --out, or
                         image/scripts/fetch-dist.sh)
  --lock-version V       the version of the solution lock
  --solution NAME        the solution (default care-clinic)
  --binary FILE          the agent, linux/arm64 (default bin/care-pi_linux_arm64; make build-linux)
  --version V            the image version (default: the lock version)
  --out DIR              the output directory (default image/out)
  --image-url URL        the URL of the .img.xz in os_list.json (default: the release image-<version>)
  --pi-gen-ref SHA       the pi-gen commit (default: image/pi-gen.ref)
  --reuse-base           keep the Raspberry Pi OS stages of an earlier build (faster; development)
  --no-verify            do not run image/scripts/verify.sh
EOF
}

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="" LOCK_VERSION="" SOLUTION="care-clinic" BINARY="${ROOT}/bin/care-pi_linux_arm64" VERSION="" OUT="${ROOT}/image/out"
IMAGE_URL="" PI_GEN_REF="" REUSE_BASE=0 VERIFY=1
while [ $# -gt 0 ]; do
	case "$1" in
	--dist) DIST="$2"; shift 2 ;;
	--lock-version) LOCK_VERSION="$2"; shift 2 ;;
	--solution) SOLUTION="$2"; shift 2 ;;
	--binary) BINARY="$2"; shift 2 ;;
	--version) VERSION="$2"; shift 2 ;;
	--out) OUT="$2"; shift 2 ;;
	--image-url) IMAGE_URL="$2"; shift 2 ;;
	--pi-gen-ref) PI_GEN_REF="$2"; shift 2 ;;
	--reuse-base) REUSE_BASE=1; shift ;;
	--no-verify) VERIFY=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) echo "build.sh: unknown option $1" >&2; usage >&2; exit 2 ;;
	esac
done

die() { echo "build.sh: $*" >&2; exit 1; }
[ -n "${DIST}" ] && [ -d "${DIST}" ] || die "--dist DIR is required"
[[ "${LOCK_VERSION}" =~ ^[0-9]{4}\.[0-9]{1,2}\.[0-9]+$ ]] || die "--lock-version must be YYYY.MM.N"
[[ "${SOLUTION}" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || die "invalid --solution"
VERSION="${VERSION:-${LOCK_VERSION}}"
[[ "${VERSION}" =~ ^[0-9A-Za-z][0-9A-Za-z.+-]*$ ]] || die "invalid --version"
[ -f "${BINARY}" ] || die "no agent binary ${BINARY}; run: make build-linux"
PI_GEN_REF="${PI_GEN_REF:-$(tr -d '[:space:]' < "${ROOT}/image/pi-gen.ref")}"
DIST="$(cd "${DIST}" && pwd)"
BINARY="$(cd "$(dirname "${BINARY}")" && pwd)/$(basename "${BINARY}")"
mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"
GIT_SHA="$(git -C "${ROOT}" rev-parse HEAD 2>/dev/null || echo unknown)"
if [ -n "$(git -C "${ROOT}" status --porcelain --untracked-files=no 2>/dev/null)" ]; then
	GIT_SHA="${GIT_SHA}-dirty"
fi

DOCKER="${DOCKER:-docker}"
BUILDER="care-pi-image-builder"
VOLUME="${CARE_PI_IMAGE_VOLUME:-care-pi-image-work}"
"${DOCKER}" build --platform linux/arm64 -t "${BUILDER}" -f "${ROOT}/image/builder/Containerfile" "${ROOT}/image/builder"
"${DOCKER}" volume create "${VOLUME}" >/dev/null

# --privileged and /dev: pi-gen mounts proc/sys/dev in the chroot and makes loop devices for the export.
"${DOCKER}" run --rm --privileged --platform linux/arm64 \
	-v /dev:/dev \
	-v "${VOLUME}:/work" \
	-v "${ROOT}:/src:ro" \
	-v "${DIST}:/in/dist:ro" \
	-v "${BINARY}:/in/bin/care-pi:ro" \
	-v "${OUT}:/out" \
	-e SOLUTION="${SOLUTION}" -e LOCK_VERSION="${LOCK_VERSION}" -e IMAGE_VERSION="${VERSION}" \
	-e PI_GEN_REF="${PI_GEN_REF}" -e GIT_SHA="${GIT_SHA}" -e IMAGE_URL="${IMAGE_URL}" \
	-e REUSE_BASE="${REUSE_BASE}" -e VERIFY="${VERIFY}" \
	"${BUILDER}" bash /src/image/builder/run.sh
