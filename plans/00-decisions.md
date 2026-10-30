# 00 — Decisions

## A. SETTLED (from your answers)

| # | Topic | Decision |
|---|---|---|
| S1 | lumine | Current native setup is a throwaway; you will clean it up (kill k3s etc.). lumine is the dev target for this project. None of its current config is an input. |
| S2 | Runtime | Containers, rootful podman, quadlet units. Keep everything as declarative as possible. |
| S3 | Kernel | Stay on the default Pi OS kernel (16 KB pages on Pi 5). Switching to the 4 KB kernel is acceptable only if an image cannot be made to work. |
| S4 | Hardware | Pi 5 first; Pi 4 (2 GB / 4 GB) must work eventually. Boot from USB SSD or NVMe HAT; both required. Must work in 2 GB. |
| S5 | Clock | Configurable policy: NTP must be reachable before Care is reachable, **or** an RTC with battery is present. (Feasibility: see `01-architecture.md` §7.) |
| S6 | Network | Ethernet required for initial setup. Wi-Fi (including ethernet→Wi-Fi failover à la the netplan bond gist) can come later via the console. |
| S7 | Sizing | Easy setup = presets chosen by plain-language questions; advanced setup = raw knobs (workers, concurrency, …). |
| S8 | Frontend | Built **on the Pi** (your measurement: ≈75 s on Pi 5). App services scale down during builds. Must also support pulling prebuilt FE assets/plug bundles/images built elsewhere. care_fe stays build-time configured for now. *SP4 amended the budget: the working set is ≈1.8–2 GiB, fits 2 GB only via zram; builder is shaped (`MemoryHigh=768M`, `OOMScoreAdjust=900`) not capped at 1 GB; warm ≈1 min, cold ≈3 min.* |
| S9 | FE plugs | Download prebuilt `remoteEntry.js` bundles (GitHub Pages/Releases) and serve them from the Pi. Configurable (local build allowed). |
| S10 | BE plugs | Preferred: build a clinic image `FROM` upstream care image + `ADDITIONAL_PLUGS` on the Pi (upstream mechanism). Also: CI-prebuilt images per solution. Plug versions pinned to ref/sha; compatibility problem ignored for now. |
| S11 | Solutions | Curated presets ("CARE Clinic", "CARE Pharmacy", "CARE Janwar", "CARE HMIS", …) = plug set + FE/BE env defaults, defined in this repo. Our CI builds the FE plugs for each solution and serves them as assets. |
| S12 | Transport | HTTPS only, even on LAN. Three TLS modes to support: local CA, public cert via DNS-01 (our own small DNS/ACME service on a separate domain, operated by us — not OHC), Tailscale/Headscale. |
| S13 | Wizard | Served from the Pi. |
| S14 | Clients | Any device should ideally work; failures to resolve `.local` on some devices are tolerable (IP or Tailscale as fallback). |
| S15 | Multihost | Support `care_multihost_files_be`-style behaviour when the operator's setup implies more than one hostname. |
| S16 | Remote access | Opt-in; Tailscale set up by the wizard with instructions for keys. |
| S17 | Proxy | Caddy is acceptable if it is easier/more maintainable than nginx. |
| S18 | Backups | ≥1 destination mandatory; multiple destinations configurable; off-site recommended, not mandatory. v1 destination types: local filesystem, "rsync", S3-compatible. PITR with configurable WAL archive interval. Retention/RPO configurable. No downtime for backups. Files may be slightly out of sync with DB on restore (acceptable). |
| S19 | Disk encryption | Not now. Physical attack out of scope. |
| S20 | Recovery material | Whatever is easiest for non-technical operators. |
| S21 | Threat model | Care's auth wall + RBAC is the main control. No compliance mandates for now. |
| S22 | Agent | Go. Separate authentication from Care. Standalone project; `care_clinic` is inspiration only. |
| S23 | Operators | Doctors or a local IT person. |
| S24 | Updates | Operator chooses a tracked ref/sha at setup; optional auto-updates inside a configurable window; nobody pushes updates; Caddy serves a maintenance page during the downtime window. Rollback of the DB = restore the pre-update snapshot. No artefact signing. OS updates: simplest workable approach. |
| S25 | Image | Raspberry Pi OS Lite base; fully offline-capable first boot (pre-pulled images); publish to Raspberry Pi Imager via a custom repository JSON. |
| S26 | Installer | Generic-Linux script is secondary; target = anything with quadlet-capable podman. |
| S27 | Naming | mDNS name defaults to `care.local`; configurable in wizard and via image customisation. |
| S28 | Scale | Single node. HA later. |
| S29 | Telemetry | Opt-in; e.g. node-exporter/VictoriaMetrics → Grafana. |
| S30 | Names (O1) | Repo `github.com/rithviknishad/care-pi`; product name **care-pi** (CLI `care-pi`, default mDNS `care.local`). Images under `ghcr.io/rithviknishad/care-pi/*`, plug bundles on the repo's GitHub Pages. Domain for the DNS/ACME side service: `care-pi.rithviknishad.dev` (S38; older docs say `care-pi.dev`, read as the same). |
| S31 | "rsync" destination (O2) | Implemented as **SFTP** (P6 accepted). |
| S32 | v1 solutions (O6) | **CARE Clinic only.** Solution definitions must be trivial to add/edit (one directory + `solution.yaml`). Proposed v1 plug set in `03-solutions-releases-updates.md` §1.1 (derived from care_clinic's catalog). |
| S33 | Upstream track (O8) | **Configurable refs per component.** For `care`, `care_fe` and each plug, the installer/operator either *tracks* a ref (branch or tag — re-resolved at every update check) or *pins* a sha/digest. Solutions supply default refs; published solution locks are fully pinned sets we tested. Model in `03-solutions-releases-updates.md` §3.1. Background: upstream's semver line stopped in Feb 2025, so the Clinic default tracks `latest` (develop) until tags resume (`08-spike-log.md`). |
| S34 | License (O14) | You add it; not a planning input. |
| S35 | Network scope (O15) | v1: **ethernet only from boot until onboarding completes**. The agent's wizard/console must then be able to (a) set the same parameters cloud-init accepts from Imager (hostname, user, Wi-Fi, locale, …) and (b) add failover networks (Wi-Fi as backup for ethernet). *Implementation note, please confirm:* cloud-init consumes `user-data`/`network-config` once at first boot; post-onboarding changes are written by the agent as NetworkManager profiles (`nmcli`/D-Bus) from `config.yaml`, not by re-running cloud-init. |
| S36 | First contact (O3) | **Public-cert mode is the primary/default TLS mode.** Pis are expected to have internet, but the box must also run fully offline: one image, mode chosen at first contact (public-cert if internet is detected, local-CA otherwise) and pre-seedable via `user-data` at flash time — no second image. Both modes can be on at once (P10): public name as primary, `care.local` + local CA as the offline fallback. Non-negotiables from `07-first-contact-trust.md` §4: name-constrained CA, Imager-password claim, no HSTS on local-CA hostnames, port 80 = `301` only. Offline-mode first contact = TLS interstitial (option B) + USB-stick export + QR enrolment from a trusted device; no plaintext bootstrap page (keeps "HTTPS only" literal). *Override Q-b if you want the plaintext page back.* |
| S37 | Proposals | P1–P20 **accepted** as written, with one clarification on P11: Tailscale is strictly **opt-in** — never installed/enabled unless the operator turns on remote access. |
| S38 | Namespace | GitHub `rithviknishad/care-pi` (created private; flip to public when ready — GitHub Pages for plug bundles needs public on the free plan). DNS/ACME service domain: **`care-pi.rithviknishad.dev`** (clinic names become `<id>.care-pi.rithviknishad.dev`). |
| S39 | lumine artefacts | Anything lumine-specific stays out of the repo or is gitignored (`.gitignore` has `local/` and `*.lumine.*`). |
| S40 | Backup destination model (P5 amended after SP6) | **Option A accepted.** pgBackRest owns exactly one local encrypted repo (`/var/lib/care-pi/backup/pg`); `archive_command` depends only on local disk. Every operator-configured destination (local/USB, SFTP, S3) is a **restic** repository that snapshots {pg repo, versitygw files, `/etc/care-pi`} on its own timer — failure-isolated per destination; DR restore is two steps (restic pull, then `pgbackrest restore`). Option B (care-pi-owned WAL re-driver to remote pgBackRest repos) is the rejected alternative, kept in `02` §3 for the record. Resolves O10. |
| S41 | Go module layout (P19 amended) | `go.mod` is at the repo root (`module github.com/rithviknishad/care-pi`). Code stays under `agent/`. Templates stay under `quadlets/templates/`. Reason: `go:embed` cannot read files outside the module directory. |
| S42 | Firewall in Phase 1 | The installer writes an nftables allow-list table (`inet care-pi`: lo, established, ICMP, 22/tcp when sshd is active, 80, 443, 5353/udp) with **no default-drop policy**. A wrong rule on a headless Pi cuts SSH. Default-drop moves to Phase 7 (hardening). |
| S43 | Base image access | The ghcr packages `care-pi/postgres-pgbackrest`, `care-pi/caddy`, and `care-pi/care` are set to **public** when CI first publishes them. The repo can stay private. The Pi pulls without a token. |
| S44 | Firewall loader (S42 amended after the Phase 1 exit test) | The installer writes the table to `/etc/care-pi/firewall.nft` and loads it with its own unit `care-pi-firewall.service` (oneshot; start and reload `nft -f`; stop `nft delete table inet care-pi`; before `network-pre.target`; `WantedBy=multi-user.target`). The unit touches **only** the table `inet care-pi`. The installer does **not** enable `nftables.service`: its stop runs `nft flush ruleset`, and an `nftables` upgrade (possible through unattended-upgrades) restarts it, which removes the netavark and Tailscale rules. Policy per S42. |
| S45 | Persistent journal (Phase 1 exit test, finding 14) | The installer keeps the journal on disk: `/etc/systemd/journald.conf.d/50-care-pi.conf` with `Storage=persistent` and `SystemMaxUse=200M`, and `/var/log/journal` (root:systemd-journal 2755). Raspberry Pi OS keeps the journal in RAM by default (`Storage=volatile`), so `care-pi logs` lost the history at each reboot. |
| S46 | Agent account recovery (O9) | Console password (argon2id) **plus 8 one-time recovery codes** shown once in the wizard. `sudo care-pi auth reset` over SSH is the last fallback. |
| S47 | Django admin (O16) | **Not routed in v1.** Support uses `care-pi care …` (`manage.py` in `care-api`). Revisit when support needs it. |
| S48 | Console origin | The console SPA and its API are served on **port 8443** (`https://<host>:8443/`), not under `/console/` on 443. Reason: a same-origin console API lets an XSS in Care or a malicious FE plug act as the operator. Port 443 redirects `/console*` to 8443. Public endpoints (status, health, root CA) are on both ports. The pod publishes 8443 on each LAN IP; the firewall allows it. |
| S49 | Console sessions | 2 h idle, 24 h absolute. Server-side store; cookie `__Host-care_pi_session`. |

## B. PROPOSED — please confirm or override

Each item: what I propose, why, and the alternative I rejected.

**P1. Everything in containers, including Postgres — with one caveat on your Q2.**
You asked whether containers make Postgres backups *easier*. Honest answer: not easier, but not a blocker. The PITR tooling (pgBackRest or WAL-G) must run *inside* the Postgres container because Postgres itself invokes `archive_command`. That means we publish our own `postgres:17 + pgbackrest` image from CI (a 3-line Containerfile on top of the official image). Once that exists, the container approach *is* the more declarative one: the DB, its backup tool and its config are a pinned image + a rendered config file, and the restore procedure is identical on every device. Native Postgres would avoid the custom image but reintroduce apt state, host users and per-distro paths. Recommendation: containers.
*Alternative rejected:* "DIY PITR" (`pg_basebackup` + `cp`-based WAL archive + restic). Fewer images, but we would own WAL-retention and restore logic that pgBackRest already gets right.

**P2. One podman pod (`care.pod`) rather than a podman network.**
All containers share one network namespace, so services talk over `localhost:<port>` and no container DNS (aardvark) is needed. Each container is still its own systemd unit with its own `MemoryMax`, restart policy and logs. Only Caddy's 80/443 are published, on the pod.
*Alternative:* a named network with per-container DNS names, closer to upstream compose. Slightly more memory and one more moving part; no functional gain for a single-node box.

**P3. Caddy replaces nginx, and there is no frontend container.**
Caddy gives us `tls internal` (local CA), ACME with DNS-01 via a plugin module, Tailscale certificate integration, and a trivial maintenance/setup page switch — all in one config we render. The FE build output and plug bundles are plain files on a volume served by Caddy's `file_server`, so updating the FE = flip a symlink, no image rebuild. Caddy is built in our CI with the modules we need (custom build is unavoidable for DNS-01 anyway).

**P4. One declarative source of truth: `/etc/care-pi/config.yaml`.**
The agent renders it into quadlet units, env files, `Caddyfile`, `pgbackrest.conf`, timers, and a resolved **lockfile** (exact image digests, git shas, FE build id). `care-pi apply` is idempotent; the console and CLI only ever edit `config.yaml` and call apply. Rendered files are never hand-edited (header says so; drift is detected by re-render + diff). This is what makes the "update", "rollback", "restore to a new device" and "generic Linux installer" stories the same code path.

**P5. pgBackRest for the database, restic for files + config, one recovery key.**
pgBackRest: online incremental/differential/full backups, continuous WAL archiving, built-in AES-256 encryption, retention that keeps WAL consistent with retained backups, `verify`, PITR restore with one command. restic: encrypted, deduplicated snapshots of the versitygw data directory and `/etc/care-pi`, same destination types. The operator sees **one recovery key**; the agent derives the two tools' passphrases from it (HKDF). Details in `02-backups-and-restore.md`.
*AMENDED after SP6 (`08` SP6, `02` §3) → **SETTLED as S40 (Option A).*** pgBackRest's multi-repo archiving turned out to be all-or-nothing (one unreachable destination stalls WAL archiving to *all* of them and grows `pg_wal`), so "pgBackRest writes to every destination" is withdrawn. pgBackRest keeps exactly one local encrypted repo; every operator destination is a restic repository that snapshots {pg repo, files, config} on its own timer — failure-isolated per destination, one destination model, two-step DR restore. *Rejected alternative (Option B):* destinations stay pgBackRest repos fed by a care-pi re-driver decoupled from `archive_command` — direct remote restore, but a WAL re-driver we would own, and files would still need restic.

**P6. "rsync" destination = SFTP.**
Neither pgBackRest nor restic speak the rsync protocol; both speak SFTP, which reaches every host rsync-over-SSH would (NAS, another Linux box, a VPS). The agent generates an SSH keypair and shows the public key to paste into the destination. **If you mean an rsync *daemon* (`rsync://`) specifically, say so** — that would be implemented as "mirror the local repo with rsync", a second-class path.

**P7. Agent: Go, runs as root, listens on a unix socket, exposed by Caddy at `https://<host>/console/`.**
Root is needed to write quadlets, call `systemctl`, run `podman`, mount backup disks and manage the network. Mitigations in `04-security.md`. Auth: operator password (argon2id) + printable recovery codes, optional TOTP; sessions in HttpOnly cookies; rate-limited. The same binary is the CLI (`care-pi status|apply|backup|restore|update|logs|support-bundle`) for SSH-capable operators and for the installer. The console UI is React + careui components, embedded in the binary with `go:embed`. Path `/console` verified free in `care_fe` routes (`/admin` is taken by care_fe itself).

**P8. Boundary with Care's own onboarding.**
Agent owns infrastructure: hostname, TLS, sizing, env vars, backups, updates, plugs, remote access. At the end of setup it creates the first Care superuser (operator-chosen password, via `manage.py` inside the API container) and hands off to Care. Facility, staff, departments etc. stay in Care (via the CARE Onboarding plug if the solution includes it). The agent never writes to Care's database directly.

**P9. "HTTPS only" with exactly one plaintext exception: port 80 serves a `301` to `https://`.**
No wizard content, no certificate download over HTTP. Consequence: on the very first visit in local-CA mode the browser shows a certificate interstitial once; the operator clicks through, the console then offers the root certificate download + per-OS instructions (and shows the fingerprint). After installing it, no more warnings on that device. In public-cert mode the same interstitial appears only until the certificate is issued (needs internet). This avoids the trust-on-first-use plaintext download `care_clinic` accepted. **Confirm the one-time interstitial is acceptable.**

**P10. TLS/naming modes are composable, not exclusive.**
`local_ca` (default; offline-capable; `care.local`), `public` (`<clinic-id>.<our-domain>` → LAN IP; needs our DNS/ACME service and working internet DNS on every client), `tailscale` (`<name>.<tailnet>.ts.net`; remote access). Any combination can be on. The agent counts resulting hostnames and auto-enables the multihost files plug when > 1 (answers your Q17: a single-hostname setup does not need it; LAN + Tailscale does).

**P11. Tailscale runs on the host (apt package), not in a container; Tailscale SaaS by default.**
Host install makes `tailscale cert`, MagicDNS and subnet-free access straightforward; the clinic owns its tailnet (free tier covers 3 users/100 devices; more doctors ⇒ paid tier). Headscale (self-hosted control plane we would run) can be added later as an option. **Confirm SaaS-first.**

**P12. Clock policy as a boot gate.**
`care-clock.service` runs before `care.target`; policy `ntp` (default) waits for `systemd-timesyncd` sync; policy `rtc` passes if an RTC device exists, the agent has *verified* the battery (passively, by comparing RTC time at boot against NTP after a real power-off; see `01-architecture.md` §7) and current time ≥ the agent binary's build time. Until the gate passes, Caddy serves a "waiting for clock" page. Pi 4 has no RTC ⇒ `ntp` or an external I²C RTC module.

**P13. Sizing presets.**
`solo` (1 practitioner), `small-clinic` (2–4 staff), `busy-counter` (5–10 concurrent), `custom`. Each maps to gunicorn workers, celery concurrency, Postgres `shared_buffers`/`work_mem`/`max_connections`, and per-unit `MemoryMax`. Numbers are to be measured in Phase 0, starting from the table in `01-architecture.md` §8.

**P14. FE build provider abstraction.**
`local` (default): `node:22` container, pre-pulled in the image, shaped per SP4 (`MemoryHigh=768M`, `MemoryMax=1700M`, `MemorySwapMax=1500M`, `OOMScoreAdjust=900`), app services scaled down to one API worker and zero celery workers during the build, persistent per-repo build dir (`node_modules` + npm cache) keyed by `package-lock.json` hash, sourcemaps off unless opted in. `artifact`: download a prebuilt bundle from a URL (our CI's per-solution default build, or anything the operator points at). `remote-build` (future): a build service API. Same abstraction for FE plug bundles.

**P15. BE plug builds: CI-prebuilt solution image first, local `podman build` as the override path.**
Local build uses a quadlet `.build` unit (podman ≥ 5.2) with a two-stage Containerfile mirroring upstream's (`builder` stage installs `build-essential`+deps and runs `install_plugins.py`; runtime stage copies the venv) so plugs with compiled dependencies also work. `FROM` is always the pinned upstream digest.

**P16. Update orchestration = stage online, then a short cutover.**
Pull images and build FE/plugs while Care is running → maintenance page → stop app containers → pgBackRest incremental + archive push ("pre-update checkpoint") → start beat (runs `migrate`) → start API/workers → health check → write new lock. On failure: restore previous lock (images/FE are still present) and offer PITR to the checkpoint. State machine in `03-solutions-releases-updates.md`.

**P17. OS updates: `unattended-upgrades` (security pocket only) + reboot inside the update window; everything else via apt during `care-pi update --os`.** A/B root partitions (RAUC/ostree) rejected as too much work for v1.

**P18. Image pipeline.**
Raspberry Pi OS Lite (trixie, arm64; one image boots Pi 4 and Pi 5). Built in CI with `rpi-image-gen` (fallback `pi-gen`) by running the *same* installer that the generic-Linux path uses, plus pre-pulling container images with `skopeo` into `/var/lib/containers/storage`. First-boot work (secrets, filesystem expansion, hostname from Imager customisation) is a oneshot unit.

**P19. Repository layout** (`care-pi`):
```
agent/        Go module: daemon, CLI, renderer, jobs, API; embeds console/dist
console/      React + Vite + careui (shadcn registry) — setup wizard & control panel
quadlets/     templates for .pod/.container/.volume/.build/.image units, timers, Caddyfile
images/       Containerfiles we publish: postgres-pgbackrest, caddy, care (solution builds), fe-builder
solutions/    one directory per solution: solution.yaml, settings schema, defaults
installer/    install.sh (curl | sh), distro detection, prerequisites
image/        rpi-image-gen/pi-gen config, firstboot units, Imager os_list.json
docs/         operator + developer docs
.github/      workflows: agent release, images, plug bundles, solution locks, pi image
```

**P20. Solutions are released as calendar-versioned locks** (`2026.11.0`): a lock pins the care digest, care_fe sha, every plug sha, the FE bundle hash and the solution image digest. Pis on track `stable` follow the latest lock of their solution; track `pinned` stays; track `custom` lets advanced operators set refs themselves (and they own breakage).

## C. OPEN questions

| # | Question | Why it matters |
|---|---|---|
| O1 | ~~Namespace & names~~ → S30, S38. | — |
| O2 | ~~SFTP as "rsync"~~ → S31. | — |
| O3 | ~~First-contact trust~~ → S36. **Remaining from `07` §6:** Q-c name-constraint set (`.local` + Pi IP + `*.care-pi.rithviknishad.dev` proposed), Q-d require Imager OS customisation. Defaults taken unless you object. | — |
| O4 | **Tailscale SaaS vs Headscale** (P11). Who owns the tailnet — the clinic, or us on their behalf? | Account/billing UX in the wizard; whether we operate another service. |
| O5 | ~~Public-cert caveats~~ → S36 accepts them with public-cert as *primary*. The offline-resilience consequence: in public-cert mode the wizard should offer "also enable `care.local` with a local certificate so Care keeps working when the internet is down" (optional CA install, done at leisure from a trusted device). | — |
| O6 | ~~v1 solution list~~ → S32 (Clinic only). **Remaining:** confirm the proposed Clinic plug set (`03` §1.1): `care_onboarding_fe` (default on), `care_multihost_files_be` (auto when >1 hostname), `care_fasterappointments_fe` (optional), `care_filly` (optional, needs Medispeak). | Drives the CI matrix for bundle builds. |
| O7 | **Verified, it *is* host-bound today** — but only matters once multihost is on. `PluginEngine.tsx` validates `meta.url` with zod v4 `z.url()`, which rejects `/plugs/x/remoteEntry.js` and `//host/…` (tested: only absolute `https://host/…` passes). The value is stored per `PlugConfig` row in the DB, so a bundle URL of `https://care.local/...` is loaded cross-origin from `https://<name>.ts.net` and fails wherever `care.local` does not resolve (e.g. a doctor at home). **Plan per your answer:** single-hostname setups need nothing. When multihost is enabled, prefer the ~5-line upstream change (resolve `meta.url` against `window.location.origin` and accept root-relative paths); until merged, the agent sets `meta.url` to the primary hostname and Caddy adds CORS headers for the other hostnames as the stopgap (works whenever the primary name is resolvable). I will ask before opening the upstream PR. | Needed as soon as multihost is on. |
| O8 | ~~Upstream track~~ → S33 (semver). | — |
| O9 | ~~Agent recovery~~ → S46 (password + recovery codes; SSH reset fallback). | — |
| O10 | ~~Hot-pluggable USB backup disk~~ → resolved by S40: an absent stick is a destination whose restic timer fails and catches up by content when it returns; only the mount unit (`nofail`, automount) and "destination degraded" UX remain, both in Phase 3. | — |
| O11 | Is **Pi 4 2 GB** in the v1 acceptance matrix, or "should not break, tested later"? | FE build time and memory margins are much tighter there. |
| O12 | Telemetry destination: who hosts the VictoriaMetrics/Grafana side? | Determines whether the agent ships `remote_write` only or a hosted-service enrolment flow. |
| O13 | Maintainers and release cadence. Do you want Renovate/Dependabot to open PRs when upstream care/care_fe/plugs move, so locks are cut from PRs? | Shapes CI design from day one. |
| O14 | ~~License~~ → S34 (yours). | — |
| O15 | ~~Network scope~~ → S35 confirmed (NetworkManager profiles post-onboarding). Bond/failover approach still needs a SPIKE. | — |
| O16 | ~~Django admin~~ → S47 (not routed in v1). | — |
| O17 | Does the operator ever need **Care's API from the public-cert hostname while the LAN is down**? (i.e. is "internet up, LAN DNS down" a real scenario in target clinics?) | Decides whether to invest in on-Pi DNS/dnsmasq advice. |
