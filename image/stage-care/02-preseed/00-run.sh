#!/bin/bash -e
# Pre-seed the solution of the image, so that a first boot without internet reaches a working Care:
# - /var/lib/care-pi-dist: the dist snapshot (index/index.json with dist_base
#   file:///var/lib/care-pi-dist/, the lock, the default FE bundle, the FE plug bundles);
# - /var/lib/care-pi/fe/<build id>/: the default FE build of the lock (no fe/current: setup mode
#   serves the setup page; the apply at the end of the setup points fe/current at the build);
# - /var/lib/care-pi/plugs/<slug>/<sha12>/: each built FE plug bundle of the lock;
# - /etc/care-pi/image-release.json.
dist="${ROOTFS_DIR}/var/lib/care-pi-dist"
rm -rf "${dist}"
install -d -m 0755 "${dist}"
rsync -rt --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r "${CARE_PI_INPUT}/dist/" "${dist}/"
chown -R 0:0 "${dist}"

python3 "${CARE_PI_INPUT}/preseed.py" "${CARE_PI_INPUT}/lock.json" "${dist}" "${ROOTFS_DIR}"

install -m 0644 "${CARE_PI_INPUT}/image-release.json" "${ROOTFS_DIR}/etc/care-pi/image-release.json"
