#!/usr/bin/env python3
"""os-list.py: write the Raspberry Pi Imager repository file of one care-pi image (docs/image.md).

The format is the os_list of Raspberry Pi Imager (doc/json-schema/os-list-schema.json of rpi-imager):
each entry needs name, description, icon, url, extract_size, extract_sha256, image_download_size,
release_date, and devices. init_format "cloudinit-rpi" is the format of Raspberry Pi OS trixie: Imager
writes user-data, meta-data, and network-config (cloud-init) to the boot partition.
"""
import argparse
import json


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--name", required=True)
    ap.add_argument("--description", required=True)
    ap.add_argument("--url", required=True, help="URL (or local path) of the .img.xz")
    ap.add_argument("--icon", required=True)
    ap.add_argument("--website", default="https://github.com/rithviknishad/care-pi")
    ap.add_argument("--extract-size", type=int, required=True)
    ap.add_argument("--extract-sha256", required=True)
    ap.add_argument("--download-size", type=int, required=True)
    ap.add_argument("--release-date", required=True, help="YYYY-MM-DD")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    entry = {
        "name": a.name,
        "description": a.description,
        "icon": a.icon,
        "url": a.url,
        "extract_size": a.extract_size,
        "extract_sha256": a.extract_sha256,
        "image_download_size": a.download_size,
        "release_date": a.release_date,
        "init_format": "cloudinit-rpi",
        # One image for Pi 4 and Pi 5 (S4). Pi OS ships kernel8 and kernel_2712; the firmware picks one.
        "devices": ["pi5-64bit", "pi4-64bit"],
        "capabilities": [],
        "website": a.website,
        "architecture": "armv8",
    }
    with open(a.out, "w") as f:
        json.dump({"os_list": [entry]}, f, indent=2)
        f.write("\n")


if __name__ == "__main__":
    main()
