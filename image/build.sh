#!/usr/bin/env bash
# image/build.sh: build the care-pi Raspberry Pi image (docs/image.md, "Build the image").
#
# It runs pi-gen (the generator of Raspberry Pi OS, branch arm64) in a privileged linux/arm64 container:
# Raspberry Pi OS Lite trixie (stages 0 to 2), then the stage image/stage-care: installer/install.sh
# --image-build in the chroot, the skopeo pre-pull of the solution lock, the pre-migrated database (S72:
# image/scripts/make-seed.sh runs on this host with docker before pi-gen), the pre-seed (dist snapshot, FE build,
# FE plug bundles), and care-firstboot.service. The outputs go to --out:
#   care-pi-<version>-<solution>.img.xz, .img.xz.sha256, os_list.json, <name>.rpi-imager-manifest,
#   <name>.image-release.json, <name>.install.log, <name>.pi-gen.log, <name>.info, <name>.verify.txt,
#   seed/pg-<sha12>.tar.zst and .json (the database seed)
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
  --seed FILE            use this database seed (pg-<sha12>.tar.zst of make-seed.sh, with its .json)
  --no-seed              no database seed: the first start migrates an empty database
  --no-verify            do not run image/scripts/verify.sh
EOF
}

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="" LOCK_VERSION="" SOLUTION="care-clinic" BINARY="${ROOT}/bin/care-pi_linux_arm64" VERSION="" OUT="${ROOT}/image/out"
IMAGE_URL="" PI_GEN_REF="" REUSE_BASE=0 VERIFY=1 SEED=make
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
	--seed) SEED="$2"; shift 2 ;;
	--no-seed) SEED=none; shift ;;
	--no-verify) VERIFY=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) echo "build.sh: unknown option $1" >&2; usage >&2; exit 2 ;;
	esac
done

die() { echo "build.sh: $*" >&2; exit 1; }
if [ -z "${DIST}" ] || [ ! -d "${DIST}" ]; then die "--dist DIR is required"; fi
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

# S72: the pre-migrated database. make-seed.sh runs the CARE image and the postgres image of the lock (by digest) with
# docker on this host; the stage 01b-dbseed copies the result into the image.
seed_args=()
if [ "${SEED}" = make ]; then
	lock_rel="lock-${SOLUTION}-${LOCK_VERSION}/${SOLUTION}-${LOCK_VERSION}.lock.yaml"
	[ -f "${DIST}/${lock_rel}" ] || die "the dist has no ${lock_rel}"
	lock_json="$("${DOCKER}" run --rm --platform linux/arm64 -v "${DIST}:/in/dist:ro" -v "${BINARY}:/in/bin/care-pi:ro" \
		"${BUILDER}" /in/bin/care-pi solution validate --json "/in/dist/${lock_rel}")"
	care_sha="$(jq -r '.care.git_sha // empty' <<< "${lock_json}")"
	if [ -z "${care_sha}" ]; then
		echo "build.sh: warning: the lock has no care.git_sha; no database seed" >&2
		SEED=none
	else
		repo_of() { jq -r --arg n "$1" '.images[$n] | (.ref | sub(":[^:/]+$"; "")) + "@" + .digest' <<< "${lock_json}"; }
		rm -rf "${OUT}/seed"
		"${ROOT}/image/scripts/make-seed.sh" --sha "${care_sha}" --out "${OUT}/seed" \
			--care "$(jq -r '.solution_image | .image + "@" + .digest' <<< "${lock_json}")" \
			--postgres "$(repo_of postgres)" --redis "$(repo_of redis)"
		SEED="${OUT}/seed/pg-${care_sha:0:12}.tar.zst"
	fi
fi
if [ "${SEED}" != none ]; then
	[ -f "${SEED}" ] && [ -f "${SEED%.tar.zst}.json" ] || die "no seed ${SEED} with its .json"
	SEED="$(cd "$(dirname "${SEED}")" && pwd)/$(basename "${SEED}")"
	seed_args=(-v "${SEED}:/in/seed/$(basename "${SEED}"):ro" -v "${SEED%.tar.zst}.json:/in/seed/$(basename "${SEED%.tar.zst}").json:ro")
fi

# --privileged and /dev: pi-gen mounts proc/sys/dev in the chroot and makes loop devices for the export.
"${DOCKER}" run --rm --privileged --platform linux/arm64 \
	-v /dev:/dev \
	-v "${VOLUME}:/work" \
	-v "${ROOT}:/src:ro" \
	-v "${DIST}:/in/dist:ro" \
	-v "${BINARY}:/in/bin/care-pi:ro" \
	-v "${OUT}:/out" \
	${seed_args[@]+"${seed_args[@]}"} \
	-e SOLUTION="${SOLUTION}" -e LOCK_VERSION="${LOCK_VERSION}" -e IMAGE_VERSION="${VERSION}" \
	-e PI_GEN_REF="${PI_GEN_REF}" -e GIT_SHA="${GIT_SHA}" -e IMAGE_URL="${IMAGE_URL}" \
	-e REUSE_BASE="${REUSE_BASE}" -e VERIFY="${VERIFY}" \
	"${BUILDER}" bash /src/image/builder/run.sh
