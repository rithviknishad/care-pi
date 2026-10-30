# How `care-pi apply` works

`care-pi apply` makes the host match `/etc/care-pi/config.yaml`.
It is idempotent: a second run without a change prints `no changes` and exits with code 0.
`care-pi bootstrap` runs `apply` at the end.

```
care-pi apply [--dry-run] [--config PATH] [--reason TEXT] [--update] [--lock-timeout DUR]
```

| Exit code | Description |
|---|---|
| 0 | apply completed, or found no change |
| 1 | a step failed, or the apply lock was not free before the timeout |
| 2 | the config is not valid, or it asks for a feature that this version does not have |

## The apply lock

Only one care-pi command changes the host at a time.
`apply`, `bootstrap`, `fe build`, and `fe prune` hold an exclusive `flock` on
`/run/care-pi/apply.lock` for their full run. care-pi creates `/run/care-pi` (root 0700) when it is missing.
Two applies that overlap are not safe. During a full cycle, the second apply sees `care.target` inactive
and starts it. That start cancels the stops of the first apply (SP6 finding 10).

A second command waits for the apply lock. After 2 seconds, it prints
`another care-pi command holds the lock; waiting`.
`--lock-timeout` sets the longest wait (default `30m`, `0` = no limit).
After the timeout, the command stops with exit code 1.

## Steps

1. **Apply lock and repair.** Take the apply lock (see "The apply lock").
   The apply lock makes sure that no FE build runs now. When the scale-down drop-in of an FE build exists,
   apply removes it and runs `daemon-reload`. When `care.target` is active, apply also restarts `care-api`
   and starts `care-worker`. apply prints `removed a leftover FE build drop-in`.
   With `--dry-run`, apply only reports the drop-in.
2. **Validate.** Load `config.yaml` and check it against the JSON Schema.
   Then reject valid settings that Phase 1 cannot apply: `tls.public`, `tls.tailscale`,
   `tls.local_ca.enabled: false`, `solution.track: pinned`, `clock.policy: rtc`,
   and a backend plug set that needs a local image build.
3. **Load the solution.** The agent embeds `solutions/<name>/solution.yaml`.
   The env merge order is: care-pi defaults, the solution, the enabled plugs, `env` in the config.
4. **Resolve the lock.** Read `/etc/care-pi/lock.yaml`. For each component, reuse the
   lock entry when the reference is the same. Resolve a component only when:
   the lock has no entry, the config reference changed, or `--update` is set and the reference is tracked.
   - Images: `skopeo inspect --override-os linux --override-arch <host arch> --format '{{.Digest}}' docker://<ref>`.
     The digest is the digest of the top-level manifest (the multi-arch list).
   - Git refs (`care_fe`, backend plugs): the agent reads the ref list over HTTPS
     (`<repo>/info/refs?service=git-upload-pack`), like `git ls-remote`. The host needs no git.
   - A digest or a full git sha is pinned and never resolves again.
   - A `localhost/` image keeps its tag.
   Without a change, apply uses no network. This makes apply safe on an offline host.
   apply prints one line for each network resolution, for example
   `resolve docker.io/library/redis:8-alpine … 21090af93f83` and `resolve care_fe develop … 83b007b72cd0`
   (the first 12 characters of the digest or the sha). A resolution takes about 5 s on a Pi 5.
   A failed resolution ends its line with `failed`.
5. **Find the addresses.** Read the LAN IPv4 addresses from the interfaces.
   The agent never resolves its own `.local` name (SP5 finding 6).
6. **Render** every file in memory (see "Rendered files").
7. **Compare** the rendered tree with the owned files on disk.
   Also compare the podman secrets with the secret files, and read the state of `care.target`.
8. **Plan** the smallest set of actions (see "Plan rules").
   With `--dry-run`, print the plan and a unified diff, then stop. Nothing is written.
9. **Execute** the actions in order. After each start or restart, make sure that the
   buckets and the pgBackRest stanza exist (see "Ensure steps"). Then make the FE build when
   `fe/current` is not the FE build id (see "FE build"). The FE build is the last long step.
10. **Write the lock** when it changed. The previous lock goes to
   `/var/lib/care-pi/state/locks/<UTC timestamp>.yaml`.

Each run with a plan writes a journal to `/var/lib/care-pi/state/jobs/<UTC timestamp>-apply.json`:
the reason, each step with its start and end time, and the result.
A run that prints `no changes` and a `--dry-run` write no journal.
Phase 1 only records the steps. A resume after a crash comes in Phase 2.

## Rendered files

| Host path | Owner, mode | Content |
|---|---|---|
| `/etc/containers/systemd/care.pod` | root 0644 | The pod. It publishes 80 and 443 on each LAN IPv4 address, and `127.0.0.1:19000` (API) and `127.0.0.1:17070` (S3) for debug. |
| `/etc/containers/systemd/care-{caddy,db,redis,s3,beat,worker,api}.container` | root 0644 | The containers. |
| `/etc/containers/systemd/care-fe-build.container` | root 0644 | Only with `fe.provider: local`. The FE builder (`docs/fe.md`). |
| `/etc/systemd/system/care.target` | root 0644 | Wants every unit of the stack. Requires `care-clock.service`. No `[Install]` section. |
| `/etc/systemd/system/care-clock.service` | root 0644 | The clock gate: `care-pi clock wait`. |
| `/etc/systemd/system/care-start.service` | root 0644 | Starts `care.target` at boot without a wait: `systemctl start --no-block care.target`. apply enables this unit, not `care.target`. |
| `/etc/care-pi/rendered/caddy/Caddyfile` | root 0600 | Routing and TLS. |
| `/etc/care-pi/rendered/caddy/status/{setup,maintenance,clock}.html` | root 0644 | The status pages. |
| `/etc/care-pi/rendered/care/backend.env` | root 0600 | The env file of api, worker, and beat. No secrets. |
| `/etc/care-pi/rendered/db/pgbackrest.conf` | root:999 0640 | One local encrypted repository (S40). The directory is root:999 0750, so postgres in the container can read it. |
| `/etc/care-pi/rendered/fe/{frontend.env,fe-build.sh}` | root 0600, 0700 | Only with `fe.provider: local`. The builder mounts the directory at `/build/env`. |

The first line of each file says: "Generated by care-pi from /etc/care-pi/config.yaml. Do not edit."
apply owns only these files: `care.pod`, `care-*.{container,pod,volume,image,build,network}`
directly in `/etc/containers/systemd/`, `care.target`, `care-clock.service`, and `care-start.service` in
`/etc/systemd/system/`, and everything in `/etc/care-pi/rendered/`.
apply deletes an owned file that the new tree does not have. It never touches other files,
for example drop-in directories (`care-api.container.d/`) or `care-pi.service`.
The one exception is the scale-down drop-in of an FE build (see "Steps", step 1).

Containers bind-mount **directories**, never single files (SP6 finding 3):
`rendered/caddy` → `/etc/caddy`, `rendered/db` → `/etc/pgbackrest`.
An atomic rename of a file in the directory is then visible in the container.

Data directories are bind mounts, not named volumes:

| Host path | Container path | Unit |
|---|---|---|
| `/var/lib/care-pi/pg` | `/var/lib/postgresql/data` | care-db |
| `/var/lib/care-pi/backup/pg` | `/var/lib/pgbackrest` | care-db |
| `/var/lib/care-pi/s3` | `/data` | care-s3 |
| `/var/lib/care-pi/redis` | `/data` | care-redis |
| `/var/lib/care-pi/caddy` | `/data` (local CA, certificates) | care-caddy |
| `/var/lib/care-pi/fe` | `/srv/fe` (read only) | care-caddy |
| `/var/lib/care-pi/plugs` | `/srv/plugs` (read only) | care-caddy |
| `/var/lib/care-pi/state/mode` | `/srv/mode` (read only) | care-caddy |
| `/var/lib/care-pi/build/care_fe` | `/build` (source, `node_modules`, npm cache) | care-fe-build |
| `/etc/care-pi/rendered/fe` | `/build/env` (read only) | care-fe-build |
| `/var/lib/care-pi/fe` | `/out` | care-fe-build |

## Unit rules

Every container unit has `RunInit=true`, `Pod=care.pod`, `PartOf=care.target`,
`Restart=always`, `WantedBy=care.target`, and both `MemoryMax=` and `MemorySwapMax=`.
`care-db` and `care-redis` have `OOMScoreAdjust=-500`.
`care-db` has `StopTimeout=110` in `[Container]` and `TimeoutStopSec=120` in `[Service]`.
`care-api`, `care-worker`, and `care-beat` have `SuccessExitStatus=143` in `[Service]`.
gunicorn and celery exit with status 143 (128 + SIGTERM) when podman stops them. Without this line,
systemd records `Failed with result 'exit-code'` at each stop and restart. The other containers exit with 0.
The `ExecStop` of quadlet runs `podman rm -f`. It uses the stop timeout of the container (default 10 s),
then it sends SIGKILL. PostgreSQL then needs crash recovery at the next start.
`TimeoutStopSec` is longer than `StopTimeout`, so systemd does not stop podman first. The FE builder has `+900`,
no `Pod=`, no `WantedBy=`, and `Restart=no`, because the agent starts it only for a build.

Start order: `care-clock.service` → `care-db`, `care-redis` (health checks with `Notify=healthy`)
→ `care-beat` (runs `migrate`, `Notify=healthy`) → `care-api` (`Notify=healthy`) and `care-worker`.
`care-caddy` and the pod do not wait for the clock gate, so Caddy can show the clock status page.
`care.target` gets an implicit `After=` on each unit that it wants, so
`systemctl start care.target` returns when the stack is healthy.

Boot: `care-start.service` (`WantedBy=multi-user.target`) runs
`systemctl start --no-block care.target`. `care.target` has no `[Install]` section.
If `multi-user.target` wanted `care.target` directly, the boot would wait for the clock gate and for
the health check of each container. Without NTP, the boot would not complete.
`care-start.service` does not wait for `network-online.target`. `care.pod` has
`Wants=network-online.target` and `After=network-online.target`, because the pod publishes on the LAN
addresses (quadlet adds the same two lines by default). Thus `multi-user.target` does not wait for
`NetworkManager-wait-online.service` (4.3 s on lumine, up to its timeout without a cable).
An older care-pi enabled `care.target`. When the link
`/etc/systemd/system/multi-user.target.wants/care.target` exists, apply runs `systemctl disable care.target`
before `systemctl enable care-start.service`.

## Plan rules

These rules implement the table in `plans/03-solutions-releases-updates.md` section 5.

| Change | Actions |
|---|---|
| First apply (no `care.target` on disk) | write all files, `daemon-reload`, `enable care-start.service`, pull missing images, `reset-failed`, `start care.target`, buckets, stanza, FE build |
| No change, `care.target` active | none: print `no changes` |
| `care.target` not active | `reset-failed`, `start care.target`, buckets, stanza |
| `backend.env` (backend env var, hostname, address) | `restart care-beat care-worker care-api` (api last). No `daemon-reload`. |
| Caddyfile or a status page only | `podman exec care-caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile`. A failed reload restarts `care-caddy`. |
| `care-api` or `care-worker` unit (sizing preset) | `daemon-reload`, restart that unit |
| `care-beat` unit | `daemon-reload`, restart beat, worker, and api |
| `care-s3` or `care-caddy` unit | `daemon-reload`, restart that unit |
| `care-db` or `care-redis` unit (for example `shared_buffers`) | `daemon-reload`, full cycle (SP6 finding 6) |
| `care.pod` (LAN address change, or any other change to the file) | `daemon-reload`, full cycle |
| image digest of a stack unit | pull the new images first, then a full cycle |
| image digest of the FE builder only | write the unit and the lock; no restart |
| frontend env or care_fe sha (new FE build id) | `ensure-fe`: the provider makes the build, then `fe/current` points at it; write the lock. With `fe.provider: local`, also write `rendered/fe/` and the builder unit, and `daemon-reload`. No container restart. |
| `care.target` or `care-start.service` | `daemon-reload`, `enable care-start.service` (and `disable care.target` when an older care-pi enabled it) |
| `care-clock.service` | `daemon-reload` |
| `pgbackrest.conf` | write it; pgBackRest reads it on each run; check the stanza; no restart |
| podman secret value | set the secret, restart its readers; a care-db secret needs a full cycle |
| stale owned file | delete it, `daemon-reload` |

A **full cycle** is:

1. `systemctl stop care.target`. `PartOf=` stops each container and the pod.
2. Poll `systemctl show -p Id -p ActiveState <units>` until each unit is `inactive` or `failed`.
   `systemctl stop` returns before the `PartOf=` stops finish. A start at that time cancels
   the stops, and old containers continue to run (SP6 finding 10). The wait limit is 5 minutes.
3. `systemctl reset-failed <units>`.
4. `systemctl start care.target`.

apply never starts or stops `care-pod.service` directly.

## Ensure steps

apply runs the ensure steps after a start, restart, or full cycle, and when the last ensure record does not match.
The ensure steps run right after the start: `systemctl start care.target` returns when the API is healthy.
They run before `ensure-fe`, because a local FE build takes minutes. PostgreSQL archives WAL from its
first start, and `archive-push` fails until the stanza exists (22 failures in the first 6 minutes of
the Phase 1 exit test, when the stanza came after the FE build).

- **Buckets.** Care never creates buckets. apply connects to versitygw on `127.0.0.1:17070`
  with minio-go and creates `FILE_UPLOAD_BUCKET` and `FACILITY_S3_BUCKET` with
  `LocationConstraint` = `BUCKET_REGION` (the same value as `VGW_REGION`).
  It sets an anonymous `s3:GetObject` policy on the facility bucket only.
  Upstream Care reads facility cover images by unsigned URL. Both steps are idempotent.
- **Stanza.** First, apply waits until care-db accepts connections:
  it polls `podman exec --user postgres care-db pg_isready --quiet` (limit 5 minutes).
  The stanza needs the database, not the API. After a start of `care.target`, care-db is already
  healthy, so the first poll passes. Then
  `podman exec --user postgres care-db pgbackrest --stanza=main stanza-create`.
  The command is idempotent.

apply records the result in `/var/lib/care-pi/state/ensure.json`.
A change to `pgbackrest.conf` runs the stanza step again.
Phase 1 renders no backup timers. Phase 3 adds them.

## Secrets

`bootstrap` generates each secret once into `/etc/care-pi/secrets/<name>` (root 0600, directory 0700):

| File | Content |
|---|---|
| `django_secret_key` | 64 random letters and digits |
| `jwks_base64` | base64 of a JWK set with one RSA 2048 private key, the shape of upstream `generate_encoded_jwks` (`n e d p q dp dq qi kty kid alg=RS256`, `kid` = RFC 7638 thumbprint) |
| `postgres_password` | 32 random letters and digits |
| `s3_access_key`, `s3_secret_key` | 20 and 40 random characters |
| `pgbackrest_repo_cipher_pass` | 64 random letters and digits |

apply delivers them as **podman secrets** (`Secret=<name>,type=env,target=<VAR>`):

| Podman secret | Variable | Units |
|---|---|---|
| `care-pi-django-secret-key` | `DJANGO_SECRET_KEY` | beat, worker, api |
| `care-pi-jwks-base64` | `JWKS_BASE64` | beat, worker, api |
| `care-pi-postgres-password` | `POSTGRES_PASSWORD` | db, beat, worker, api |
| `care-pi-database-url` | `DATABASE_URL` (built from the password) | beat, worker, api |
| `care-pi-s3-access-key` | `ROOT_ACCESS_KEY` (s3), `BUCKET_KEY` (care) | s3, beat, worker, api |
| `care-pi-s3-secret-key` | `ROOT_SECRET_KEY` (s3), `BUCKET_SECRET` (care) | s3, beat, worker, api |
| `care-pi-pgbackrest-cipher-pass` | `PGBACKREST_REPO1_CIPHER_PASS` | db |

Why podman secrets and not a root-only env file:

- The rendered files then contain no secret. `care-pi render` works without root and
  without the secret files, and the goldens are safe to commit and review.
- A diff of the rendered files never shows a secret, also in `apply --dry-run`.
- SP2 verified `Secret=...,type=env` on podman 5.4.2.

apply runs `podman secret create --replace <name> -` and sends the value on stdin,
so the value never shows in the process list or in the journal.
apply stores a short SHA-256 fingerprint of each value in `/var/lib/care-pi/state/secrets.json`
to detect a change. A changed or missing podman secret is set again, and its units restart.
pgBackRest reads the cipher pass from `PGBACKREST_REPO1_CIPHER_PASS`, so `pgbackrest.conf` has no secret.

## Status pages and modes

Caddy chooses the page for each request. No reload is necessary to switch a page.
The agent creates or deletes flag files in `/var/lib/care-pi/state/mode/`:

| Condition | Page | Status |
|---|---|---|
| `state/mode/clock-wait` exists | `status/clock.html` | 503 |
| `state/mode/maintenance` exists | `status/maintenance.html` | 503 |
| `fe/current/index.html` exists | the care_fe build (SPA) | 200 |
| else (`fe.provider: none`, or no build yet) | `status/setup.html` | 503 |

`care-pi clock wait` creates `clock-wait` when it starts to wait and deletes it when the clock passes.
Then it runs `podman exec care-caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile --force`.
When the reload fails, it runs `systemctl try-restart care-caddy.service`. Both steps are best effort.
`try-restart` does not start a Caddy that does not run.
Why: Caddy starts before the clock gate. With a stale clock (`fake-hwclock`), Caddy can load an expired
`tls internal` certificate (12 hours) as valid. After NTP sets the clock, Caddy serves that certificate
until its next renewal check, about 10 minutes later. The reload checks the certificates at once.
`--force` is necessary, because Caddy skips the reload of a config that did not change.
The update flow of Phase 4 uses `maintenance`.
The API, bucket, and plug routes do not depend on the mode.

## Caddy

- Global options: the admin API on a unix socket inside the Caddy container
  (`admin unix//run/caddy-admin.sock`), `auto_https disable_redirects`,
  `default_sni <primary LAN IP>`, and HTTP/1.1 and HTTP/2 only (443/udp is not published).
  The containers of the pod share localhost, so a TCP admin port would let any container change Caddy.
  `admin off` makes `caddy reload` impossible.
- Port 80: `redir https://{host}{uri} 308` only.
- Sites: `<hostname>.local` and each LAN IPv4 address, `tls internal`.
- Routes: `/api/*`, `/staticfiles/*`, `/mediafiles/*`, `/ping/`, `/health/*`, `/app_version/`
  → `localhost:9000` with `X-Forwarded-Proto https`, and without the HSTS header of Django.
  `/admin/*` is not routed (O16).
  `/<FILE_UPLOAD_BUCKET>/*` and `/<FACILITY_S3_BUCKET>/*` → `localhost:7070`, no prefix strip,
  Host kept, `request_body max_size` = `uploads.max_body_mb`, `flush_interval -1`.
  `/plugs/*` → files in `/var/lib/care-pi/plugs`, CORS `*`, immutable cache.
  `/console/*` and `/_care-pi/status` → a placeholder that returns 503 JSON (Phase 2 connects the agent).
  Everything else: the mode table above. The SPA gets the security headers of care_fe
  without HSTS, `/assets/*` immutable, and `encode zstd gzip`.

## Backend env

`backend.env` sets: `ADDITIONAL_PLUGS` (JSON of the enabled backend plugs), `BUCKET_PROVIDER=MINIO`,
`BUCKET_ENDPOINT=http://localhost:7070`, `BUCKET_EXTERNAL_ENDPOINT=https://<hostname>.local`,
`CELERY_BROKER_URL` and `REDIS_URL` (`redis://localhost:6379/0`), `POSTGRES_DB/HOST/PORT/USER`,
`DJANGO_ALLOWED_HOSTS` (JSON: the names, the LAN addresses, `localhost`, `127.0.0.1`), and
`CSRF_TRUSTED_ORIGINS` (JSON: `https://` and each name and address).
`localhost` is in `DJANGO_ALLOWED_HOSTS` because the upstream health check calls `http://localhost:9000/ping/`.
The upstream scripts (`start.sh`, `celery_worker.sh`, `celery_beat.sh`) read `POSTGRES_*` in
`wait_for_db.sh` and `REDIS_URL` in `wait_for_redis.sh`. Without `REDIS_URL` they build a `rediss://` URL.
The units add `GUNICORN_WORKERS` (api) and `CELERY_WORKER_CONCURRENCY` (worker), so a preset change
restarts only the affected unit. `docs/config.md` lists the defaults that `env.backend` can change.

## Other commands

| Command | What it does |
|---|---|
| `care-pi render --config PATH --out DIR [--ip IP]...` | Render to `DIR` without root and without network. It uses the lock below `--root` when the lock exists, else the tags. It also writes `DIR/etc/care-pi/lock.yaml`. |
| `care-pi config validate` | Validate only. |
| `care-pi status [--json]` | Unit states, health, memory (current, peak, cap), lock, names, and addresses. Exit code 1 when a unit is not active or not healthy. |
| `care-pi logs UNIT [-f] [-n N]` | `journalctl --unit care-<UNIT>.service`. |
| `care-pi care create-superuser --username U --email E [--password-stdin]` | `podman exec care-api python manage.py createsuperuser`. With `--password-stdin` the password goes in `DJANGO_SUPERUSER_PASSWORD`, not on the command line. |
| `care-pi clock wait [--timeout DUR]` | Wait for `timedatectl show -p NTPSynchronized` = `yes`. Then reload Caddy (see "Status pages and modes"). |
| `care-pi host check [--json]` | Report the host prerequisites. Exit code 3 when a required one is missing. |
| `care-pi bootstrap [--hostname NAME] [--preset NAME] [--fe-provider NAME] [--yes] [--lock-timeout DUR]` | Host check, directories, secrets, default `config.yaml`, then apply. `--fe-provider` (`local`, `artifact`, or `none`, default `local`) goes into a new `config.yaml`. bootstrap holds the apply lock for its full run. |

## FE build

`docs/fe.md` describes the providers (`none`, `artifact`, `local`), the build id, and the
scale-down during a local build. `agent/internal/fe` defines the interface:

```go
type Provider interface {
    Name() string
    Ensure(ctx context.Context, buildID string) (Build, error)
}
```

The build id is a hash of the care_fe sha (from the lock) and the merged frontend env.
When the provider is not `none` and `fe/current` does not point at the build id, the plan has the
step `ensure-fe`. `--dry-run` prints `FE build <id> needed (provider <name>)` and builds nothing.
`ensure-fe` runs `Ensure`, then `fe.SetCurrent` swaps the relative symlink `fe/current` atomically.
Caddy checks `fe/current/index.html` on each request, so it needs no reload.
apply records the build in the `fe` section of the lock (`build_id`, `care_fe_sha`,
`url` and `sha256` for `artifact`, `built_at` and `duration` for `local`).
With `fe.provider: none`, apply does not change `fe/current`.
A failed `ensure-fe` does not stop apply. The buckets and the stanza ran before it.
apply writes the lock and exits with code 1. The next apply tries the build again.

The local provider sets care-api to 1 gunicorn worker and stops care-worker during the build.
It writes the drop-in `/etc/containers/systemd/care-api.container.d/50-care-pi-build.conf`.
apply does not own drop-in directories. The provider removes the drop-in after the build, also after a failure,
Ctrl-C, SIGTERM, or SIGHUP (for example an SSH disconnect).
When a build stops before the restore (for example a power loss), the next apply removes the drop-in
(see "Steps", step 1).
