#!/bin/bash -e
# The pre-migrated database (S72, docs/image.md section 6.5): image/scripts/make-seed.sh ran on the build host (docker)
# before pi-gen; this step copies its output into the image as /var/lib/care-pi/seed/pg-<sha12>.tar.zst and its
# record pg-<sha12>.json. At the first full apply, the agent extracts it into the empty /var/lib/care-pi/pg (owner
# 999:999), sets the password of the device, and Care then runs only the migrations that a newer lock adds.
# A build without a seed (image/build.sh --no-seed) installs nothing: the first start migrates an empty database.
shopt -s nullglob
seeds=("${CARE_PI_INPUT}"/seed/pg-*.tar.zst)
if [ "${#seeds[@]}" -eq 0 ]; then
	echo "no database seed: the first start migrates an empty database"
	exit 0
fi
install -d -m 0755 "${ROOTFS_DIR}/var/lib/care-pi/seed"
for f in "${seeds[@]}"; do
	install -m 0644 "${f}" "${f%.tar.zst}.json" "${ROOTFS_DIR}/var/lib/care-pi/seed/"
	echo "database seed: ${f##*/} ($(stat -c %s "${f}") bytes)"
done
