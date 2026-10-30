# postgres-pgbackrest

This image is PostgreSQL 17 with pgBackRest.
The `care-db` container of care-pi runs it.

Image name: `ghcr.io/rithviknishad/care-pi/postgres-pgbackrest`

## What the image contains

- The official `docker.io/library/postgres:17` image (Debian 13, trixie).
- The `pgbackrest` package from the PGDG apt repository.
  The base image already configures this repository for its Debian release.
  The Containerfile does not add it again.
- The directories `/var/lib/pgbackrest`, `/var/log/pgbackrest`, `/var/spool/pgbackrest`,
  and `/etc/pgbackrest`. The `postgres` user owns them.

The `ENTRYPOINT` and `CMD` stay as in the base image.
The image behaves like `postgres:17` until the agent adds `pgbackrest` configuration.

This Containerfile is the one that spike SP6 verified on the Raspberry Pi 5 (16 KB page kernel).
SP6 used pgBackRest 2.60.0 and PostgreSQL 17.

## How the agent uses the image

- The agent renders `pgbackrest.conf` into `/etc/care-pi/rendered/db/`.
  The agent mounts that directory, not the file.
- The agent sets `archive_command = 'pgbackrest --stanza=main archive-push %p'` in the
  PostgreSQL configuration.
- pgBackRest runs inside `care-db` with exactly one local encrypted repository (decision S40).
  The repository is `/var/lib/pgbackrest`. The agent binds it to `/var/lib/care-pi/backup/pg`.
- The agent copies that repository to each backup destination with restic.
  pgBackRest never writes to a remote repository.
- The agent runs `pgbackrest stanza-create`, `check`, `backup`, `verify`, and `restore`
  with `podman exec` in `care-db`.
- The unit that runs the container must set `RunInit=true`.
  Without an init process, an orphaned pgBackRest worker can make PostgreSQL restart (SP6).

## Build locally

Run the commands from the repository root.

Podman finds the `Containerfile` by itself:

```sh
podman build images/postgres-pgbackrest -t care-pi/postgres-pgbackrest:dev
```

Docker looks for a file with the name `Dockerfile`. Name the file with `-f`:

```sh
docker build -f images/postgres-pgbackrest/Containerfile images/postgres-pgbackrest \
  -t care-pi/postgres-pgbackrest:dev
```

Check the result:

```sh
docker run --rm care-pi/postgres-pgbackrest:dev pgbackrest version
docker run --rm care-pi/postgres-pgbackrest:dev postgres --version
```

To install an exact pgBackRest version, set the build argument `PGBACKREST_VERSION`.
Use the upstream version, for example `2.60.0`.
The build fails when the PGDG repository does not offer that version.

```sh
docker build -f images/postgres-pgbackrest/Containerfile images/postgres-pgbackrest \
  --build-arg PGBACKREST_VERSION=2.60.0 -t care-pi/postgres-pgbackrest:dev
```

## Tags

The workflow `.github/workflows/images.yml` builds and pushes the image.
It builds for `linux/arm64` and `linux/amd64`.
It runs on each push to `main` that changes this directory, on demand, and every week.
The weekly run picks up base image and pgBackRest updates.

| Tag | Meaning |
|---|---|
| `17` | The newest build on PostgreSQL major version 17. |
| `17-pgbackrest<version>` | The same build. The tag names the installed pgBackRest version, for example `17-pgbackrest2.60.0`. |
| `latest` | The newest build. |
| `sha-<short>` | The build of one commit. The tag uses the 7-character commit hash. |

The agent does not use these tags at run time.
The lock file pins the image by digest.
