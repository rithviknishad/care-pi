# 09 — Handoff: state of care-pi after planning + spikes SP1–SP6

Written 2026-10-29 so another agent (or a future you) can continue without the original
conversation. Everything here is either a decision recorded in `00-decisions.md` or a number
measured on lumine and logged in `08-spike-log.md`; nothing is assumed. When this document
and another file disagree, `00` wins for decisions and `08` wins for measurements — then fix
this file.

## 1. What care-pi is

Self-hosted CARE (backend `ohcnetwork/care`, frontend `ohcnetwork/care_fe`) for a small
clinic or pharmacy on a Raspberry Pi with as little as 2 GB RAM. Deliverables, in order of
delivery (`06-roadmap.md`):

1. **Agent + CLI** (`care-pi`, Go, root, unix socket) that renders one declarative
   `/etc/care-pi/config.yaml` into quadlet units, env files, `Caddyfile`, `pgbackrest.conf`,
   restic timers and a resolved lockfile, and applies it idempotently (`care-pi apply`).
2. **Console** (React + Vite + careui/shadcn, embedded in the agent, served by Caddy at
   `https://<host>/console/`): setup wizard (easy presets / advanced knobs), dashboard,
   backups, updates, network, certificate download.
3. **Installer** `install.sh` (`curl | sh`) for Pi OS / Debian-family hosts with quadlet-capable
   podman.
4. **Pi image** (Raspberry Pi OS Lite trixie arm64, one image for Pi 4 and Pi 5, built with
   `rpi-image-gen`, container images pre-pulled, published to Raspberry Pi Imager via a custom
   `os_list.json`), fully offline-capable at first boot.
5. **Solutions**: curated plug sets + env defaults (`solutions/<name>/solution.yaml`), v1 =
   **CARE Clinic** only. CI resolves solution locks (fully pinned digests/shas), builds solution
   images and FE plug bundles.

It is an independent project (not OHC): repo `github.com/rithviknishad/care-pi` (private for
now; GitHub Pages for bundles needs public), images under `ghcr.io/rithviknishad/care-pi/*`,
DNS/ACME side service on `care-pi.rithviknishad.dev` (separate project, Phase 6).
`~/ohc.network/care_clinic` is inspiration only.

## 2. Decisions in one screen

Authoritative ledger: `00-decisions.md` (S1–S45 settled, P1–P20 all accepted, O-list in §C).
The ones an implementer must not drift from:

| Area | Decision |
|---|---|
| Runtime | Rootful podman ≥ 5.4, quadlets, **one pod** `care.pod`; services talk over `localhost:<port>`. Lifecycle via `care.target` + `PartOf=`; never start/stop the pod unit directly. Default Pi OS kernel (16 KB pages on Pi 5). |
| Proxy / FE | **Caddy** replaces nginx; no FE container — care_fe build output and plug bundles are files on a volume served by `file_server`. Same origin: `REACT_CARE_API_URL=""`. |
| Transport | **HTTPS only.** Port 80 = `301` only. TLS modes composable: `public` (DNS-01 via our service; primary when online), `local_ca` (name-constrained CA, `care.local` + raw IP, offline fallback), `tailscale` (strictly opt-in, host apt package, never installed unless remote access is enabled). No HSTS on local-CA hostnames. One image; mode auto-picked at first contact, pre-seedable via Imager `user-data`. |
| First contact | S36 + `07` §6: TLS interstitial once in local-CA mode, root-CA download with fingerprint + per-OS instructions, QR/USB enrolment of further devices; Imager password is the claim credential; no plaintext bootstrap page. |
| Config | One source of truth `config.yaml`; rendered files never hand-edited; lockfile pins exact digests/shas/build ids. |
| Sizing | Presets `solo` 1/1, `small-clinic` 2/1 (default), `busy-counter` 3/2 (warn on 2 GB), `custom`. These are **CPU** presets; memory caps per unit in `01` §8. |
| FE | Built **on the Pi** by default (`node:22` container, shaped not capped — see §3), app scaled to `solo` + worker stopped during builds; `artifact` provider (CI-built bundle URL) as the alternative; `remote-build` later. care_fe stays build-time configured. |
| BE plugs | CI-prebuilt solution image first; local `.build` quadlet (`FROM` pinned upstream digest + `ADDITIONAL_PLUGS`) as override. |
| Refs | Per component, operator either *tracks* a ref (branch/tag, re-resolved at update check) or *pins* a sha/digest. Clinic default tracks `latest` because upstream semver tags stopped Feb 2025. Solution locks are calendar-versioned (`2026.11.0`), fully pinned. |
| Updates | Operator-chosen; optional auto-update inside a window; nobody pushes. Stage online → maintenance page → stop app → pgBackRest checkpoint → start beat (`migrate`) → api/worker → health → new lock; rollback = previous lock + PITR offer. OS: `unattended-upgrades` security pocket + reboot in window. |
| Backups (**S40**) | pgBackRest inside `care-db` with **exactly one local encrypted repo**; every operator destination (`posix`/USB, `sftp`, `s3`) is a **restic** repo snapshotting {pg repo, versitygw files (xattrs!), `/etc/care-pi`} on its own timer. ≥ 1 destination mandatory, off-site recommended. One operator **recovery key** → HKDF → both tools' passphrases. Configurable `archive_timeout`. |
| Files | versitygw over `/var/lib/care-pi/s3`; agent creates buckets; presigned URLs proxied by Caddy unchanged. |
| Clock | Boot gate `care-clock.service`: policy `ntp` (default, waits for timesyncd) or `rtc` (RTC present + passively verified battery + time ≥ agent build time). Caddy shows "waiting for clock" until it passes. |
| Network | Ethernet required until onboarding completes; afterwards the console writes NetworkManager profiles (Wi-Fi, failover) — not cloud-init re-runs. mDNS name default `care.local`, configurable; **collisions are real** (`care.local` was already taken on the owner's LAN) so always show the actually advertised name + IP. |
| Agent auth | Separate from Care: password (argon2id) + recovery codes, optional TOTP, HttpOnly sessions, rate limits. Agent creates the first Care superuser via `manage.py` in `care-api`, never writes Care's DB directly. |
| Security scope | Care's auth wall + RBAC is the main control; no disk encryption, physical attacks out of scope (v1). |
| Image | Pi OS Lite trixie; `rpi-image-gen` (pi-gen fallback); first-boot oneshot; pre-pulled images (~1.9 GB set) via skopeo into `/var/lib/containers/storage`. |
| Repo layout | P19: `agent/ console/ quadlets/ images/ solutions/ installer/ image/ docs/ .github/`. lumine-specific material is gitignored (`local/`, `*.lumine.*`). |

## 3. Measured facts (lumine: Pi 5 2 GB, trixie, kernel 6.18 16 KB pages, podman 5.4.2, USB SSD)

Full detail per spike in `08-spike-log.md`; folded into `01` §4/§5/§8, `02`, `03` §7, `05`.

| Topic | Number / fact |
|---|---|
| 16 KB page kernel | `redis:8-alpine`, `postgres:17`, `caddy`, `versity/versitygw`, `node:22`, `ghcr.io/ohcnetwork/care:latest` all run. Pre-pull set 1.9 GB. |
| Quadlet | `.pod/.container/.volume/.image/.build`, `Secret=`, `Notify=healthy` all work on 5.4.2. |
| Memory accounting | Stock Pi OS `cmdline.txt` has `cgroup_disable=memory` → image/installer must set `cgroup_enable=memory cgroup_memory=1`. `MemoryMax` is only enforced when `MemorySwapMax` is also set. |
| Idle stack (`small-clinic`) | ≈ 820 MB sum of units; host `MemAvailable` ≈ 0.97 GB. |
| Peaks | api 286 MiB (solo) / hits 512M cap at 3 workers; beat 314 MiB during `migrate`; db 187 MiB (migrate), 233 MiB (during pgBackRest backup); redis 36; versitygw 79 (50 MB transfer); Caddy 27 (50 MB transfer). |
| Throughput (20 clients, simple API) | solo ≈ 54 req/s; small-clinic ≈ 93; busy-counter ≈ 114. CPU-bound, not memory-bound. |
| Recommended caps | api `64M + 200M × workers` (320/512/768); worker 384M c=1 / 512M c=2; beat 512M (384M until the Phase 1 exit test, `08`); db 384M (512M at 256 MB buffers); redis 96M; versitygw 128–256M; Caddy 96M; agent 128M / 32M (64M until wave 4d, `08` "Phase 2 exit test"). All with `MemorySwapMax` set. |
| Start-up | Empty DB → API healthy 3 min 20 s. api+worker restart 45 s (upstream runs `collectstatic` + `compilemessages` every start). Full cold stack restart 44 s. |
| FE build (care_fe, vite 8 / rolldown) | Working set ≈ 1.8–2 GiB; a 1 GiB `MemoryMax` is OOM-killed every time. Fits 2 GB only via Pi OS zram (1× RAM, zstd, ~4:1). Winning unit: `MemoryHigh=768M MemoryMax=1700M MemorySwapMax=1500M OOMScoreAdjust=900`, db/redis `OOMScoreAdjust=-500`, app at `solo` + worker stopped → API 15–30 ms throughout. Warm ≈ 1 min, cold (`npm ci`, 1456 pkgs) ≈ 3 min. `--sourcemap false` → 13 MB vs 45 MB. Source via `codeload.github.com/<org>/<repo>/tar.gz/<sha>` (no git needed). Persistent per-repo `node_modules` 1.2 GB + npm cache 0.4 GB. |
| Caddy + versitygw | Presigned S3 URLs proxy unchanged (path-style; don't strip prefix or rewrite Host). `VGW_REGION` must equal Care's `BUCKET_REGION` (else `AuthorizationHeaderMalformed`). Care never creates buckets → agent creates `patient-bucket`/`facility-bucket` with `LocationConstraint`; facility bucket needs an anonymous `s3:GetObject` policy (upstream fetches cover images unsigned). versitygw stores object metadata in **xattrs** (`user.etag`, …). |
| Caddy by IP | `default_sni <LAN IP>` is mandatory or IP clients get a TLS `internal error`. Publish 80/443 on explicit LAN IPs, not `0.0.0.0` (collided with `tailscale serve`). Django static prefix is `/staticfiles/`. Pass `X-Forwarded-Proto`; set `CSRF_TRUSTED_ORIGINS`. |
| FE bundle | `REACT_CARE_API_URL=""` yields a same-origin bundle (0 references to the upstream API host). |
| mDNS | On-Pi nss-mdns resolved the Pi's own name to podman0 `10.88.0.1` → avahi `allow-interfaces=eth0 wlan0`. |
| pgBackRest | Custom `postgres:17` + PGDG pgbackrest 2.60 via `.build` quadlet, 40 s build. 46 MB DB: full 4 s, incr 5 s, PITR restore 8 s + 5 s recovery, exact to the second, from SFTP and S3; repo is ciphertext (`Salted__`); API unaffected during backups. |
| pgBackRest multi-repo | **All-or-nothing**: one unreachable repo stalls `archive-push` to all (pg_wal grew to 145 MB in 4 min); `backup` without `--repo=N` writes only repo1. → S40. |
| pgBackRest misc | libssh2 negotiates **ECDSA** host keys → pin the fingerprint pgBackRest reports (TOFU). Remote `verify` is slow (77 s for 5 MB over SFTP) → `restic check --read-data-subset` for remotes. Scratch restore must mount data at the same `--pg1-path` and run with `archive_mode=off`. Neither pgBackRest nor Care create buckets. restic 0.18 and rclone are in trixie. |
| Container robustness | Postgres as PID 1 crash-restarted when an orphaned async worker died → **`RunInit=true` on every container** (verified fix). `systemctl stop care.target` returns before `PartOf=` stops finish → agent waits for units `inactive` before `start`. `sed -i` on a bind-mounted file swaps the inode and the container keeps the old one → bind-mount **directories**. |
| Care behaviours | Patient-create 400s under concurrency come from Care's global Redis `PatientCreateLock` (not ours). JWT access token lives 10 min. `load_fixtures` is unusable in the prod image. Upstream semver tags stopped Feb 2025 (`latest` = develop). `/console` path is free in care_fe routes (`/admin` is taken). |
| Plug URLs | care_fe `PluginEngine.tsx` validates `meta.url` with zod `z.url()` → absolute URLs only → plug bundles are host-bound; only matters when > 1 hostname (O7). |

## 4. What is still unmeasured

| # | Spike | Why it matters | Fallback |
|---|---|---|---|
| SP7 | Custom Caddy build (acme-dns module, Tailscale cert getter, agent-supplied **name-constrained** root for `tls internal`); browser facts F5–F7 in `07` §2 per browser/OS (HTTPS-first fallback, service worker on bypassed origin, bypass persistence, name-constraint enforcement on Apple); root-CA install UX on iOS/Android/Windows/macOS | Local-CA mode UX and whether name constraints hold | Document caveats; push public-cert mode harder |
| SP8 | mDNS `care.local` from Android (several versions), Windows 10/11, iOS, macOS | How often the IP/QR fallback is needed | Already planned |
| SP9 | Pi 5 RTC passive battery verification; `fake-hwclock` interplay; `systemd-time-wait-sync` gating | `rtc` clock policy feasibility | `ntp` only in v1 |
| SP10 | `rpi-image-gen` trixie Lite with chroot hook running `install.sh` + skopeo pre-pull; Imager customisation still reaches avahi/cloud-init | Phase 5 pipeline | `pi-gen` |
| SP11 | NetworkManager eth+wlan failover (gist-style bond vs secondary connection) | Phase 6 | Secondary connection, no bond |
| — | Pi 4 (2 GB/4 GB): everything above, especially FE build time/margins and throughput | O11 | `artifact` FE provider on Pi 4 |
| — | Browser-driven file upload through Care's UI (SP5 used curl against the presigned URLs) | Confirms CORS/Content-Type path end to end | — |

## 5. Open questions and when they block

From `00` §C (resolved ones struck there): **O4** Tailscale SaaS vs Headscale (Phase 6);
**O6** Clinic plug set confirmation (`03` §1.1 — Phase 4 CI matrix); **O7** timing of the
upstream care_fe PR for relative `meta.url` (ask before opening; stopgap = CORS from Caddy;
only when multihost is on); **O9** password + recovery codes vs SSH reset (Phase 2); **O11**
Pi 4 in v1 acceptance; **O12** who hosts the telemetry side (Phase 7); **O13** Renovate for
upstream bumps (CI design, Phase 4); **O16** expose Django admin? (Phase 2); **O17** on-Pi DNS
advice for "internet up, LAN DNS down" (Phase 6). Also from `07` §6: Q-c name-constraint set
(`.local` + Pi IP + `*.care-pi.rithviknishad.dev` assumed) and Q-d require Imager
customisation (assumed yes) — defaults taken unless overridden.

Working rule with the owner: **ask when unclear, don't assume**; concise answers; questions
batched with the trade-off stated.

## 6. Next steps

### 6a. Finish Phase 0 (optional before Phase 1, required before Phases 2/5/6)

SP7 and SP8 need real client devices; SP9 needs a power-off cycle on lumine; SP10 needs an
x86 or arm64 build host with `rpi-image-gen`; SP11 needs a Wi-Fi network lumine can join.
None blocks Phase 1.

### 6b. Phase 1 — declarative core (size L), in this order

1. **Repo scaffold** per P19; `go.mod` for `agent/`; CI skeleton (lint/test; arm64+amd64
   image builds).
2. **`images/postgres-pgbackrest`**: `FROM postgres:17` + PGDG `pgbackrest` (what SP6 built via
   `.build`), published to ghcr. **`images/caddy`**: `xcaddy` with acme-dns + Tailscale
   modules (SP7 confirms the module list; `tls internal` works without them).
3. **`config.yaml` schema** (`01` §6) + **renderer** → `quadlets/` templates: `care.pod`
   (publish 80/443 on known LAN IPs), `care-caddy`, `care-db` (`RunInit=true`, directory
   bind-mounts, `MemoryMax`+`MemorySwapMax`), `care-redis`, `care-s3`, `care-api`,
   `care-worker`, `care-beat`, volumes, `care.target` with `PartOf=`, `care-clock.service`,
   `care-fe-build` (shaped unit from SP4). Every container `RunInit=true`.
4. **`care-pi apply`**: render → diff → write → `daemon-reload` → minimal restart set; waits
   for `inactive` after stops; creates buckets with policies (SP5); creates secrets; `status`,
   `logs`.
5. **Host prep in `install.sh`**: podman/skopeo/restic/avahi; `cgroup_enable=memory
   cgroup_memory=1`; zram ≥ 1× RAM; avahi `allow-interfaces`; nftables (80/443/5353 only);
   `/etc/care-pi`, `/var/lib/care-pi` layout (`01` §3).
6. **Caddy**: local CA + `care.local` + IP (`default_sni`), routing table `01` §5
   (`/api`, `/staticfiles/`, `/s3/` → versitygw unchanged, `/plugs/`, `/console/`, FE
   fallback), status pages (clock gate, maintenance, setup).
7. **FE provider `artifact`** (CI builds a default care_fe bundle with `REACT_CARE_API_URL=""`
   and publishes it), then **provider `local`** (SP4 recipe: codeload tarball, persistent
   build dir keyed by `package-lock.json` hash, scale-down/restore around the build).
8. **Solution image `care-clinic`** in CI (`FROM ghcr.io/ohcnetwork/care@sha256:…` +
   `ADDITIONAL_PLUGS`), zero or one plug.
9. **Exit test on a freshly flashed lumine**: `curl | sh` → `https://care.local` serves Care;
   `care-pi care create-superuser`; upload a file; reboot → everything returns; edit preset in
   `config.yaml` → `apply` does the minimal restart.

Then Phase 2 (console + wizard), Phase 3 (backups per S40: pgBackRest local repo + restic
destinations + recovery key + restore flows), Phase 4 (solutions/locks/updates), Phase 5
(image), Phase 6 (public certs/Tailscale/Wi-Fi + DNS/ACME service), Phase 7 (hardening).

## 7. Dev target: lumine

Pi 5 2 GB, `ssh rithviknishad@lumine`, passwordless sudo, owner has said "it's all yours".
Its current (spike) state — running units, paths, credentials, what to leave alone — is
kept **out of the repo** in `local/LUMINE.md` (gitignored, S39). If that file is missing,
treat lumine as unknown: `systemctl list-units 'care*'`, `ls /etc/containers/systemd
/etc/care-pi-spike /var/lib/care-pi-spike`, and leave the owner's `tailscaled` and
node-exporters alone.

Practical pitfalls learned while driving it:

- Heredocs/nested quotes over `ssh` are fragile — write scripts into `local/`, `scp`, run.
- `pkill -f <pattern>` can match your own ssh shell.
- `getfattr`, `zramctl`, `bc`, `sysctl` are absent on Pi OS Lite; use python/awk.
- Restart via `care.target`, never a single unit (`systemctl restart care-db` leaves
  api/worker failed); after `stop`, poll until units are `inactive` before `start`.
- `pgbackrest backup` needs `--repo=N`; run Care management commands via
  `podman exec care-api python manage.py …`.

## 8. Phase 1 state (2026-10-30): code complete, exit test passed on lumine (8d)

Written after waves 0–2 of Phase 1 (`06` §Phase 1, `09` §6b items 1–8). Four sub-agents
wrote the code in one day; one independent review found 9 issues; all 9 are fixed. Nothing is
committed yet (owner review first). Wave 3 removed the spike stack from lumine and ran the exit
test there (8d).

### 8a. What exists

| Area | Content | Verified by |
|---|---|---|
| `go.mod` (root), `agent/` | `care-pi` CLI: `bootstrap`, `apply`, `render`, `config validate`, `status`, `logs`, `care create-superuser`, `clock wait`, `host check`, `fe build\|status\|prune`, `version`. Packages: `config` (+ JSON Schema), `solution`, `secrets`, `lock`, `netinfo`, `render` (71 golden files), `plan`, `apply`, `applock`, `host` (exec + fake), `s3admin`, `status`, `hostcheck`, `fe` (`artifact`, `local`). | `go test -race` 17 packages; golangci-lint 0 issues; `make build-linux` arm64 + amd64 |
| `quadlets/templates/` | `care.pod`, 7 `care-*.container`, `care-fe-build.container`, `care.target`, `care-start.service`, `care-clock.service`, `Caddyfile`, `backend.env`, `frontend.env`, `fe-build.sh`, `pgbackrest.conf`, 3 status pages | quadlet `-dryrun` on podman 5.4.2 and 5.8.9, 0 warnings; `caddy validate` + live route test in Docker |
| `images/` | `postgres-pgbackrest` (postgres 17.11 + pgBackRest 2.60.0), `caddy` (2.11 + `acmedns` + `caddy-tailscale`), `care` (solution image; `ADDITIONAL_PLUGS` build arg) | `docker build` arm64 + amd64; module list; zero-plug and one-plug builds |
| `solutions/` | `care-clinic/solution.yaml` (4 plugs per `03` §1.1; only `care_onboarding_fe` on), `schema/solution.schema.json`, `embed.go` | ajv + check-jsonschema; 15 negative cases |
| `installer/` | `install.sh` (POSIX sh, 14 steps since the follow-ups of 8d, idempotent, `--dry-run`), `uninstall.sh` | shellcheck; dry run and double run on `debian:trixie`; fixtures for cmdline, nftables, avahi, download |
| `.github/workflows/` | `ci` (vet, test, lint, cross-build), `images`, `solution-image`, `fe-bundle` | actionlint |
| `docs/` | `config.md`, `apply.md`, `fe.md`, `install.md` | — |

Real FE build in Docker (`node:22-bookworm-slim`, Mac): cold 55 s, warm 13 s, 12 MB output,
0 references to the upstream API host.

### 8b. Decisions the implementation took (owner informed; override in `00` if wrong)

- Caddy admin API on a unix socket inside the container, not `admin off`: `apply` needs
  `caddy reload`; a TCP admin port would be reachable from every container in the pod.
- Secrets reach containers as podman secrets (`Secret=…,type=env`). Rendered files, the
  lock, `--dry-run` output and status never contain a secret. `render` works unprivileged.
- Status pages switch by flag files in `/var/lib/care-pi/state/mode/`; Caddy needs no reload.
- `care-start.service` (oneshot, `systemctl start --no-block care.target`) is what
  `multi-user.target` wants, so boot does not wait for NTP or for the stack to be healthy.
- `fe.provider` defaults to `local` (S8). `bootstrap --fe-provider` and the installer's
  `--fe-provider` override it. The agent and CI do **not** set `REACT_PUBLIC_URL` (care_fe
  uses it only for `og:url`), so a CI bundle's build id matches a default Pi.
- IP addresses do not count as hostnames for the `when_multihost` plug.
- The agent reads git refs over HTTPS (no `git` on the host). `skopeo` resolves image digests.
- One `flock` (`/run/care-pi/apply.lock`) serializes `apply`, `bootstrap`, `fe build`, `fe prune`.
- `care-db` has `StopTimeout=110` (podman's default 10 s caused crash recovery).
- Installer: requires podman ≥ 5.4 (trixie; Ubuntu 24.04 refused); adds `catatonit`
  (for `RunInit=true`); apt-holds the podman stack (`--no-apt-hold` to skip); nftables
  allow-list only (S42), loaded by `care-pi-firewall.service`, not `nftables.service` (S44);
  a persistent journal (S45); zram drop-in `/etc/rpi/swap.conf.d/50-care-pi.conf` is **untested on
  a Pi**.
- `DJANGO_SETTINGS_MODULE=config.settings.production`, `USE_SMS=False`,
  `DJANGO_SECURE_SSL_REDIRECT=False` (upstream health check is plain HTTP on localhost).
- Not implemented in this version, by design: `solution.track: pinned`, local solution
  image builds, `tls.public`, `tls.tailscale`, `clock.policy: rtc`, backup timers, restic.

### 8c. Next step: wave 3, the exit test on lumine (`06` Phase 1 exit criteria)

This wipes the spike stack. Before it: set the ghcr packages public after the first CI
publish (S43), or point `images.*` in `config.yaml` at locally built images.

1. Flash Pi OS Lite trixie arm64 to lumine's SSD. Boot with ethernet.
2. `make build-linux`; `scp bin/care-pi_linux_arm64 lumine:`.
3. `sudo sh install.sh --binary ./care-pi_linux_arm64 --hostname care --preset small-clinic --yes`
   (the repo is private, so the download path 404s; use `--binary`). Expect a reboot request
   for the memory cgroup, then the printed `bootstrap` command.
4. After `bootstrap`: `care-pi status`; `https://care.local` (or the printed IP) serves the setup
   page, then Care once the FE build ends (cold ≈ 3 min + node image pull). Watch
   `systemd-cgtop -m` and `MemAvailable` ≥ 370 MiB during the build.
5. `sudo care-pi care create-superuser --username admin --email a@b.c`; log in; upload a file.
6. `sudo reboot`; everything returns without intervention; `care-start.service` must not delay boot.
7. Edit `size.preset` → `solo`; `sudo care-pi apply --dry-run`, then `apply`: only api/worker
   units restart.
8. Record results in `08-spike-log.md` as "Phase 1 exit test"; update `local/LUMINE.md`.

Things the review and the agents flagged to check on real hardware: `podman exec` sees the
`type=env` secrets; `LogDriver=passthrough` on the FE builder gives single journal lines;
the rpi-swap drop-in; the avahi journal line that reports the advertised name; Ctrl-C during
`fe build --force` restores api/worker.

### 8d. Exit test result (wave 3, 2026-10-30)

**PASS, 5 of 5 exit criteria**, on lumine after an in-place clean of the spike stack (not a fresh
flash). Detail, timings, and memory: `08-spike-log.md`, "Phase 1 exit test". Raw output:
`local/wave3/` (S39).

| Criterion | Result |
|---|---|
| Install → `https://lumine.local` serves Care | PASS (`--binary`, host name `lumine`, local base images) |
| Superuser from `care-pi care create-superuser`, login | PASS |
| File upload (presigned PUT and GET through Caddy) | PASS |
| Reboot → all units active without intervention | PASS (118 s; `multi-user.target` at 8.3 s) |
| Preset edit → minimal restart | PASS (only `care-api`, 22 s) |

The review items of 8c: `type=env` secrets work in `podman exec`; `LogDriver=passthrough` gives 1
journal line; avahi reports `Host name is lumine.local.`; SIGINT during `fe build --force`
restores care-api and care-worker; the rpi-swap drop-in was wrong and is fixed (below).

Fixed in wave 3 (uncommitted):

1. `agent/internal/host/clients.go` + test: `reset-failed` on units that systemd has not loaded
   stopped the first apply.
2. `installer/install.sh`: `MaxSizeMiB=` (empty) does not remove the 2048 MiB zram limit; now 65536.
3. `quadlets/templates/fe-build.sh.tmpl` + goldens + `docs/fe.md`: `npm install` rewrote
   `package-lock.json`, so warm builds became cold builds.
4. `docs/fe.md`, `docs/apply.md`: API restarts during a local FE build; no job journal for a
   no-change apply.

Follow-ups for the orchestrator (detail in `08`, findings 4 to 15). The owner accepted all of them.
**Done 2026-10-30** and verified on lumine (`08`, "Follow-ups applied"); still uncommitted:

- ~~Decision `01` §8: care-beat cap (382 MiB of 384M plus 85 MiB swap during `migrate`).~~ Done: 512M / 128M for each preset.
- ~~`SuccessExitStatus=143` for care-api, care-worker, care-beat (units go to `failed` at each stop).~~ Done.
- ~~Plan order: `ensure-stanza` before `ensure-fe` (22 failed archive-push runs on the first apply).~~ Done, with an explicit `pg_isready` wait.
- ~~Decision S42: do not enable `nftables.service` (its stop flushes the container rules; an
  nftables upgrade restarts it).~~ Done: S44, `care-pi-firewall.service`; the installer migrates an older install.
- ~~Contract: move `network-online.target` from `care-start.service` to `care.pod`.~~ Done: `multi-user.target` at 4.4 s (was 8.3 s).
- SP8: AAAA records for `.local` but no IPv6 publish. avahi IPv4 only (change C) was applied and then
  **reverted** by the owner: without the AAAA record, each macOS lookup waited 2.7–5.0 s, because avahi sends no
  negative AAAA answer (avahi issue 428). With the AAAA record, the Pi resets the IPv6 SYN and the fallback is fast
  (0.03–0.12 s). **Follow-up (SP8):** publish 80/443 on the LAN IPv6 addresses too.
- Still open: SP9, the clock gate is 26 s of the boot.
- ~~Phase 5: persistent journal.~~ Done in the installer (S45): `Storage=persistent`, `SystemMaxUse=200M`.
- ~~`bootstrap` prints nothing during the lock resolution.~~ Done: one `resolve … <id12>` line each.
- Still open: podman's transient health check units fail during the start period and stay in `systemctl --failed`.

The later fresh-flash test must also cover what this run did not: `curl | sh` and the release
download; the ghcr images and their digests; the image pulls (≈ 1 GB); the `REBOOT REQUIRED`
path for the memory cgroup; the default host name `care` and a name collision; a 4 GB or 8 GB Pi
for the zram drop-in; an SD card instead of the USB SSD; a first boot without NTP sync.

### 8e. Phase 2 wave 4a: the agent daemon and the console API server (2026-10-30)

Wave 4a adds `care-pi serve` (`care-pi.service`): the console API of `docs/api/openapi.yaml` (v0.3.0) and the
console SPA on the unix socket `/run/care-pi/api.sock`, behind Caddy on port 8443 (S48). One agent wrote most
of the code and stopped before the end. A second agent made the gap list, completed the code, the tests, and the
docs, and ran the checks. Nothing is committed (owner review first). The console SPA is a separate wave (4b).
`docs/console-server.md` is the developer guide.

| Area | Content |
|---|---|
| `agent/internal/server` | Routes of each operation, the middleware (session, CSRF token, `Origin` check, rate limits, 1 MiB body, content type), SSE (jobs and logs), the SPA, the unix socket, the loopback dev listener, sd_notify (`READY=1`, `STOPPING=1`, no cgo), graceful stop. `contracttest`: kin-openapi `openapi3filter` checks of each test exchange. |
| `agent/internal/service` | The shared layer of the HTTP handlers and the CLI (CLI parity): status, setup, config (ETag, `If-Match`, merge patch, plan preview, `apply=true`), apply, jobs, logs, support bundle (redacted `.tar.gz`), network, TLS (root CA DER/PEM/mobileconfig, install steps for 6 OSs), FE, Care, power. |
| `agent/internal/auth` | argon2id (19 MiB, t 2, p 1, one hash at a time), `secrets/agent.json`, claim code `secrets/claim_code`, the Imager password behind `auth.OSPassword` (needs SP10), 8 recovery codes, TOTP (RFC 6238), sessions (`state/sessions.json`, 2 h idle, 24 h absolute, 10 at most), rate limits and locks of README section 4. |
| `agent/internal/setup`, `agent/internal/jobs`, `agent/internal/problem` | The setup state machine and record (`state/setup.json`, `apply_phase`); the job journals (Phase 1 format plus members) with `<id>.events.jsonl`, cancel files, retention; the RFC 9457 problem types. |
| `agent/cmd/care-pi` | `serve`, `auth …`, `setup status\|skip\|finish`, `config show\|set\|presets`, `jobs …`, `support-bundle`, `tls show\|root-ca`, `care info`, `power …`, `apply --phase`, `bootstrap --unattended`, `logs agent --since --json`, `fe status --json`, `host check --probe`. |
| Setup mode | `bootstrap` on a new host: `apply --phase=bootstrap` (only `care.pod` and `care-caddy`), a claim code, the box with the console URLs. A Phase 1 host (a `lock.yaml`) gets the full apply and is `ready` after the claim. The finish job: validate, host check, host name, full apply, wait for care-api, Care superuser. Golden tree `small-clinic-bootstrap`. |
| Caddy and units | Port 8443 site (`tls internal`, security headers, no HSTS, `reverse_proxy unix//run/care-pi/api.sock`, `flush_interval -1`); port 443 `/_care-pi/*` to the socket, `/console*` → `308`; `503` problem when the agent does not answer. `care.pod` publishes 8443. `care-caddy` mounts `/run/care-pi`, `Wants=`/`After=care-pi.service`. |
| Installer | Step 13 writes, enables, and starts `care-pi.service` (`Type=notify`, `ProtectSystem=strict` + `ReadWritePaths`, `MemoryMax=64M`, `MemorySwapMax=32M`, `Restart=always`). The firewall accepts 8443/tcp. Setup mode is the default; `--unattended` gives the Phase 1 full apply. `uninstall.sh` removes the unit. |
| Docs | New `docs/console-server.md`; updates of `docs/apply.md`, `docs/install.md`, `docs/README.md`, `installer/README.md`, `agent/README.md`, `quadlets/README.md`. |

Decisions of wave 4a (owner informed; override in `00` if wrong):

- The installer owns `care-pi.service`, not `apply`: an apply must not rewrite and restart the unit of its own process.
  `ProtectControlGroups=` and `SystemCallFilter=` wait for Phase 7 (`podman exec` needs them off). `After=network.target`,
  not `network-online.target`, so the boot does not wait. `podman pull` runs in a transient unit (`systemd-run`),
  because a pull needs more memory than the 64M cap.
- The installer flag `--unattended` also selects the full apply (no console). There is no separate flag.
- The job journals of the CLI and of the agent are the same files. A CLI `apply` stays in its own process, behind the apply lock.
- An unsafe request without a body needs no `Content-Type` (README section 3 asks for it on each unsafe request).
- The served hosts of the `Origin` check also include the site names of the rendered Caddyfile, so the console stays
  available on the old name after the identity step and before the finish.
- The finish job sets the OS host name when `identity.hostname` changed: `hostnamectl set-hostname`, the `127.0.1.1`
  line of `/etc/hosts` (so `ReadWritePaths` has `-/etc/hosts`), and `systemctl try-restart avahi-daemon.service`.
  Its step action is `set-hostname`.
- `Makefile`: `VERSION` uses only tags `v[0-9]*`. The `fe-*` tags of the fe-bundle workflow gave versions such as
  `fe-care-clinic-…-dirty`. A dev build is each version that is not `vX.Y.Z`.
- `.gitignore` keeps `console/dist/.gitkeep`: without it a fresh clone cannot compile `console/embed.go`.
- The dev listener (`serve --insecure-dev --listen tcp://127.0.0.1:8443 [--fake-host] [--allow-origin O]`) uses the
  cookie `care_pi_session` without `Secure` and accepts the origin `http://<Host>`. It refuses a non-loopback address.

Spec changes that the contract needs (not made; `docs/api/**` is unchanged):

1. `x-problems` of `getConfig`, `getHostCheck`, and `getApplyPlan`: add `invalid-request` (a query parameter that
   fails its schema; `contracttest.KnownGaps`).
2. README section 3: "An unsafe request with a body needs `Content-Type: application/json`" (not each unsafe request).
3. `JobStep.action`: add the key `set-hostname` to the list of the description.

Verification on the Mac (2026-10-30): `go build`, `go vet`, `go test -race` (29 packages), golangci-lint v2.15.0
0 issues, `shellcheck -S style installer/*.sh`, `make build-linux` (arm64 + amd64), `make api-lint`. The contract tests
check each of the 73 operations (55 of Phase 2) at least once; `go test -v ./agent/internal/server` prints the statuses.
quadlet `-dryrun` (podman 5.4) on `small-clinic` (8 units) and `small-clinic-bootstrap` (2 units): 0 warnings.
`caddy validate` (2.11.7) on both Caddyfiles. Live Caddy container: `:443/console/` → `308`, `:8443/` and
`:8443/api/v1/health` → the socket, `:443/_care-pi/status` → the socket, a client `X-Forwarded-For` is replaced,
SSE passes at once, no HSTS, `503` problem when the agent stops; the real `care-pi_linux_arm64` behind Caddy:
claim, `__Host-` cookie, CSRF, `Origin` of port 443 → `403`. `systemd-analyze verify` (systemd 257) on `care-pi.service`.
The dev listener and the curl walkthrough of `docs/console-server.md` (claim → session → status → setup → SSE → apply job).

Not verified yet (needs lumine or a Pi): the sandbox of `care-pi.service` with podman and `podman exec`;
`systemd-run` for `podman pull`; the host name change with avahi; the Imager password claim (SP10); the trust of the
root CA on port 8443 in each browser (SP7); a reboot with the agent; the memory of the agent during a setup job.

Deploy on lumine (a Phase 1 host; do not reflash):

1. `make build-linux`; `scp bin/care-pi_linux_arm64 installer/install.sh lumine:`.
2. `sudo sh install.sh --binary ./care-pi_linux_arm64 --hostname lumine --yes`. The installer writes and starts
   `care-pi.service` and allows 8443/tcp. bootstrap finds `lock.yaml`, so it runs the full apply and prints the claim
   code. The pod publishes a new port, so the apply makes one full cycle of `care.target` (about 2 minutes without Care).
3. Check: `systemctl status care-pi.service`, `sudo care-pi logs agent -n 20`, `curl -sk https://lumine.local/_care-pi/status`,
   `curl -sk https://lumine.local:8443/api/v1/health`, `curl -skI https://lumine.local/console/` (`308`).
4. Claim: read the code with `sudo care-pi auth claim-code`. Open `https://lumine.local:8443/`, or send
   `POST /api/v1/session/claim` with curl (`docs/console-server.md`, section 9, with `A=https://lumine.local:8443`,
   `curl -k`, and `CODE` set by hand). The state is `ready` at once.
5. To test setup mode on lumine, a new host is necessary: `sudo sh installer/uninstall.sh --purge-data --yes`, then step 2.
   bootstrap then starts setup mode. Complete the setup in the console; the finish runs the full apply (image pulls,
   FE build) and creates the Care superuser.

### 8f. Phase 2 exit test result (wave 4c, 2026-10-30)

**PASS** on lumine: a laptop (Chromium 1280 × 800) and a phone (WebKit, iPhone 13) each claimed a new install
and completed the setup without a terminal (Finish → ready 418 s and 404 s), the phone also completed the
journey of a ready device (login, dashboard, certificate, logs, TOTP), and the CLI completed the setup
unattended (401 s). The owner
approved a purge of the Phase 1 data first. Detail, timings, and memory: `08-spike-log.md`, "Phase 2 exit
test". Raw output: `local/wave4c/` (S39). The journeys are now committed specs: `console/e2e/real`
(`npm run test:e2e:real`, `console/e2e/real/README.md`). Limitation: the Mac was on another subnet,
so the browsers used an ssh tunnel; mDNS, the TLS interstitial, and SP7 were not tested.

| Criterion | Result |
|---|---|
| Purge, install in setup mode, public endpoints | PASS (install 6 s; claim code box with both console URLs) |
| Laptop: claim → wizard → finish → hand-off → dashboard | PASS in the final run, Finish → ready 418 s. Run 1 failed at `ensure-stanza` (fix 2 below) |
| Phone: claim → wizard → finish → hand-off → dashboard | PASS, Finish → ready 404 s (final install) |
| Phone: login, dashboard, certificate, logs, TOTP, logout, TOTP login | PASS; the units table scrolls inside its card (follow-up) |
| CLI parity; unattended CLI setup from a new install | PASS; `setup finish` 401 s after a doc fix |
| Reboot | PASS; `care-pi.service` not on the critical chain; session kept |
| Negative checks (413, `Sec-Fetch-Site`, 404 job, 412/428, `reauth-required`, lockout, `serve` guards) | PASS |

Fixes (signed commits, not pushed): `c95f2dc` uninstall wait and podman secrets; `0ab0558` `podman exec` of the
agent in the host mount namespace (the setup job failed without it: "unable to find user postgres"); `7f0fbaf`
expected time of the first start; `5c040b1` the hand-off card kept the job result; `f319727` 10 skeleton rows for
the units; `cf1f9d2` `console/e2e/real`; `64caa26` Go soft memory limit (the agent was OOM-killed under a login
and status load); `2022c9f` the CLI setup example needs a `tls.*` key; `21687c7` the job time counted from 00:00; `0d1ef45` the setup
journey on a phone (`phone.setup.spec.ts`).

Measured: `care-pi.service` idle 7.5–12 MiB; during the first setup job before `64caa26` it reached its 64M cap
89 times and 31 of 32 MiB swap, after it 46 MiB and no swap. FE build 771 MiB + up to 768 MiB zram, host
`MemAvailable` minimum 445 MiB. Care `/ping/` median 13–14 ms during the job; console health median 8 ms.

Follow-ups applied in wave 4d (2026-10-31; detail in `08`, "Follow-ups applied"):

- **A. Agent memory cap 128M** (follow-up 1). `care-pi.service` has `MemoryMax=128M`, `MemorySwapMax=32M`; the Go
  soft limit is 64 MiB. The load of wave 4c plus skopeo: 64M reached its cap in each run (16–29 events, swap
  12–14 MiB); 128M reached it in 1 of 3 runs, swap 0, no OOM kill. 24 parallel `GET /status` OOM-kill the agent
  with both caps (new follow-up 10).
- **B. No full cycle after a boot** (follow-up 2). `apply` and `bootstrap` wait up to 60 s for a LAN IPv4 address
  before the render (`--address-wait`); the NetworkManager hook waits 15 s for more events in the first 3 minutes
  after the boot, else 5 s. Finding: `network-online.target` does not guarantee an IPv4 address on Pi OS trixie
  (IPv6 can activate eth0 first). Two reboots of lumine: one apply at 22 s with the address, no file change,
  0 full cycles (before: 1); the pod started once at 11.8–11.9 s; Care `/ping/` 200 after 113–114 s (before:
  179 s); `multi-user.target` 4.4–4.5 s.
- **C. Console UX** (follow-ups 3 to 6). Operator text for each job step (26 keys, `docs/console-ui.md` section 9);
  the agent shows the lock resolution as the step `resolve-lock` (23 s on lumine) with an indeterminate bar; a failed
  setup shows the step, the problem, Retry, Copy details, and the log of the unit; the hardware step shows the
  failures first and one row for the 43 passed checks (24 directories); one byte format (`119.2 GiB`); on a phone the
  labels are above the values and the units are cards (lumine: no side scroll, no cut text).

Still open after wave 4d (detail in `08`): follow-ups 7 to 11 (a failed finish does not write the lock; small items;
mDNS and SP7 from the same LAN; parallel `GET /status`; Caddy near its cap).

Follow-ups for the owner (detail in `08`): ~~the 64M cap still meets podman and skopeo children~~ (done, A); ~~the NetworkManager
hook applies at boot before the DHCP address, then a full cycle 90 s later (Care at 179 s after a reboot)~~ (done, B); ~~the
units table on a phone; raw step names and a 26 s silent lock resolution on the progress page; the raw error of a
failed setup; 43 checks (25 directories) on the hardware step~~ (done, C); small items (duplicate `X-Content-Type-Options`,
empty 502 on port 443 in setup mode, duplicate CLI lines). ~~Pre-existing, not changed: `npm run api:check` fails
since `efc6f60` (the contract changed, `src/api/types.gen.ts` was not made again).~~ Passes since `e499906`.
