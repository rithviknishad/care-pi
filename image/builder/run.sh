#!/bin/bash
# run.sh: the build of the care-pi Raspberry Pi image inside the build container (docs/image.md).
# image/build.sh starts it. Do not run it on a host.
#
# Mounts: /src (the repository, read-only), /in/dist (a dist directory with the release layout
# <tag>/<asset>: the lock, its FE bundle, its FE plug bundles), /in/bin/care-pi (the agent, linux/arm64),
# /in/seed (optional: pg-<sha12>.tar.zst and .json, the database seed of make-seed.sh, S72),
# /out (the outputs), /work (a volume with an ext4 or similar file system: pi-gen and the pre-pull
# need device nodes, extended attributes, and overlay mounts, so not a bind mount of macOS).
#
# Environment: SOLUTION, LOCK_VERSION, IMAGE_VERSION, PI_GEN_REF, GIT_SHA, IMAGE_URL (the URL of the
# .img.xz for os_list.json), ICON_URL, REUSE_BASE (1: keep the stages 0 to 2 of an earlier build with
# the same PI_GEN_REF), VERIFY (1: run image/scripts/verify.sh).
set -euo pipefail

: "${SOLUTION:?}" "${LOCK_VERSION:?}" "${IMAGE_VERSION:?}" "${PI_GEN_REF:?}"
GIT_SHA="${GIT_SHA:-unknown}"
REUSE_BASE="${REUSE_BASE:-0}"
VERIFY="${VERIFY:-1}"
SRC=/src
WORK=/work
OUT=/out
DIST_IN=/in/dist
NAME="care-pi-${IMAGE_VERSION}-${SOLUTION}"
IMAGE_URL="${IMAGE_URL:-https://github.com/rithviknishad/care-pi/releases/download/image-${IMAGE_VERSION}/${NAME}.img.xz}"
ICON_URL="${ICON_URL:-https://raw.githubusercontent.com/rithviknishad/care-pi/main/image/icon.png}"

log() { printf '[image] %s\n' "$*"; }
die() { printf '[image] ERROR: %s\n' "$*" >&2; exit 1; }
t0=$(date +%s)
elapsed() { echo "$(( $(date +%s) - t0 )) s"; }

# The outputs must be readable by the user who ran build.sh: the install log comes from the chroot as root 0600,
# and on a Linux host every file of /out belongs to root. build.sh gives OUT_UID and OUT_GID. Also on a failure.
fix_out_perms() {
	chmod -R a+rX "${OUT}" 2>/dev/null || true
	if [ -n "${OUT_UID:-}" ]; then
		chown -R "${OUT_UID}:${OUT_GID:-${OUT_UID}}" "${OUT}" 2>/dev/null || true
	fi
}
trap fix_out_perms EXIT

# ---------------------------------------------------------------- 1. inputs
input="${WORK}/input"
rm -rf "${input}"
mkdir -p "${input}/dist/index"
install -m 0755 /in/bin/care-pi "${input}/care-pi"
agent_version="$("${input}/care-pi" version | sed -n 's/^version: *//p')"
log "agent: ${agent_version}"

lock_rel="lock-${SOLUTION}-${LOCK_VERSION}/${SOLUTION}-${LOCK_VERSION}.lock.yaml"
[ -f "${DIST_IN}/${lock_rel}" ] || die "the dist has no ${lock_rel}"
"${input}/care-pi" solution validate "${DIST_IN}/${lock_rel}"
"${input}/care-pi" solution validate --json "${DIST_IN}/${lock_rel}" > "${input}/lock.json"
[ "$(jq -r .name "${input}/lock.json")" = "${SOLUTION}" ] || die "the lock is not for ${SOLUTION}"

# The dist snapshot: the lock, its FE bundle, and its built FE plug bundles, with their .sha256 files.
copy_asset() {
	local rel="$1"
	[ -f "${DIST_IN}/${rel}" ] || die "the dist has no ${rel}"
	mkdir -p "${input}/dist/$(dirname "${rel}")"
	cp "${DIST_IN}/${rel}" "${input}/dist/${rel}"
	if [ -f "${DIST_IN}/${rel}.sha256" ]; then
		cp "${DIST_IN}/${rel}.sha256" "${input}/dist/${rel}.sha256"
	else
		(cd "${input}/dist/$(dirname "${rel}")" && sha256sum "$(basename "${rel}")" > "$(basename "${rel}").sha256")
	fi
}
copy_asset "${lock_rel}"
copy_asset "$(jq -r .fe.url "${input}/lock.json")"
while read -r rel; do
	copy_asset "${rel}"
done < <(jq -r '.plugs // {} | to_entries[] | .value.frontend // empty | select(.source == "built") | .url' "${input}/lock.json")
"${input}/care-pi" solution index --dist-base file:///var/lib/care-pi-dist/ --lock "${input}/dist/${lock_rel}" > "${input}/dist/index/index.json"
"${input}/care-pi" solution validate "${input}/dist/index/index.json"
(cd "${input}/dist/index" && sha256sum index.json > index.json.sha256)

# The pre-pull list: <repo> <tag> <digest>. The solution image runs api, worker, and beat.
jq -r '
  ([.solution_image.image, .solution_image.tag, .solution_image.digest] | @tsv),
  (.images | to_entries[] | .value | (.ref | capture("^(?<repo>.+?)(:(?<tag>[^:/]+))?$")) as $r
    | [$r.repo, ($r.tag // "latest"), .digest] | @tsv)
' "${input}/lock.json" > "${input}/prepull.tsv"
log "pre-pull list:"
cat "${input}/prepull.tsv"

cp "${SRC}/installer/install.sh" "${input}/install.sh"
cp "${SRC}/image/builder/preseed.py" "${input}/preseed.py"
# S72: the database seed (stage 01b-dbseed). seed.pg of image-release.json records it, or null.
mkdir -p "${input}/seed"
seed_json=null
for f in /in/seed/pg-*.tar.zst; do
	[ -f "${f}" ] || continue
	cp "${f}" "${f%.tar.zst}.json" "${input}/seed/"
	seed_json="$(jq -c --arg file "/var/lib/care-pi/seed/${f##*/}" '{care_git_sha, created_at, file: $file, size_bytes, migrations}' "${f%.tar.zst}.json")"
	if [ "$(jq -r .care_git_sha "${f%.tar.zst}.json")" != "$(jq -r '.care.git_sha // ""' "${input}/lock.json")" ]; then
		log "warning: the seed is of another CARE commit than the lock; the first start migrates the difference"
	fi
done
log "database seed: ${seed_json}"
built_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n --arg version "${IMAGE_VERSION}" --arg solution "${SOLUTION}" --arg lock_version "${LOCK_VERSION}" \
	--arg lock_sha256 "$(sha256sum "${DIST_IN}/${lock_rel}" | cut -d' ' -f1)" --arg agent "${agent_version}" \
	--arg built_at "${built_at}" --arg git_sha "${GIT_SHA}" --arg pi_gen "${PI_GEN_REF}" --argjson seed "${seed_json}" \
	'{version: $version, solution: $solution, lock_version: $lock_version, lock_sha256: $lock_sha256,
	  agent_version: $agent, built_at: $built_at, git_sha: $git_sha,
	  base: ("Raspberry Pi OS Lite (64-bit) trixie, pi-gen arm64 " + $pi_gen), seed: {pg: $seed}}' > "${input}/image-release.json"
cat "${input}/image-release.json"

# ---------------------------------------------------------------- 2. pi-gen
pigen="${WORK}/pi-gen"
if [ ! -d "${pigen}/.git" ]; then
	git clone --quiet https://github.com/RPi-Distro/pi-gen.git "${pigen}"
fi
git -C "${pigen}" fetch --quiet origin arm64
git -C "${pigen}" checkout --quiet --force "${PI_GEN_REF}"
git -C "${pigen}" clean --quiet -fdx
# Export only the care-pi stage, not Raspberry Pi OS Lite of stage2.
touch "${pigen}/stage2/SKIP_IMAGES"
# HTTPS for the Debian and Raspberry Pi archives: the debootstrap of stage0 and the apt sources of the
# image. Plain HTTP from a container goes through the HTTP proxy of Docker Desktop, which gave 22 KB/s
# (HTTPS: 4 MB/s). The image keeps these sources; apt checks the signatures in both cases.
sed -i -e 's#http://deb\.debian\.org/#https://deb.debian.org/#g' -e 's#http://archive\.raspberrypi\.com/#https://archive.raspberrypi.com/#g' \
	"${pigen}/stage0/prerun.sh" "${pigen}/stage0/00-configure-apt/files/debian.sources" "${pigen}/stage0/00-configure-apt/files/raspi.sources"
grep -q 'https://deb.debian.org/' "${pigen}/stage0/prerun.sh" || die "pi-gen ${PI_GEN_REF}: the mirror of stage0/prerun.sh changed"
rsync -a --delete "${SRC}/image/stage-care/" "${WORK}/stage-care/"

pigen_work="${WORK}/pi-gen-work"
if [ "${REUSE_BASE}" = 1 ] && [ -d "${pigen_work}/stage2/rootfs" ] && [ "$(cat "${pigen_work}/base.ref" 2>/dev/null)" = "${PI_GEN_REF}" ]; then
	log "reuse the stages 0 to 2 of an earlier build (pi-gen ${PI_GEN_REF})"
	touch "${pigen}/stage0/SKIP" "${pigen}/stage1/SKIP" "${pigen}/stage2/SKIP"
else
	rm -rf "${pigen_work}"
fi
rm -rf "${pigen_work}/stage-care" "${pigen_work}/export-image" "${WORK}/deploy"
mkdir -p "${pigen_work}"

cat > "${WORK}/config" <<EOF
IMG_NAME="${NAME}"
PI_GEN_RELEASE="care-pi image ${IMAGE_VERSION}"
RELEASE=trixie
DEPLOY_COMPRESSION=xz
COMPRESSION_LEVEL=6
TARGET_HOSTNAME=care
FIRST_USER_NAME=pi
ENABLE_SSH=0
ENABLE_CLOUD_INIT=1
STAGE_LIST="stage0 stage1 stage2 ${WORK}/stage-care"
WORK_DIR="${pigen_work}"
DEPLOY_DIR="${WORK}/deploy"
export CARE_PI_INPUT="${input}"
EOF
log "pi-gen ${PI_GEN_REF}: build (after $(elapsed))"
(cd "${pigen}" && ./build.sh -c "${WORK}/config")
echo "${PI_GEN_REF}" > "${pigen_work}/base.ref"
log "pi-gen done (after $(elapsed))"

# ---------------------------------------------------------------- 3. outputs
xz_file="$(ls "${WORK}"/deploy/*.img.xz)"
mkdir -p "${OUT}"
rm -f "${OUT}/${NAME}".*
cp "${xz_file}" "${OUT}/${NAME}.img.xz"
(cd "${OUT}" && sha256sum "${NAME}.img.xz" > "${NAME}.img.xz.sha256")
download_size="$(stat -c %s "${OUT}/${NAME}.img.xz")"
extract_size="$(xz --robot --list "${OUT}/${NAME}.img.xz" | awk '$1 == "totals" { print $5 }')"
extract_sha256="$(xz -T0 -dc "${OUT}/${NAME}.img.xz" | sha256sum | cut -d' ' -f1)"
lock_title="$(jq -r '.solution.title // .name' "${input}/lock.json")"
for target in os_list.json "${NAME}.rpi-imager-manifest"; do
	python3 "${SRC}/image/builder/os-list.py" \
		--name "Care: ${lock_title} ${LOCK_VERSION} (image ${IMAGE_VERSION})" \
		--description "Raspberry Pi OS Lite (64-bit, trixie) with Care and the ${lock_title} solution ${LOCK_VERSION}. Works without internet. After the first boot, open https://<host name>.local:8443/ and sign in with the password of the user that you set in Imager." \
		--url "${IMAGE_URL}" --icon "${ICON_URL}" \
		--extract-size "${extract_size}" --extract-sha256 "${extract_sha256}" --download-size "${download_size}" \
		--release-date "${built_at%%T*}" --out "${OUT}/${target}"
done
cp "${input}/image-release.json" "${OUT}/${NAME}.image-release.json"
cp "${input}/care-pi-install.log" "${OUT}/${NAME}.install.log" 2>/dev/null || true
cp "${pigen_work}/build.log" "${OUT}/${NAME}.pi-gen.log" 2>/dev/null || true
for f in "${WORK}"/deploy/*.info "${WORK}"/deploy/*.bmap; do
	[ -f "${f}" ] && cp "${f}" "${OUT}/${NAME}.${f##*.}"
done
log "image: ${OUT}/${NAME}.img.xz (${download_size} bytes; ${extract_size} bytes extracted; sha256 of the extracted image ${extract_sha256})"

# Free the work volume before the check: keep only stage2 (Raspberry Pi OS Lite) for --reuse-base.
rm -rf "${pigen_work}/stage0" "${pigen_work}/stage1" "${pigen_work}/stage-care" "${pigen_work}/export-image" "${WORK}/deploy"

# ---------------------------------------------------------------- 4. check
if [ "${VERIFY}" = 1 ]; then
	"${SRC}/image/scripts/verify.sh" "${OUT}/${NAME}.img.xz" "${input}/lock.json" "${WORK}/verify" 2>&1 | tee "${OUT}/${NAME}.verify.txt"
fi
log "done after $(elapsed)"
