#!/bin/bash -e
# Run the installer of care-pi in the chroot: the same installer/install.sh as on a running host, with
# --image-build (packages, cmdline.txt, zram, avahi, time, journal, firewall, unattended-upgrades, apt
# holds, directories, the agent, care-pi.service, the NetworkManager hook; nothing starts).
dir="${ROOTFS_DIR}/var/tmp/care-pi-image"
install -d -m 0700 "${dir}"
install -m 0644 "${CARE_PI_INPUT}/install.sh" "${dir}/install.sh"
install -m 0755 "${CARE_PI_INPUT}/care-pi" "${dir}/care-pi"

on_chroot << EOF2
sh /var/tmp/care-pi-image/install.sh --image-build --binary /var/tmp/care-pi-image/care-pi
EOF2

# pi-gen empties each file in /var/log at the export. Keep the installer log for the build artefacts.
cp "${ROOTFS_DIR}/var/log/care-pi-install.log" "${CARE_PI_INPUT}/care-pi-install.log"
rm -rf "${dir}"
