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
| `MemoryMax`, `MemorySwapMax` | `64M`, `32M` | `01` §8. The agent hashes one password at a time (argon2id, 19 MiB). At the start, `serve` gives the Go garbage collector a soft limit of half of `memory.max` of its cgroup (32 MiB); `GOMEMLIMIT` in the environment wins. The child processes (podman, skopeo, journalctl) count in the same cgroup. |
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
  changes the state machine of a Phase 1 host. The claim deletes the hint.
  `sudo care-pi auth claim-code` prints the code and the console URLs. `--rotate` makes a new code.
- **Imager password** (`os-password`). The agent reads the user name from the cloud-init `user-data` of
  Raspberry Pi Imager (`/boot/firmware/user-data`), then compares the password with the SHA-crypt hash of that
  user in `/etc/shadow`. `claim_methods` lists `os-password` only when such a user exists.
  `os-password` is the first method, except when the claim hint is `claim-code`. The console shows the field of the
  first method. When the operator types a claim code (`XXXX-XXXX-XXXX`) in the Imager password field, the console
  shows the claim code field.
  This method needs a test on a Pi that Imager flashed (spike SP10). The code is behind the interface `auth.OSPassword`.
- **Sessions.** `/var/lib/care-pi/state/sessions.json` (root 0600) has the SHA-256 of each session id. A restart
  of the agent keeps the sessions. Idle timeout 2 h, absolute timeout 24 h (S49), 10 sessions at most.
  The cookie is `__Host-care_pi_session=<id>; Path=/; Max-Age=86400; HttpOnly; Secure; SameSite=Strict`.
- **CSRF.** Each unsafe request with a session needs `X-CSRF-Token` (from `GET /session`, claim, login, or recover).
  Each unsafe request, also claim, login, and recover, needs `Origin: https://<served host>:8443`.
  A served host is a host name of `config.yaml`, a LAN address, an address of the rendered `care.pod`, or a
  site name of the rendered Caddyfile. The last item keeps the console on the old name after the identity step
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
sudo care-pi config set identity.display_name='Sunrise Clinic' identity.hostname=care size.preset=small-clinic
sudo care-pi setup skip env
sudo care-pi setup skip backups   # only a dev build
printf '%s\n' 'the care superuser passphrase' | sudo care-pi setup finish \
  --care-username admin --care-email admin@clinic.example --care-password-stdin
sudo care-pi setup status
```

As root, `setup finish` counts `account` (after `auth set-password`), `recovery-codes`, and `hardware` as done.

## 6. Jobs and the event stream

- A job is the journal `/var/lib/care-pi/state/jobs/<id>.json` (the Phase 1 format with more members) and its
  events `<id>.events.jsonl`. The id is `<UTC time>-<kind>`, for example `20261030T101500.123Z-apply`.
  The CLI and the agent write the same files, so `GET /jobs` and `care-pi jobs list` show the jobs of both.
- The kinds `apply`, `setup`, `fe-build`, and `fe-prune` hold the apply lock `/run/care-pi/apply.lock`.
  The agent waits 5 s for the lock, then gives `409 job-running` with `job_id`
  (`null` when a CLI command without a journal holds the lock).
- Cancel: `POST /jobs/{id}/cancel` and `care-pi jobs cancel ID` write `<id>.cancel`. Only `fe-build` and
  `support-bundle` jobs stop. Another job gives `409 job-not-cancellable`.
- The agent keeps the last 100 jobs and each job of the last 30 days, and up to 20000 events for each job.
- `GET /jobs/{id}/events` with `Accept: text/event-stream` gives SSE: `retry: 3000`, then each event with
  `id:`, `event:`, and `data:` (one line of JSON), then `done` and the end of the stream.
  A `heartbeat` (no `id:`) comes every 15 s. `?after=N` or `Last-Event-ID: N` resumes after event `N`
  (the header wins). Without `Accept: text/event-stream` the same URL gives one `JobEventPage`.
- `GET /logs/{unit}?follow=true` with `Accept: text/event-stream` follows the journal (`journalctl --follow`).
  The event id is the journal cursor. The units are the units of `care-pi logs`, and `agent` (`care-pi.service`).
- A session has 4 open streams at most, and the agent 16. When the session ends, the agent closes its streams.
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
| `/var/lib/care-pi/state/jobs/<id>.json`, `<id>.events.jsonl`, `<id>.cancel` | Job journals, events, cancel requests | root 0600 |
| `/var/lib/care-pi/state/support/` | The support bundles (`.tar.gz`, redacted) | root 0700 |
| `/var/lib/care-pi/caddy/caddy/pki/authorities/local/root.crt` | The root CA of Caddy (the agent reads it for `/tls/root-ca`) | root |

## 8. CLI parity

`docs/api/README.md` section 11 lists the CLI command of each operation. Phase 2 adds `serve`, `auth …`,
`setup status|skip|finish`, `config show|set|presets`, `jobs list|show|follow|cancel`, `support-bundle`,
`tls show|root-ca`, `care info`, `power reboot|shutdown`, and the flags `host check --probe`,
`logs --since --json`, `logs agent`, and `fe status --json`. The `--json` output of a command is the JSON
object of the operation.

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
- The body limits, a slow body, `Sec-Fetch-Site`, and a live socket (`limits_test.go`).
- `internal/auth`, `internal/jobs`, and `internal/setup` have unit tests. `internal/auth` also checks that the
  locks stay after a restart and that the claim hint orders `claim_methods`. `agent/internal/render` has the golden
  tree `small-clinic-bootstrap` (setup mode).
