#!/bin/bash -e
# Pre-pull each container image of the solution lock into the containers-storage (overlay) of the
# image with skopeo on the build host (plans/05-image-and-installer.md, section 2). No podman runs in
# the chroot. The local name is <repo>:<tag>; the digest of the lock is the identity. The units of
# care-pi use <repo>@<digest>, and podman finds the image by that digest.
store="${ROOTFS_DIR}/var/lib/containers/storage"
runroot="${STAGE_WORK_DIR}/prepull-runroot"
install -d -m 0700 "${store}"
mkdir -p "${runroot}"

while IFS=$'\t' read -r repo tag digest; do
	[ -n "${repo}" ] || continue
	echo "pre-pull ${repo}:${tag} (${digest})"
	skopeo copy --retry-times 3 --override-os linux --override-arch arm64 \
		"docker://${repo}@${digest}" \
		"containers-storage:[overlay@${store}+${runroot}]${repo}:${tag}"
done < "${CARE_PI_INPUT}/prepull.tsv"

rm -rf "${runroot}"
du -sh "${store}"
