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

Authoritative ledger: `00-decisions.md` (S1–S40 settled, P1–P20 all accepted, O-list in §C).
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
| Recommended caps | api `64M + 200M × workers` (320/512/768); worker 384M c=1 / 512M c=2; beat 384M; db 384M (512M at 256 MB buffers); redis 96M; versitygw 128–256M; Caddy 96M; agent 64M. All with `MemorySwapMax` set. |
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
