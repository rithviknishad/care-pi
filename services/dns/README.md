# services/dns: the care-pi DNS/ACME service

This directory holds the care-pi DNS/ACME side service: a Cloudflare Worker with a D1 database at
`https://care-pi.rithviknishad.dev`. It gives each care-pi device:

1. A clinic id and a public name, `<id>.care-pi.rithviknishad.dev`.
2. DDNS: the name points to the LAN address of the device (A, AAAA; TTL 60 s).
3. An acme-dns-compatible `POST /update`, so that Caddy (`dns.providers.acmedns`) gets a Let's Encrypt
   certificate for the name with DNS-01.
4. Credential rotation and revocation.
5. Public pages: `GET /` (with the opt-in "clinics on my network" list) and `GET /cert` (QR enrolment).

Read these documents:

| Document | Content |
|---|---|
| [`API.md`](API.md) | The wire protocol: paths, signatures, bodies, errors, test vectors. The Go agent uses it. |
| [`../../docs/dns-service.md`](../../docs/dns-service.md) | Design, data model, limits, privacy, operations, cost, the Public Suffix List plan. |
| [`testdata/vectors.json`](testdata/vectors.json) | Signature test vectors (RFC 8032 test keys). |

## Layout

| Path | Content |
|---|---|
| `wrangler.toml` | Worker `care-pi-dns`: the custom domain, the D1 binding `DB`, the cron trigger, the variables. |
| `migrations/` | D1 schema (`0001_init.sql`). |
| `src/index.ts` | `fetch` and `scheduled` handlers. |
| `src/router.ts` | The routes. No framework. |
| `src/handlers/` | `clinics.ts` (`/v1`), `acme.ts` (`/update`), `lookup.ts`, `pages.ts`. |
| `src/auth.ts` | Ed25519 request signatures (WebCrypto), the replay cache, the acme-dns key check. |
| `src/cf-dns.ts` | The Cloudflare DNS records API client. |
| `src/dns-ops.ts` | A/AAAA upsert, the TXT rolling update, the cleanup of a clinic. |
| `src/ratelimit.ts` | Fixed-window counters in D1. |
| `src/ip.ts`, `src/ids.ts`, `src/encoding.ts` | Address policy, ids and secrets, encodings. |
| `src/pages/` | Static HTML, CSS, and the browser scripts (strings; no external assets). |
| `test/` | Tests in workerd (`@cloudflare/vitest-plugin`) and `test/node/` (the browser scripts in Node). |
| `scripts/vectors.mjs` | Makes `testdata/vectors.json`. |
| `scripts/smoke.mjs` | End-to-end test of a deployed service (register, DDNS, `dig`, TXT, rotate, delete). |
| `scripts/fake-cf-api.mjs` | A fake Cloudflare DNS API for `wrangler dev`. |

## Commands

Run the commands in this directory. Node 20 or later.

```sh
npm ci
npm test                  # typecheck, workerd tests, Node tests
npm run vectors           # make testdata/vectors.json again
npx wrangler deploy --dry-run --outdir .dryrun
```

Local development with a fake DNS API:

```sh
node scripts/fake-cf-api.mjs &                       # 127.0.0.1:8788
echo 'CF_DNS_API_TOKEN=dev-token' > .dev.vars         # .dev.vars is in .gitignore
npm run migrate:local
npx wrangler dev --local --var CF_API_BASE:http://127.0.0.1:8788/client/v4
node scripts/smoke.mjs --base http://localhost:8787 --no-dig
```

Deploy, secrets, backups, and logs: `docs/dns-service.md` section 8. The workflow
`.github/workflows/dns-service.yml` tests each change and deploys from `main`.

## Notes

- The package `@cloudflare/vitest-pool-workers` is now `@cloudflare/vitest-plugin` (the old name is
  deprecated). The tests use the new package with Vitest 4.
- The configuration is `wrangler.toml` (not `wrangler.jsonc`) because the project plan asks for TOML. Both work.
