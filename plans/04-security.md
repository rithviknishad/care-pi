# 04 — Security

Scope per your answers: Care's auth wall + RBAC is the primary control; physical
attacks, disk encryption and compliance regimes are out of scope for now (S19,
S21). What remains in scope: transport security on untrusted LANs, the agent as a
root-privileged control plane, secrets at rest on the box, update integrity
(optional signing since Phase 7, S55), and backup confidentiality off-site.
Section 9 lists what Phase 7 built and what is still later.

## 1. Transport

- HTTPS only; port 80 is a redirect (P9). HSTS is **not** sent for `.local`/IP
  hosts (it would wedge devices after a certificate change); sent for the
  public-cert hostname.
- Local CA: Caddy's internal CA with a per-device root (generated at first boot,
  stored under `/var/lib/care-pi/caddy`). Root certificate download only from the
  console over HTTPS after the one-time interstitial; fingerprint shown alongside;
  per-OS install instructions (iOS requires the extra "Certificate Trust Settings"
  toggle; Android distinguishes user vs system CAs — user CA is enough for Chrome;
  Windows/macOS/Linux via the OS store or Firefox's own store).
- Public cert: ACME DNS-01 against our service; private keys never leave the Pi;
  A record = LAN IP, so the name is only useful on the LAN (and it leaks the LAN IP
  to DNS, which is harmless).
- Tailscale: `tailscale cert` for `<name>.<tailnet>.ts.net`; ACLs are the clinic's
  tailnet policy; the console shows who is in the tailnet.
- The console has its own origin, `https://<host>:8443` (S48), so an XSS in Care or a
  malicious FE plug cannot read console responses (no CORS headers) and cannot make
  unsafe requests (`Origin` check + `X-CSRF-Token`). Cookies are not port-scoped: the
  cookie is `__Host-care_pi_session`, and the browser also sends it to 443. Caddy strips it
  before Care: the `@api` and `@s3` proxies remove it from the `Cookie` header, so Care and
  its plugs never get it. The agent rejects a request with the session cookie when
  `Sec-Fetch-Site` is not `same-origin` or `none`, so a page on 443 cannot start a GET with a cost.

## 2. Agent (root) hardening

- Listens only on a unix socket; Caddy is the sole client. No TCP listener.
- Authentication: argon2id password hash in `/etc/care-pi/secrets/agent.json`,
  8 printable recovery codes (hashed, S46), optional TOTP. Login rate-limited per IP
  and globally; lockout with exponential backoff; all auth events in journald.
- Authorization is enforced in the Go handlers, never only by hiding UI.
- Sessions: random 256-bit IDs, HttpOnly/Secure/SameSite=Strict, **2 h idle, 24 h
  absolute** (S49); re-authentication for destructive actions (restore, factory reset,
  show recovery key, SSH enable, password change, shutdown). Recovery codes: 8 (S46).
- Input that becomes shell arguments (paths, hostnames, S3 endpoints, plug refs) is
  validated against strict schemas; execution uses `exec.Command` with argument
  arrays, never a shell string.
- systemd unit: `ProtectHome=yes`, `PrivateTmp=yes`, `ProtectKernelTunables=yes`,
  `ProtectControlGroups=yes`, `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
  AF_NETLINK`, `SystemCallFilter=@system-service`, `ReadWritePaths=` limited to
  `/etc/care-pi /etc/containers/systemd /var/lib/care-pi /run/care-pi /etc/avahi
  /etc/hostname /etc/systemd/system`. It must remain root because of podman and
  systemctl; splitting a privileged helper out behind a narrow socket API is a
  later hardening step.
  *As built* (`docs/console-server.md` §2): `ProtectSystem=strict` with `ReadWritePaths`,
  `ProtectHome=read-only`, `PrivateTmp`, `ProtectKernelTunables`, `RestrictAddressFamilies`,
  `MemoryMax=128M`. `ProtectControlGroups` and `SystemCallFilter` are not set: `podman exec`
  writes the cgroup of the container.
- **Privileged helper (Phase 7, S53).** `care-pi helper` runs as root on
  `/run/care-pi-helper/helper.sock` (0660 root:care-pi, socket activation) and runs only the
  commands of an allow-list, each argument checked; it logs each request and refusal.
  `care-pi serve --helper` sends every host command through it and runs with
  `NoNewPrivileges=yes` and four file capabilities (installer `--helper`, opt-in). The daemon
  still runs as uid 0 and writes its files itself; the switch to `User=care-pi` with an empty
  `CapabilityBoundingSet=` needs typed file requests in the helper (`docs/console-server.md` §14).
- Support bundle redacts secrets by key-name pattern and by value match against
  known secret files.

## 3. Secrets at rest

| Secret | Generated | Stored | Delivered to |
|---|---|---|---|
| `DJANGO_SECRET_KEY`, `JWKS_BASE64` | first boot | `/etc/care-pi/secrets/` | api/worker/beat env (podman secret) |
| Postgres password | first boot | same | db + app env; app connects over the pod's localhost only |
| versitygw root key/secret | first boot | same | s3 + app env |
| recovery key | setup | same | derived passphrases rendered into pgbackrest/restic config inside the containers' secret mounts |
| agent password hash, recovery codes, TOTP seed | setup | same | agent only |
| Tailscale auth key | operator | same, deleted after enrolment | tailscaled once |
| destination credentials (S3 keys, SFTP private key) | operator / generated | same | pgbackrest/restic |

`/etc/care-pi/secrets` is `0700 root`, files `0600`; it is included in the restic
config snapshot (encrypted with the key derived from the recovery key, which is
itself excluded). No secret appears in `config.yaml`, logs, the support bundle, or
unit files (`Secret=` references only).

## 4. Network exposure

- nftables default-deny inbound; allow 80/443 TCP, 5353 UDP, 22 TCP only when SSH
  is enabled, 41641 UDP when Tailscale is on. Outbound unrestricted (updates,
  backups, Tailscale).
  *As built* (S42, S54): the table `inet care-pi` has this allow-list (also 8443/tcp, ICMP and
  ICMPv6 with neighbour discovery, DHCP and DHCPv6 replies). Its policy is `accept` by default
  on every host; `security.firewall.policy: drop` is opt-in, with a 90 s rollback timer that
  the operator stops from a second session (`care-pi firewall confirm`). `security.ssh.allow`
  closes 22/tcp. On Fedora, firewalld keeps its own policy (S57).
- SSH: disabled unless enabled in Raspberry Pi Imager customisation or in the
  console; password auth disabled when the agent enables it (key only, shown as a
  paste box). Default Pi OS user is whatever Imager created — the console warns
  if a user with a weak/known default password exists (*inference*: Pi OS trixie
  forces user creation at image time, so this is mostly moot).
- Containers publish nothing except the pod's 80/443; inter-service traffic stays
  on the pod's loopback.
- Optional Coraza WAF (OWASP CRS) on `/api/*`, off by default on 2 GB (CRS rule
  compilation costs memory); offered in advanced settings for 4 GB boxes.
  *As built* (S58): `security.waf` (off by default; `detect` or `block`; paranoia 1 to 4), the
  CRS embedded in the module; `care-caddy` gets 256 MiB when it is on. Expect false positives on
  file uploads; start with `detect`.

## 5. Update integrity (no signing, S24; optional signing, S55)

- Registry/GitHub over TLS; everything pinned by digest or sha in locks produced by
  our CI; the agent refuses a lock whose digests don't match what it pulled.
- `index.json` and locks are fetched from a single origin we control; the agent
  pins that origin's hostname and verifies TLS normally. Adding minisign later is a
  small change (one public key in the agent, one signature per lock).
- *Phase 7* (S55): CI signs `index.json` and each lock with minisign when the secret
  `MINISIGN_KEY` is set. With `updates.require_signed: true`, the agent verifies the `.minisig`
  of each index and lock (also `--lock-file` and the dist snapshot of the image) against the
  embedded keys and `/etc/care-pi/keys/*.pub`, and refuses with `lock-signature-invalid`. The
  default is `false` until the owner makes the key (`docs/solutions.md` §18).

## 6. Backups

- Confidentiality off-site: AES-256 (pgBackRest) / restic's AEAD; keys derived from
  the recovery key; destination credentials cannot decrypt.
- Integrity: `pgbackrest verify`, `restic check`; the console treats a failed verify
  as a red banner.
- Ransomware on a staff PC: backups are written by the Pi with its own credentials;
  the console recommends S3 object lock / append-only credentials where the provider
  supports it, and never mounts destinations with write access to anything else.

## 7. Plugs

Plugs run in-process in Care with full database access; a malicious or buggy plug
is equivalent to a malicious Care. Controls: solutions pin plug shas; `custom` track
is visibly marked unsupported; the console shows plug provenance (repo, sha) and the
lock diff before an update. Sandboxing plugs is out of scope.

## 8. Residual risks to state in the operator docs

Device theft exposes data (no disk encryption); an attacker already on the LAN
can observe hostnames and attempt logins (rate-limited; Care's own lockout
applies); the first-visit interstitial is a one-time MITM window for the local-CA
root download (mitigated by the displayed fingerprint and by using the Pi's
ethernet LAN rather than public Wi-Fi for setup).

## 9. Phase 7 status (2026-11-02)

| Item | State | Where |
|---|---|---|
| Privileged helper | Built; opt-in (`--helper`); daemon still uid 0 | S53, `docs/console-server.md` §14 |
| Default-drop firewall | Built; opt-in, with the rollback timer | S54, `docs/console-server.md` §13 |
| Local-CA rotation | Built (`POST /tls/local-ca/rotate`) | S60, `docs/connectivity.md` §2.5 |
| Lock signing | Built; opt-in (`updates.require_signed`); no key yet | S55, `docs/solutions.md` §18 |
| Coraza WAF | Built; off by default | S58, `docs/console-server.md` §15 |
| Telemetry | Built; off by default; nothing leaves the box when off | S56, `docs/telemetry.md` |
| `care-pi doctor`, support bundle | Built; the bundle redacts secrets, env values, Tailscale keys and peers | `docs/console-server.md` §16 |

Still later: the daemon as `User=care-pi` with an empty `CapabilityBoundingSet=` (typed file requests in
the helper), `ProtectControlGroups` and `SystemCallFilter` for the daemon, `PUT /security/ssh` (the SSH
switch of the console; `501` today; `security.ssh.allow` closes the port only), SELinux labels for Fedora
enforcing, `require_signed: true` as the default after the key ceremony, and an RTC policy (SP9).
