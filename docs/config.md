# config.yaml reference

`/etc/care-pi/config.yaml` is the desired state of one care-pi host.
Edit the file, then run `care-pi apply`.
`care-pi config validate` checks the file without a change to the host.
`care-pi config schema` prints the JSON Schema (draft 2020-12).
The schema source is `agent/internal/config/schema/config.schema.json`.

Rules for the whole file:

- The schema rejects unknown keys at each level.
- A missing key gets the default in this document.
- `null` is the same as a missing key for the keys that allow `null`.
- Secrets are never in this file. They are in `/etc/care-pi/secrets/`.
- The "Phase" column tells when care-pi uses the key.
  "Phase 1" keys change the rendered files now.
  "validate" keys are checked now and used in a later phase.
  A "validate" key with a value that needs a later phase makes `apply` stop with exit code 2.

## Minimal file

```yaml
version: 1
```

## Full example

```yaml
version: 1
identity:
  display_name: "Sunrise Clinic"
  hostname: care
solution:
  name: care-clinic
  track: stable
size:
  preset: small-clinic
tls:
  local_ca: {enabled: true}
clock:
  policy: ntp
backups:
  wal_archive_timeout_min: 5
env:
  backend: {EMAIL_HOST: smtp.example.org, EMAIL_PORT: "587"}
  frontend: {REACT_ALLOWED_LOCALES: "en,ml"}
fe:
  provider: local
uploads:
  max_body_mb: 200
```

## `version`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `version` | integer, `1` | required | Phase 1 | The version of this format. |

## `identity`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `identity.display_name` | string, 1 to 80 characters, no `<` or `>` | `CARE` | Phase 1 | The name of the clinic. The status pages show it. |
| `identity.hostname` | DNS label: `a-z`, `0-9`, `-`, 1 to 63 characters | `care` | Phase 1 | The mDNS label. The host name is `<hostname>.local`. |

The host name `<hostname>.local` goes into the Caddy site list, `DJANGO_ALLOWED_HOSTS`,
`CSRF_TRUSTED_ORIGINS`, `BUCKET_EXTERNAL_ENDPOINT`, and `CURRENT_DOMAIN`.
Phase 1 does not change the OS host name or avahi.
avahi announces the OS host name, so keep `identity.hostname` equal to the OS host name.
`care-pi bootstrap` writes the OS host name by default.
`apply` prints a warning when the two names differ.
On a LAN with a name collision, avahi can announce `care-2.local`.
`care-pi status` shows the names and the addresses in use.

## `solution`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `solution.name` | string | `care-clinic` | Phase 1 | A solution in `solutions/<name>/solution.yaml`. The agent embeds the solutions. |
| `solution.track` | `stable`, `pinned`, `custom` | `stable` | Phase 1 (`stable`, `custom`); validate (`pinned`) | `stable` uses the refs of the solution. `custom` uses the overrides below. `pinned` needs the published solution locks of Phase 4. |
| `solution.pin` | string `YYYY.MM.N`, or `null` | `null` | validate | The solution lock version for `track: pinned`. Required when `track` is `pinned`. |
| `solution.custom.care_ref` | image tag or `sha256:` digest, or `null` | `null` | Phase 1 | The ref of the care image for `track: custom`. |
| `solution.custom.care_fe_ref` | branch, tag, or full git sha, or `null` | `null` | Phase 1 | The care_fe ref for `track: custom`. The lock resolves it when `fe.provider` is not `none`. |
| `solution.custom.plugs` | list of plug overrides | `[]` | Phase 1 (validate for backend changes) | Add or change plugs for `track: custom`. |

Each entry of `solution.custom.plugs`:

| Key | Type | Description |
|---|---|---|
| `id` | string, required | The plug id. An unknown id adds a new plug. |
| `enabled` | boolean | Turn the plug on or off. A new plug is on by default. |
| `backend.name` | Django app name | Required for a new backend plug. |
| `backend.package` | pip requirement without a version | Required for a new backend plug, for example `git+https://github.com/<org>/<plug>.git`. |
| `backend.ref` | branch, tag, or full git sha | The ref of the backend plug. Default for a new plug: `main`. |
| `backend.configs` | object | The `configs` of the `ADDITIONAL_PLUGS` entry. Do not put secrets here. |
| `frontend.repo`, `frontend.ref`, `frontend.url` | strings | The bundle source (Phase 2 and later). |

A plug set with backend plugs that differ from the solution image needs a local image build.
The local build comes in Phase 4. Until then `apply` stops with exit code 2.
To use such a plug set now, build an image with the plugs and set `images.care`.

## `size`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `size.preset` | `solo`, `small-clinic`, `busy-counter`, `custom` | `small-clinic` | Phase 1 | The CPU preset. |
| `size.custom.gunicorn_workers` | integer 1 to 8 | none | Phase 1 | Required for `preset: custom`. |
| `size.custom.celery_concurrency` | integer 1 to 4 | none | Phase 1 | Required for `preset: custom`. |
| `size.custom.pg_shared_buffers_mb` | integer 32 to 2048 | none | Phase 1 | Required for `preset: custom`. |

| Preset | gunicorn workers | Celery concurrency | Postgres `shared_buffers` | Use |
|---|---|---|---|---|
| `solo` | 1 | 1 | 128 MB | 1 practitioner, 1 or 2 devices |
| `small-clinic` | 2 | 1 | 128 MB | 2 to 4 staff (default) |
| `busy-counter` | 3 | 2 | 256 MB | 5 to 10 at the same time; 4 GB RAM is better |

Memory caps for each unit (`MemoryMax` / `MemorySwapMax`), measured in spike SP3 and in the Phase 1 exit test:

| Unit | `solo` | `small-clinic` | `busy-counter` | `custom` |
|---|---|---|---|---|
| `care-api` | 320M / 64M | 512M / 128M | 768M / 128M | 4 or more workers: 64M + 200M for each worker, rounded up to 128M / 128M |
| `care-worker` | 384M / 128M | 384M / 128M | 512M / 128M | 3 or more: 512M + 192M for each child above 2 / 128M |
| `care-beat` | 512M / 128M | 512M / 128M | 512M / 128M | 512M / 128M |
| `care-db` | 384M / 0 | 384M / 0 | 512M / 0 | up to 192 MB buffers: 384M; up to 256 MB: 512M; above: buffers + 256M, rounded up to 128M |
| `care-redis` | 96M / 0 | 96M / 0 | 96M / 0 | 96M / 0 |
| `care-s3` | 256M / 0 | 256M / 0 | 256M / 0 | 256M / 0 |
| `care-caddy` | 128M / 0 | 128M / 0 | 128M / 0 | 128M / 0 |

The `care-beat` cap is the same for each preset. care-beat runs `migrate` at each start.
In the Phase 1 exit test, `migrate` on an empty database used 382 MiB of the old 384M cap and 85 MiB of swap.

Postgres always gets `max_connections=40` and `work_mem=4MB`.

## `tls`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `tls.local_ca.enabled` | boolean | `true` | Phase 1 | Serve `<hostname>.local` and each LAN IPv4 address with the local CA of Caddy (`tls internal`). Phase 1 needs `true`. |
| `tls.public.enabled` | boolean | `false` | validate (Phase 6) | Public certificate mode. `true` makes `apply` stop with exit code 2. |
| `tls.public.clinic_id` | string, or `null` | `null` | validate (Phase 6) | Required when `tls.public.enabled` is `true`. |
| `tls.tailscale.enabled` | boolean | `false` | validate (Phase 6) | Tailscale mode. `true` makes `apply` stop with exit code 2. |

Caddy never sends HSTS for the local CA names.

## `clock`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `clock.policy` | `ntp`, `rtc` | `ntp` | Phase 1 (`ntp`); validate (`rtc`) | The boot gate. `ntp` waits until systemd-timesyncd reports a synchronized clock. `rtc` needs spike SP9: `apply` stops with exit code 2 and `care-pi clock wait` stops with an error. |

## `backups`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `backups.wal_archive_timeout_min` | integer 1 to 60 | `5` | Phase 1 | Postgres `archive_timeout`, in minutes. It is the RPO of an idle database. |
| `backups.schedule.db_full` | systemd calendar text | `Sun 01:00` | Phase 3 | Full pgBackRest backup to the local repository. |
| `backups.schedule.db_incr` | systemd calendar text | `daily 02:00` | Phase 3 | Incremental pgBackRest backup to the local repository. |
| `backups.schedule.files` | systemd calendar text | `daily 02:30` | Phase 3 | The `files` snapshot (`/var/lib/care-pi/s3` and `/etc/care-pi`) to each destination. |
| `backups.schedule.verify` | systemd calendar text | `Sat 03:00` | Phase 3 | `pgbackrest verify` of the local repository, `restic prune` and `restic check --read-data-subset=5%` of each destination. |
| `backups.schedule.drill` | systemd calendar text | `*-*-01 04:00` | Phase 3 | The restore drill. Only when `backups.drill_enabled` is `true`. |
| `backups.retention.db_full` | integer 1 to 52 | `2` | Phase 1 | `repo1-retention-full` of pgBackRest. pgBackRest keeps the WAL of each kept full backup, so the restore window is about this number of weeks. |
| `backups.retention.archive_days` | integer 1 to 3650 | `14` | not used | Valid, but Phase 3 does not use it. `retention.db_full` sets the restore window. |
| `backups.retention.files_keep_daily` | integer 0 to 3650 | `14` | Phase 3 | The default of `retention.keep_daily` of each destination. |
| `backups.retention.files_keep_weekly` | integer 0 to 520 | `8` | Phase 3 | The default of `retention.keep_weekly` of each destination. |
| `backups.destinations` | list | `[]` | Phase 3 | The restic destinations (decision S40). Setup needs 1 or more. |
| `backups.paused` | boolean | `false` | Phase 3 | `true` stops the backup timers. "Back up now" still works. A restore from backup on a second device can set it (docs/api/README.md section 13). |
| `backups.alert_after_hours` | integer 1 to 720 | `24` | Phase 3 | A destination without a successful snapshot for this time gets the banner `destination-unreachable`. |
| `backups.drill_enabled` | boolean | `true` | Phase 3 | Run the restore drill at `backups.schedule.drill`. The agent skips a drill when the free disk space is less than 2 times the database size. |

A schedule is the `OnCalendar=` text of a systemd timer. systemd does not accept `daily HH:MM`, so `apply` writes
`*-*-* HH:MM` for it. Other texts go to the timer unchanged. Run `systemd-analyze calendar 'TEXT'` to check a text.

pgBackRest has exactly one local encrypted repository: `/var/lib/care-pi/backup/pg`.
Each destination is a restic repository. It gets two kinds of snapshot:

- `db`: the local pgBackRest repository (already encrypted) and `/etc/care-pi`, every `interval_min` minutes;
- `files`: `/var/lib/care-pi/s3` (with the xattrs of versitygw) and `/etc/care-pi`, at `backups.schedule.files`.

The snapshots contain `/etc/care-pi/secrets/` without `recovery.key`. Secrets are never in this file.
The S3 keys of a destination are in `/etc/care-pi/secrets/backup/<id>.json`. Set them with
`PUT /api/v1/backups/destinations/{id}/secrets` or `care-pi backup destination secrets`.

Each destination has `id` (`a-z0-9-`, up to 31 characters) and `type`:

| `type` | Required keys | Optional keys |
|---|---|---|
| `posix` | `path` or `disk_label` (one of the two) | `interval_min`, `retention` |
| `sftp` | `host`, `user`, `path` | `port` (default `22`), `host_key`, `interval_min`, `retention` |
| `s3` | `endpoint`, `bucket` | `region`, `prefix`, `interval_min`, `retention` |

| Key | Type | Default | Description |
|---|---|---|---|
| `path` | absolute path | none | `posix`: a directory. `sftp`: the directory on the server. |
| `disk_label` | `care-` and 1 to 11 characters `a-z0-9-` | none | `posix`: a disk that care-pi prepared (`POST /api/v1/backups/disks/{disk_id}/prepare`). `apply` writes a mount unit with `nofail` and `x-systemd.automount` at `/var/lib/care-pi/disks/<label>`. |
| `host`, `port`, `user` | strings, integer | port `22` | `sftp`: the server and the user. The agent connects with its own SSH key (`GET /api/v1/backups/ssh-key`). |
| `host_key` | `<key type> <base64>` | none | `sftp`: the pinned host key of the server. The destination test returns it at the first contact (TOFU). The timers do not connect to a server without it. |
| `endpoint`, `region`, `bucket` | URL, strings | none | `s3`: the S3 service and the bucket. The destination test can create the bucket. |
| `prefix` | key prefix | none | `s3`: a prefix inside the bucket, without `/` at the start or at the end. |
| `interval_min` | integer 5 to 1440 | `15` | Minutes between two `db` snapshots. The off-site RPO is `interval_min` + `wal_archive_timeout_min`. |
| `retention.keep_within_days` | integer 0 to 3650 | `14` | `restic forget --keep-within <N>d` for the `files` snapshots. The `db` snapshots always use `--keep-within 48h` (`docs/backups.md`, "Retention"). |
| `retention.keep_daily` | integer 0 to 3650 | `retention.files_keep_daily` | `restic forget --keep-daily`. |
| `retention.keep_weekly` | integer 0 to 520 | `retention.files_keep_weekly` | `restic forget --keep-weekly`. |
| `retention.keep_monthly` | integer 0 to 120 | `6` | `restic forget --keep-monthly`. |

The restic repository of a destination is the directory `care-pi-<key id>` below the location
(`path`, the mount point of `disk_label`, or `<bucket>/<prefix>`). The key id comes from the recovery key.
A new recovery key gives a new directory. docs/api/README.md section 13 tells the full model.

## `updates`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `updates.auto` | boolean | `false` | validate (Phase 4) | Apply new solution locks inside the window. |
| `updates.window.days` | list of `Mon` to `Sun` | `[Sun]` | validate (Phase 4) | Days of the update window. |
| `updates.window.start` | `HH:MM` | `02:00` | validate (Phase 4) | Start of the window. |
| `updates.window.end` | `HH:MM` | `05:00` | validate (Phase 4) | End of the window. |
| `updates.os_security` | boolean | `true` | validate (Phase 4) | OS security updates. |

## `env`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `env.backend` | map of `NAME: "value"` | `{}` | Phase 1 | Non-secret variables for `care-api`, `care-worker`, and `care-beat`. |
| `env.frontend` | map of `NAME: "value"` | `{}` | Phase 1 | Build-time variables of care_fe. A change gives a new FE build id, and apply makes a new build (`docs/fe.md`). |

Rules:

- Quote numbers and booleans: `EMAIL_PORT: "587"`.
- A value cannot contain a line break.
- The merge order is: care-pi defaults, then the solution, then the enabled plugs, then `env`.
  The last value wins.
  For `REACT_MFE_REGISTERED_COMPONENTS`, the merge joins the values with commas.
- care-pi sets these backend variables. The schema rejects them in `env.backend`:
  `ADDITIONAL_PLUGS`, `BUCKET_ENDPOINT`, `BUCKET_EXTERNAL_ENDPOINT`, `BUCKET_KEY`,
  `BUCKET_PROVIDER`, `BUCKET_SECRET`, `CELERY_BROKER_URL`, `CELERY_WORKER_CONCURRENCY`,
  `CSRF_TRUSTED_ORIGINS`, `DATABASE_URL`, `DJANGO_ALLOWED_HOSTS`, `DJANGO_SECRET_KEY`,
  `GUNICORN_WORKERS`, `JWKS_BASE64`, `POSTGRES_DB`, `POSTGRES_HOST`, `POSTGRES_PASSWORD`,
  `POSTGRES_PORT`, `POSTGRES_USER`, `REDIS_URL`.
- care-pi sets this frontend variable. The schema rejects it in `env.frontend`:
  `REACT_CARE_API_URL` (always empty: same origin).
- care-pi does not set `REACT_PUBLIC_URL`. In care_fe it sets only the `og:url` meta tag,
  and care_fe has a default. You can set it in `env.frontend`. It then changes the build id,
  so the default bundle of the artifact provider does not fit (`docs/fe.md`).
- care-pi defaults that `env.backend` can change:

| Variable | Default | Why |
|---|---|---|
| `BUCKET_REGION` | `ap-south-1` | Care default. care-pi sets `VGW_REGION` of versitygw to the same value. |
| `FILE_UPLOAD_BUCKET` | `patient-bucket` | Caddy routes `/<bucket>/*` to versitygw. `apply` creates the bucket. |
| `FACILITY_S3_BUCKET` | `facility-bucket` | Same. `apply` also sets an anonymous read policy on this bucket only. |
| `DJANGO_SETTINGS_MODULE` | `config.settings.production` | `manage.py` defaults to the local settings. The deployment settings use a fixed OTP. |
| `USE_SMS` | `False` | The production settings send OTPs by SMS. A Pi has no SMS backend. |
| `DJANGO_SECURE_SSL_REDIRECT` | `False` | Caddy redirects port 80. The container health check uses plain HTTP on localhost. |
| `DJANGO_SECURE_HSTS_INCLUDE_SUBDOMAINS`, `DJANGO_SECURE_HSTS_PRELOAD` | `False` | No HSTS on local CA names. Caddy also removes the HSTS header of Django. |
| `CURRENT_DOMAIN` | `https://<hostname>.local` | Care builds password reset links from it. |

Bucket names must be valid S3 names and must not be a Caddy route
(`api`, `staticfiles`, `mediafiles`, `ping`, `health`, `app_version`, `plugs`, `console`, `_care-pi`, `assets`, `admin`).

## `fe`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `fe.provider` | `none`, `artifact`, `local` | `local` | Phase 1 | The source of the care_fe build. `local` builds care_fe on the host (S8). The first build takes about 3 minutes and needs internet. `artifact` downloads a bundle. `none` serves the setup status page and does not change `fe/current`. |
| `fe.artifact_url` | `https://` or `file://` URL, or `null` | `null` | Phase 1 | The bundle (`tar.gz`) for `provider: artifact`. `null`: the default bundle of the solution for the care_fe sha of the lock and the build id, `https://github.com/rithviknishad/care-pi/releases/download/fe-<solution>-<sha12>-<build id>/care_fe-<solution>-<sha12>-<build id>.tar.gz`. |
| `fe.artifact_sha256` | 64 hex digits, or `null` | `null` | Phase 1 | The expected SHA-256 of the bundle. A different value stops the download. For the default bundle, the agent also reads the `.sha256` file next to the bundle. |

`docs/fe.md` describes the providers, the build id, the scale-down during a local build, and the fe-bundle workflow.
`provider: local` renders `care-fe-build.container`, `rendered/fe/fe-build.sh`, and `rendered/fe/frontend.env`.

## `images`

Each key overrides one default image with a full reference (`repo:tag` or `repo@sha256:...`).

| Key | Default | Phase |
|---|---|---|
| `images.care` | `ghcr.io/ohcnetwork/care:<care ref>` when the solution has no backend plug that is on; else `ghcr.io/rithviknishad/care-pi/care:<solution>-latest` | Phase 1 |
| `images.postgres` | `ghcr.io/rithviknishad/care-pi/postgres-pgbackrest:17` | Phase 1 |
| `images.caddy` | `ghcr.io/rithviknishad/care-pi/caddy:2` | Phase 1 |
| `images.redis` | `docker.io/library/redis:8-alpine` | Phase 1 |
| `images.s3` | `docker.io/versity/versitygw:latest` | Phase 1 |
| `images.node` | `docker.io/library/node:22-bookworm-slim` | Phase 1 (`fe.provider: local`: the image of `care-fe-build.container`) |

The lock pins a tag to a digest (`skopeo inspect`).
A reference with a digest is pinned and never resolves again.
A reference that starts with `localhost/` names a local image. The lock keeps the tag,
for example `images.postgres: localhost/care-pi/postgres:17-pgbackrest`.

## `uploads`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `uploads.max_body_mb` | integer 1 to 10240 | `200` | Phase 1 | Caddy `request_body max_size` for the bucket routes. |

## `network`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `network.wifi` | `null`, or `{ssid, failover}` | `null` | validate (Phase 6) | Wi-Fi profile. `ssid` is required, 1 to 32 characters. |

The LAN addresses come from the interfaces, not from this file.
care-pi uses the IPv4 addresses of the interfaces that are up.
It ignores loopback, link-local addresses, and the interfaces `podman*`, `veth*`,
`tailscale*`, `docker*`, `cni*`, `br-*`, `virbr*`, `wg*`, `tun*`, `utun*`, and `zt*`.

## `telemetry`

| Key | Type | Default | Phase | Description |
|---|---|---|---|---|
| `telemetry.enabled` | boolean | `false` | validate (Phase 7) | Opt-in metrics. |
| `telemetry.remote_write_url` | `http(s)://` URL, or `null` | `null` | validate (Phase 7) | The Prometheus remote-write URL. |
