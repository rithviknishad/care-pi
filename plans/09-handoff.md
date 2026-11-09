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
   pi-gen `arm64` after SP10 (`rpi-image-gen` has no Imager customisation), container images pre-pulled,
   published to Raspberry Pi Imager via a custom `os_list.json`), fully offline-capable at first boot (§8l).
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
| — | Boot of the Pi image (Phase 5, §8l) on a Pi 5 and a Pi 4: Imager host name → avahi, Imager-password claim (yescrypt), offline setup, then an update with internet. SP10 itself is done (`08` SP10: pi-gen) | Phase 5 exit | Claim code; `docs/image.md` §8 |
| SP11 | NetworkManager eth+wlan failover (gist-style bond vs secondary connection) | Phase 6 | Secondary connection, no bond |
| — | Pi 4 (2 GB/4 GB): everything above, especially FE build time/margins and throughput | O11 | `artifact` FE provider on Pi 4 |
| — | Browser-driven file upload through Care's UI (SP5 used curl against the presigned URLs) | Confirms CORS/Content-Type path end to end | — |

## 5. Open questions and when they block

From `00` §C (resolved ones struck there): ~~**O4** Tailscale SaaS vs Headscale~~ (S59: both, SaaS default);
**O6** Clinic plug set confirmation (`03` §1.1 — Phase 4 CI matrix); **O7** timing of the
upstream care_fe PR for relative `meta.url` (ask before opening; stopgap = CORS from Caddy;
only when multihost is on); **O9** password + recovery codes vs SSH reset (Phase 2); **O11**
Pi 4 in v1 acceptance; ~~**O12** who hosts the telemetry side~~ (S56: `remote_write` only); **O13** Renovate for
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

### 8g. Phase 3 wave 5a: backups and restore, server side, and the exit test (2026-10-31)

Wave 5a implements contract v0.4.0 (`docs/api/README.md` section 13) in the agent and runs the Phase 3 exit test on
lumine. The console of the same contract is wave 5b (`5eb08f6`, a parallel agent). `docs/backups.md` is the guide
(the model, the key derivation and its test vectors, the destinations, the units, the restore flows, the DR procedure).

| Area | Content |
|---|---|
| `agent/internal/backup` | The recovery key (24 BIP-39 words, HKDF-SHA256 v1: key id, pgBackRest cipher pass, restic password, SSH ed25519 seed; QR and `.txt`), the key store (`recovery.key`, `.pending`, `state/backups/key.json`), the destinations (repository `care-pi-<key id>`), the restic session of each run (`/run/care-pi-backup/`, only paths in the environment), pgBackRest (`info`, the encrypted `backup.info`), the state files, the locks, the health and the banners, the destination tests (SSH TOFU, the SSH key, S3 with `create_bucket`, the posix probe), `ensure-repositories`. |
| `agent/internal/restore` | PITR (maintenance, then the checkpoint; rollback to the checkpoint on failure), files (in place or side directory), the drill (a throwaway container, `archive_mode=off`), the rotation and the re-key, the restore from backup of a new device. |
| `agent/internal/disks` | `lsblk` and the disk preparation (GPT, one ext4 partition `care-…`, mount and automount units). |
| Render, plan, apply | `care-backup.target`, the timers and services (the CLI runs them), the mount units, `ssh_config` and `known_hosts`; the plan actions `ensure-mounts`, `ensure-repositories`, `ensure-timers`; the restore apply (`NoStart`). Golden tree `backups`. |
| Service, server, CLI | Each operation of section 13; `SetupState.restore`; the step `backups`; `Status.backups`; `GET /logs/backup`; `care-pi backup …` and `care-pi restore …`. The dev host (`serve --fake-host`) answers the backup commands. |
| Installer | `uninstall.sh` stops and removes the backup units and the mount units; `--purge-data` uses `rm -rf --one-file-system`. `care-pi.service` gets no new path and no capability (comment only). |

Decisions of wave 5a (owner informed; override in `00` if wrong):

- **Privilege.** No `CAP_SYS_ADMIN` and no new `ReadWritePaths` for `care-pi.service`. restic, the probe of a destination,
  and `wipefs`/`sfdisk`/`mkfs.ext4` run in transient units (`systemd-run --wait --pipe`), as the image pulls of Phase 2;
  systemd mounts the disks from rendered mount units. The timers run `care-pi backup …` as root in their own units.
- **Timer runner and locks.** The CLI runs the timers, not the daemon. Backups never take the apply lock; each target has its
  own lock (`/run/care-pi/backup-<id>.lock`, `backup-local.lock`). The restore jobs take the apply lock and stop
  `care-backup.target` first. A destination timer writes no job, only its last 10 runs.
- **Repository layout.** The local repository stays `/var/lib/care-pi/backup/pg` (the contract: one repository). The re-key of a
  Phase 1 or Phase 2 host moves the old one to `pg.legacy` and deletes it after success. The destinations use
  `care-pi-<key id>`.
- **Retention.** `db` snapshots `--keep-within 48h` (owner decision), `files` snapshots `keep_within_days`; always `--keep-last 1`.
- **Test targets on lumine.** S3: a second versitygw quadlet on lumine (`w5-s3`), fewer moving parts than MinIO on the Mac.
  USB: a loop device (`losetup` exists on trixie). SFTP: the Mac over Tailscale.
- **The local CA of Caddy is in each snapshot.** A restored device keeps the root CA that the clinic devices trust. The
  switch happens after the event `done` (care-caddy restarts, a few seconds), so the open event stream keeps its certificate.
- **Console alignment (wave 5b notes).** `RestoreDrResult.display_name`; the sessions end after the event `done` reached each
  open stream of the job; the `.txt` download with `Accept: text/plain`; the label of `disk-prepare` from the request (a label
  of another connected disk → 409); a PITR time up to the start of the job; `GET /logs/backup` with the unit of each line.

Exit test: **PASS (9 of 9)**, 6 fixes before the commit (the calendar `daily HH:MM`, the drill `max_connections`, the drill
start, a USB disk that is not connected, the local CA of Caddy in the snapshots and its switch after a restore from
backup, the CLI that exited before the end of its job). Detail, timings, and memory: `08-spike-log.md`, "Phase 3 exit test".
Follow-ups there: the Phase 3 console on the device, a real USB disk, an internet S3 provider, DR on a second Pi, the
checkpoint of two restores in one day, the drill `max_connections` from the model.

### 8h. Phase 4 wave 6a: solutions, locks, plugs, updates — server side and CI (2026-10-31)

Code complete on the branch `phase-4-solutions`; not run on a Pi. The spec is `docs/solutions.md` (section 17 has the
status of each part); the contract is `docs/api/openapi.yaml` 0.5.0 and `docs/api/README.md` §14.

What exists:

- **CI.** `solution-lock.yml` (dispatch: resolve, the solution image with each backend plug, the default FE bundle
  through `fe-bundle.yml`, each FE plug bundle through `plug-bundle.yml`, the lock, the release `lock-<s>-<v>`, the job
  summary), `solution-index.yml` (`index.json` from each lock release and `withdrawn.yaml` into the release `index`),
  `plug-bundle.yml`. No pull request and no push: the owner commits each lock to `solutions/<s>/locks/`.
  `solutions/scripts/make-lock.sh` (`make lock-local`) makes the same lock on a dev machine. Not run on GitHub yet.
- **Agent.** Lock version 2 with the migration from version 1 (`internal/lock`); solution locks, `index.json`, schemas,
  settings schemas (`internal/solution`); the window (`internal/window`); the update machine (`internal/update`):
  index client, tracks `stable`/`pinned`/`custom`, jobs `update-check`/`-stage`/`-apply`/`-rollback` with the cutover,
  automatic rollback, PITR offer, recovery after an interruption, OS updates, plug changes, image retention;
  `plug_config` rows through `manage.py shell` (`internal/plugcfg`); plan actions `cutover` and `plug-config`; the
  podman secret `care-additional-plugs`; the timers `care-update-{check,apply}`; the local backend build
  (`care-build-<solution>.build`, 768M); the local FE plug build (experimental). Each §14 route; CLI `update`,
  `plugs`, `solution`.
- **Tests.** Fake host end-to-end of N → N+1 → rollback, migration failure → automatic rollback with the offer,
  checkpoint failure, withdrawn lock, `agent-too-old`, bad hash, HTTPS index, OS update commands, plug plans and the
  restart path, the window run, recovery; DST table tests; lock migration; contract tests of each §14 operation;
  goldens (`custom-build` is new); quadlet `-dryrun` (podman 5.4.2) of the `.build` and the plug builder.

Decisions (owner informed; details in `docs/solutions.md` section 17):

- The checkpoint is the interface `update.Checkpointer` with the stub `update.PGBackRestStub` (incremental backup,
  restore point, `pgbackrest check`, stanza `main`). At the merge with Phase 3, set `Updater.NewCheckpointer` to the
  backup package in `service.Updater()`.
- apt runs in the transient unit `care-pi-apt`; the sandbox of `care-pi.service` does not change.
- The `.build` quadlet uses `PodmanArgs=--build-arg=…` (podman 5.4 has no `BuildArg=`).
- The default index URL is the release asset `index/index.json` of this repository: HTTP 404 while it is private
  (open point 1 of `docs/solutions.md`). Tests use `updates.index_url: file:///…`.
- `prune-images` keeps the images of the current lock and of `history.previous`.
- Migrate limit 20 min. An interrupted cutover after `activate` rolls back (no resume after `migrate`).

Merge notes for Phase 3: the shared files got only additions (`paths`, `config`, `render.Model`, `plan` kinds, server
routes, `jobs` kinds, `problem` slugs). `service.applyOptions` sets `apply.Options.Checkpoint`. The problem members
`pitr_offer`, `next_window_at`, and `min_agent_version` are in `internal/problem`.

Next step: the Phase 4 exit test on lumine after the merge: `08-spike-log.md`, "Phase 4 exit test — plan".
It needs a superset solution image (dispatch `solution-image.yml` on the merge branch) and the test image
`images/care-test/Containerfile.broken-migrate` (pushed to a test tag).

### 8i. Merge of Phases 3 and 4 (2026-11-01)

`main` (Phase 3, `3cc2277`) and `phase-4-solutions` came from the same base (`2e706b3`); the merge kept both sides of each
shared list, table, switch, and section (Phase 3 first). The semantic changes beyond the conflicts:

- **The checkpoint.** `update.BackupCheckpointer` is the checkpoint of Phase 3 (`docs/api/README.md` section 13.11):
  `backup.Ops.LocalBackup(incr)` under the local backup lock, recorded in `RestorePoints.checkpoints` with the job and its
  kind (`update-apply`, `update-rollback`, `plug-change`, or `apply` for the plan action `cutover`). `service.Updater` sets
  `update.NewCheckpointer` to it; `apply.Options.Checkpoint` (now with the job id) is `service.Checkpoint`, also for
  `care-pi apply`. `PGBackRestStub` is gone; tests keep their fake checkpointers. The PITR target of an update is just
  after the end of that backup (at least 1 s after its `undo_point`); `pitr_offer.restore` is a `RestorePitrRequest`
  (`{kind: pitr, time}`), and `restore_point` is `null` (no named restore point).
- **The restore link.** The PITR offer card links to the Restore page of Phase 3 (`/restore?time=…`); the page opens
  "Undo a mistake" with that time and a note (`docs/console-ui.md` sections 10.4 and 11.4). The fallback page of Phase 4
  (`src/features/restore/`) and the `501` probe of the console are gone.
- **The contract is v0.6.0.** `JobKind` has the kinds of both phases (the reserved `backup`, `verify`, `restore`, `update`,
  and `rollback` are gone). The update checkpoint schema is `UpdateCheckpoint` (Phase 3 owns `Checkpoint`). The problem
  `verify-failed` has one title for both phases ("The verify failed"). `docs/console-ui.md` section 10 is Phase 3; the
  Phase 4 section is 11. The step `checkpoint` reads "Make a backup point of the database"; in `restore-pitr` it keeps
  "Record an undo point" (`strings.steps.byKind`, which now also wins over the text while a step runs).
- Goldens: each `maintenance.html` has the Phase 3 step sentences (`render.MaintenanceSteps`); `backups` has the Phase 4
  files (lock v2, `care-additional-plugs`, the update timers); `custom-build` has the Phase 3 backup units.

Next step: unchanged — the Phase 4 exit test on lumine (`08-spike-log.md`, "Phase 4 exit test — plan"), now on the merged binary.

### 8j. Phase 4 exit test result (wave 6b, 2026-11-01)

**PASS (3 of 3) on lumine, with 8 fixes** (`a85395e` … `137057a`, one signed commit each, with tests). The record is
`08-spike-log.md`, "Phase 4 exit test — lumine, 2026-11-01". The merged agent (Phases 3 and 4, F1 to F9) runs on lumine.

- **Criterion 1.** The window timer updated 2026.11.0 → 2026.11.1 at 10:05:32 (window 09:57–10:57). The maintenance page
  was on for 65.2 s and showed the live step text (checkpoint, migrate, start, verify); `/_care-pi/status` had
  `maintenance{}`. Both FE plugs load from the device (`/plugs/<slug>/<sha12>/`). No stale `activating` marker (F3).
- **Criterion 2.** The broken image failed in `migrate`; the agent rolled back to 2026.11.1 and gave the PITR offer.
  The test took the offer through `POST /restore` (68 s job): the data before the checkpoint stays, the data after the
  rollback goes, Care is healthy. The manual rollback, `systemctl stop care-update-apply.service` in `migrate` (F6),
  and the return to 2026.11.1 work.
- **Criterion 3.** `PUT /plugs/{id}`: an FE plug off and on in 4.2 s (the `PlugConfig` row goes and comes back; the
  browser loads the plug only when it is on); a backend plug on (`cutover`, 63 s) and off (`restart`, 60 s). A
  plug-change cutover stopped with SIGTERM writes `config.yaml` back and renders the plug set of the previous lock (F4).
- **Phase 3 after the merge.** Run and verify of the 4 destinations pass; 14 timers active; the console journeys of
  `backups.spec.ts` pass. Reboot: Care answers after 113 s; `multi-user.target` 9.2 s.

The fixes: `make-lock.sh --plug-bundle` (built FE plug bundles in a dev lock); `restic unlock` of stale locks before
forget, prune, and check, also in the timer run (an SFTP run that lost its connection left a lock, and `mac` stayed
`degraded`); the path of an update target is `cutover` (the console said "Care keeps running"); `config set` removes
the quotes of a quoted env value; a `--lock-file` lock is not `latest`/`available` when an index exists; two spec
fixes of `e2e/real` (the login of `backups.spec.ts`, the optional dialogs of `updates.spec.ts`).

Images: the ghcr packages are private and the `gh` token has no `read:packages`, so the test built the solution image
(same inputs as run 41271422028) and the broken image on the Mac and loaded them into podman on lumine under their
ghcr names; the locks name them by the digest that podman recorded. The dist is `file:///var/lib/care-pi-dist/`.

Open after Phase 4 (the owner decides; `08` has the list): public images (or a pull credential) and public release
assets (`docs/solutions.md` open point 1); a maintenance page for the path `restart` (56 s of API break); the staging
of a failed `--lock-file` update; `care-api` at 487 of 512 MiB; `agent-too-old` with a tagged build; `update --os --now`
and F5; F1 on a device. Next phase: Phase 5 (the image).

### 8k. Deferred to the final acceptance: done and remaining

The owner kept these for the end. The final acceptance on lumine (wave 10, §8r) did them, except the items for new
hardware.

Done (wave 10, `08-spike-log.md` "Final acceptance — lumine, 2026-11-02"):

- `care-pi update --os --now` with a real `apt-get full-upgrade` (14 packages, the holds kept, 34.8 s), and the reboot
  path (`--reboot --now`; fix 2). The upgrade of `tailscale` broke the Tailscale name until fix 3.
- `agent-too-old` with the release `v0.1.0`: the check and the job; `GET /updates` shows the blocker after fix 1.
- S62: the restart of `care-api` runs behind the page (22 s of page at the first apply of `v0.1.0`).
- S63: the `care-api` peak is 471–473 MiB of 576M.
- S61: on the ts.net console no address lock after wrong passwords (`address=tailscale`); the LAN keeps its lock; a
  spoofed listener header stays the LAN address.
- The test leftovers on lumine: the destinations `s3` and `local`, `w5-s3`, `/var/lib/care-pi-test/s3`,
  `/srv/care-backups` (fix 9 for the timers). Kept on purpose: `/var/lib/care-pi-test/loop.img` (the disk of the
  destination `usb`), and on the Mac `~/care-pi-sftp-backups/care-pi-b7616345` with its restricted `authorized_keys`
  line (`b7616345` is the live key id of the destination `mac`).

Remaining:

- Owner decisions: the `care-caddy` cap (measured 100–116 MiB of 128M with 3 names and the WAF off; 116.5 MiB of 256M
  with the WAF; 160M is the candidate); the `care-api` cap of `custom` with 4 or more workers (not measured); the
  findings of `08` (the policy `drop` closes the other services of the host, the page for a full cycle,
  `os.held_packages`, `52care-pi.conf`).
- New hardware (owner): the Phase 1 fresh-flash test (`curl | sh`, ghcr pulls, the reboot-required cgroup path, host name
  `care` and a collision), the image flash (Phase 5), Wi-Fi, and DR to a second Pi.
- A tag `v0.1.1` with fixes 1–9, so lumine can run a release build again.
- Wizard O1 (wave 13, `8u`): the disabled invitation checkbox of the staff form looks enabled; dim the row or hide the
  option when `invite_available` is false. Next lock after 2026.11.4.
- The seeded first boot (S72) on real hardware: flash the v0.3.0 image and watch the first start restore
  `pg-<sha12>.tar.zst` ("No migrations to apply"); lumine's RTC battery (`clock detect --dry-run`).

The ghcr packages are public since 2026-11-01 (S43 done). lumine pulls `care`, `caddy`, `postgres-pgbackrest`
without a token. The recovery key words of lumine are in `local/care-recovery-key` on the owner's Mac (S39).

### 8l. Phase 5 wave 7: the Raspberry Pi image (2026-11-01)

Branch `phase-5-image` (from `main` at `cbd34b0`). Not on lumine; not on a Pi yet. Records: `08-spike-log.md` "SP10" and
"Phase 5 build"; the guide is `docs/image.md`.

What exists:

- **SP10.** `rpi-image-gen` builds in Docker and runs chroot hooks, but its image has no Imager customisation (no
  cloud-init seed, no `rpi-cloud-init-mods`) and its `rpi5` layer is Pi-5-only (ext4 with 16 KB blocks). → pi-gen
  `arm64`, pinned in `image/pi-gen.ref`.
- **`image/`.** `build.sh` (host) → `builder/run.sh` (privileged linux/arm64 container, Docker volume
  `care-pi-image-work`) → pi-gen stages 0–2 (Raspberry Pi OS Lite) + `stage-care`: `00-install` (`installer/install.sh
  --image-build` in the chroot), `01-prepull` (skopeo into overlay `containers-storage`), `02-preseed` (`builder/preseed.py`:
  the dist snapshot `/var/lib/care-pi-dist`, the FE build, the FE plug bundles, `/etc/care-pi/image-release.json`),
  `03-firstboot` (`care-firstboot.service` + script, the ACT LED units). Outputs in `image/out/` (ignored): `.img.xz`,
  `.sha256`, `os_list.json`, `.rpi-imager-manifest`, logs, `verify.txt`. `scripts/verify.sh` checks the file tree;
  `scripts/boot-test.sh` boots the root file system with systemd-nspawn (no network); `scripts/fetch-dist.sh` gets a lock
  and its bundles from the releases.
- **Installer.** `--image-build` (`CARE_PI_IMAGE_BUILD`): each file written and each unit enabled offline, nothing
  started; the skipped runtime actions are in `docs/install.md`, "The image build". Docker check: two runs, the second
  "already configured" for each step.
- **Agent.** `care-pi bootstrap --from-image` (pre-seed `/etc/care-pi/preseed.yaml` → `--unattended`; import of the dist
  snapshot into the update cache; claim code without the claim hint `claim-code`; the marker
  `/var/lib/care-pi/state/firstboot-done`). The index order: `updates.index_url` → the cache → the snapshot
  (`update.Client.ImportLocalDist`, `Updater.index`, the check job). Setup mode takes the Caddy digest from the solution lock
  of the track (no registry). In setup mode `care.target` does not require the clock gate. The Imager-password claim reads
  `user:` (Imager 2.x) and checks yescrypt (`github.com/openwall/yescrypt-go`, new dependency).
- **CI.** `.github/workflows/pi-image.yml` (`workflow_dispatch` {solution, lock_version, image_version, publish} and
  `release: published` for `lock-*`; `ubuntu-24.04-arm`; release `image-<version>`; `os_list.json` of the release `imager`).
  `ci.yml` runs shellcheck on `image/`. Not run on GitHub yet.

Decisions of wave 7 (owner informed; override in `00` if wrong):

- pi-gen `arm64` instead of `rpi-image-gen` (P18 fallback). The image keeps HTTPS apt sources (Docker Desktop proxies plain
  HTTP at 22 KB/s).
- `care-pi.service` is enabled in the image and starts at boot before `care-firstboot` ends, as after the installer.
  `care-firstboot.service` runs after `cloud-final.service` with `DefaultDependencies=no` (`cloud-final` is
  `After=multi-user.target`).
- The pre-seed is cloud-init `write_files` → `/etc/care-pi/preseed.yaml` (a `config.yaml`); it means `--unattended`. An
  invalid pre-seed stops the first boot. A partial pre-seed for setup mode is an open point.
- No `fe/current` in the image: setup mode must show the setup page. The apply at the end of the setup points it.
- The ACT LED blinks (`heartbeat`) from the end of the first boot until the claim (`care-pi-led.service`, `.path`,
  `-off.service`; the agent cannot write `/sys`).
- One image for Pi 4 and Pi 5; `os_list.json` `devices: [pi5-64bit, pi4-64bit]`, `init_format: cloudinit-rpi`. Image
  version = the lock version unless `image_version` is set; an `image-<version>` release never changes.
- `solution-lock.yml` does not start `pi-image.yml` (a `GITHUB_TOKEN` release starts no workflow): run it by hand.

Verification: Go tests, `go vet`, golangci-lint (0 issues); shellcheck of the installer and `image/`; actionlint; the
installer container checks (dry run, smoke twice, image build twice); the local image build (34 min on Docker Desktop, 17 of
them a slow apt download; 1.05 GB `.img.xz`) with all file-tree checks passed; `os_list.json` valid against the schema of
rpi-imager; the container boot (`image/scripts/boot-test.sh`, systemd-nspawn without a network): 18 of 18 checks, with
cloud-init, the first boot, setup mode from the pre-pulled images, and the claim with the Imager password (yescrypt)
(`08-spike-log.md`, "Phase 5 build"). It found two defects of the offline first boot, both fixed (setup mode waited for
the clock gate; setup mode resolved the Caddy tag at the registry).

**Blocking finding for the exit criterion "no internet":** the clock gate (`clock.policy: ntp`) waits for NTP before Care
starts. The claim and the wizard work offline; the install waits for the clock. An owner decision is needed
(`docs/image.md` §10 point 5): for example the time of the browser at the claim, an NTP server on the LAN, or `rtc` after SP9.

Next steps: (1) the owner decides the clock policy without internet; (2) run `solution-lock.yml` (a released lock and the
`index` release), then `pi-image.yml`; (3) the hardware checklist of `docs/image.md` §9 on a Pi 5 (USB and NVMe) and a
Pi 4 (2 GB and 4 GB), with the Phase 5 exit test: flash with Imager and the repository, no internet, the wizard to a working
Care, then internet and an update; (4) a QEMU boot test in CI.

### 8m. Phase 6 agent/console: connectivity (2026-11-01)

Code complete on the branch `phase-6-connectivity`; not run on a Pi. The spec is `docs/connectivity.md`; the contract is
`docs/api/openapi.yaml` 0.7.0 and `docs/api/README.md` §15. The DNS/ACME service (`services/dns/`, `docs/dns-service.md`)
is a separate part of the same branch; the agent client follows `services/dns/API.md` (the test vectors of its §3.6 pass).

What exists:

- **Local CA (S36).** `internal/ca`: the root of the agent (ECDSA P-256, 10 years, path length 1) with critical name
  constraints: `.local`, `.care-pi.rithviknishad.dev`, each other local name, and the `/24` of each LAN address. Not the
  Tailscale name or address (the local CA never signs them). The Caddyfile gives the root to the `local` CA (`pki`).
  Plan action `ensure-ca`: the one-time switch of an existing device (deletes the root, the intermediate, and the local
  leaves of Caddy; restarts care-caddy; `state/ca.json` records the old fingerprint and the time). `GET /tls` shows the
  constraints, `uncovered`, and the switch. Backups and DR keep the root (`ca.Adopt`). The update verifier trusts it.
- **Public name.** `internal/dnsclient` (signed Ed25519 requests, register, address, status, rotate, delete, retries, a
  fake service). Job `tls-public-enrol`: register, address, config, apply, wait for the certificate (5 min), apply again for
  the redirect of port 80. The site has `tls { dns acmedns /run/secrets/care-pi-acme-dns; resolvers 1.1.1.1 8.8.8.8 }`
  (the podman secret `care-pi-acme-dns`, `type=mount`), optional `ca` (`tls.public.acme_ca`, staging), and HSTS.
  `care-dns-update.timer` (each 5 min; resend after 6 h), and the plan action `update-dns` (the NetworkManager hook apply).
  Off: config, apply, delete the registration.
- **Tailscale (S37).** `internal/tailscale`. Job `tls-tailscale-enrol`: install from `pkgs.tailscale.com` in the transient
  unit `care-pi-tailscale-install` (only now; the installer never installs it), `up` with an auth key file or the login
  link (shown only in `GET /tls`), MagicDNS name into `tls.tailscale.hostname`, apply. The site uses
  `get_certificate tailscale` through `/run/tailscale`. The pod publishes on the Tailscale address and waits up to 30 s for
  it (`ExecStartPre=-care-pi network wait-address`). The firewall drop-in adds 41641/udp. Off: `down`, `logout`; the
  package stays.
- **Wi-Fi (S35, SP11).** `internal/wifi`: the secondary connection `care-pi-wifi` (keyfile through a transient unit; with
  the failover, metric 600 and priority -10); job `network-wifi` (radio, profile, connect, route check, config, apply);
  scan; remove. The bond is rejected. The pod publishes on each LAN address; a failover only removes an address, so the
  apply does not cycle the pod (`state/pod-live.json`).
- **Names.** The order public, local, Tailscale; the first is the canonical host (`CURRENT_DOMAIN`,
  `BUCKET_EXTERNAL_ENDPOINT`, `meta.url`). More than 1 name turns on `care_multihost_files_be` (a cutover). Port 80:
  `301` to the public name when its certificate is issued, else `308` to the same host. The solution default
  `ALLOWED_HOSTS` has `*.care-pi.rithviknishad.dev`.
- **CLI.** `care-pi tls public enable|disable|status|update-address|rotate`, `tls tailscale enable|disable|status`,
  `network wifi scan|set|remove|status`. `care-pi apply` sends a new address (update-dns). `CARE_PI_DNS_URL` changes the
  service for a test.
- **Console.** Settings > Connectivity (public name, Tailscale with the login link and QR code, Wi-Fi with the scan), the
  Certificate page (the name constraints, the one-time "the certificate changed" warning, the QR code of
  `https://care-pi.rithviknishad.dev/cert#<DER>` with the fingerprint QR code as the offline fallback), the dashboard names
  with their modes. Mock scenarios (`connectivity: fresh|migrated|public|tailscale`, failures), Vitest, Playwright
  `e2e/connectivity.spec.ts` (desktop, mobile, narrow), and `e2e/real/connectivity.spec.ts` (opt-in, not run).
- **Checks.** `go test -race ./...`, golangci-lint 0, `make api-lint`, shellcheck; `caddy validate` (the image of
  `images/caddy`) of each golden; quadlet `-dryrun` (podman 5.4.2) of `public` and `public-tailscale`; the console
  checks and the full Playwright suite. A live Caddy test in Docker: the chain leaf ← `care-pi - ECC Intermediate` ← our
  root verifies for `care.local` and an IP; HSTS only on the public and Tailscale sites; the port-80 redirects.

Decisions (owner informed; override in `00` if wrong):

- The IP constraint is the `/24` of each LAN address, not the `/32` (`07` §4.1 "generous"): a new DHCP address in the same
  network keeps the root. A move to another network puts the address in `local_ca.uncovered`; the fix is
  `POST /tls/local-ca/rotate` (Phase 7).
- The constraints do not have the Tailscale name or address (deviation from Q-c of `07`): the Tailscale site never uses
  the local CA, and the root then never changes when Tailscale turns on.
- acme-dns is TXT-direct (no CNAME), the same as `services/dns/API.md`. The client tries 3 times (5 s, 10 s,
  `Retry-After`); the timer tries again each 5 min (API.md asks for a 30 s to 15 min backoff for 5xx: the timer is the
  long backoff).
- Tailscale: `--accept-dns=false`, `--reset`; disable keeps the package. The login link is not in a job log.
- The firewall: a rendered drop-in of `care-pi-firewall.service`, not a change of `firewall.nft` (the installer owns it).
- Wi-Fi: secondary connection; the PSK is only in `secrets/wifi.json` and the keyfile of NetworkManager.
- `care-pi tls public rotate` has no API operation. `x-phase: 6` stays on the new operations (an older agent gives `501`).

Next step: the Phase 6 exit test on lumine after the merge: `08-spike-log.md`, "Phase 6 exit test — plan". It needs the
deployed DNS service, a Tailscale auth key (or a login by the owner), the Wi-Fi SSID and passphrase, and the
Let's Encrypt staging run first.

### 8n. S52: the clock without internet (2026-11-01)

Code complete on `main` (`f795ed6`); not run on a Pi. The review fixes of the merged Phases 5 and 6 came first (10 commits,
`08-spike-log.md`, "Review fixes after the merge of Phases 5 and 6"). The spec is `docs/api/README.md` section 16;
the contract is `docs/api/openapi.yaml` 0.7.1.

What exists:

- **Claim.** `POST /session/claim` has `client_time` (RFC 3339 with an offset; the console sends
  `new Date().toISOString()`). `auth.Manager.ClaimWith` calls `Service.ClockFromBrowser` after the claim secret is correct
  and before the account and the session, so a failed claim never changes the clock, and the session has the new clock.
- **The set.** `internal/clockgate.FromBrowser`: only when NTP is not synchronized, no record exists, and the time is
  plausible (after `buildinfo.Date`, before 10 years after it; a dev build uses 2026-10-01). `host.ExecClock.SetTime` runs
  `date -u -s @N` and `fake-hwclock save` in the transient unit `care-pi-clock-set`. The record is
  `state/clock-trusted.json` (`trusted: false`, `source: browser`, `set_at`, `host_time_before`). The agent reloads Caddy
  2 s later (no restart: the answer of the claim goes through Caddy).
- **The gate.** `care-pi clock wait` passes on NTP (and deletes the record), or with the record after 20 s more of NTP
  tries; it reloads Caddy in both cases (F9). The record stays over a reboot, so a reboot without internet passes with the
  clock of `fake-hwclock`.
- **Status.** `clock.source` (`ntp`, `browser`, `rtc`, `none`) and `clock.trusted` in `GET /status` and
  `GET /_care-pi/status`; a read with a synchronized clock deletes the record.
- **Console.** The banner "Clock not trusted" in the reserved slot of the maintenance banner (top bar; in place of the
  device name below 640 px); the maintenance banner wins the slot. Mock option `clock: untrusted` and
  `POST /__mock/ntp-sync`; Vitest (`tests/clock-banner.test.tsx`, `tests/claim-page.test.tsx`); Playwright
  `e2e/clock.spec.ts` (desktop, mobile, narrow).

Decisions (owner informed; override in `00` if wrong):

- `date -s`, not `timedatectl set-time`: `set-time` refuses while NTP is on, and the `set-ntp false` and `true` around it
  would stop and start `systemd-timesyncd` (a crash between them leaves NTP off). With `date`, timesyncd keeps running and
  corrects the clock when an NTP server answers. Not tested on Pi OS trixie yet.
- One set only (the record blocks a second set). The time of the browser is not taken again at a later login: a device
  that NTP synchronized once and then reboots without internet waits at the gate, as before S52.
- `rtc` is a source (an RTC without NTP) but not trusted: the battery check of SP9 is not done.

Next step: a first boot of a fresh image on a Pi without internet: the claim, the setup, Care starts, and the banner;
then internet: the banner goes within 15 s of the NTP synchronization (`timedatectl show -p NTPSynchronized`).

### 8p. Phase 7: hardening and breadth (2026-11-02)

Code complete on the branch `phase-7-hardening` (from `main` at `e130030`); not run on a Pi. One signed commit for each
part (the WAF and the telemetry share one commit). The contract is `docs/api/openapi.yaml` 0.8.0 (additive;
`docs/api/README.md` section 17). Decisions S53 to S60 (`00`); the security status is `04` section 9; the exit test plan is
`08-spike-log.md`, "Phase 7 exit test — plan".

| Part | What exists | Commit |
|---|---|---|
| Headscale (S59) | `tls.tailscale.control_url` → `tailscale up --login-server`; CLI `--control-url`; console field; `GET /tls` shows it | `c12e420` |
| Firewall (S54) | The agent renders `inet care-pi` and the loader drop-in; `security.firewall.policy`, `security.ssh.allow`; the rollback timer; `GET /security/firewall`, `POST /security/firewall/confirm`; CLI `care-pi firewall status\|policy\|allow-ssh\|confirm\|rollback`; installer `--firewall-drop`; console Security > Firewall; `nft -c` of each golden in CI | `7e738d6` |
| CA rotation (S60) | `POST /tls/local-ca/rotate`, job `tls-local-ca-rotate`, `ca/root.prev.crt`, both fingerprints for 7 days; CLI `care-pi tls local-ca rotate`; console card and banner | `747388c` |
| Signing (S55) | `agent/internal/minisign` (interop with minisign 0.12), `updates.require_signed`, `lock-signature-invalid`, `care-pi solution verify`; CI signs when `MINISIGN_KEY` is set | `070030e` |
| WAF (S58), telemetry (S56) | coraza-caddy in the Caddy image, `security.waf`; `telemetry.*` with vmagent and node_exporter, `GET /api/v1/metrics`, CLI `care-pi metrics`, `care-pi telemetry set-password` | `836f8ac` |
| Doctor | `care-pi doctor [--json]`, `GET /api/v1/doctor`, Support > Health checks; the support bundle has 8 more files | `e2f55e4` |
| Helper (S53) | `care-pi helper`, `host.NewHelperClient`, `serve --helper`; installer user `care-pi`, `care-pi-helper.socket` and `.service`, `--helper` drop-in; uninstaller | `b81f1d7` |
| Installer breadth (S57) | Ubuntu 25.04 and newer by the podman check; Fedora Server 42 and newer experimental (dnf, firewalld, SELinux permissive) | `d376e90` |
| Docs site | MkDocs Material + Redoc (`docs/site/`), `docs-site.yml` (artifact; Pages with `DOCS_PAGES=true`); clinic quick start, troubleshooting, releasing | `cf95730` |

Shrunk or skipped (state them in a review):

- The helper is opt-in and the daemon stays uid 0 (S53): the full split needs typed file requests in the helper
  (`docs/console-server.md` section 14 lists 5 items). `CARE_PI_TEST_HELPER=1` runs every contract test through the
  helper (a CI step), so the allow-list covers what the tests exercise; the live run of step 1 of the plan finds the rest.
- `PUT /security/ssh` (the SSH switch) stays `501`; `security.ssh.allow` only opens or closes 22/tcp in the table.
- No key `solutions/keys/care-pi.pub` yet: the owner makes it (`docs/solutions.md` section 18), then sets the secret
  `MINISIGN_KEY`; only then can `require_signed` default to `true`.
- The WAF uses the CRS that the module embeds (`load_owasp_crs`), not files in `/etc/caddy/crs`.
- Fedora: SELinux enforcing is refused (no `:z`/`:Z` labels in the quadlets); no test on a Fedora host with systemd.
  `quadlet -dryrun` of podman 5.8.9 (fedora:43) passes on each golden.
- The docs site is an artifact only while the repository is private.

Checks on the Mac: section "Verification" of the report of this wave; the Docker runs: `nft -c` of each golden
(nftables 1.1.3), podman 5.4.2 under the policy `drop` (egress, DNAT of a published port, a dropped port, the accept of
the rollback), the Caddy image with the WAF (`images/caddy/test-waf.sh`: block 403, detect 200 and rule 942100),
vmagent and node_exporter against VictoriaMetrics (`quadlets/scripts/test-telemetry.sh`), `quadlet -dryrun` of the 10
goldens with podman 5.4.2 and 5.8.9, and the installer on debian:trixie, ubuntu:26.04, fedora:43 (dry run and two
idempotent runs), debian:bookworm and ubuntu:24.04 (refused).

Next step: the Phase 7 exit test on lumine after the Phase 6 exit test (`08-spike-log.md`); then the final acceptance
(§8k).


### 8o. Phase 6 exit test result (waves 8 and 8-ts, 2026-11-02)

**PASS on lumine except Wi-Fi (owner choice), with 10 fixes; both exit criteria pass.** Wave 8 ran the CA switch and the
public name and found the Tailscale mode blocked by the owner's `tailscale serve`; in wave 8-ts the owner removed that
serve, and the Tailscale mode, its files, its browsers, off and on, a reboot, and the regression passed. The record is
`08-spike-log.md`, "Phase 6 exit test — lumine, 2026-11-02"; the raw output is `local/wave8-p6/` and `local/wave8-ts/`.
lumine runs `85d5d3b` (`main` with fixes 7–10).

- **CA switch (S36).** The first apply of the merged agent made the name-constrained root (critical: `.local`,
  `.care-pi.rithviknishad.dev`, `192.168.165.0/24`) in 1 s, and restarted only care-caddy; Care kept running. The console
  shows the one-time warning with the new fingerprint; the old root fails. Root: `D5:12:73:DB:9D:55:F0:A4:…:1A:44`.
- **Public name.** The live DNS service registered `lumine-ooyb`, then (after off and on) `lumine-shaj`. Staging first,
  then the switch of the same name to production: the Let's Encrypt chain (`YE2` ← `ISRG Root X2`/`X1`) verifies with the
  system store. Port 80 of `lumine.local` gives `302` with `Cache-Control: no-store` only with the production certificate
  (`308` with staging, M2). Chromium without a care-pi root loads the console on the public name with no warning
  (`connectivity.spec.ts`, `desktop.login.spec.ts`; `phone.ready.spec.ts` in WebKit). Registration → certificate 79 s;
  the switch staging → production 17 s; each name change is a cutover of the multihost plug (Care down 57–61 s).
- **Tailscale (wave 8-ts).** `sudo tailscale serve reset` (the only change of the Tailscale state; it clears the handlers
  of `svc:care-box` too, the pref `AdvertiseServices` stays). With `tls.tailscale.manage_daemon: false` the enrol job
  reuses the owner's login (no install, no `up`): 61 s, one full cycle (the pod gets `100.67.15.72:80/443/8443`; Care down
  58.6 s), 3 names. Caddy gets the certificate of `lumine.orthrus-bass.ts.net` from tailscaled (`YE1` ← `ISRG Root X2`/`X1`,
  verify 0 on the Mac without `-k`). From the Mac, which reaches lumine only through the tailnet (the phone on mobile
  data): Chromium and WebKit with the system trust only load the console and Care with no warning
  (`connectivity.spec.ts`, `desktop.login.spec.ts`, `phone.ready.spec.ts`, a WebKit check without
  `ignoreHTTPSErrors`; `lumine.local` fails as the negative control).
- **Files.** Each presigned link names the host of the request: a 1 MiB file uploaded through the ts.net name opens through
  the public name and `lumine.local`, and the reverse. With fix 7 the agent adds the names of the device to `ALLOWED_HOSTS`
  of `care_multihost_files_be`, so lumine no longer needs the operator setting of wave 8 (removed; with fix 8).
- **Disable, reboot, regression.** Tailscale off leaves tailscaled `Running` and its prefs unchanged; with fix 9 it frees the
  ports of `100.67.15.72` (a full cycle, Care down 63.9 s); on again 57.7 s. Public off deletes the registration
  (`NXDOMAIN` within 1 min). Reboot with 3 names: SSH 22 s, Care on the ts.net and public names 110 s, `care-pod` did not
  fail (B1). Phase 1–5: backup runs and verify of the 4 destinations, update check, FE status, dry runs with no changes,
  `systemctl --failed` empty.

The fixes (each with a test):

1. `plan`: a cutover of the backend plugs restarts the other changed units (care-caddy) and reloads a changed Caddyfile.
2. `tls.tailscale.manage_daemon` (default `true`): `false` never installs, connects, or disconnects tailscaled.
3. The Tailscale job stops with `state-conflict` when `tailscale serve`/`funnel` uses 80, 443, or 8443 of the node.
4. `wait-certificate` waits for a certificate of the configured directory (not the staging one after the switch).
5. `care-caddy.container` has the label `care-pi.acme-ca`: a switch of the ACME directory restarts Caddy.
6. The agent does not keep the status read of a cancelled request.
7. The agent adds each name of the device that no pattern matches to `ALLOWED_HOSTS` of a plug with
   `auto: when_multihost` (`0d8e8e5`).
8. A change or removal of an existing operator value of a plug planned "path none" and ran nothing (`65ef0a1`).
9. The Tailscale mode off kept the publishes of the Tailscale address in the running pod (`db43a92`).
10. With the Tailscale mode on, every status said "The IP address changed." (`85d5d3b`; the description of
    `address_changed` in the contract, no schema change).

Fixes 1–6 were staged in wave 8 (1Password refused each signature then) and are committed signed together as `36a861d`;
7–10 are one signed commit each.

Decisions of waves 8 and 8-ts (owner informed; override in `00` if wrong):

- To keep 1 production certificate and end with a re-registered id in production, the re-registration ran with staging,
  and the production switch came after it.
- `manage_daemon: false` stays in the `config.yaml` of lumine, so a later test cannot log the owner's device out.
- A Tailscale serve conflict is `state-conflict` with a message (no new problem type, no contract change).
- The owner removed the stale `tailscale serve` of lumine (wave 8-ts); the Tailscale mode stays **on** as the end state of
  lumine, with the public name: 3 names.
- The names of the device are always in `ALLOWED_HOSTS` of the multihost plug; an operator cannot remove them (fix 7).
- The Tailscale mode off is a full cycle when tailscaled keeps its address (fix 9): about 60 s of downtime, as before.

Next steps: (1) every client in the tailnet has the address `10.88.0.1` (the masquerade of tailscaled on forwarded
packets): the per-address limits of the console and of Care are shared by the tailnet (`08` follow-up 9; an owner
decision); (2) the agent reached its 128M cap during 3 browser journeys (no OOM; `08` follow-up 4); (3) the console names
the local name of the device on the public card (follow-up 3) and a certificate state for the Tailscale name (follow-up
10); (4) a real phone on mobile data, Wi-Fi and the cable pull, the offline fallback, a fresh device in public mode, F7,
S52 on hardware, when the owner wants them; (5) the design for a host that keeps its own `tailscale serve` (follow-up 2).

### 8q. Final code wave (wave 9, 2026-11-02)

On `main` after `9ff6634`: 4 signed commits, one for each item. Not run on a Pi; the acceptance wave on lumine is next
(§8k and "Acceptance" below). No contract change (`docs/api/openapi.yaml` stays 0.8.0).

| Item | What exists | Commit |
|---|---|---|
| S61, the fallback | Each Caddy site sends `X-Care-Pi-Listener` (`local`, `public`, `tailscale`) to the agent (`import care <name>`, `import console <name>`; the value replaces the header of a client). On the Tailscale site, a request whose address is not a tailnet address gets the shared client address `tailscale`: no address lock for a failed password, claim secret, or recovery code (the account and claim locks stay), a failed TOTP code still counts, 600 public reads each minute. A tailnet address (no masquerade) keeps its own limits. `docs/connectivity.md` §4.7. | `b073b99` |
| S62 | `apply.RestartBehindPage`: the plan action `restart` with `care-api.service` and the plug path `restart` set the maintenance flag (`maintenance.json`: the kind of the job, `step: restart`), restart, wait until `care-api` is healthy (5 min at most), and delete the flag, also after a failure. A page of a cutover or a restore stays as it is. The page shows "CARE restarts. This takes about 1 minute."; `plan.maintenance` is `true` for the plug path `restart`. | `5ae0ec3` |
| S63 | `care-api` 576M / 128M (`small-clinic`, 2 workers) and 832M / 128M (`busy-counter`, 3 workers): 64M + 256M for each worker. `solo` stays 320M / 64M. | `3310e51` |
| Release | `.github/workflows/release.yml` (a tag `vX.Y.Z`: test, the console, `care-pi_linux_{arm64,amd64}` with the version of the tag, `SHA256SUMS`, the release with `install.sh` and `uninstall.sh`, `--latest`). `installer/test-download.sh` (a CI step) checks the installer URLs and names against it. Only a release build `vX.Y.Z` compares with `min_agent_version`; a dev build is the newest agent. The fallback `min_agent_version` is `v0.1.0`. `docs/releasing.md`, "An agent release". | `8c595a0` |

**S61: why the fallback.** The `tailscale_auth` provider of `caddy-tailscale` calls WhoIs of `tailscaled` with the TCP peer
of the request. On the Tailscale site that peer is the pod bridge (`10.88.0.1`, the masquerade of `ts-postrouting`), so
WhoIs gives "peer not found", and the provider answers `401` to each request. It also names the user, not the device. A
test with the image of `images/caddy` (the modules of `9ff6634`) and a fake local API shows it: the WhoIs query had the
address of the Docker gateway, and Caddy logged `auth provider returned error … peer not found`
(`local/wave9/ts-exp/result.txt`). The agent cannot find the device either. A real address for each device needs a
`tailscaled` without the masquerade (`--snat-subnet-routes=false` is a candidate, not tested; an operator setting). The
agent then keeps the address of each device by itself. This closes `08` follow-up 9 as far as the agent can.

Decisions of wave 9 (owner informed; override in `00` if wrong):

- S61: the fallback of the brief, plus two rules: a failed TOTP code still counts for the address lock of `tailscale`
  (the account lock does not count TOTP codes, so without it a client with the password could try codes at 10 each
  minute), and the shared address has 600 public reads each minute (the console polls each 3 to 15 s, the page each
  5 s). The credential limit (10 each minute) stays shared. A tailnet device that comes through a subnet router to the
  LAN or public name also has the bridge address; those sites keep the address lock.
- S62: only a restart with `care-api` shows the page. On a failure the page goes (Caddy then gives `502`), unlike a
  cutover, where the page stays until the rollback. The restart text is on the page only: the console keeps "Start the
  changed services again" for the step `restart`, because the step also restarts other units.
- S63: `custom` with 4 or more workers is unchanged (§8k).
- `agent-too-old`: a build after a tag (`v0.1.0-3-g9ff6634`) is a dev build and counts as the newest agent (before: it
  compared its last tag). The fallback `min_agent_version` of `make-lock.sh` and `solution-lock.yml` is `v0.1.0`, not
  `v0.4.0`: no release `v0.4.0` exists, and a lock without a minimum must install on the first release.
- The first release has the commits as its notes, without `--generate-notes`: GitHub would start the notes at the
  newest release of any kind (a bundle). From the second release, `--generate-notes --notes-start-tag <previous>`.
- The installer has a source guard (`CARE_PI_SOURCE_ONLY=1`: define the functions, run nothing) for its test.
- The repository is public: the "private repository" notes of the install and solution docs are gone.

Checks on the Mac: `go build`, `go vet`, `go test -race ./...` (38 packages), `CARE_PI_TEST_HELPER=1 go test
./agent/internal/server`, golangci-lint v2.15.0 (0 issues), `make api-lint`, shellcheck (installer, `solutions/scripts`,
`image`), actionlint, `installer/test-download.sh` (on macOS and in `debian:trixie`), the console `api:check`,
`typecheck`, `lint`, `test` (143), and `test:e2e` (167 passed, 1 skipped), `solutions/scripts/e2e-local.sh`;
`caddy validate` of the 10 golden Caddyfiles with the image of `images/caddy`, and `caddy adapt` of `public-tailscale`
(each site sets its own listener name; a spoofed header on a local site arrives as `local`); `quadlet -dryrun` of the 10
goldens with podman 5.4.2 (`care-api` `MemoryMax=576M` for `small-clinic`, `832M` for `busy-counter`, `320M` for `solo`);
`make build-linux VERSION=v0.1.0`: both binaries print `version: v0.1.0`; the download step of `install.sh` from a
release layout (`file://`, in `debian:trixie`) installs `v0.1.0`, and a changed binary fails the checksum.

**Acceptance (lumine; the Mac is the only tailnet client):**

1. **Deploy.** The agent of this wave, then `sudo care-pi apply`. Expect: the Caddyfile reloads; `care-api` restarts
   behind the maintenance page (its unit changed: S63 and S62 at once). `systemctl show care-api -p MemoryMax` gives
   `603979776` (576M).
2. **S61, one client.** From the Mac through the tailnet, 6 wrong passwords at
   `https://lumine.orthrus-bass.ts.net:8443/api/v1/session/login`: each is `401 bad-credentials` (before: the sixth was
   `429 locked-out`). `journalctl -u care-pi -g 'password failed'` shows `address=tailscale`. Log in with the password
   (fewer than 10 failures in a row, so no account lock); `care-pi auth sessions` shows `tailscale`. On lumine itself,
   5 wrong passwords at `https://lumine.local:8443` (the LAN site, with `Origin: https://lumine.local:8443`) lock the
   address `192.168.165.91`: the sixth request
   is `429` with "from this address"; then `sudo care-pi auth unlock`. On the LAN site, a request with
   `X-Care-Pi-Listener: tailscale` still logs the LAN address (Caddy replaces the header). 61 reads of
   `/_care-pi/status` in one minute through the ts.net name give 61 × `200`; through `lumine.local`, the 61st gives `429`.
3. **S62.** Change a backend setting (for example `care-pi plugs set care_multihost_files_be --setting
   backend.ALLOWED_HOSTS=… --apply`, or `env.backend` and `care-pi apply`). While it runs: `https://lumine.local/`
   gives `503` with "CARE restarts. This takes about 1 minute.", and `GET /_care-pi/status` has `maintenance.active:
   true`, `kind` of the job, `step: restart`. After it: the page is gone, Care gives `200`. Record the time of the page.
4. **S63.** Watch `memory.peak` of the `care-api` cgroup during the browser journeys; the old peak was 487–491 MiB.
5. **Release.** The orchestrator makes and pushes the signed tag `v0.1.0`. Check: the run of `release` passes; the
   release `v0.1.0` has 5 files and is the latest (`gh api repos/rithviknishad/care-pi/releases/latest --jq .tag_name`);
   `curl -fsSL https://github.com/rithviknishad/care-pi/releases/latest/download/SHA256SUMS` lists the 4 files.
6. **`agent-too-old`.** Install `v0.1.0` on lumine (`curl -fsSL
   https://github.com/rithviknishad/care-pi/releases/download/v0.1.0/install.sh | sudo sh -s -- --version v0.1.0 --yes`,
   or the binary and its sum). `care-pi version` gives `v0.1.0`. The locks of the lumine dist have
   `min_agent_version: v0.4.0` (made by `make-lock.sh` before this wave): `care-pi update check` gives `agent-too-old`
   ("… needs agent v0.4.0 or newer. This agent is v0.1.0."), and the Updates page shows the blocker. The running lock
   keeps working (apply does not check it). A lock made with `--min-agent v0.1.0` (or by `solution-lock.yml` after the
   tag) installs. Put the dev agent back after the test, or the old locks stay blocked.
7. **`curl | sh`** of the Phase 1 fresh-flash test (§8k) uses the same release: the default `latest` downloads `v0.1.0`.
   The default index (`releases/download/index/index.json`) exists only after `solution-lock.yml` runs.

### 8r. Final acceptance result (wave 10, 2026-11-02)

**PASS on lumine (A to D), with 9 fixes.** The record is `08-spike-log.md`, "Final acceptance — lumine, 2026-11-02"; the
raw output is `local/wave10/`. lumine runs the dev build `v0.1.0-fix.10` (`49ca4de`, after the 9 fixes), not a release
build: a tag `v0.1.1` and `install.sh --version v0.1.1` put it on a release again.

- **A. The release `v0.1.0`.** The installer from `releases/latest/download/install.sh` downloaded and checked the agent
  (15 s). The first apply: `care-api` `MemoryMax=576M` (S63), its restart behind the page (22 s; S62), the listener
  header in the Caddyfile (S61), and the Phase 7 firewall table. `agent-too-old`: the check logs the exact text; with
  a newer lock, `GET /updates` shows `blocked_by` only after fix 1. Lock 2026.11.2 (`--min-agent v0.1.0`) installs.
- **B. Phase 4 deferred.** `update --os --now`: a real `full-upgrade` of 14 packages with the holds kept (34.8 s); the
  reboot path works after fix 2 (SSH 36 s, Care 110 s); fix 3 keeps the Tailscale name up when `tailscaled` restarts.
  The update to 2026.11.2: 63.2 s behind the page; then the Caddy image of the lock (the WAF module).
- **C. The Phase 7 exit test.** All 6 criteria pass: the helper (no refusal of a real request; 8 hand-made requests
  refused and logged; fixes 4 and 8), the policy `drop` (SSH, Care, the console, mDNS, DHCP, NTP, egress, backups; the
  rollback after 90.7 s; a reboot in the window boots `accept`; a confirmed `drop` stays after a reboot), the CA rotation
  (with the new root, `curl` and Chromium on the Mac open Care, with the old one they fail; both fingerprints on the
  page; fix 5 for the install steps; no real phone), the WAF (`detect`: 0 audit lines for a short normal use with an
  upload, no token or cookie in the log; `block`: `403`), telemetry to VictoriaMetrics on the Mac over the tailnet
  (fixes 6 and 7; off: no unit and no connection to the receiver, checked with `ss`, as lumine has no `tcpdump`), and
  `care-pi doctor` (the 2 injected faults first, the same on the Support page). S61 and the Phase 6 regression pass.
- **D. Cleanup.** 2 destinations stay: `usb` (a loop device, not a real USB disk) and `mac` (SFTP). Fix 9: the timers
  of a removed destination stop first.

The fixes: (1) `12ace01` the blocker in `GET /updates`; (2) `fef512b` the CLI waits for its reboot; (3) `1bbf445` the
`tailscaled` drop-in `RuntimeDirectoryPreserve=yes`; (4) `af129f2`, `83d1951` the drill stops and removes its container
in the namespace of the host; (5) `6b104ee` the console texts of the install steps; (6) `f277b77` the exporter port
19100; (7) `71c5c0e` telemetry off resets its units; (8) `8fda826` the installer restarts the helper after a new binary;
(9) `49ca4de` the timers of a removed destination. Docs: `d103536`.

Decisions of wave 10 (owner informed; override in `00` if wrong):

- The test of `agent-too-old` used a TEST ONLY lock 2026.11.3 (min `v0.4.0`) for 2 checks, then the dist without it. The
  fix was checked with a CLI binary of `12ace01` that says `v0.1.0`, run from `~/wave10` only (a dev build is never
  blocked). That binary is deleted.
- Lock 2026.11.2 has the solution of `HEAD` with `care_fasterappointments_fe` on (as lock 2026.11.1 of wave 6b), so the
  update kept the plug set of lumine. The override `images.caddy` of wave 6b is gone: the lock gives the Caddy image.
- `/var/lib/care-pi-test` keeps `loop.img`: it is the disk of the destination `usb`, which the brief keeps.
- lumine ends with the policy `drop` confirmed, as the brief asks, although it closes the owner's exporters to
  `avocado` (`08` finding 1). One command opens them: `sudo care-pi firewall policy accept`.
- One restart of `tailscaled` (after the apt upgrade had restarted it once) checked fix 3. No `tailscale up/down/logout`.

End state of lumine: lock 2026.11.2 (`stable`), the helper mode on, the policy `drop` confirmed, the WAF in `detect`,
telemetry off, 3 names (the public name with its production certificate, `lumine.local` with the rotated root,
the ts.net name), 2 destinations, `systemctl --failed` empty. `local/LUMINE.md` has the details.

Next: the owner decides the findings of `08` and tags `v0.1.1`; the hardware tests (§8k, "Remaining").

### 8s. v0.1.1

`v0.1.1` is `v0.1.0` with the 9 fixes of the final acceptance (§8r) and S64 (wave 11, 2026-11-03). The contract is
`docs/api/openapi.yaml` 0.8.1 (additive). No config change is necessary. The orchestrator tags `v0.1.1` after the
commits of wave 11 and installs lumine again from the release.

Release notes:

**Fixes from the final acceptance on lumine**

1. Updates: `GET /updates` gives `blocked_by` (`agent-too-old`, with the detail) for a newer lock that the agent cannot
   install, and `care-pi update status` prints it (`12ace01`).
2. `care-pi update --os` waits for its reboot, also with `--reboot-if-required` (`fef512b`).
3. Tailscale mode: the drop-in `tailscaled.service.d/50-care-pi.conf` (`RuntimeDirectoryPreserve=yes`) keeps the
   Tailscale name up when tailscaled restarts, for example after an apt upgrade (`1bbf445`).
4. The restore drill stops and removes its container in the namespace of the host, so the next drill does not fail on
   the name (`af129f2`, `83d1951`).
5. Console: the install steps of the root CA show their texts; the download button follows the file (`6b104ee`).
6. Telemetry: `care-node-exporter` listens on `127.0.0.1:19100`, so a node_exporter of the host on 9100 does not
   stop it (`f277b77`).
7. Telemetry off resets the failed state of its units (`71c5c0e`).
8. The installer restarts the running helper after a new agent binary (`8fda826`).
9. The timers of a removed backup destination stop before their files go (`49ca4de`).

Docs: the uninstaller removes the tailscaled drop-in; a removed destination stops its timers (`d103536`).

**New: extra firewall ports (S64)**

- `security.firewall.allow_ports`: up to 32 entries `{port, proto, iface, comment}` that the table `inet care-pi`
  accepts after its own rules, for example a node_exporter of the host. A port that care-pi already accepts is refused
  ("already allowed by care-pi"). CLI `care-pi firewall allow add PORT[/PROTO] [--iface IF] [--comment C]`,
  `allow remove`, `allow list`; console Security > Firewall; `FirewallState.allow_ports` in `GET /security/firewall`.
- The policy `drop` does not close tailscaled: its peer API, MagicDNS, and `tailscale status` work (measured). A
  service of the host on the tailnet needs an entry with `--iface tailscale0`.

**Upgrade**: `curl -fsSL https://github.com/rithviknishad/care-pi/releases/download/v0.1.1/install.sh | sudo sh -s --
--version v0.1.1 --yes` (add `--helper` when the helper mode is on). A host with the policy `drop` keeps its table; add
its other services with `care-pi firewall allow add`, then nothing needs a new confirmation (only a switch to `drop`
starts the timer).

**Wave 11** (`08-spike-log.md`, "Wave 11 — S64 on lumine"; raw output `local/wave11/`):

| Item | Commit |
|---|---|
| S64 agent: config, schema, validation, render, CLI, contract 0.8.1, docs, decision | `437570c` |
| S64 console: the extra ports in Security > Firewall, mock, Vitest, Playwright journey | `7c51065` |
| The record: `08`, this section, the roadmap, `docs/releasing.md` | the commit after `7c51065` |

Decisions of wave 11 (owner informed; override in `00` if wrong):

- **No built-in `iifname "tailscale0" accept`.** The brief proposed it for the peer API of tailscaled. On lumine the
  peer API answers new connections under `drop` (tailscaled serves them in its own network stack), so the finding of
  §8r was wrong on that point. The rule would open each service of the host to each device of a tailnet with the
  default policy. The entries 9100 and 9256 on `tailscale0` are therefore necessary on lumine, not redundant.
- Port 22/tcp and 41641/udp are "already allowed" only while their rule is in the table (`security.ssh.allow: true`;
  the Tailscale mode). With SSH off, an entry such as `{port: 22, iface: tailscale0}` opens SSH on one interface.
- The console edits the list in the same change as the policy and SSH (one preview, one apply), so a switch to `drop`
  with the extra ports is one trial.
- `remove PORT[/PROTO]` without `--iface` removes each entry of that port and protocol.
- Two drop trials with the old agent (`v0.1.0-fix.10`, no confirmation, back to `accept` by the timer after 90 s each)
  measured the finding before the fix; the exporters were closed for 90 s each time.

End state of lumine: the dev build `v0.1.1-dev` (`7c51065`, with the helper), the policy `drop` confirmed, the entries
9100 and 9256 on `tailscale0`; the rest as in §8r.

Next: the tag `v0.1.1`, the install of lumine from the release; then the hardware tests (§8k, "Remaining").

### 8t. Wave 13 agent (2026-11-09): internet check, RTC policy, Care 3.1 from git, pre-baked database

Branch `w13-agent` (worktree `care-pi-w13a`, from `main` at `9ac7c9b`). The onboarding plugs (S73) are the other
branch of the wave (`w13-onboarding`). Contract `docs/api/openapi.yaml` 0.9.1 (additive: each new member is optional);
the console only regenerated `src/api/types.gen.ts`.

| Item | Decision | Commit |
|---|---|---|
| A. The internet check | S74 | `614c2a9`, `0ff2aaf` (the CLI doctor) |
| B. The RTC clock policy | S75, SP9 | `25338de` |
| C. Care 3.1 refs from the git source | S71 | `5c02da8`, `9efb69f` (the rules of care in Go, not in the schema) |
| D. The pre-baked database and no pull of a present image | S72 | `ad3d4cf` |
| The record: `08` (SP9, care.local findings), this section | — | `c099588` |
| In-repo plugs of `w13-onboarding` (S73): the lock pipeline; `backend.subdirectory`, `frontend.path`, `@<sha>#subdirectory=` | S73 | `35851cd` (CI), `9a662ac` (agent) |

What changed for an operator:

- **Internet (S74).** The agent checks at its start, after 15 s, 30 s, 60 s, then every 2 minutes until the first
  success, then every 5 minutes; `github.com`, then `1.1.1.1`, so "the internet is reachable, but DNS does not work"
  differs from "not reachable". The hardware probe, `care-pi host check`, and `care-pi doctor` check now. The hardware
  step never keeps a stale failure. The status pages of Caddy show the live check (`docs/api/README.md` section 19.1).
- **Clock (S75).** `care-pi clock detect` (in bootstrap: the installer and the first boot) sets `clock.policy: rtc` only
  when the RTC kept the time through the boot and the Pi 5 reports a battery voltage; care.local has no battery
  (`ntp`). With `rtc` the gate passes from the RTC, and NTP confirms it later (`docs/image.md` section 6.4).
- **Care 3.1 (S71).** `solutions/care-clinic/solution.yaml`: `care: {source: git, repo: https://github.com/ohcnetwork/care,
  ref: ENG-737-backup-backup}`, `care_fe.ref: 3.1/base`. CI (`care-base.yml`) builds `docker/prod.Dockerfile` of the
  commit for arm64 and amd64 on native runners into `ghcr.io/rithviknishad/care-pi/care-base:<sha12>`; the solution
  image is `FROM` it. A host builds CARE itself only on the track `custom` with 4 GB of RAM or more
  (`docs/solutions.md` section 13.4).
- **Image (S72).** The image has `/var/lib/care-pi/seed/pg-<sha12>.tar.zst` (7.5 MB for lock 2026.11.1); the first
  start restores it and sets the password of the device, so `migrate` has nothing or only the difference to do
  (`docs/image.md` section 6.5). The job logs say "image present, no pull".

Decisions of wave 13 (owner informed; override in `00` if wrong):

- **The care branch resolves with the smart-HTTP ref list (`info/refs`), not the GitHub API.** The brief asked for
  `api.github.com/repos/…/commits/<ref>` with a 1-hour cache for the limit of 60 requests an hour. The agent resolves
  `care_fe` and the plugs with the ref list already: no rate limit, no token, no cache. CI uses `gh api` (it has a token).
- **Both architectures of `care-base`, each on its own runner**, then a manifest list. QEMU for the Python wheels of
  CARE would take hours; arm64 only would break the amd64 solution image.
- **The battery check uses `battery_voltage`** (a Pi 5 reports 0 without a battery) next to the RTC time at boot from
  the kernel message: a warm reboot keeps the RTC time also without a battery, so the time alone gives a false `rtc`.
- **The seed is made on the build host with docker**, not in the chroot and not with podman in the build container
  (the cgroup limits of SP10). `pi-image.yml` and the Mac both have docker.
- **The new contract members are optional** in the schema (the agent always sends them), so console fixtures of 0.9.0
  stay valid without an edit.

Not done, with the reason:

- **The lock `2026.11.3` was not dispatched.** `workflow_dispatch --ref w13-agent` needs the branch on GitHub, and this
  wave does not push. After the merge (or a push of the branch): `gh workflow run solution-lock.yml --ref <branch> -f
  solution=care-clinic -f version=2026.11.3`, then `image/build.sh --dist <fetch-dist of 2026.11.3> --lock-version
  2026.11.3 --reuse-base`. The git-source image was built and checked on the Mac instead: `docker/prod.Dockerfile` of
  `28211e29a359` (4 min 17 s, 772 MB), the solution image `FROM` it (52 s), and `migrate` + `sync_*` of an empty
  database (847 migrations) pass.
- **lumine** was offline the whole wave: its RTC (`clock detect --dry-run`) is still to read.
- The hardware page of the wizard on care.local: the setup is `ready`, so the page is closed; the API, the CLI, and the
  public status show the live check.

For the merge with `w13-onboarding`: add `path: ${{ matrix.plug.path }}` to the call of `plug-bundle.yml` in
`solution-lock.yml` (this branch has no such input of `plug-bundle.yml`, so actionlint refuses the line here), and
apply `local/wave13/patches/onboarding/agent-tests-care_pi_onboarding.patch` of that branch. Checked in a merged
work tree: the merge has no conflict, `go test ./...` passes, actionlint passes, `care-pi solution validate` passes;
Redocly gives one warning for the `if`/`then` of `frontendPlug` in `solution.schema.json` (as for `care` here, `9efb69f`).

End state of care.local: the dev build `v0.3.0-dev` at `9a662ac`, the earlier binary at
`/usr/local/bin/care-pi.v0.2.0.bak`, `clock.policy: ntp`, setup `ready`, Care running (not restarted).

### 8u. Wave 13 merge, lock 2026.11.3 and 2026.11.4, care.local (2026-11-09)

- **Merge.** `w13-agent` (`1a30cb5`) and `w13-onboarding` (`19efeaa`) merged into `main` without a conflict. The merge
  commit `b6db3fc` applied the agent-tests patch, added `path: ${{ matrix.plug.path }}` to `solution-lock.yml`,
  regenerated `console/src/api/types.gen.ts` (contract 0.9.1), and added `docs/onboarding.md` to the docs nav.
- **Reviews.** Two independent code reviews. Onboarding branch: 1 major (the lock pipeline, done on `w13-agent`),
  2 minor, 1 nit (`200c8e2`, `f1e8929`, `89b18ac`). Agent branch: 1 major (a cancelled or timed-out probe was stored
  as "no route"), 4 minor (any TLS failure counted as reachable; the pending seed password step could not run when
  `care-db` was up; the RTC drift warning never cleared; any skopeo error counted as "no image"), 2 nits. All fixed in
  `f8a7d7d` (7 commits, tests for each). `b8f34ab` removed a stray scp artefact (an 18 MB arm64 binary) from the root.
- **Lock 2026.11.3** (`solution-lock.yml` run 42585236532 on `b6db3fc`): Care from git `28211e29a359`
  (`care-base`, arm64 + amd64), care_fe `3.1/base` `c4e670c1b4fa` (3.5 MB bundle, 12.2 MB unpacked, 107 files),
  `care_pi_onboarding` backend `@f1e8929…#subdirectory=plugs/care_pi_onboarding`, FE bundle 101 KB. `min_agent_version`
  stays `v0.2.0`: the lock decoder is not strict, and a v0.2.0 agent uses only the digests and the URLs.
  Lock `2026.11.2` was never a public release (lumine has a local lock).
- **Migration path.** `develop` and `ENG-737-backup-backup` diverged (48 ahead, 6 behind); none of the develop-only
  commits adds a migration, so the 3.1 branch holds every migration of the 2026.11.1 devices.
- **care.local.** Dev agent `b8f34ab`: `host check --probe`, the doctor, and the journal show the internet reachable;
  `clock detect --dry-run` keeps `ntp` (RTC present, 1970 at boot, battery 0 V). `update stage` + `update apply --now`
  from 2026.11.1 to 2026.11.3: 3 images "present, no pull", checkpoint, 848 migrations, `plug-config: 1 row(s)`, no
  error. `plug_config` has `care_pi_onboarding_fe` with the URL of the bundle; `remoteEntry.js` 200 with CORS.
- **Wizard live run** (Playwright against `https://care.local`, results in
  `care-pi-w13b/local/wave13/wizard-live/`): blocker D1, `window.CARE_API_URL` is `""` on care-pi, so the shipped
  plug sent no request. With the fix, the first run took 8.5 s (one preview per screen, one POST per action), the
  second run created 0 objects, `DELETE demo_data/` removed 5 patients and 5 visits only, one log line per call, no
  stack trace. D2 to D7 (invitations without a mail server, union-territory aliases, counts, plural, deleted users)
  fixed with D1 in `e142a96` (42 Django tests on Care 3.1, 19 Vitest, 6 Playwright). Merged `acce1ee`.
- **Image.** `image/build.sh --reuse-base` from lock 2026.11.3 built `v0.3.0-rc1` (436 s, agent `b6db3fc`) and
  `v0.3.0-rc2` (535 s, agent `b8f34ab`): the seed `pg-28211e29a359.tar.zst` (7.9 MB, 848 migrations), both plug
  bundles pre-seeded, 79 checks pass. The nspawn boot test is still unreliable on Docker Desktop.
- **Lock 2026.11.4** cut on `acce1ee` for the plug fixes. Next: update care.local to it, run the wizard without a
  shim, rebuild the image, update lumine (offline during the wave), tag v0.3.0.
- **Wizard run 2** on care.local with lock 2026.11.4 (shipped plug `e142a96`, no shim): the redirect after sign-in
  opens the wizard with 1 status call; one run end to end in 1.5 s with 22 requests and 0 created (demo_data 5);
  the invitation option is off without a mail server; the demo data deleted cleanly; 19 log lines, no stack trace.
  One UX note O1 in `8k`. Results in `care-pi-w13b/local/wave13/wizard-live/run2/`.
- **CI on main after the merge** failed twice: shellcheck SC2015 (`image/build.sh`, `image/scripts/make-seed.sh`,
  CI's shellcheck reports info notes that the local 0.11.0 does not), and the offline make-lock e2e without a bundle of
  `care_pi_onboarding_fe`. Fixed in `077688f` and `e52e301`; CI green.
- **v0.3.0** tagged on `e52e301` (signed), release with 5 assets, "latest"; the notes have a summary at the top.
  care.local runs the release build (`install.sh --version v0.3.0`), doctor clean. `pi-image.yml` dispatched on the
  tag for lock 2026.11.4 (run 42601543761), its first run in CI; it publishes the release `image-2026.11.4`.
- **Release integrity defect (fixed).** The first CI run of `pi-image.yml` failed in `fetch-dist.sh`: the care_fe
  bundle `fe-care-clinic-f7a4b424d67f-7502e51944595062` did not match the SHA-256 of lock 2026.11.4. Cause: the push
  of `077688f` to `main` (paths `solutions/**`) ran `fe-bundle.yml`, which built the same build id again and uploaded
  the asset with `--clobber`; the tar.gz is not byte-reproducible, so the hash changed 20 min after the lock was cut.
  Repair: the original tarball (from the local `fetch-dist` of 2026.11.4) was uploaded again, so the asset matches the
  lock. Fix: `fe-bundle.yml` now checks for the release first and never builds or uploads a published bundle again
  (as `plug-bundle.yml` already did); its outputs come from the published asset (`docs/fe.md`, "The fe-bundle
  workflow"). `pi-image.yml` dispatched again on the tag.
- **Second `pi-image.yml` run** (42602228610, on the tag v0.3.0): the image built and passed `verify.sh` in CI in
  20 min on `ubuntu-24.04-arm`; then `upload-artifact` failed with EACCES on `<name>.install.log` (root 0600 from
  the chroot). Fix `f8d447d`: `image/builder/run.sh` sets the owner (`OUT_UID`/`OUT_GID` from `build.sh`) and the
  mode of `/out` at exit; `pi-image.yml` has a `sudo chmod` fallback. The workflow checks out the tag, so **v0.3.1**
  (`f8d447d`, release with 5 assets, notes) carries both pipeline fixes; `pi-image.yml` dispatched on v0.3.1 (run
  42605544412). care.local runs v0.3.1.
- **Third `pi-image.yml` run** (42605544412, on v0.3.1): success; the release `image-2026.11.4` has the image
  (1.05 GB), `.sha256`, `image-release.json`, `verify.txt`, the Imager manifest; the release `imager` has the new
  `os_list.json` ("Care: CARE Clinic 2026.11.4").
- **lumine** (2026-11-09 16:45 IST, powered on again): loop device reattached (`losetup -f -P`), `usb` back to `ok`
  after one run; agent v0.3.1 from the release; `clock detect --dry-run`: RTC present, **no battery** (0 V) → `ntp`,
  so the `rtc` policy stays untested on hardware (neither Pi 5 has a battery); `host check --probe`: internet
  reachable. `updates.index_url` set to the default (it pointed at the wave-10 local dist); `update stage` 54 s
  (4 pulls, 1 present), `update apply --now` 92 s from 2026.11.2 to 2026.11.4: checkpoint, migrate, `plug-config:
  1 row(s)`, doctor 0 warnings. The plug bundle serves on all 3 names with CORS. Care had no facility, so the
  wizard runs a true first-time setup there (next item).
