#!/bin/bash -e
# pi-gen stage of the care-pi image (docs/image.md). It starts from the root file system of
# stage2 (Raspberry Pi OS Lite).
if [ ! -d "${ROOTFS_DIR}" ]; then
	copy_previous
fi
