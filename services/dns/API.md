# care-pi DNS service: wire protocol

This document is the contract between a care-pi device (the Go agent, `agent/internal/dnsclient`) and the
care-pi DNS/ACME service (this directory). You can write a client from this document only.
The design, the operations, and the privacy notes are in `docs/dns-service.md`.

- Base URL: `https://care-pi.rithviknishad.dev` (TLS only).
- Clinic names: `<id>.care-pi.rithviknishad.dev`.
- ACME challenge names: `_acme-challenge.<id>.care-pi.rithviknishad.dev` (TXT, written by the service).
- Test vectors: `testdata/vectors.json` (section 3.6).

## 1. Conventions

- Request and response bodies are JSON objects in UTF-8. A request body is 4096 bytes or less (else `413`).
- Send `Content-Type: application/json` with a body. The service does not require it.
- The service ignores unknown request fields.
- Times in responses are RFC 3339 UTC strings without fractions (`2027-01-01T00:00:00Z`), except
  `server_time`, which is Unix seconds.
- Every error response has this form, with a stable `error` code (section 7):

  ```json
  {"error": "bad_signature", "message": "The signature does not match the canonical request and the registered key."}
  ```

- A `429` response has the `Retry-After` header (seconds) and the fields `bucket` and `retry_after`.

## 2. Endpoints

| Method and path | Auth | Purpose | Section |
|---|---|---|---|
| `GET /v1/nonce` | none | Get a single-use registration nonce. | 4.1 |
| `POST /v1/register` | signature by the new key | Register a clinic. Get the id and the acme-dns credentials. | 4.2 |
| `GET /v1/clinics/{id}` | signature | Read the status of the clinic. | 5.1 |
| `PATCH /v1/clinics/{id}` | signature | Change settings (`listing_opt_in`). | 5.2 |
| `PUT /v1/clinics/{id}/address` | signature | Set the A and AAAA records (DDNS). | 5.3 |
| `POST /v1/clinics/{id}/rotate` | signature (+ new key) | New acme-dns credentials; optionally a new signing key. | 5.4 |
| `DELETE /v1/clinics/{id}` | signature | Revoke the clinic and delete its DNS records. | 5.5 |
| `POST /update` | `X-Api-User`, `X-Api-Key` | acme-dns: set the ACME TXT value. Caddy calls this. | 6.1 |
| `POST /register` | — | acme-dns open registration: always `403 registration_closed`. | 6.2 |
| `GET /health` | none | `200 {"status":"ok"}`. | 6.3 |
| `GET /lookup` | none | Opted-in clinics of the caller's public network. | 8.1 |
| `GET /`, `GET /cert` | none | Static pages. `/cert#<DER>` is for QR enrolment. | 8.2 |

## 3. Request signatures

The `/v1` API uses Ed25519 request signatures (RFC 8032, "pure" Ed25519, no pre-hash).
The device keeps the private key. The service keeps the public key. No bearer token exists.

### 3.1 Keys

- The device makes an Ed25519 key pair before registration (Go: `ed25519.GenerateKey(rand.Reader)`).
- The public key goes on the wire as **standard base64 with padding** of the 32 raw bytes (44 characters),
  for example `11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=`.
- Keep the private key in the agent state with mode `0600`. It is the identity of the clinic at the service.

### 3.2 Canonical string

The device signs the UTF-8 bytes of this string. The five lines are joined by one LF (`0x0A`). There is no
LF at the end.

```text
CARE-PI-DNS-V1
<METHOD>
<PATH>
<TIMESTAMP>
<BODY-SHA256>
```

| Line | Value |
|---|---|
| 1 | The literal `CARE-PI-DNS-V1`. |
| 2 | The HTTP method in upper case: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`. |
| 3 | The request path exactly as sent, without the query string and without a trailing slash, for example `/v1/clinics/lumine-k3x9/address`. The `/v1` API uses no query strings. |
| 4 | The value of the `X-Care-Pi-Timestamp` header: Unix time in decimal seconds, no sign, no leading zeros, max. 12 digits. |
| 5 | Lower-case hex of the SHA-256 of the **exact body bytes** that the device sends. An empty body (GET, DELETE) gives `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`. |

Hash the bytes that go on the wire. Do not encode the JSON again after you hash it.

### 3.3 Headers

| Header | Value |
|---|---|
| `X-Care-Pi-Timestamp` | Line 4 of the canonical string. |
| `X-Care-Pi-Signature` | Standard base64 with padding of the 64-byte signature (88 characters). |
| `X-Care-Pi-Signature-New` | Only for a key rotation (section 5.4): the signature of the same canonical string by the new key. |

### 3.4 Replay protection and clock

- The service accepts a timestamp within **±300 seconds** of its clock. Else: `401 stale_timestamp` with
  `server_time` (Unix seconds) in the body. A Pi has no battery clock: if NTP is not synchronized, use
  `server_time` (or `server_time` from `GET /v1/nonce`) to compute an offset, then sign again.
- The service remembers each accepted canonical string (per clinic) for the window. The same canonical string
  again gives `401 replayed`. Two identical requests in the same second are a replay: sign each retry with the
  current timestamp. For an idempotent request (`PUT address`, `PATCH`), `replayed` means that the first copy
  arrived.
- The registration (section 4.2) also uses a single-use nonce.

### 3.5 Verification order (for error handling)

1. Too many failed authentications from the caller's network → `429` (`bucket: "authfail"`).
2. The id is not a valid id → `404 unknown_clinic`.
3. Missing headers → `401 missing_signature`; bad timestamp format → `401 bad_timestamp`; out of window →
   `401 stale_timestamp`; signature not 64 bytes in standard base64 → `401 bad_signature`.
4. No such clinic → `404 unknown_clinic`. Revoked clinic → `410 revoked`.
5. Signature does not verify → `401 bad_signature` (this counts as a failed authentication).
6. Replay → `401 replayed`.
7. Rate limit of the endpoint → `429`.
8. Body validation → `400`.

A request that passes step 6 counts against the rate limit, also if step 8 rejects it.

### 3.6 Test vectors

`testdata/vectors.json` holds these vectors and more (`status`, `settings`, `rotate`, `delete`).
The keys are the RFC 8032 section 7.1 test keys. **Never use them for a real clinic.**

| Key | Seed (hex) | Public key (base64) |
|---|---|---|
| current | `9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60` | `11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=` |
| new | `4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb` | `PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw=` |

Go: `priv := ed25519.NewKeyFromSeed(seed)`; `sig := ed25519.Sign(priv, []byte(canonical))`.

**Vector `address`** (timestamp `1798761600`, key `current`):

```text
body (35 bytes):  {"ipv4":"192.168.1.23","ipv6":null}
body SHA-256:     339370aff12f70c67beeb06686b315840b3dfa576b34479a49767b624b6217d4
canonical:        "CARE-PI-DNS-V1\nPUT\n/v1/clinics/lumine-k3x9/address\n1798761600\n339370aff12f70c67beeb06686b315840b3dfa576b34479a49767b624b6217d4"
X-Care-Pi-Signature: Km62Ebr0YPOvKs+mD8HBbBIYGSIuH20JtOt/1AAsACiQ3NOFQL7vyYs6Dd78Byp8A/nzPDiPUrAgt37rAXfEBQ==
```

**Vector `register`** (timestamp `1798761600`, key `current`; the body holds the same key):

```text
body:         {"hostname_hint":"lumine","pubkey":"11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=","nonce":"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA","listing_opt_in":false}
body SHA-256: 8d76c7f84eae06beec45d4335a76979a13b740de13af1f5f86e1b64dd90648e6
canonical:    "CARE-PI-DNS-V1\nPOST\n/v1/register\n1798761600\n8d76c7f84eae06beec45d4335a76979a13b740de13af1f5f86e1b64dd90648e6"
X-Care-Pi-Signature: TRufeuMLWG2B8CDQNyp7GxuZ8upXWyj71NfrlqksXO49l965EEzaC7y55tUtIBsqlxcN9IQruS45n6W17V6NAQ==
```

**Vector `rotate_with_new_key`** (both signatures are over the same canonical string):

```text
body:         {"pubkey":"PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw="}
body SHA-256: 25642bdd5b0bffc8404da23d9aac2ec2136a472f4897960f90bcc907b36d3bb8
canonical:    "CARE-PI-DNS-V1\nPOST\n/v1/clinics/lumine-k3x9/rotate\n1798761600\n25642bdd5b0bffc8404da23d9aac2ec2136a472f4897960f90bcc907b36d3bb8"
X-Care-Pi-Signature:     ORZueO8mjAMAP6xS51h5zOlOHmVUSYHg20U8THZNalkU0hbReD0Z5g6SDbwBjX0qUDIEB/30IoNCsHr6x6rQBA==
X-Care-Pi-Signature-New: WL8PBwqw6JAG+ozKSJcSdaIYQ9ibFAxv1vJD4W3JnhM7QiKpxEZDXtfaXj9i4zmsQi+BNiESGEdopRyWYClJBA==
```

**Vector `delete`** (empty body):

```text
canonical:    "CARE-PI-DNS-V1\nDELETE\n/v1/clinics/lumine-k3x9\n1798761600\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
X-Care-Pi-Signature: bYxrkrbQZlJI4O2E0Vuefs1/hmz389RJTQzGAZc2DdUoBIHMgXhfulJ5zfIo0iuPPXS/aigxMmtx0on9jBLvDQ==
```

The service tests (`test/auth.test.ts`) and a Go `crypto/ed25519` check verify these vectors.
`npm run vectors` makes the file again (Ed25519 is deterministic, so the output does not change).

## 4. Registration

### 4.1 `GET /v1/nonce`

```http
GET /v1/nonce HTTP/1.1
Host: care-pi.rithviknishad.dev
```

```json
{"nonce": "1hLyOzLrp-8GZeMqqSJYvJfUVS6Mjz0CfPIRrLva8Ro", "expires_in": 300, "server_time": 1793523197}
```

The nonce is 43 base64url characters, works one time, and expires after `expires_in` seconds.
Limit: 20 per minute per public network.

### 4.2 `POST /v1/register`

Body:

| Field | Type | Rule |
|---|---|---|
| `hostname_hint` | string | 1 to 63 characters. The service makes the id from it (section 4.3). Use the Pi host name (for example the Imager host name). |
| `pubkey` | string | The new Ed25519 public key, standard base64 (section 3.1). |
| `nonce` | string | From `GET /v1/nonce`. |
| `listing_opt_in` | boolean, optional | Default `false`. `true` puts the clinic on `GET /lookup` (section 8.1). |

Sign the request with the **new key** (the key in `pubkey`), with the headers of section 3.3.

```http
POST /v1/register HTTP/1.1
Host: care-pi.rithviknishad.dev
Content-Type: application/json
X-Care-Pi-Timestamp: 1798761600
X-Care-Pi-Signature: TRufeuMLWG2B8CDQNyp7GxuZ8upXWyj71NfrlqksXO49l965EEzaC7y55tUtIBsqlxcN9IQruS45n6W17V6NAQ==

{"hostname_hint":"lumine","pubkey":"11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=","nonce":"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA","listing_opt_in":false}
```

Response `201`:

```json
{
  "id": "lumine-k3x9",
  "fulldomain": "lumine-k3x9.care-pi.rithviknishad.dev",
  "acme": {
    "username": "eecebdd9-2832-4e05-8f71-9424fcaa25bf",
    "password": "q0kJ3Fh5mQzVb7pX2sLr9tYwN4cGd8eA1uZo6iHjKlM",
    "subdomain": "lumine-k3x9",
    "fulldomain": "_acme-challenge.lumine-k3x9.care-pi.rithviknishad.dev",
    "server_url": "https://care-pi.rithviknishad.dev",
    "allowfrom": []
  },
  "listing_opt_in": false,
  "created_at": "2027-01-01T00:00:00Z"
}
```

- Keep `acme.password`: the service keeps only its SHA-256 and cannot show it again. If you lose it, rotate (5.4).
- `acme` has the fields of an acme-dns registration, so it is also a valid acme-dns client storage entry.
- There is no `ddns_token`: the signing key authenticates the DDNS updates.

Errors: `400 bad_request` (field), `401 bad_signature`, `401 bad_nonce` (unknown, used, or expired: get a new
nonce), `401 stale_timestamp`, `429` (`register`: 5 per public network per day; `authfail`).

### 4.3 Clinic id

- Form: `<slug>-<suffix>`, for example `lumine-k3x9`. Regular expression:
  `^[a-z0-9](?:[a-z0-9]|-(?!-)){0,19}-[a-z2-7]{4}$`.
- `slug`: the hint, NFKD-normalized, combining marks removed, lower case, each run of other characters replaced
  by one `-`, trimmed of `-`, max. 20 characters. An empty slug becomes `clinic`.
- `suffix`: 4 random characters of the RFC 4648 base32 alphabet in lower case (`a-z`, `2-7`): 20 bits.
- The service never gives an id two times, also after `DELETE`.
- Show the id to the operator: "is this my clinic?" depends on it (`docs/dns-service.md` section 6).

## 5. Clinic endpoints

All need a signature with the current key (section 3). `{id}` is the clinic id.
Rate limits per clinic: `address` 6 per minute; `manage` (status, settings, rotate, delete) 6 per minute.

### 5.1 `GET /v1/clinics/{id}`

Response `200`:

```json
{
  "id": "lumine-k3x9",
  "fulldomain": "lumine-k3x9.care-pi.rithviknishad.dev",
  "hostname_hint": "lumine",
  "created_at": "2027-01-01T00:00:00Z",
  "last_seen": "2027-01-01T00:05:00Z",
  "listing_opt_in": false,
  "address": {"ipv4": "192.168.1.23", "ipv6": null, "ttl": 60, "updated_at": "2027-01-01T00:01:00Z"},
  "acme": {"username": "…", "subdomain": "lumine-k3x9", "fulldomain": "_acme-challenge.lumine-k3x9.care-pi.rithviknishad.dev",
           "server_url": "https://care-pi.rithviknishad.dev", "allowfrom": []}
}
```

`address` is `null` before the first `PUT address`. `acme` has no password.

### 5.2 `PATCH /v1/clinics/{id}`

Body: `{"listing_opt_in": true}` (boolean, required). Response `200`: the status object of 5.1.

### 5.3 `PUT /v1/clinics/{id}/address`

Body: `{"ipv4": "192.168.1.23", "ipv6": null}`. The request replaces both records:

| Field | Value | Effect |
|---|---|---|
| `ipv4` | dotted-quad string | Sets `A <id>.care-pi.rithviknishad.dev` (TTL 60, not proxied). |
| `ipv4` | `null` or absent | Deletes the A record. |
| `ipv6` | IPv6 string | Sets the AAAA record (the service stores the RFC 5952 form). |
| `ipv6` | `null` or absent | Deletes the AAAA record. |

At least one of the two must be a string. Accepted addresses (policy `private`, the default):

- IPv4: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10` (CGNAT), and the documentation ranges
  `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`. Never loopback, link-local, `0.0.0.0/8`, multicast, or
  reserved. Public IPv4 addresses only with the server setting `ADDRESS_POLICY=any`.
- IPv6: ULA `fc00::/7`, global unicast `2000::/3`, documentation `2001:db8::/32`. Never link-local, loopback,
  multicast, or IPv4-mapped.

Response `200`:

```json
{"id": "lumine-k3x9", "fulldomain": "lumine-k3x9.care-pi.rithviknishad.dev", "ipv4": "192.168.1.23", "ipv6": null,
 "ttl": 60, "changed": true, "updated_at": "2027-01-01T00:01:00Z"}
```

- `changed` is `true` when the addresses differ from the stored addresses.
- An unchanged address makes no DNS change. After one hour, the service writes an unchanged address again
  (this repairs a record that was changed by hand).
- When to call: at start, when an address changes (NetworkManager dispatcher), and every 6 hours.
  Do not call more often: the limit is 6 per minute, and the free D1 quota counts writes.

Errors: `400 bad_address` (with a reason), `502 dns_error` (the Cloudflare API failed: try again later with a
new timestamp), `503 dns_unconfigured`.

### 5.4 `POST /v1/clinics/{id}/rotate`

Body `{}`: new acme-dns credentials. The old `acme.username` and `acme.password` stop working at once.

Body `{"pubkey": "<new key, base64>"}` with the extra header `X-Care-Pi-Signature-New`: also replace the
signing key. Sign the same canonical string with the old key (`X-Care-Pi-Signature`) and the new key
(`X-Care-Pi-Signature-New`). After the response, only the new key works.

Response `200`:

```json
{"id": "lumine-k3x9", "fulldomain": "lumine-k3x9.care-pi.rithviknishad.dev", "pubkey": "<the key now in use>",
 "acme": {"username": "…", "password": "…", "subdomain": "lumine-k3x9",
          "fulldomain": "_acme-challenge.lumine-k3x9.care-pi.rithviknishad.dev",
          "server_url": "https://care-pi.rithviknishad.dev", "allowfrom": []}}
```

Write the new credentials to the Caddy configuration (section 6.4) before Caddy needs a certificate.
Errors: `401 bad_new_signature`.

### 5.5 `DELETE /v1/clinics/{id}`

No body. The service revokes the clinic at once (the key and the acme-dns credentials stop working), then
deletes the A, AAAA, and TXT records. Response `200`:

```json
{"id": "lumine-k3x9", "revoked_at": "2027-01-01T01:00:00Z", "dns_cleanup": "done"}
```

`dns_cleanup` is `pending` if the Cloudflare API failed; the scheduled handler deletes the records within
15 minutes. Later requests for this id give `410 revoked`. To use the service again, register again (new id).

## 6. acme-dns API

The service is compatible with the client side of acme-dns (`github.com/joohoi/acme-dns`), as used by
`github.com/libdns/acmedns` v0.5 and the Caddy module `dns.providers.acmedns` (`github.com/caddy-dns/acmedns`).

**Difference from acme-dns: no CNAME.** acme-dns serves the TXT record at `<uuid>.<acme-dns zone>` and the
user adds `CNAME _acme-challenge.<domain> → <uuid>.<acme-dns zone>`. This service writes the TXT record
**directly** at `_acme-challenge.<id>.care-pi.rithviknishad.dev` in the Cloudflare zone. The client sends
only `{subdomain, txt}` to `/update`; it does not read `fulldomain`, so the client needs no change.
Thus `acme.fulldomain` is `_acme-challenge.<id>.care-pi.rithviknishad.dev`, and the device must **not** create
a CNAME.

### 6.1 `POST /update`

```http
POST /update HTTP/1.1
Host: care-pi.rithviknishad.dev
X-Api-User: eecebdd9-2832-4e05-8f71-9424fcaa25bf
X-Api-Key: q0kJ3Fh5mQzVb7pX2sLr9tYwN4cGd8eA1uZo6iHjKlM
Content-Type: application/json

{"subdomain": "lumine-k3x9", "txt": "rs8SFh1wl25_ohZ9sYmFTWYc2SagaxJQxqjVXjsUbaY"}
```

- `subdomain` must be the clinic id of the user. `txt` is the ACME DNS-01 value: 43 base64url characters.
- Response `200`: `{"txt": "rs8SFh1wl25_ohZ9sYmFTWYc2SagaxJQxqjVXjsUbaY"}`.
- Like acme-dns, the service keeps the **two** newest values (a name and its wildcard need two). It deletes
  values older than one hour (the client does not delete: `DeleteRecords` of libdns/acmedns does nothing).
  The same value again is a no-op `200`.
- TTL 60 seconds. The record is on the Cloudflare authoritative servers within seconds.
- Errors (acme-dns codes): `401 forbidden` (unknown user, wrong key, or `subdomain` of another clinic),
  `400 bad_txt`, `400 malformed_json_payload`, `429` (6 per minute per clinic), `502 dns_error`.
- The service stores `SHA-256(password)` only. The password is 256 random bits, so a slow hash adds nothing.

### 6.2 `POST /register`

Always `403 {"error": "registration_closed", …}`. Use `POST /v1/register`.

### 6.3 `GET /health`

`200 {"status": "ok"}`.

### 6.4 Caddy configuration (for the agent)

Recommended: a credentials file (mode `0600`, readable by Caddy) and one `dns acmedns` line:

```json
{"username": "eecebdd9-2832-4e05-8f71-9424fcaa25bf", "password": "q0kJ3Fh5mQzVb7pX2sLr9tYwN4cGd8eA1uZo6iHjKlM",
 "subdomain": "lumine-k3x9", "server_url": "https://care-pi.rithviknishad.dev"}
```

```caddyfile
lumine-k3x9.care-pi.rithviknishad.dev {
	tls {
		dns acmedns /etc/caddy/acme-dns.json
		resolvers 1.1.1.1 8.8.8.8
	}
	…
}
```

- The module also accepts `dns acmedns { username … password … subdomain … server_url … }`. Its `password`
  goes through the Caddy replacer, so `{env.*}` placeholders work there. The credentials file keeps the
  password out of the Caddyfile and the Caddy JSON.
- `resolvers`: Caddy checks the propagation of the TXT record. A clinic router can block or rewrite DNS; public
  resolvers avoid this.
- Use the Let's Encrypt staging CA for tests (`acme_ca https://acme-staging-v02.api.letsencrypt.org/directory`):
  the production limits are shared by all clinics (`docs/dns-service.md` section 7).

## 7. Errors

| Status | `error` | Meaning |
|---|---|---|
| 400 | `bad_request` | A field is missing or wrong; `message` names it. |
| 400 | `bad_address` | `PUT address`: not an address, or outside the policy. |
| 400 | `bad_txt`, `malformed_json_payload` | `/update` body. |
| 400 | `unknown_ip` | `/lookup`: the service cannot read the caller's IP address. |
| 401 | `missing_signature`, `bad_timestamp`, `stale_timestamp`, `bad_signature`, `replayed` | Section 3. |
| 401 | `bad_nonce` | Registration nonce unknown, used, or expired. |
| 401 | `bad_new_signature` | Rotation: `X-Care-Pi-Signature-New` missing or wrong. |
| 401 | `forbidden` | `/update`: credentials or subdomain wrong. |
| 403 | `registration_closed` | `POST /register`. |
| 403 | `ip_mismatch` | `/lookup?ip=` is not the caller's address. |
| 404 | `unknown_clinic`, `not_found` | No such clinic; no such path. |
| 405 | `method_not_allowed` | The `Allow` header has the methods. |
| 410 | `revoked` | The clinic was deleted. Register again. |
| 413 | `payload_too_large` | Body over 4096 bytes. |
| 429 | `rate_limited` | `bucket`, `retry_after`, `Retry-After`. |
| 500 | `internal` | Bug. Try again later. |
| 502 | `dns_error` | The Cloudflare DNS API failed. Try again later (new timestamp). |
| 503 | `dns_unconfigured`, `id_exhausted` | The service has no DNS token; no free id for this hint (use another hint). |

Client rules: retry `429` after `Retry-After`; retry `5xx` with backoff (start 30 s, max. 15 min) and a new
timestamp; do not retry other `4xx` without a change. On `410`, stop and show "the registration was revoked"
in the console.

## 8. Public endpoints

### 8.1 `GET /lookup`

Lists the clinics that have `listing_opt_in = true`, are not revoked, and registered from (or last called
from) the caller's public network: the IPv4 address, or the IPv6 `/64`.

```json
{"ip": "203.0.113.7", "network": "203.0.113.7",
 "clinics": [{"id": "lumine-k3x9", "fulldomain": "lumine-k3x9.care-pi.rithviknishad.dev", "registered_at": "2027-01-01T00:00:00Z"}]}
```

`?ip=<address>` is accepted only if it is in the caller's network (else `403 ip_mismatch`): nobody can list
another network. Limit: 30 per minute per network. The page `GET /` calls this endpoint.

### 8.2 `GET /cert#<certificate>`

The console shows a QR code with `https://care-pi.rithviknishad.dev/cert#<base64url of the DER root
certificate, no padding>` (standard base64 also works). The fragment never goes to the server. The page shows
the SHA-256 fingerprint (upper-case hex, `:`-separated, the same form as `openssl x509 -fingerprint -sha256`)
and offers `.crt` (DER), `.pem`, and `.mobileconfig` files, built on the device. Keep the root certificate
under about 1 KB of DER so that the QR code stays readable.

## 9. What the agent must implement

1. Make and keep an Ed25519 key (3.1). Sign each `/v1` request (3.2, 3.3). Check your code with the vectors (3.6).
2. Register: `GET /v1/nonce`, then `POST /v1/register` (4). Keep `id`, `fulldomain`, and `acme` (with the
   password, mode `0600`). Show the id in the console.
3. DDNS: `PUT /v1/clinics/{id}/address` with the LAN addresses at start, on change, and every 6 hours (5.3).
4. Caddy: render `dns acmedns` with the `acme` block (6.4). Do not create a CNAME.
5. Rotation (5.4) from the console; key rotation with both signatures. Re-render Caddy after it.
6. Deletion (5.5) when the operator leaves public-cert mode.
7. Clock: on `stale_timestamp`, use `server_time` (3.4). Retries: section 7.
8. Optional: `PATCH` `listing_opt_in` (5.2) from a console switch (default off).
