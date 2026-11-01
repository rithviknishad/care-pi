# image

This directory holds the build of the care-pi Raspberry Pi image (Phase 5).
The image is Raspberry Pi OS Lite (64-bit, trixie) after `installer/install.sh` ran, with the container images of one
solution lock already pulled. One image boots a Pi 4 and a Pi 5.
The operator and developer guide is `docs/image.md`. The design is `plans/05-image-and-installer.md`.
Spike SP10 and the measured build are in `plans/08-spike-log.md`.

| Path | Purpose |
|---|---|
| `build.sh` | The entry point. It builds the container `care-pi-image-builder` and runs `builder/run.sh` in it (privileged, linux/arm64, the volume `care-pi-image-work`). |
| `pi-gen.ref` | The pinned commit of pi-gen (branch `arm64`). Change it on purpose and test the image again. |
| `builder/Containerfile` | The build container: Debian trixie, the dependencies of pi-gen, skopeo, podman, jq. |
| `builder/run.sh` | In the container: the inputs (the dist snapshot, the pre-pull list, `image-release.json`), pi-gen, the outputs (`.img.xz`, `.sha256`, `os_list.json`, the manifest), the check. |
| `builder/preseed.py` | Extracts the FE bundle and the FE plug bundles of the lock into the root file system (the rules of the agent). |
| `builder/os-list.py` | Writes the Raspberry Pi Imager repository file (`os_list.json`). |
| `stage-care/` | The pi-gen stage after stage2: `00-install` (`install.sh --image-build` in the chroot), `01-prepull` (skopeo), `02-preseed`, `03-firstboot` (the units and scripts in `03-firstboot/files/`), `EXPORT_IMAGE`. |
| `scripts/verify.sh` | Checks the file tree of a built image (loop mounts, `systemctl --root`, `podman --root`). |
| `scripts/boot-test.sh` | Boots the root file system of a built image with systemd-nspawn (no network) after an Imager-like customisation, and checks the first boot, setup mode, and the Imager-password claim. |
| `scripts/fetch-dist.sh` | Downloads a lock and its bundles from the GitHub releases into a dist directory (CI). |
| `icon.png` | The icon of the image in Imager. |
| `out/` | The outputs and the development dist. Not in git (`.gitignore`). |

Build on an arm64 host with Docker:

```sh
make build-linux
image/scripts/fetch-dist.sh --solution care-clinic --lock-version 2026.11.0 --out image/out/dist --care-pi <care-pi>
image/build.sh --dist image/out/dist --lock-version 2026.11.0
```

CI: `.github/workflows/pi-image.yml` (`ubuntu-24.04-arm`). It publishes the release `image-<version>` and updates
`os_list.json` of the release `imager`.

Rules:

- The image runs the same `installer/install.sh` as a host. Do not copy installer steps into the stage. Add a step to
  the installer and make `--image-build` skip its runtime part.
- No device secret goes into the image. The first boot makes the secrets, `config.yaml`, and the claim code.
- The shell files pass `shellcheck` (CI job `shell`).
