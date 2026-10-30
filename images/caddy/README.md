# caddy

This image is Caddy 2 with extra modules.
The `care-caddy` container of care-pi runs it.

Image name: `ghcr.io/rithviknishad/care-pi/caddy`

## What the image contains

The Containerfile has two stages.

1. The first stage starts from `docker.io/library/caddy:2-builder`.
   It runs `xcaddy build` with two modules.
2. The second stage starts from `docker.io/library/caddy:2`.
   It replaces `/usr/bin/caddy` with the new binary.

The Caddy major version is pinned to `2`.
The builder stage runs on the build host and cross-compiles for the target platform.
This stage does not need QEMU.

| Module | Caddy module names | Purpose |
|---|---|---|
| `github.com/caddy-dns/acmedns` | `dns.providers.acmedns` | DNS-01 challenge against the care-pi DNS and ACME service (Phase 6). |
| `github.com/tailscale/caddy-tailscale` | `tls.get_certificate.tailscale` and others | Certificates for `*.ts.net` names. The operator must opt in to Tailscale. |

The module `tls internal` is part of Caddy.
It needs none of the modules above.
The local CA mode works with the stock Caddy image.

The architecture plan mentions an optional Coraza module.
This image does not contain it.

## Status of the module list

Spike SP7 has not run yet.
SP7 confirms the final module list.
SP7 also tests the local CA with name constraints on real client devices.
Change the `--with` lines in the Containerfile when SP7 changes the list.

## Build locally

Run the commands from the repository root.
A build takes about 2 minutes.

Podman finds the `Containerfile` by itself:

```sh
podman build images/caddy -t care-pi/caddy:dev
```

Docker looks for a file with the name `Dockerfile`. Name the file with `-f`:

```sh
docker build -f images/caddy/Containerfile images/caddy -t care-pi/caddy:dev
```

Check the modules:

```sh
docker run --rm care-pi/caddy:dev caddy list-modules | grep -E 'acmedns|tailscale'
```

## Tags

The workflow `.github/workflows/images.yml` builds and pushes the image.
It builds for `linux/arm64` and `linux/amd64`.
It runs on each push to `main` that changes this directory, on demand, and every week.

| Tag | Meaning |
|---|---|
| `2` | The newest build on Caddy major version 2. |
| `latest` | The newest build. |
| `sha-<short>` | The build of one commit. The tag uses the 7-character commit hash. |

The agent does not use these tags at run time.
The lock file pins the image by digest.

## Known issues

- None on 2026-10-30.
  On that date both modules compiled with Caddy v2.11.7 and Go 1.27.3.
  The built image listed `dns.providers.acmedns` and `tls.get_certificate.tailscale`.
- `xcaddy` selects the newest version of each module at build time.
  The module `caddy-tailscale` has no tagged releases.
  A change on its main branch can break the build.
  The weekly workflow run shows such a failure.
- `xcaddy` tries to run `setcap` during the build and prints a message about admin privileges.
  The `COPY` step does not keep file capabilities.
  The container runs as root, so Caddy can bind ports 80 and 443.

If `caddy-tailscale` fails to compile against a new Caddy version, use this procedure:

1. Remove the `--with github.com/tailscale/caddy-tailscale` line from the Containerfile.
2. Keep the `acmedns` module.
3. Write the exact compiler error in this section.
4. Tell the owner of the Tailscale mode (Phase 6).
