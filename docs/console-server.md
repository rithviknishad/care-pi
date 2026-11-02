# The console server (`care-pi serve`)

This document describes the server side of the console as implemented in Phase 2 (wave 4a).
The contract is `docs/api/openapi.yaml` and `docs/api/README.md`. When this document and the contract
disagree, the contract wins.

## 1. Architecture

```text
browser ──https:443──▶ care-caddy ──▶ Care (care-api), the FE build, the status pages
   │                      │  /_care-pi/*      ─┐
   │                      │  /console*  → 308  │ unix socket /run/care-pi/api.sock
   └──https:8443─────────▶│  each request     ─┤
                                               ▼
                      care-pi.service: "care-pi serve" (root, on the host, not in the pod)
                        internal/server   HTTP: routes, auth middleware, CSRF, SSE, the SPA
                        internal/service  the operations (the HTTP handlers and the CLI call them)
                        internal/auth     account, claim, sessions, TOTP, rate limits
                        internal/setup    the setup record and the state machine
                        internal/jobs     job journals, events, the apply lock
                        internal/apply …  the Phase 1 packages (render, plan, apply, fe, host)
```

- The agent listens only on the unix socket `/run/care-pi/api.sock` (mode 0660, root). Caddy is its only client.
  The agent has no TCP listener, except the dev listener (section 9).
- Caddy runs in the pod. It sees the socket through the bind mount `Volume=/run/care-pi:/run/care-pi`
  of `care-caddy.container`. The mount is the directory, because the agent makes a new socket at each start.
- `care-caddy.container` has `Wants=care-pi.service` and `After=care-pi.service`. It does not need the agent:
  when the agent does not answer, Caddy gives `503` with a `Problem` (type `unavailable`).
- The HTTP handlers (`internal/server`) and the CLI commands (`cmd/care-pi`) call the same methods of
  `internal/service`. Thus each operation has the same effect from the console and from the CLI (CLI parity, `06` Phase 2).
- The agent serves the console SPA at `/` on port 8443. `console/embed.go` embeds `console/dist`.
  Without a console build, the agent gives a `503` page that tells how to build the console (`make console-build`).

## 2. `care-pi.service`

The installer writes `/etc/systemd/system/care-pi.service`, enables it, and starts it (installer step 13).
`care-pi apply` never writes this unit: an apply must not restart the process that runs the apply.
`care-pi bootstrap` enables and starts the unit when the file exists.

| Item | Value | Why |
|---|---|---|
| `ExecStart` | `/usr/local/bin/care-pi serve` | |
| `Type` | `notify` | The agent sends `READY=1` when the socket listens, and `STOPPING=1` at the stop (sd_notify datagram, no cgo). |
| `Restart`, `RestartSec` | `always`, `2` | |
| `TimeoutStopSec` | `30` | The agent closes the event streams, waits 10 s for the requests, then up to 20 s for the jobs. |
| `MemoryMax`, `MemorySwapMax` | `128M`, `32M` | `01` §8. The agent hashes one password at a time (argon2id, 19 MiB). At the start, `serve` gives the Go garbage collector a soft limit of half of `memory.max` of its cgroup (64 MiB); `GOMEMLIMIT` in the environment wins. The child processes (podman, skopeo, journalctl) count in the same cgroup: `skopeo inspect` 34 MiB, `podman exec` 27 MiB, `podman ps` 15 MiB (lumine, wave 4c). The earlier cap of 64M was too small for these children beside the hashes of logins (wave 4d measured the 128M cap: `plans/08-spike-log.md`, "Phase 2 exit test", "Follow-ups applied"). |
| `ProtectSystem` | `strict` | `04` §2. |
| `ReadWritePaths` | `/etc/care-pi /var/lib/care-pi /run /etc/containers/systemd /etc/systemd/system /var/lib/containers -/var/cache/containers -/etc/hosts` | `/run`: podman writes `/run/containers`, `/run/libpod`, and `/run/crun`. `/etc/hosts`: the setup finish writes the line of a new host name. |
| `ProtectHome`, `PrivateTmp`, `ProtectKernelTunables` | `read-only`, `yes`, `yes` | `04` §2. |
| `RestrictAddressFamilies` | `AF_UNIX AF_INET AF_INET6 AF_NETLINK` | `04` §2. |
| `After` | `network.target` | Not `network-online.target`: the agent reads the LAN addresses at each request, so the boot does not wait for the network. |

`ProtectControlGroups=` and `SystemCallFilter=` are not in the unit yet: `podman exec` writes the cgroup of the
container and needs more system calls. Phase 7 (hardening) adds them.

The agent runs `podman pull` in a transient unit (`systemd-run --wait --pipe --collect`): an image pull needs
more memory than the cap of the agent. The agent runs `podman exec` with `nsenter --target 1 --mount`, in the
mount namespace of the host: the sandbox gives the agent its own mount namespace, and podman mounts the root
file system of a container that starts after the agent only in the host namespace (containers/storage keeps its
overlay mounts private). Without `nsenter`, `podman exec --user postgres` (and an exec into `care-api`, whose image
user is `django`) fails with "unable to find user" (lumine, wave 4c). Each other host command runs in the agent process.

Logs: the agent writes one line for each request and each event to its standard output in the `key=value`
format of `log/slog` (no time: journald adds it). Read them with `care-pi logs agent` or
`journalctl -u care-pi.service`. The log has no secret: no password, claim code, recovery code, TOTP secret,
session id, or CSRF token.

## 3. Caddy routes

| Port | Path | Target |
|---|---|---|
| 80 | each path | `308` to `https://{host}{uri}` |
| 443 | `/_care-pi/*` | the agent socket. The agent answers only the public operations there (`status`, `health`, `tls/root-ca`, `tls/root-ca/mobileconfig`, `tls/instructions/{os}`). Each other path gives `404`. |
| 443 | `/console`, `/console/*` | `308` to `https://{host}:8443/` |
| 443 | `/api/*`, `/health/*`, and the other Care paths | Care, as in Phase 1 (`docs/apply.md`, "Caddy"). Caddy strips the console cookie before Care. |
| 443 | the two S3 buckets | versitygw. Caddy strips the console cookie before versitygw. |
| 8443 | each path | the agent socket, with `flush_interval -1` (the event streams pass at once). Caddy compresses only `/assets/*`. Caddy rejects a body larger than 1 MiB (`request_body`, `413 payload-too-large`). |

Port 8443 has the same site names and certificates as port 443 (`tls internal`, `default_sni`).
Caddy sends no HSTS on both ports. Port 8443 sends `X-Frame-Options`, `X-Content-Type-Options`,
`Referrer-Policy`, and `Permissions-Policy`.
Caddy replaces the `X-Forwarded-For` header of a client with the client address. The agent uses the last item
of the header for the rate limits and the session records. Caddy keeps the `Host` header with the port
(for example `care.local:8443`). The `Origin` check uses it.

When the agent does not answer (`502`, `503`, or `504` from the socket), Caddy gives `503` with
`Content-Type: application/problem+json`, `Retry-After: 5`, and the problem type `unavailable`.
For a body larger than 1 MiB on port 8443, Caddy gives `413` with the problem type `payload-too-large`.

The console cookie is not specific to a port, so the browser also sends `__Host-care_pi_session` to port 443.
Caddy strips it before Care: the proxies to Care (`@api`) and to versitygw (`@s3`) remove it from the `Cookie`
header and keep the other cookies. Thus Care and its plugs never get the session of the root agent.
The agent on port 443 (`/_care-pi/*`) gets the cookie, but its public operations do not use a session.

The pod publishes `8443` on each LAN address, the same as `80` and `443`. The firewall table `inet care-pi`
accepts `8443/tcp`.

## 4. Authentication and the claim

The agent implements `docs/api/README.md` sections 2 to 4. Facts of the implementation:

- **Account.** `/etc/care-pi/secrets/agent.json` (root 0600): the argon2id hash of the console password
  (PHC string; m = 19 MiB, t = 2, p = 1), the SHA-256 hashes of the 8 recovery codes, the TOTP secret,
  the claim record, and the account lock. The agent hashes one password at a time.
- **Claim code.** `/etc/care-pi/secrets/claim_code` (root 0600): 12 Crockford base32 characters in 3 groups,
  for example `4F7K-Q2MX-9PDA`. The agent accepts lower case and the letters `I`, `L`, and `O` for `1`, `1`, and `0`.
  `care-pi bootstrap` makes the code when the device is not claimed. The claim deletes the file.
  When bootstrap makes the code, it also writes the claim hint `/var/lib/care-pi/state/claim-hint` (`claim-code`):
  bootstrap prints the code, so the operator has it. The hint is not in `setup.json`, because a new setup record
  changes the state machine of a Phase 1 host. The claim deletes the hint. `care-pi bootstrap --from-image` (the first
  boot of the Raspberry Pi image, `docs/image.md`) makes the code but writes no hint: no terminal shows the code there.
  `sudo care-pi auth claim-code` prints the code and the console URLs. `--rotate` makes a new code.
- **Imager password** (`os-password`). The agent reads the user name from the cloud-init `user-data` of
  Raspberry Pi Imager (`/boot/firmware/user-data`: the key `user:` of Imager 2.x, or the list `users:`), then compares
  the password with the hash of that user in `/etc/shadow`: yescrypt (`$y$`, what Imager 2.x writes for each OS of 2023
  or later) or SHA-crypt (`$5$`, `$6$`). `claim_methods` lists `os-password` only when such a user exists.
  `os-password` is the first method, except when the claim hint is `claim-code`. The console shows the field of the
  first method. When the operator types a claim code (`XXXX-XXXX-XXXX`) in the Imager password field, the console
  shows the claim code field.
  Spike SP10 read the user-data generator of rpi-imager and tested the method in a container boot of the image
  (`image/scripts/boot-test.sh`); a test on a Pi that Imager flashed is still to do. The code is behind the interface
  `auth.OSPassword`.
- **Clock from the browser** (S52, `docs/api/README.md` section 16). The claim body has `client_time`. After the claim
  secret is correct, and before the account and the session, `auth.Manager.ClaimWith` calls `Service.ClockFromBrowser`:
  when NTP did not synchronize the clock and the time is plausible (after the build time of the agent, and before 10
  years after it), the agent runs `date -u -s @<seconds>` and `fake-hwclock save` in the transient unit
  `care-pi-clock-set`, writes `state/clock-trusted.json`, and reloads Caddy 2 s later. `GET /status` and
  `GET /_care-pi/status` give `clock.source` and `clock.trusted`; a status read with a synchronized clock deletes the
  record. The console shows "Clock not trusted" in the top bar until then.
- **Sessions.** `/var/lib/care-pi/state/sessions.json` (root 0600) has the SHA-256 of each session id. A restart
  of the agent keeps the sessions. Idle timeout 2 h, absolute timeout 24 h (S49), 10 sessions at most.
  The cookie is `__Host-care_pi_session=<id>; Path=/; Max-Age=86400; HttpOnly; Secure; SameSite=Strict`.
- **CSRF.** Each unsafe request with a session needs `X-CSRF-Token` (from `GET /session`, claim, login, or recover).
  Each unsafe request, also claim, login, and recover, needs `Origin: https://<served host>:8443`.
  A served host is a host name of `config.yaml` (the local name, and in Phase 6 the public name and the Tailscale name),
  a LAN address, an address of the rendered `care.pod` (also the Tailscale address), or a site name of the rendered
  Caddyfile. So `https://<id>.care-pi.rithviknishad.dev:8443` and `https://<host>.<tailnet>.ts.net:8443` pass. The last item keeps the console on the old name after the identity step
  writes a new name and before the finish applies it.
  An unsafe request with a body needs `Content-Type: application/json` (`application/merge-patch+json` for
  `PATCH /config`). A request without a body needs no content type.
- **Sec-Fetch-Site.** Each request to an operation with a session, also a `GET` and an `EventSource`, must not
  have `Sec-Fetch-Site` with a value other than `same-origin` or `none`. Else the agent gives `403 csrf`.
  Port 443 and port 8443 are the same site, so `SameSite=Strict` does not stop a page on port 443 that starts
  a `GET` with a cost (for example `GET /host/check?probe=true` writes 256 MB, and `GET /apply/plan?update=true`
  holds the apply lock). The console sends `same-origin`, and a URL that the operator types gives `none`.
  A client without the header (curl) passes. The agent does this check before it reads the session, so a
  rejected request does not move the idle timeout.
- **Request bodies.** The agent checks a request in this order: `Origin` (unsafe requests), `Sec-Fetch-Site` and
  the session (operations with a session), the CSRF token, the reauthentication, then the body. Thus a request
  without a session never makes the agent read more than 8 KiB. The body limits:

  | Operations | Limit |
  |---|---|
  | `POST /session/claim`, `/session/login`, `/session/recover`, `/session/reauth`, `/security/totp/verify` | 8 KiB |
  | `PUT /config`, `PATCH /config`, `PUT /setup/steps/env`, `PUT /setup/steps/tls` (they hold a part of `config.yaml`) | 1 MiB |
  | Each other unsafe operation | 64 KiB |

  A larger body gives `413 payload-too-large`. The agent reads a body for 10 s at most. A body that does not
  arrive in 10 s gives `400 malformed-request` (the catalogue has no problem type for `408`), and the agent closes
  the connection. The server has `ReadHeaderTimeout` 10 s, `IdleTimeout` 2 min, and `MaxHeaderBytes` 64 KiB.
  It has no `ReadTimeout`: net/http also applies it to the background read during a handler, and that
  timeout ends the event streams.
- **Rate limits and locks.** The table of `docs/api/README.md` section 4. The request limits are in memory.
  The locks of the account are in `agent.json`. The agent writes the claim lock, the address locks, and the failed
  attempts of each address and of the claim to `/var/lib/care-pi/state/auth-locks.json` (root 0600) under the file
  lock of the account. A restart reads the file and removes the expired entries, so a restart of the agent does not
  remove a lock (`07` §4.2: 5 attempts, then 15 minutes). The file keeps 1024 addresses at most for each map.
  `sudo care-pi auth unlock` removes each lock: the CLI writes `/var/lib/care-pi/state/auth-unlock` and an empty
  `auth-locks.json`, and the agent reads the unlock time at the next request.
- **Events.** The agent writes each auth event (claim, login, failure, lock, recover, password change, TOTP change,
  session revoke) to the journal with the address and the user agent.

The CLI commands of the account (as root, on the host):

| Command | Effect |
|---|---|
| `care-pi auth claim-code [--rotate]` | Print the claim code (or a new code) and the console URLs. |
| `care-pi auth set-password --password-stdin` | Set the console password. On an unclaimed device it also claims the device and prints 8 recovery codes. |
| `care-pi auth reset --password-stdin` | Last fallback (S46): set the password, make 8 new recovery codes, turn TOTP off, end each session. |
| `care-pi auth recovery-codes --regenerate` | Make 8 new recovery codes. |
| `care-pi auth totp enrol`, `care-pi auth totp disable` | Turn TOTP on (print the secret, then read the first code) or off. |
| `care-pi auth sessions [--json]`, `care-pi auth sessions revoke REF` | List the sessions, or end one. |
| `care-pi auth unlock` | Remove each lock. |
| `care-pi auth status --json` | The `SecurityStatus` object of `GET /security` (no secret). |

## 5. Setup mode

`care-pi bootstrap` chooses the apply phase:

| Host | Phase | Result |
|---|---|---|
| New host (no setup record, no `lock.yaml`) | `bootstrap` (setup mode) | bootstrap writes the setup record with `apply_phase: bootstrap`. Apply renders and starts only `care.pod`, `care-caddy`, and the start units. Caddy serves the setup page on port 443 and the console on port 8443. Care does not start. |
| Setup record with `apply_phase: bootstrap` and no `ready_at` | `bootstrap` | The same. A second bootstrap is safe. |
| Host of Phase 1 (`lock.yaml`, no setup record) | `full` | A full apply. After the claim, the setup state is `ready`. |
| `bootstrap --unattended` | `full` | A full apply now (the Phase 1 behaviour). No console setup. The installer passes `--unattended` when it has `--unattended`. |

At the end, bootstrap prints the claim code in a box with the console URL of each host name and LAN address.

`care-pi apply` without `--phase` uses the phase of the host (`setup.Phase`): `bootstrap` until the setup is
finished, then `full`. `--phase bootstrap|full` overrides it.

The state machine is `internal/setup` (`docs/api/README.md` section 5). The record is
`/var/lib/care-pi/state/setup.json` (root 0600). Each step `PUT` validates its values and writes them into
`config.yaml`. The setup record keeps the step states, the Care superuser name and email, and the job id.
The Care superuser password is only in the memory of the agent.

`POST /setup/finish` (and `care-pi setup finish`) starts a job of kind `setup`. It holds the apply lock and runs:

1. `validate: config.yaml`, then `host-check: required checks`.
2. `set-hostname`, only when `identity.hostname` is not the OS host name: `hostnamectl set-hostname NAME`,
   the `127.0.1.1` line of `/etc/hosts`, then `systemctl try-restart avahi-daemon.service`.
   The browser must open the new URL. The job result has it. The IP URL works too.
3. A full apply (`apply_phase` becomes `full`): images, units, buckets, stanza, FE build.
   When the lock needs the network (a new install, or `update`), the first step of the apply is
   `resolve-lock: image digests and git refs`: one `log` event for each image digest or git sha, for example
   `resolve ghcr.io/ohcnetwork/care:latest … 77fe743e51a6` (about 25 s on lumine). An apply job of the console
   (`POST /apply`, `PUT /config?apply=true`) shows the same step. Without a network resolution there is no such step.
4. `wait-healthy: care-api.service` (10 minutes at most).
5. `create-care-superuser: NAME`. When the user exists, the job keeps it.

On success the record gets `ready_at`, and the state is `ready`. On failure the state is `setup` again, and
`GET /setup` gives the `job_id` of the failed job. At its start, the agent marks each `running` job without a
process as `failed` ("interrupted"). An apply is idempotent, so the operator can finish again.

Only a dev build can skip the `backups` step. A dev build has a version that is not `vX.Y.Z`
(`Makefile`: `git describe --tags --match 'v[0-9]*'`). Thus a release build of Phase 2 cannot finish the setup.

### Setup on the host

The CLI can do the full setup without a browser:

```sh
printf '%s\n' 'the console passphrase' | sudo care-pi auth set-password --password-stdin
sudo care-pi config set identity.display_name='Sunrise Clinic' identity.hostname=care size.preset=small-clinic \
  tls.local_ca.enabled=true
sudo care-pi setup skip env
sudo care-pi setup skip backups   # only a dev build
printf '%s\n' 'the care superuser passphrase' | sudo care-pi setup finish \
  --care-username admin --care-email admin@clinic.example --care-password-stdin
sudo care-pi setup status
```

As root, `setup finish` counts `account` (after `auth set-password`), `recovery-codes`, and `hardware` as done.
`config set` marks the step of each key that it sets as done: `identity`, `tls`, `size`, and `env` (`env` and `fe` keys).
The `tls` step needs a `tls.*` key, also when the value does not change (lumine, wave 4c: without it, `setup finish`
stopped with `missing steps: tls`).

## 6. Jobs and the event stream

- A job is the journal `/var/lib/care-pi/state/jobs/<id>.json` (the Phase 1 format with more members) and its
  events `<id>.events.jsonl`. The id is `<UTC time>-<kind>`, for example `20261030T101500.123Z-apply`.
  The CLI and the agent write the same files, so `GET /jobs` and `care-pi jobs list` show the jobs of both.
- The kinds `apply`, `setup`, `fe-build`, `fe-prune`, `restore-pitr`, `restore-files`, `restore-dr`, `disk-prepare`, and
  `recovery-key-rotate` hold the apply lock `/run/care-pi/apply.lock`. The backup kinds (`backup-run`, `backup-verify`,
  `restore-drill`) hold the lock of their target (`/run/care-pi/backup-<id>.lock`, `backup-local.lock`) and wait in the
  state `queued` for it (`docs/backups.md`, section 6).
  Phase 4 adds `update-stage`, `update-apply`, `update-rollback`, `os-update`, and `plug-change` to the apply lock.
  `update-check` does not hold it: a check changes no file that apply reads.
  The agent waits 5 s for the lock, then gives `409 job-running` with `job_id`
  (`null` when a CLI command without a journal holds the lock).
- Cancel: `POST /jobs/{id}/cancel` and `care-pi jobs cancel ID` write `<id>.cancel`. Only `fe-build`,
  `support-bundle`, `backup-run`, `backup-verify`, `restore-drill`, and (Phase 4) `update-stage` jobs stop. Another job
  gives `409 job-not-cancellable`.
- The agent keeps the last 100 jobs and each job of the last 30 days, and up to 20000 events for each job.
- `GET /jobs/{id}/events` with `Accept: text/event-stream` gives SSE: `retry: 3000`, then each event with
  `id:`, `event:`, and `data:` (one line of JSON), then `done` and the end of the stream.
  A `heartbeat` (no `id:`) comes every 15 s. `?after=N` or `Last-Event-ID: N` resumes after event `N`
  (the header wins). Without `Accept: text/event-stream` the same URL gives one `JobEventPage`.
- `GET /logs/{unit}?follow=true` with `Accept: text/event-stream` follows the journal (`journalctl --follow`).
  The event id is the journal cursor. The units are the units of `care-pi logs`, and `agent` (`care-pi.service`).
- A session has 4 open streams at most, and the agent 16. When the session ends, the agent closes its streams.
- The job `restore-dr` ends each session after the event `done`. It first waits until each open stream of the job sent
  `done` (5 s at most; `jobs.Spec.AfterDone`, `Manager.Watch`). The result has `display_name`, because the console cannot
  read `GET /config` after that.
- A timer run of a destination snapshot writes no job (`docs/backups.md`, section 4). The other backup timers write
  a job with `source: timer`.
- `care-pi jobs follow ID` prints the same events on a terminal.

## 7. File locations

| Path | Content | Owner, mode |
|---|---|---|
| `/usr/local/bin/care-pi` | The agent and the CLI | root 0755 |
| `/etc/systemd/system/care-pi.service` | The unit of the agent (installer) | root 0644 |
| `/run/care-pi/api.sock` | The socket of the agent | root 0660 |
| `/run/care-pi/apply.lock` | The apply lock | root 0600 |
| `/etc/care-pi/secrets/agent.json` | The console account: password hash, recovery code hashes, TOTP secret, locks | root 0600 |
| `/etc/care-pi/secrets/claim_code` | The claim code, until the claim | root 0600 |
| `/var/lib/care-pi/state/sessions.json` | The session records (SHA-256 of each id) | root 0600 |
| `/var/lib/care-pi/state/setup.json` | The setup record | root 0600 |
| `/var/lib/care-pi/state/auth-unlock` | The unlock request of `care-pi auth unlock` | root 0600 |
| `/var/lib/care-pi/state/auth-locks.json` | The claim lock, the address locks, and the failed attempts | root 0600 |
| `/var/lib/care-pi/state/claim-hint` | The claim method that the operator has (`claim-code` after bootstrap), until the claim | root 0600 |
| `/var/lib/care-pi/state/clock-trusted.json` | S52: the clock that the browser set at the claim, until NTP synchronizes the clock | root 0600 |
| `/var/lib/care-pi/state/jobs/<id>.json`, `<id>.events.jsonl`, `<id>.cancel` | Job journals, events, cancel requests | root 0600 |
| `/var/lib/care-pi/state/support/` | The support bundles (`.tar.gz`, redacted) | root 0700 |
| `/etc/care-pi/secrets/recovery.key`, `recovery.key.pending` | The recovery key (64 hex digits). Never in a response, except `GET /backups/recovery-key` and its download. | root 0600 |
| `/etc/care-pi/secrets/backup/<id>.json` | The S3 keys of a destination | root 0600 |
| `/var/lib/care-pi/state/backups/` | `key.json`, `local.json`, `destinations/<id>.json`, `drill.json`, `checkpoints.json` (`docs/backups.md`) | root 0700 |
| `/run/care-pi/backup/` | The files of one restic run (password, SSH key, S3 keys); deleted after the run | root 0700 |
| `/var/lib/care-pi/ca/root.crt`, `root.key` | Phase 6: the name-constrained root CA of the agent (`/tls/root-ca`; `docs/connectivity.md`) | root 0644, 0600 |
| `/var/lib/care-pi/caddy/caddy/pki/authorities/local/root.crt` | The root CA of Caddy. The agent reads it for `/tls/root-ca` only before the Phase 6 switch | root |
| `/var/lib/care-pi/state/{ca.json,tls-public.json,tls-tailscale.json,wifi.json,pod-live.json}` | Phase 6: the CA switch, the registration with the DNS service, the last Tailscale and Wi-Fi state, the addresses of the pod that runs | root 0600 |
| `/etc/care-pi/secrets/{dns_client.key,dns_client.key.pending,acme_dns.json}` | Phase 6: the Ed25519 key of the DNS client (and the new key of `tls public rotate --new-key` until the service takes it), the credentials of acme-dns. The Wi-Fi passphrase is only in the keyfile of NetworkManager; the Wi-Fi jobs delete the `wifi.json` of an older agent | root 0600 |
| `/run/care-pi/tailscale-auth-key` | Phase 6: the auth key while `tailscale up` runs; deleted after the step | root 0600 |
| `/var/lib/care-pi/state/{updates.json,update-history.jsonl,update-cache/,staging/,plug-config.json,images.json}` | Phase 4: the update state (`docs/solutions.md` section 6.4) | root 0600, directories 0700 |
| `/etc/care-pi/secrets/plug-<id>.json` | Phase 4: the secret settings of one plug | root 0600 |

## 8. CLI parity

`docs/api/README.md` section 11 lists the CLI command of each operation. Phase 2 adds `serve`, `auth …`,
`setup status|skip|finish`, `config show|set|presets`, `jobs list|show|follow|cancel`, `support-bundle`,
`tls show|root-ca`, `care info`, `power reboot|shutdown`, and the flags `host check --probe`,
`logs --since --json`, `logs agent`, and `fe status --json`. The `--json` output of a command is the JSON
object of the operation. Phase 3 adds `backup …` and `restore …` (`docs/backups.md`, section 10) and `logs backup`.

## 9. The dev listener (for the console developer)

The dev listener runs the agent on a laptop without podman and systemd:

```sh
go build -o bin/care-pi ./agent/cmd/care-pi
bin/care-pi serve --root ./.dev-root --listen tcp://127.0.0.1:8443 --insecure-dev --fake-host
```

- `--insecure-dev` serves plain HTTP. It accepts only a loopback address. It needs `--root` with a dev directory.
  `serve` checks these flags before it opens the socket.
- `serve` does not remove a socket that another `care-pi serve` answers on. It stops with the error
  "another care-pi serve answers on PATH". It replaces a socket without a server.
  An empty root gets the directories, the secrets, a `config.yaml` (`fe.provider: none`), a setup record
  in setup mode, and a claim code. The agent prints the claim code on the standard error.
- The session cookie is `care_pi_session` without `Secure` (a `__Host-` cookie needs HTTPS).
- The `Origin` check accepts `http://<Host header>`. `--allow-origin ORIGIN` adds one more origin.
- `--fake-host` answers the host commands (`systemctl`, `podman`, `skopeo`, `journalctl`) with plausible
  output and changes nothing on the laptop. Each unit is active, each container is healthy.
  The backup commands also answer: the local repository has one full and one incremental backup, `lsblk` shows
  the SD card and one empty USB disk, the probe of a `posix` destination succeeds, and each restic run succeeds
  with two snapshots. The tests of `sftp` and `s3` destinations connect to the real servers.
  Without `--fake-host` the agent runs the real commands.
- The Vite dev server of the console sends `/api` and `/_care-pi` to the agent:
  `CARE_PI_API=http://127.0.0.1:8443 npm run dev` (in `console/`). The proxy sets `Host` and `Origin`
  to the target, so the check accepts the requests.
- Never use `--insecure-dev` on a clinic device.

### curl walkthrough

Start the agent as above (terminal 1). Then, in terminal 2 (needs `curl` and `jq`):

```sh
A=http://127.0.0.1:8443
O="Origin: $A"
J='Content-Type: application/json'
CODE=$(cat .dev-root/etc/care-pi/secrets/claim_code)

# 1. The public status. No session.
curl -s "$A/_care-pi/status" | jq -c '{setup_state, claim_methods, console_port}'
# {"setup_state":"unclaimed","claim_methods":["claim-code"],"console_port":8443}

# 2. Claim: the claim code and the new console password. The response has the 8 recovery codes.
curl -s -c jar -H "$O" -H "$J" \
  -d "{\"method\":\"claim-code\",\"secret\":\"$CODE\",\"new_password\":\"sunrise clinic console 2026\"}" \
  "$A/api/v1/session/claim" > claim.json
jq -c '{codes: (.recovery_codes | length), setup_state: .session.setup_state}' claim.json
# {"codes":8,"setup_state":"claimed"}

# 3. The session: read the CSRF token. Each unsafe request sends it.
CSRF=$(curl -s -b jar "$A/api/v1/session" | jq -r .csrf_token)
H="X-CSRF-Token: $CSRF"
send() { curl -s -b jar -H "$O" -H "$J" -H "$H" -X "$1" ${3:+-d "$3"} "$A/api/v1$2"; }

# 4. The status with the session.
curl -s -b jar "$A/api/v1/status" | jq -c '{setup_state, dev_build: .agent.dev_build}'
# {"setup_state":"claimed","dev_build":true}

# 5. An apply before the setup gives 409 setup-incomplete.
send POST /apply '{"reason":"walkthrough"}' | jq -r .type
# https://care-pi.rithviknishad.dev/problems/setup-incomplete

# 6. The setup steps, then the finish.
send PUT /setup/steps/recovery-codes "{\"confirm_code\":\"$(jq -r '.recovery_codes[0]' claim.json)\"}" > /dev/null
send PUT /setup/steps/hardware '{"acknowledged":true}' > /dev/null
send PUT /setup/steps/identity '{"display_name":"Dev Clinic","hostname":"care"}' > /dev/null
send PUT /setup/steps/tls '{"local_ca":{"enabled":true}}' > /dev/null
send PUT /setup/steps/size '{"preset":"small-clinic"}' > /dev/null
send POST /setup/steps/env/skip > /dev/null
send POST /setup/steps/backups/skip > /dev/null
send PUT /setup/steps/care-admin \
  '{"username":"admin","email":"admin@clinic.example","password":"a care superuser passphrase"}' | jq -c '{state, can_finish}'
# {"state":"setup","can_finish":true}
JOB=$(send POST /setup/finish | jq -r .id)

# 7. Follow the setup job (SSE). The stream ends after the done event.
curl -sN -b jar -H 'Accept: text/event-stream' "$A/api/v1/jobs/$JOB/events?after=0" | grep -c '^event:'
curl -s -b jar "$A/api/v1/setup" | jq -c '{state, ready_at}'
# {"state":"ready","ready_at":"2026-10-30T14:39:45.347Z"}

# 8. An apply job. Resume its events after event 3 (SSE), then read the job.
JOB=$(send POST /apply '{"reason":"walkthrough"}' | jq -r .id)
curl -sN -b jar -H 'Accept: text/event-stream' -H 'Last-Event-ID: 3' "$A/api/v1/jobs/$JOB/events" | sed -n '3,5p'
# id: 4
# event: log
# data: {"time":"…","step_index":null,"stream":"agent","line":"no changes"}
curl -s -b jar "$A/api/v1/jobs/$JOB" | jq -c '{kind, state, result}'
# {"kind":"apply","state":"succeeded","result":{"kind":"apply","changed":false,"lock_changed":false,"fe_build_id":null}}

# 9. Log out.
curl -s -o /dev/null -w '%{http_code}\n' -b jar -H "$O" -H "$H" -X DELETE "$A/api/v1/session"
# 204
```

Stop the agent with Ctrl-C. To start again from an unclaimed device, remove `.dev-root`.

## 10. Tests

- `agent/internal/server/*_test.go` send each request through `httptest` and check the request and the response
  against `docs/api/openapi.yaml` with kin-openapi (`internal/server/contracttest`): the schemas, the problem types
  of `x-problems`, `Cache-Control`, no CORS header, the SSE frames, and `Retry-After`.
  `TestMain` fails when a test does not check each operation of the contract. `go test -v` prints the statuses
  that the tests checked for each operation.
- The flow tests: claim, recovery codes, CSRF, the steps, finish, ready; a failed finish and a second finish with
  a new host name; a Phase 1 host; the lockout with a fake clock; SSE resume and heartbeat; `412` and `428`;
  the `Origin` check; the operations of later phases (`501`).
- `backups_test.go` (Phase 3): the key ceremony, the destination tests (TOFU, the SSH key, the bucket), the disks, the
  step `backups`, `SetupState.restore` and the restore from backup in setup, each job of section 13 of the contract with
  its problems, the reauthentication, the lock between the restores and the backups, and the end of the sessions after
  the event `done` of `restore-dr`.
- Phase 4: `updates_test.go` checks each operation of `docs/api/README.md` §14 (`/updates*`, `/plugs*`,
  `/solutions*`) with success and problem responses, on a fake host with a local `file://` index of two locks
  (`internal/update/updatetest`). `maintenance_test.go` checks `maintenance{}` in both statuses.
- The body limits, a slow body, `Sec-Fetch-Site`, and a live socket (`limits_test.go`).
- `internal/auth`, `internal/jobs`, and `internal/setup` have unit tests. `internal/auth` also checks that the
  locks stay after a restart and that the claim hint orders `claim_methods`. `agent/internal/render` has the golden
  tree `small-clinic-bootstrap` (setup mode).

## 11. Phase 4: updates, plugs, and solutions

The routes of `docs/api/README.md` §14 are implemented (wave 6a). `agent/internal/service/updates.go` holds the
logic of each operation, and `agent/internal/server/updates.go` the handlers. The update machine is the package
`agent/internal/update` (`docs/solutions.md` sections 7 to 12).

| Operation | Job kind | Notes |
|---|---|---|
| `GET /updates`, `GET /updates/history` | — | Reads `state/updates.json`, the cached index, and the history. No network. |
| `POST /updates/check` | `update-check` | A running check is returned as it is (`202` with the same job). With `updates.auto`, a check that finds a target that needs no local build starts `update-stage`. |
| `POST /updates/stage` | `update-stage` | Care stays up. Cancellable. |
| `POST /updates/apply` | `update-apply` | Without `now: true`, outside the window: `409 update-window-closed` with `next_window_at`. |
| `POST /updates/rollback` | `update-rollback` | Recent authentication. `409 rollback-unavailable` without a previous lock. |
| `GET /updates/os`, `POST /updates/os/apply`, `POST /updates/os/reboot` | `os-update` | Recent authentication for apply and reboot. apt runs in the transient unit `care-pi-apt`. |
| `GET /plugs`, `PUT /plugs/{plug_id}`, `GET /plugs/{plug_id}/settings-schema` | `plug-change` | `PUT` writes `solution.plugs.<id>` and `secrets/plug-<id>.json`, and returns the plan (`dry_run=true` writes nothing). `apply=true` starts the job. |
| `GET /solutions`, `/solutions/{name}`, `/solutions/{name}/locks/{version}` | — | The embedded solutions and locks, the update cache, and the cached index. |

`GET /status` (both forms) has `updates` (the summary) and `maintenance` (`active`, `job_id`, `kind`, `step`,
`started_at`). The maintenance page reads the public form (`GET /_care-pi/status`).

The sandbox of `care-pi.service` does not change in Phase 4. The agent runs the work that needs more rights or memory
in units outside its sandbox: apt (`systemd-run --unit care-pi-apt`), the write of `/etc/apt/apt.conf.d/52care-pi.conf`
(`systemd-run … tee`), the local image build (`care-build-<solution>-build.service`), and the FE plug build
(`care-plug-build.service`). `systemctl reboot` is a D-Bus call to systemd.

At start, `care-pi serve` marks interrupted jobs, then runs `RecoverUpdates`: a cutover that a power loss
interrupted rolls back (`docs/solutions.md` section 8.6).

## 12. Phase 6: connectivity

The routes of `docs/api/README.md` §15 are implemented. `agent/internal/service/connectivity.go` holds the logic, and
`agent/internal/server/handlers.go` the handlers. The packages are `agent/internal/ca` (the root CA),
`agent/internal/dnsclient` (the client of the DNS/ACME service), `agent/internal/tailscale`, and `agent/internal/wifi`.
`docs/connectivity.md` is the specification.

| Operation | Job kind | Notes |
|---|---|---|
| `GET /tls` | — | Reads the root, `state/ca.json`, `state/tls-public.json`, the certificates in the data of Caddy, and `tailscale status --json` (only with Tailscale installed). No network. |
| `PUT /tls/public` | `tls-public-enrol` | The DNS service is `https://care-pi.rithviknishad.dev`. For a test, the environment variable `CARE_PI_DNS_URL` (a drop-in of `care-pi.service` and of `care-dns-update.service`) changes it. The job waits up to 5 minutes for the certificate. |
| `PUT /tls/tailscale` | `tls-tailscale-enrol` | Recent authentication. Cancellable while it waits for the login (15 minutes at most). |
| `GET /network` | — | Also the default route, the Wi-Fi state (`nmcli`), the DHCP advice, and the Tailscale address. |
| `GET /network/wifi/scan` | — | `nmcli --terse device wifi list --rescan yes`. About 5 s. |
| `PUT /network/wifi`, `DELETE /network/wifi` | `network-wifi` | The PSK goes to the keyfile of NetworkManager only. |

The sandbox of `care-pi.service` does not change in Phase 6. The work that needs more rights runs in transient units:
the install of Tailscale (`systemd-run --unit care-pi-tailscale-install`), the login of Tailscale
(`care-pi-tailscale-up`), the write of the Wi-Fi keyfile (`care-pi-wifi-keyfile`), and the Wi-Fi country
(`care-pi-wifi-country`). `nmcli` and `tailscale` talk to their daemons over D-Bus and unix sockets.
`care-pi tls public update-address` (the timer) runs as root outside the agent; it takes the apply lock only when it
applies.

## 13. Phase 7: the firewall (default-drop, S42)

S42 kept the table `inet care-pi` without a drop policy in Phase 1, because a wrong rule cuts SSH on a headless Pi.
Phase 7 adds the drop policy as an option with a rollback timer.

**Who owns the table.** The installer writes `/etc/care-pi/firewall.nft` (policy `accept`) and
`care-pi-firewall.service` (S44). From the first apply, the agent owns the table: it renders
`/etc/care-pi/rendered/firewall/care-pi.nft` from `config.yaml` and the drop-in
`/etc/systemd/system/care-pi-firewall.service.d/50-care-pi.conf`, which makes the unit load that file (`ExecStart=` and
`ExecReload=` replaced). The file of the installer stays as the fallback before the first apply. The drop-in of
Phase 6 (`50-care-pi-tailscale.conf`, the Tailscale port only) goes at the next apply: the table of the agent has the
port. Without the unit (`install.sh --no-firewall`, ufw, or firewalld), the drop-in does nothing.

**The table.**

| Rule | Policy `accept` | Policy `drop` |
|---|---|---|
| `lo`, `established,related` | accept | accept |
| `ct state invalid` | — | drop |
| ICMP, ICMPv6 (neighbour discovery, router advertisements) | accept | accept |
| DHCPv4 offers (`udp sport 67 dport 68`), DHCPv6 replies (`fe80::/10`, `udp sport 547 dport 546`) | accept | accept |
| 22/tcp | accept when `security.ssh.allow` (default `true`) | the same |
| 80, 443, 8443/tcp | accept | accept |
| 5353/udp (mDNS) | accept | accept |
| 41641/udp (tailscaled) | accept in the Tailscale mode | the same |
| 53 (tcp, udp) on `podman*` (aardvark-dns of a podman network) | accept | accept |
| other input | accept | drop |

The forward and output chains are not in the table. The pod publishes 80, 443, and 8443 with DNAT (netavark), so these
packets pass the forward hook, not the input hook. A check in Docker (`debian:trixie`, podman 5.4.2, netavark, the table
of the golden case `hardened`, policy `drop`) showed: a container reaches the internet (DNS and HTTPS), a published port
answers a client on another host (HTTP 200), a port of the host outside the allow-list times out, and
`nft 'chain inet care-pi input { policy accept ; }'` opens it again at once. NTP replies are `established`.

**Config.** `security.firewall.policy: accept|drop` (default `accept`, so an existing host does not change) and
`security.ssh.allow` (default `true`). `install.sh --firewall-drop` writes `drop` for a new install (it asks first when
it runs on a terminal without `--yes`). The image keeps `accept`: nobody can confirm on a headless first boot.
`drop` is the planned default after the Phase 7 test on lumine.

**The rollback timer.** When the apply loads a `drop` table after an `accept` table (the plan action `reload-firewall`):

1. The agent starts the transient timer `care-pi-firewall-rollback.timer` (`systemd-run --on-active=90s`) first. Its
   service runs `care-pi firewall rollback --reason timeout`. When `systemd-run` fails, the agent does not load the table.
2. The agent loads the table (`systemctl try-reload-or-restart care-pi-firewall.service`) and writes
   `/var/lib/care-pi/state/firewall.json` (`policy`, `pending_until`).
3. The operator checks a **new** SSH session and the console, then confirms: `care-pi firewall confirm`, or
   Security > Firewall > Keep (`POST /security/firewall/confirm`). The confirmation stops the timer.
4. Without the confirmation, the rollback sets the live policy to `accept` with one `nft` command, then sets
   `security.firewall.policy: accept` in `config.yaml` and writes the `accept` table, without a full apply (it works while
   a job holds the apply lock). The next apply renders the same table. `GET /security/firewall` shows `rolled_back_at` and
   `rollback_reason: timeout`.

The agent does not start the timer when the unit is not active (the table is not loaded then), or for a later load of a
`drop` table (for example a new Tailscale rule). A switch back to `accept` stops a running timer.

**CLI.** `care-pi firewall status [--json]`, `policy accept|drop`, `allow-ssh true|false` (each writes `config.yaml` and
applies, as `care-pi config set … --apply`), `confirm`, `rollback`.

## 15. Phase 7: the Coraza WAF (optional, off by default)

`01` section 5 plans an optional web application firewall on `/api/*`. `security.waf` turns it on:

```yaml
security:
  waf: {enabled: true, mode: detect, paranoia: 1}
```

- **Image.** `images/caddy/Containerfile` adds `github.com/corazawaf/coraza-caddy/v2` (pinned, `CORAZA_CADDY_VERSION`).
  The module embeds the OWASP Core Rule Set v4 (`coraza-coreruleset`): `load_owasp_crs` reads the rules from the binary,
  so no rule file is on the disk and the rules change with the image of the lock. (The plan said `/etc/caddy/crs`; the
  embedded CRS is the same set without a second copy.)
- **The Caddyfile.** The global block gets `order coraza_waf first`; the handler `@api` (`/api/*`, `/staticfiles/*`,
  `/mediafiles/*`, `/ping/`, `/health/*`, `/app_version/`) gets `coraza_waf` with `coraza.conf-recommended`,
  `crs-setup.conf.example`, `SecRuleEngine DetectionOnly` (mode `detect`) or `On` (mode `block`), the paranoia level
  (`tx.blocking_paranoia_level`), the request body on, the response body off, and the audit log of the relevant
  transactions as JSON on the standard output (parts A, B, H, Z): `care-pi logs caddy` shows the rule ids. The S3
  buckets, the plug bundles, the FE, and the console have no WAF.
- **Memory.** In Docker, Caddy used 25 MiB without the WAF and 60 MiB with the CRS loaded (cgroup memory; VmRSS 105 MiB).
  With the WAF, `care-caddy.container` gets `MemoryMax=256M` (else 128M).
- **Safety.** An apply that turns the WAF on checks the modules of the running Caddy (`caddy list-modules`): without
  `http.handlers.waf` (an image of an older lock), it stops with an error before it writes a file, because the next
  start of Caddy would fail on the unknown directive.
- **False positives.** Expect them on file uploads, rich text (notes, prescriptions), and search with quotes. Use
  `detect` first, read the audit lines for some days, then `block`. Paranoia 1 is the right level for Care; 2 and more
  give many more false positives. Off by default, also on a 4 GB Pi.
- **Test.** `images/caddy/test-waf.sh [IMAGE]`: the module, `caddy validate` of the golden case `hardened` (block,
  paranoia 2), and live requests with a stub of care-api: a normal request 200; `?search=1' OR '1'='1' -- ` 403 in block
  mode, 200 with the audit line of the CRS rule 942100 in detect mode. It passed with Caddy 2.11.7 and coraza-caddy
  2.6.2 (arm64).

## 16. Phase 7: `care-pi doctor` and the support bundle

`care-pi doctor [--json]`, `GET /api/v1/doctor` (session), and Support > Health checks in the console run the same checks.
They change nothing; each problem has a fix hint (a command or a page). The failed checks come first; the status of the
report is the worst status (`ok`, `warn`, `fail`, `skip`). The CLI exits with code 1 when a check failed. The checks
follow the pitfalls of `plans/09-handoff.md` section 3:

| Id | Check | `warn` / `fail` |
|---|---|---|
| `host-*` | The host check of the installer (the memory controller, podman 5.4, …) | `fail` for a required item |
| `ports` | `ss -ltnpH`: another program than conmon on 80, 443, or 8443 of a published address or of all addresses (SP5: `tailscale serve` on 443 of the Tailscale address) | `fail` |
| `mdns` | avahi announces another name than `<hostname>.local` (a collision: `care-2.local`) or none | `warn` |
| `clock` | NTP synchronized; the browser time of S52 | `warn` (browser), `fail` (none) |
| `memory`, `zram` | `MemAvailable` below 256 MiB; no `/dev/zram` in `/proc/swaps` | `warn` |
| `disk-*` | less than 2 GiB or 10 % free | `warn` |
| `units` | a unit of Care `failed`; `inactive` after the setup | `fail`, `warn` |
| `cert-local`, `cert-public` | the end of the local root or of the public certificate in less than 14 days; a LAN address outside the name constraints | `warn`, `fail` (expired) |
| `backup-<id>`, `backups` | the last snapshot of a destination is older than `backups.alert_after_hours`; no snapshot; no destination | `fail`, `warn` |
| `firewall` | `care-pi-firewall.service` not active; a policy `drop` that waits for its confirmation; a loaded policy that differs from `config.yaml` | `warn` |
| `reboot` | `/run/reboot-required` | `warn` |
| `updates` | a newer lock on the track | `warn` |

**The support bundle** (Support > Make a support bundle, `care-pi support-bundle`) also has, since Phase 7:
`doctor.json`; `rendered-tree/etc/containers/systemd/care*` and `rendered-tree/etc/care-pi/rendered/**` (env files
without the values of secret keys; `additional-plugs.json` left out, because plug settings can be secret);
`nft-ruleset.txt` (`nft list ruleset`); `listening-sockets.txt` (`ss -ltnupH`); `podman-ps.txt`;
`tailscale-status.json` (`tailscale status --json` without the keys, the peers, and the users); `memory-peaks.txt`
(`MemoryPeak`, `MemorySwapPeak`, `MemoryMax` of the units `care-*` and `care-pi*`); `firewall-state.json`. The values of
the secret files are replaced with `<redacted>` in each section, as before.

