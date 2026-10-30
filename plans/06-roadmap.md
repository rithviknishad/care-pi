# 06 — Roadmap

Ordering principle: retire the risks that could invalidate the architecture first
(Phase 0), then get a plain Care running declaratively (Phase 1), then put the
operator in control (Phase 2), then make it safe to rely on (backups, Phase 3),
then make it maintainable (solutions/updates, Phase 4), then make it shippable
(image, Phase 5), then widen connectivity and polish (Phases 6–7).

Sizes are relative (S/M/L/XL) rather than dates; they assume one engineer with AI
assistance and lumine available. Each phase ends with a demo on lumine.

## Phase 0 — Spikes on lumine (de-risk) · size M

After you clear k3s/tailscale/cloudflared from lumine and `apt install podman skopeo`.

| # | Spike | Pass criterion | Fallback if it fails | Result |
|---|---|---|---|---|
| SP1 | 16 KB page kernel vs images: `redis:8-alpine`, `postgres:17`, `caddy`, `versity/versitygw`, `node:22`, `ghcr.io/ohcnetwork/care:latest` | each container starts and serves | our own redis build with `MALLOC=libc`; last resort 4 KB kernel (S3) | **PASS** — `08` SP1 |
| SP2 | podman 5.4 quadlet features: `.pod` with `PublishPort`, `.container` with `Pod=`, `Secret=`, `.image`, `.build`, `.volume`. **Also:** confirm the memory cgroup controller is active (`MemoryMax` is silently ignored otherwise). Stock Pi OS `cmdline.txt` ships `cgroup_disable=memory`; lumine currently has *both* `cgroup_disable=memory` and a trailing `cgroup_enable=memory` (left over from k3s). Our image must set `cgroup_enable=memory cgroup_memory=1` deliberately. | `systemctl start care.target` brings up the pod from generated files; `systemd-cgtop` shows memory accounting for the units | drop `.build` (shell out to `podman build`), drop pod (network) | **PASS** — `08` SP2; set `MemorySwapMax` too; lifecycle via `care.target` |
| SP3 | Care stack in the pod with presets `solo`/`small-clinic`: measure RSS per unit idle and under a small load script (login, patient create, file upload) | stays under 1.5 GB with zram; no OOM kills | adjust presets; `busy-counter` 4 GB-only | **PASS** — `08` SP3; CPU-bound not memory-bound; presets table in `01` §8 is now measured |
| SP4 | FE build in `node:22` container with `MemoryMax=1G` while app scaled down, on Pi 5 (and Pi 4 if available) | build completes; record time and peak RSS | provider `artifact` as default; lower `max-old-space-size` | **PASS with corrected budget** — `08` SP4; 1 GiB cap is killed, working set ≈1.8–2 GiB, fits via zram with a shaped unit; warm ≈1 min / cold ≈3 min; Pi 4 still to measure |
| SP5 | versitygw arm64 + Care presigned uploads/downloads through Caddy path routing | upload + view a file in Care | silo/minio image (what upstream ships) | **PASS** — `08` SP5; needs `VGW_REGION`=`BUCKET_REGION`, agent-created buckets, `default_sni` for IP access; browser UI not yet driven |
| SP6 | pgBackRest in a custom postgres image: archive-push to posix + SFTP + S3 (MinIO on your laptop) simultaneously; incr backup; PITR restore with `--delta`; measure RSS | restore to a point in time succeeds; `verify` passes | DIY `pg_basebackup` + restic path | **PASS on mechanics** (PITR exact, 8 s restore, 233 MiB peak, encrypted) but **multi-repo archive-push is all-or-nothing** — one dead destination stalls WAL to all. Design amended and **settled as S40**: one local pgBackRest repo + restic per destination (`02` §3) |
| SP7 | Caddy custom build (acme-dns module, Tailscale cert) with `tls internal` for `care.local` + raw IP, root supplied by the agent **with X.509 name constraints**; root CA install UX on iOS, Android, Windows, macOS; verify the browser facts F5–F7 in `07-first-contact-trust.md` (HTTPS-first fallback behaviour per browser, service-worker/API availability on a bypassed origin, bypass persistence, name-constraint enforcement on Apple) | each device reaches `https://care.local` without warnings after install; F5–F7 recorded per browser/version | document per-device caveats; recommend public-cert mode | pending |
| SP8 | mDNS resolution of `care.local` from Android (several versions), Windows 10/11, iOS, macOS | works or documented | IP/QR fallback already planned | pending. **Input from the Phase 1 exit test (macOS 27, lumine):** avahi publishes the A record and an AAAA record (the IPv6 ULA). The Mac prefers the AAAA (its resolver lists it first). The pod publishes 80/443 on IPv4 only, so the Pi answers the IPv6 SYN to 443 with an RST (`Connection refused`), and the fallback to IPv4 is fast: `curl https://lumine.local/ping/` 0.03–0.12 s. (`getaddrinfo` puts the A first, because RFC 6724 ranks a ULA below IPv4; an IPv6-only connect is refused after ≈ 1 s.) Tried and reverted: `use-ipv6=no` and `publish-aaaa-on-ipv4=no` (no AAAA). avahi sends no negative AAAA answer (avahi issue 428), so each macOS lookup for both families waited 2.7–5.0 s. **Follow-up:** publish 80/443 on the LAN IPv6 addresses too (pod `PublishPort`, Caddy sites, `DJANGO_ALLOWED_HOSTS`, `CSRF_TRUSTED_ORIGINS`), so that the AAAA record is valid. Measure browsers and the other OSes here. |
| SP9 | Pi 5 RTC passive battery verification; `fake-hwclock` interplay; timesyncd gating with `systemd-time-wait-sync` | gate behaves as specified in `01` §7 | `ntp` policy only in v1 | pending |
| SP10 | `rpi-image-gen` trixie Lite image with a chroot hook running `install.sh` and skopeo pre-pull; Imager customisation still works | image boots on Pi 5 and Pi 4; hostname customisation reaches avahi | `pi-gen` | pending |
| SP11 | NetworkManager bond eth+wlan (gist approach) on Pi | failover keeps IP | Wi-Fi as a secondary connection without bonding (later phase anyway) | pending |

Exit: a written results table replacing the *inference* marks in `01` §4/§8.

## Phase 1 — Declarative core on a fresh Pi OS Lite · size L

Deliverables:
- `agent/` skeleton: `config.yaml` schema, renderer, `care-pi apply|status|logs`, journaled jobs; no HTTP UI yet.
- `quadlets/` templates: pod, caddy, db (pgBackRest image), redis, s3, api/worker/beat, volumes, timers (backup timers disabled until Phase 3).
- `images/`: `postgres-pgbackrest`, `caddy`, CI workflows publishing them (arm64 + amd64).
- `installer/install.sh` for Pi OS/Debian: prerequisites + agent + `care-pi bootstrap`.
- Caddy: local CA, `care.local` + IP, routing table from `01` §5, status page states.
- FE: provider `artifact` first (CI builds a default care_fe bundle), provider `local` right after (SP4).
- Secrets generation; clock gate (`ntp` policy); zram; nftables; avahi.
- Solution image for one solution (`care-clinic` with zero or one plug) built in CI.

Exit criteria: on a freshly flashed Pi OS Lite (lumine), `curl | sh` → `https://care.local`
serves Care; login as a superuser created by `care-pi care create-superuser`; upload
a file; reboot the Pi → everything returns without intervention; `care-pi apply`
after editing `config.yaml` (e.g. preset) does the minimal restart.

## Phase 2 — Agent HTTP API + console v1 (setup wizard, control panel) · size L

Deliverables:
- `console/`: React + Vite + careui; embedded into the agent; served at `/console`.
- Auth: password + recovery codes (+ TOTP optional); sessions; rate limits.
- Setup wizard easy path (without backups page until Phase 3 — the step exists but
  is skippable **only in dev builds**); advanced path for sizing, env, hostname.
- Dashboard: health, resources, clock, network, cert download page with fingerprint.
- Jobs with SSE progress; logs viewer; support bundle.
- First Care superuser creation; hand-off link.
- CLI parity for everything the console does.

Exit: a non-technical tester completes setup from a phone and a laptop without the
terminal; `care-pi` CLI can do the same unattended.

## Phase 3 — Backups and restore · size L

Deliverables: destinations (posix incl. formatted USB disk, SFTP with generated key,
S3), recovery key UX, pgBackRest + restic rendering, timers, health per
destination, verify, same-device PITR from the console, files-only restore,
"restore from backup" branch of the wizard (new device), optional monthly restore
drill, disk-space guards.

Exit: golden-path e2e from `05` §7 passes on lumine with a local + an S3
destination; a second Pi (or the same one re-flashed) is rebuilt from backups +
recovery key with no other input.

## Phase 4 — Solutions, plugs, updates · size XL

Deliverables:
- `solutions/` format + at least two solutions (per O6); plug settings schema → console forms.
- CI: lock resolution, solution images, FE plug bundles → Pages, default FE bundles, `index.json`.
- Agent: tracks, update check/apply with window, staging, maintenance page, checkpoint, rollback, lock history.
- Local `.build` for custom BE plug sets; local FE plug build.
- `PlugConfig` management through Care's API; multihost auto-enable (depends on O7).
- OS security updates + reboot window; apt holds for podman stack.

Exit: a Pi on `stable` updates itself inside its window from lock N to N+1 with a
visible maintenance page and returns healthy; a forced migration failure rolls back
to N and offers PITR; switching a plug on/off in the console produces a working Care.

## Phase 5 — Pi image pipeline · size M

Deliverables: `image/` config (per SP10), CI building `.img.xz` per release with
pre-pulled images and pre-seeded bundles, `os_list.json`, release notes, hardware
matrix test checklist (Pi 5 USB/NVMe, Pi 4 2 GB/4 GB), QEMU smoke test in CI.

Exit: flash with Raspberry Pi Imager (custom repo), ethernet only, **no internet**,
reach a working Care through the wizard; then connect internet and receive an
update.

## Phase 6 — Connectivity: public certs, Tailscale, Wi-Fi · size L (+ separate DNS/ACME project)

Deliverables:
- Separate project: DNS/ACME service (registration, per-clinic credentials,
  DDNS A record, acme-dns-compatible TXT API) on the domain from O1; rate-limit and
  PSL plan (O5).
- Agent: `public` mode enrolment, Caddy ACME via acme-dns module, renewal
  monitoring; `tailscale` mode (auth key or login URL, MagicDNS name, `tailscale
  cert`), multihost consequences wired to Phase 4.
- Wi-Fi: NetworkManager secondary connection or bond (per SP11) configured from the console.

Exit: a phone on mobile data (Tailscale) and a laptop on the clinic LAN (public
name) both use the same Care without warnings; file links work from both.

## Phase 7 — Hardening and breadth · size M

Opt-in telemetry (O12); generic-Linux installer across Debian/Ubuntu/Fedora;
Coraza WAF option; agent privilege split (helper socket); optional lock signing;
Headscale option; documentation site for operators (per solution) and developers.

## Cross-cutting threads (run alongside)

- **Upstream PRs**: care_fe relative `meta.url` (O7); any care_fe build-memory or care start-time improvements discovered in SP3/SP4.
- **Docs**: operator guide grows with each phase (setup, certificate install per OS, backups, restore, updates, troubleshooting, hardware list).
- **Test rig**: lumine as the always-on dev Pi; a second Pi (ideally a Pi 4 2 GB) for the restore-to-new-device and image tests.

## Risk register

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| 16 KB page incompatibility in a required image | blocks Pi 5 default kernel | medium (redis/jemalloc) | SP1; own builds; 4 KB kernel fallback |
| Memory under load on 2 GB with 2 gunicorn + celery + PG + builds | OOM kills, slow UX | medium | SP3 presets; zram; scale-down during builds; `busy-counter` 4 GB-only |
| FE build infeasible on Pi 4 2 GB | config changes need internet/artifact | medium | artifact provider; O11 |
| Local-CA install UX too hard for operators | staff see warnings / give up | high for iOS | public-cert mode default when online (O5); QR + per-OS guides |
| Public-cert mode depends on internet DNS and our service uptime | LAN access breaks when uplink is down | medium | keep `local_ca` on in parallel; long TTLs; status page explains |
| Plug host-binding (`meta.url`) | multihost breaks plugs | certain without O7 | upstream change or rewrite plug |
| `rpi-image-gen` immaturity | image pipeline delays | medium | pi-gen fallback |
| Podman stack upgrade breaks containers | boot loop after OS update | low–medium | apt holds; our releases bump deliberately |
| Operator loses recovery key and device | data loss | low but severe | confirmation ceremony; persistent banner; printable sheet |
| Let's Encrypt rate limits as clinic count grows | enrolment failures | low early | PSL application; rate-limit increase request |

## Status (2026-10-30, after the Phase 2 exit test)

Phase 0: SP1–SP6 are done (results in `08-spike-log.md`, folded back into `01`/`02`/`03`/`05`);
SP7–SP11 are pending. All proposals in `00` §B are accepted (S37, S40); S41–S43 were added
during Phase 1. **Phase 1 code is complete** (agent, quadlet templates, images, solution,
installer, FE providers) and passes its tests on macOS and in Docker. **The Phase 1 exit test
passed on lumine** (wave 3, in-place clean of the spike stack, `08-spike-log.md` "Phase 1 exit
test", `09-handoff.md` §8d). A test on a freshly flashed Pi and the follow-ups in §8d are still to do.
**Phase 2 code is complete and its exit test passed on lumine** (wave 4c: a laptop and a phone each claimed a new
install and finished the setup in the console, and the CLI finished it unattended; 8 fixes; `08-spike-log.md` "Phase 2 exit test",
`09-handoff.md` §8f). Its follow-ups (agent memory cap, the boot apply of the NetworkManager hook, the phone
units table, the progress texts) and a first-contact test on the same LAN (SP7) are still to do.
The remaining open questions (`00` §C: O4, O6 plug set, O7 upstream PR timing, O9, O11, O12,
O13, O16, O17) each become blocking only in the phase noted there.
