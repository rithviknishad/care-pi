#!/bin/bash -e
# The first boot: care-firstboot.service (care-pi bootstrap --from-image) and the ACT LED units.
install -d -m 0755 "${ROOTFS_DIR}/usr/local/lib/care-pi"
install -m 0755 files/care-firstboot files/care-pi-led "${ROOTFS_DIR}/usr/local/lib/care-pi/"
install -m 0644 files/care-firstboot.service files/care-pi-led.service files/care-pi-led-off.service \
	files/care-pi-led.path "${ROOTFS_DIR}/etc/systemd/system/"

on_chroot << EOF2
systemctl enable care-firstboot.service care-pi-led.service care-pi-led.path
EOF2
