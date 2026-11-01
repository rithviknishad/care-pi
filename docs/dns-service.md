# The care-pi DNS/ACME service

This document describes the DNS/ACME side service of care-pi (Phase 6, `plans/06-roadmap.md`; decisions S30,
S38; `plans/07-first-contact-trust.md` §3.8 Option H and §6). The code is in `services/dns/`.
The wire protocol is in `services/dns/API.md`. When this document and `API.md` disagree, `API.md` wins.

## 1. Purpose

A care-pi device on a clinic LAN must serve Care over HTTPS without a certificate warning, and without a
certificate installation on each phone and laptop (decision S36: public-cert mode is the default when the
device has internet). The service gives this:

1. **A name.** Each device gets a clinic id, for example `lumine-k3x9`, and the name
   `lumine-k3x9.care-pi.rithviknishad.dev`.
2. **DDNS.** The name has an A record (and an optional AAAA record) with the **LAN address** of the device.
   A private address in public DNS is the design: browsers on the LAN find the device by a public name.
3. **A certificate.** Caddy on the device gets a Let's Encrypt certificate for the name with the DNS-01
   challenge. The Caddy module `dns.providers.acmedns` sends the challenge value to the service, and the
   service writes the TXT record `_acme-challenge.<id>.care-pi.rithviknishad.dev`.
4. **Credential rotation and revocation.**
5. **Two public pages.** `GET /` explains the service and lists, on request, the clinics of the caller's
   network that agreed to be listed. `GET /cert` installs a local CA certificate from a QR code (§10).

## 2. Architecture

```text
care-pi device (agent, Go)                       Cloudflare
  ed25519 key, clinic id, acme-dns credentials     ┌──────────────────────────────────────────────┐
  │                                                 │ Worker "care-pi-dns"                         │
  │ GET /v1/nonce, POST /v1/register (signed) ─────▶│  https://care-pi.rithviknishad.dev           │
  │ PUT /v1/clinics/{id}/address (signed) ─────────▶│  router → handlers → D1 "care-pi-dns"        │
  │                                                 │                    → Cloudflare DNS API ──┐  │
Caddy (dns acmedns)                                 │  cron */15: housekeeping                  │  │
  │ POST /update (X-Api-User, X-Api-Key) ──────────▶│                                           │  │
  │                                                 └───────────────────────────────────────────┼──┘
  │                                                   zone rithviknishad.dev (irena/ray.ns...) ◀┘
Let's Encrypt ── DNS query TXT _acme-challenge.<id>.care-pi.rithviknishad.dev ──▶ Cloudflare DNS
Browser on the LAN ── A <id>.care-pi.rithviknishad.dev → 192.168.1.23 ──▶ https to the device directly
```

- The Worker serves one host name, `care-pi.rithviknishad.dev` (a Workers Custom Domain). It does not serve
  the clinic names: those are DNS-only records (not proxied) that point to clinic LANs.
- The Worker writes the records with the Cloudflare DNS API and the secret `CF_DNS_API_TOKEN`
  ("Zone - DNS - Edit" on `rithviknishad.dev` only).
- Care traffic never goes through the service. The browser connects to the device on the LAN.

## 3. Design choices

| Topic | Choice | Reason |
|---|---|---|
| ACME TXT location | **Direct TXT** at `_acme-challenge.<id>.care-pi.rithviknishad.dev`. No CNAME, no separate acme-dns zone. | The Caddy module (`caddy-dns/acmedns` → `libdns/acmedns` v0.5) sends only `{subdomain, txt}` to `<server_url>/update` with `X-Api-User`/`X-Api-Key`. It does not read `fulldomain` and does not check a CNAME. Caddy's propagation check queries `_acme-challenge.<name>` and follows a CNAME only if one exists. Thus the service can write the TXT record at the challenge name itself. The device creates no CNAME. `acme.fulldomain` is the challenge name. |
| acme-dns compatibility | `POST /update`, `X-Api-User`/`X-Api-Key`, the error codes `forbidden`, `bad_txt`, `malformed_json_payload`, two rolling TXT values, `GET /health`. `POST /register` is closed (`403`). | The device gets its credentials from the signed registration. Open registration would let anyone make records in the zone. |
| `subdomain` | The clinic id. | It maps `/update` to one clinic. The service checks that it matches the user. |
| `/v1` authentication | **Ed25519 request signatures** over `CARE-PI-DNS-V1\nMETHOD\nPATH\nTIMESTAMP\nhex(SHA-256(body))`, window ±300 s, replay cache in D1. No bearer `ddns_token`. | No bearer secret can leak from logs or proxies. A captured request cannot be used again (the replay cache), also not for a rotation, whose response holds new credentials. Go signs with `crypto/ed25519` in a few lines. |
| Registration proof | The standard request signature by the new key, over a body that holds a single-use nonce from `GET /v1/nonce`. | One signature format for all requests. The nonce stops replays of a registration. |
| Id | `<slug of hostname_hint, max. 20>-<4 random base32>`, never used again. | Readable ("is this my clinic?") and not guessable from the hostname alone. 20 random bits per slug; a collision gives a new suffix. |
| TTL | 60 s for A, AAAA, and TXT (`RECORD_TTL`). | A DHCP address change reaches the browsers in about one minute. 60 s is the minimum TTL of the Free plan. See §6.4 for the trade-off. |
| Address policy | IPv4: private, CGNAT, documentation only (`ADDRESS_POLICY=private`). IPv6: ULA, global unicast, documentation. | The name must not point to a public server: this stops the use of the zone for phishing on the internet. IPv6 LAN addresses are usually global unicast, so the policy accepts them (a known gap). |
| Rate limits | Fixed-window counters in D1 (one upsert per check). | The Workers Rate Limiting binding supports only 10 s and 60 s periods and counts per Cloudflare location. The registration limit is per day. |
| Secrets at rest | The acme-dns password is 256 random bits; D1 keeps `SHA-256(password)` only. | A slow hash (argon2) protects low-entropy passwords. It adds nothing for a 256-bit random secret and costs Worker CPU time (10 ms on the Free plan). |
| Framework | None. A small router (`src/router.ts`). | Fewer dependencies; the API is small. |

## 4. Data model (D1 `care-pi-dns`)

The schema is `services/dns/migrations/0001_init.sql`. Times are Unix seconds.
A *network key* is an IPv4 address, or the `/64` of an IPv6 address.

| Table | Columns | Notes |
|---|---|---|
| `clinics` | `id` PK, `pubkey`, `created_at`, `created_ip`, `hostname_hint`, `acme_user` UNIQUE, `acme_key_hash`, `listing_opt_in`, `last_seen`, `last_ip`, `revoked_at` | The service never deletes a row, so an id is never used again. Revocation clears `acme_user` and `acme_key_hash`. |
| `addresses` | `id` PK → clinics, `ipv4`, `ipv6`, `a_record_id`, `aaaa_record_id`, `updated_at` | The Cloudflare record ids make an update one `PATCH`. |
| `txt_records` | `record_id` PK, `id` → clinics, `value`, `created_at` | At most two per clinic. The cron deletes rows older than one hour. |
| `nonces` | `nonce` PK, `expires_at` | Single use (`DELETE … RETURNING`). |
| `replay` | `digest` PK, `expires_at` | `SHA-256(id \n canonical string)`. |
| `rate` | (`bucket`, `key`, `window_start`) PK, `count` | Fixed windows. |

## 5. Protocol summary and rate limits

`services/dns/API.md` is the full protocol. In short:

1. `GET /v1/nonce` → `POST /v1/register` (signed by the new key) → `{id, fulldomain, acme}`.
2. `PUT /v1/clinics/{id}/address {ipv4, ipv6}` (signed) at start, on a change, and every 6 hours.
3. Caddy: `dns acmedns` with the `acme` credentials; Caddy calls `POST /update`.
4. `POST /v1/clinics/{id}/rotate` (new acme-dns credentials, optionally a new key), `PATCH` (listing),
   `DELETE` (revoke).

| Bucket | Limit (default) | Key | Counts |
|---|---|---|---|
| `nonce` | 20 per minute | network | each `GET /v1/nonce` |
| `register` | 5 per day | network | each registration with a valid signature and nonce |
| `address` | 6 per minute | clinic | each authenticated `PUT address` |
| `acme` | 6 per minute | clinic | each authenticated `POST /update` |
| `manage` | 6 per minute | clinic | status, settings, rotate, delete |
| `lookup` | 30 per minute | network | each `GET /lookup` |
| `authfail` | 20 per 10 minutes | network | failed signatures and acme-dns keys; when full, the network gets `429` for all authenticated calls |

Change the limits without a code change: set `RATE_LIMITS` in `wrangler.toml`, for example
`RATE_LIMITS = '{"register":{"limit":10,"window":86400}}'`, then deploy. Clinics behind one carrier-grade
NAT share one public IPv4 address, so they share the `register` limit. Increase it if many clinics are
installed on one day behind one carrier.

## 6. Security, abuse, and privacy

### 6.1 Private addresses in public DNS

- The A record of a clinic name is a LAN address, for example `192.168.1.23`. Anybody can read it with
  `dig`. It tells only the internal address of one device. It does not give access to the device.
- **DNS-rebinding protection.** Some routers and resolvers refuse a public name that resolves to a private
  address (dnsmasq `stop-dns-rebind`, OpenWrt `rebind_protection`, pfSense and OPNsense "DNS rebinding
  checks", some ISP routers). Then the name does not resolve on that LAN. Remedies, in order:
  1. Allow the domain in the router: OpenWrt `list rebind_domain 'care-pi.rithviknishad.dev'`; dnsmasq
     `rebind-domain-ok=/care-pi.rithviknishad.dev/`; pfSense/OPNsense "Domain overrides" or private-domain
     setting.
  2. Use a public resolver on the clients (1.1.1.1, 8.8.8.8).
  3. Use the offline fallback `care.local` with the local CA (`07` §6 item 3).
  The setup wizard detects the problem with a browser fetch of `https://<id>.care-pi.rithviknishad.dev/ping`
  (`07` §3.8). This check is on the device, not in this service.
- The address policy (§3) stops a clinic name from pointing to a public server.

### 6.2 "Is this my clinic?"

Anybody can register an id and get a valid certificate for it. An attacker on the clinic LAN can send an
operator to `https://evil-ab12.care-pi.rithviknishad.dev` with a page that looks like Care. Thus:

- The console shows the id of the device in large text. The operator must compare it with the address bar.
- The id has a random suffix, so an attacker cannot get the same id. Ids are never used again.
- `GET /lookup` (opt-in) shows the clinics of the operator's public network, as a visibility aid.

The prize of this attack is one phished password (severity level 1 of `07` §1.2), not a root CA on the
device.

### 6.3 What the service can see and do

| The service sees | The service does not see |
|---|---|
| The clinic id and the `hostname_hint` (often the clinic name). | Care data, patient data, or any traffic to Care. |
| The public key of the device. | The private key of the device or of its TLS certificate. |
| The LAN addresses (also public in DNS). | The acme-dns password (only its SHA-256). |
| The public IP address (network key) at registration and at the last call, and the times. | The local CA certificate on the `/cert` page (it stays in the URL fragment). |

- **Public by design:** clinic ids are in public DNS and, after the first certificate, in the public
  Certificate Transparency logs (crt.sh). Tell operators: the host name becomes public; do not put patient or
  personal data in it.
- **Trust in the operator of the service:** the service (or an attacker who controls it, the Cloudflare
  account, or the zone) can write any TXT record, so it can get a certificate for any clinic name. With such a
  certificate and a position on the clinic LAN, it could impersonate a device. Mitigations: the Cloudflare
  account has two-factor authentication; the DNS token is limited to one zone; a future agent check can watch
  the CT logs for unexpected certificates of its own name.
- Workers Logs keep the request logs (with the caller IP) for 3 days on the Free plan (7 days on Paid).
  The service logs events (register, address, acme_txt, rotate, revoke) with the id and the network key. It
  never logs a password, a key, or a TXT value.
- `GET /lookup` is opt-in per clinic (default off). It lists only the caller's own network: the `ip`
  parameter must be the caller's address, so nobody can list another network.

### 6.4 TTL and internet outages

`07` §3.8 notes that a long TTL (about one day) keeps the name working through a long internet outage. This
service uses 60 s (the Phase 6 brief): a new DHCP address reaches the clients in one minute. During an outage longer
than the TTL, the name does not resolve, and the clinic uses the offline fallback (`care.local` with the
local CA). To prefer outage tolerance, give the device a DHCP reservation and set `RECORD_TTL` to `3600` or
`86400`. The setting is global.

## 7. Let's Encrypt limits and the Public Suffix List

Let's Encrypt counts certificates per *registered domain*, which it finds with the Public Suffix List (PSL).
Today the registered domain of every clinic name is `rithviknishad.dev`, so all clinics share these limits
(2026 values, `https://letsencrypt.org/docs/rate-limits/`):

| Limit | Value | Effect on care-pi |
|---|---|---|
| New certificates per registered domain | 50 per 7 days (refills 1 per 202 min) | Shared by all new clinics and by the owner's other names. Renewals almost never hit the limits. |
| New certificates per exact set of names | 5 per 7 days | A device that loses its Caddy data and starts again five times in a week is blocked. The agent must keep the Caddy data volume. |
| Authorization failures per name per account | 5 per hour | A wrong TXT setup fails fast. Use the staging CA for tests. |

Two paths raise the shared limit:

1. **Let's Encrypt override form** (`https://isrg.formstack.com/forms/rate_limit_adjustment_request`) for the
   registered domain `rithviknishad.dev`. This is the correct path for a rate-limit problem. Do it first,
   before many clinics register.
2. **The PSL.** The PSL maintainers do **not** accept an entry whose only reason is a rate limit
   (`https://github.com/publicsuffix/list/wiki/Guidelines`, "Validation and Non-Acceptance Factors"). The
   valid reason for care-pi is isolation: `care-pi.rithviknishad.dev` gives subdomains to mutually-untrusting
   parties (clinics). On the PSL, browsers keep the cookies of each clinic separate, and Let's Encrypt counts
   each clinic as its own registered domain.

### PSL application checklist

- [ ] Wait until the service has real use: the maintainers usually decline projects with fewer than
      thousands of users.
- [ ] Make sure that `rithviknishad.dev` has more than 2 years of registration left. Renew it first if not.
      Commit to keep more than 1 year left at all times.
- [ ] Read `https://github.com/publicsuffix/list/wiki/Guidelines` and the PR template again (the rules change).
- [ ] Fork `publicsuffix/list`. In `public_suffix_list.dat`, in the PRIVATE section, add an entry in the
      sorted position:

      ```text
      // care-pi : https://github.com/rithviknishad/care-pi
      // Submitted by Rithvik Nishad <owner e-mail>
      care-pi.rithviknishad.dev
      ```

- [ ] Open the pull request. Fill in every part of the template: the reason (mutually-untrusting clinics;
      cookie and site isolation), example names (`<id>.care-pi.rithviknishad.dev`), the number of users, and
      the statement about the registration term. Say clearly that rate limits are not the reason.
- [ ] Add the DNS proof and keep it for as long as the entry exists:
      `_psl.care-pi.rithviknishad.dev TXT "https://github.com/publicsuffix/list/pull/<number>"`.
      (A clinic id never starts with `_`, so no clinic can take this name.)
- [ ] Answer the questions of the maintainers.
- [ ] After the merge, wait: browsers and Let's Encrypt take the new list in weeks to months.
- [ ] Effects to accept: `care-pi.rithviknishad.dev` itself becomes a public suffix. Browsers then refuse
      cookies for it. The service sets no cookies, so it is not affected.

## 8. Operations

### 8.1 Resources

| Resource | Value |
|---|---|
| Account | `9df47fe134fdde8490d93c610a9ec93e` |
| Zone | `rithviknishad.dev`, id `f2b1e99594afa341b94411c77ae8624d`, Free plan, made in 2022 (1,000 DNS records) |
| Worker | `care-pi-dns`, Custom Domain `care-pi.rithviknishad.dev` (no `workers.dev` URL) |
| D1 | `care-pi-dns`, id `d56b89be-120b-4ab6-8c4c-f2f65fbd571b` (region APAC) |
| Cron | `*/15 * * * *`: delete TXT records older than 1 h, expired nonces and replay rows, rate rows older than 2 days, and the records of revoked clinics |
| Worker secret | `CF_DNS_API_TOKEN` |
| Repository secrets | `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID` (CI deploy) |

### 8.2 The DNS token (`CF_DNS_API_TOKEN`)

The wrangler OAuth login cannot make this token. Make it in the dashboard:

1. Open `https://dash.cloudflare.com/profile/api-tokens`. Select **Create Token**.
2. At **Edit zone DNS**, select **Use template**.
3. Permissions: keep **Zone → DNS → Edit**. Do not add other permissions.
4. Zone Resources: **Include → Specific zone → rithviknishad.dev**.
5. Client IP Address Filtering: leave it empty (Workers send from many addresses).
6. TTL: optional. If you set an end date, write it in your calendar: the service stops at that date.
7. Select **Continue to summary**, then **Create Token**. Copy the token. Cloudflare shows it one time only.
8. Check the token without putting it on the command line:

   ```sh
   read -rs CF_TOKEN && curl -s -H "Authorization: Bearer ${CF_TOKEN}" \
     https://api.cloudflare.com/client/v4/user/tokens/verify; unset CF_TOKEN
   ```

9. Store it in the Worker (the command asks for the value; paste it):

   ```sh
   cd services/dns
   npx wrangler secret put CF_DNS_API_TOKEN
   ```

   If the Worker does not exist yet, wrangler asks to make it. Answer yes, or run `npx wrangler deploy` first.

To rotate the token: make a new token (steps 1–7), run step 9 again, then delete the old token in the
dashboard. The new value takes effect at once; no deploy is necessary.

### 8.3 First deploy

```sh
cd services/dns
npm ci && npm test
npx wrangler d1 migrations apply care-pi-dns --remote   # done on 2026-11-01
npx wrangler secret put CF_DNS_API_TOKEN                  # §8.2
npx wrangler deploy                                       # makes the Custom Domain and its DNS record
curl -s https://care-pi.rithviknishad.dev/health
node scripts/smoke.mjs                                    # §8.5
```

Without `CF_DNS_API_TOKEN`, the Worker runs, but the DNS calls (`PUT address`, `POST /update`) give
`503 dns_unconfigured`.

### 8.4 CI deploy

`.github/workflows/dns-service.yml` runs `npm ci`, `npm test`, and a dry-run bundle for each change in
`services/dns/`. On a push to `main` (or a manual run), it applies the D1 migrations, runs `wrangler deploy`,
and checks `/health`. Add these repository secrets (Settings → Secrets and variables → Actions):

| Secret | Value |
|---|---|
| `CLOUDFLARE_API_TOKEN` | A token from the **Edit Cloudflare Workers** template, plus **Account → D1 → Edit**. Account resources: the account above. Zone resources: `rithviknishad.dev`. |
| `CLOUDFLARE_ACCOUNT_ID` | `9df47fe134fdde8490d93c610a9ec93e` |

If a secret is missing, the deploy job fails with an error that names it. The workflow does not touch
`CF_DNS_API_TOKEN`.

### 8.5 Smoke test

`node services/dns/scripts/smoke.mjs` registers a clinic `smoke-test-xxxx`, sets `A` to `192.0.2.10`,
waits for `dig +short <name> @1.1.1.1`, sets a TXT value and waits for it, checks a wrong key (`401`), rotates
(old credentials `401`, new `200`), then deletes the clinic and checks with the authoritative server that the
records are gone. It deletes the test clinic also after a failure (`--keep` stops this). The last line is
`# RESULT: PASS` or `# RESULT: FAIL`.

### 8.6 Logs

- Live: `npx wrangler tail care-pi-dns --format pretty`.
- Dashboard: Workers & Pages → `care-pi-dns` → Observability (Workers Logs, 3 days on Free).
- Events (JSON lines): `register`, `address`, `acme_txt`, `settings`, `rotate`, `revoke`,
  `revoke_cleanup_failed`, `dns_error`, `housekeeping`, `housekeeping_txt_failed`,
  `housekeeping_revoke_failed`, `internal_error`.

### 8.7 Backups

- D1 Time Travel restores to any minute of the last 7 days (Free) or 30 days (Paid):
  `npx wrangler d1 time-travel info care-pi-dns`, then
  `npx wrangler d1 time-travel restore care-pi-dns --timestamp=<RFC 3339 or Unix time>`.
- A file copy (do this before each migration, and monthly):

  ```sh
  npx wrangler d1 export care-pi-dns --remote --output "care-pi-dns-$(date +%F).sql"
  ```

  The export holds no usable secret (only password hashes and public keys). It holds the public IP addresses
  of the clinics: keep it private. Do not commit it.
- After a restore, DNS records and D1 can disagree. The next `PUT address` of each device repairs its A
  record within one hour (§5, the one-hour refresh).

### 8.8 Revoke a clinic by hand (abuse)

```sh
npx wrangler d1 execute care-pi-dns --remote --command \
  "UPDATE clinics SET revoked_at = unixepoch(), acme_user = NULL, acme_key_hash = NULL, listing_opt_in = 0 WHERE id = '<id>'"
```

The key and the acme-dns credentials stop working at once. The cron deletes the DNS records within
15 minutes. The id is never used again.

### 8.9 Find things

```sh
npx wrangler d1 execute care-pi-dns --remote --command "SELECT id, created_at, last_seen, revoked_at FROM clinics ORDER BY created_at DESC LIMIT 20"
npx wrangler d1 execute care-pi-dns --remote --command "SELECT COUNT(*) FROM clinics WHERE revoked_at IS NULL"
```

## 9. Cost on the Free plan

| Item | Free plan limit | Use per clinic | Ceiling |
|---|---|---|---|
| Worker requests | 100,000 per day | about 5 per day (4 address refreshes, rare TXT and status calls) | about 20,000 clinics |
| Worker CPU | 10 ms per request | < 1 ms (one Ed25519 check, two SHA-256) | — |
| Subrequests (fetch and D1) | 50 per request | max. about 20 (`DELETE`) | — |
| D1 rows written | 100,000 per day | about 20 per day (rate, replay, last seen, address) | about 5,000 clinics |
| D1 rows read | 5,000,000 per day | about 30 per day | — |
| D1 storage | 500 MB per database | < 1 KB | — |
| Workers Logs | 200,000 events per day | about 5 per day | about 40,000 clinics |
| Cron triggers | 5 per account | 1 | — |
| DNS records in the zone | 1,000 (zone made before September 2024) | 1–2 (A, AAAA) + up to 2 TXT for minutes | **about 400–900 clinics** |
| Cloudflare API | 1,200 calls per 5 minutes per user | about 4 per day | — |

Thus the first limit is the number of DNS records in the zone. Before about 400 clinics, move to a paid zone
plan for `rithviknishad.dev` (higher record limits; check the current numbers in the Cloudflare DNS
documentation) or to a dedicated domain on a paid plan. Workers Paid ($5 per month) raises the other limits.
A new Free zone made after September 2024 has only 200 records, so a new Free zone is not a solution.

## 10. The `/cert` page

`plans/07-first-contact-trust.md` §4.4: a device that already trusts the local CA of the care-pi device
enrols the next device with a QR code.

- The console shows a QR code with
  `https://care-pi.rithviknishad.dev/cert#<base64url of the DER root certificate>`.
- Browsers do not send the part after `#` to the server. The page is static: its script reads
  `location.hash`, decodes the certificate on the device, and shows the subject, the issuer, the validity
  dates, whether it is a CA, and the **SHA-256 fingerprint** (`AA:BB:…`, the same form as
  `openssl x509 -noout -fingerprint -sha256`).
- The operator compares the fingerprint with the console screen. The screen is the authenticated channel.
- The page builds the `.crt` (DER), `.pem`, and `.mobileconfig` (`com.apple.security.root` payload, profile
  UUIDs derived from the fingerprint, so a new download replaces the old profile) files on the device.
- The page loads no external asset and makes no network request: CSP `connect-src 'none'`, no inline script,
  `Referrer-Policy: no-referrer`.
- The QR code needs internet on the new device. Without internet, the console can show the fingerprint only.
- Verify on real devices (SP7): the iOS profile install from a downloaded `.mobileconfig`, and the Android
  CA install path for each vendor.

## 11. Tests

`npm test` in `services/dns` runs the type check, the workerd tests (`@cloudflare/vitest-plugin`, local D1),
and the Node tests of the browser scripts:

- every endpoint, with signature failures, a changed body, a changed path, a replay, a stale timestamp, a used
  or expired nonce, and each rate limit;
- acme-dns `POST /update`: the happy path, the rolling two values, a wrong key, an unknown user, another
  clinic's subdomain, a bad value;
- `/lookup`: opt-in off by default, opt-in with `PATCH`, another network, `?ip=` of another network, a revoked
  clinic, IPv6 `/64` grouping;
- the Cloudflare DNS client against a fake API, and the scheduled housekeeping;
- the `/cert` page: static checks in workerd, and the page script in Node against an OpenSSL-made root
  certificate (fingerprint, names, dates, `.crt`, `.pem`, `.mobileconfig`, bad input);
- the signature test vectors (`testdata/vectors.json`), also checked with Go `crypto/ed25519`.
