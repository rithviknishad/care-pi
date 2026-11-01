#!/usr/bin/env node
// End-to-end smoke test of a deployed care-pi DNS service (docs/dns-service.md, "Smoke test").
//
//   node scripts/smoke.mjs [--base https://care-pi.rithviknishad.dev] [--address 192.0.2.10]
//                          [--resolver 1.1.1.1] [--authoritative ray.ns.cloudflare.com] [--no-dig] [--keep]
//
// It registers a test clinic, sets its A record, checks it with dig, sets an ACME TXT value, checks it with
// dig, rotates the credentials, and deletes the clinic (also after a failure, unless --keep).

import { execFile } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { promisify } from "node:util";

const run = promisify(execFile);
const args = process.argv.slice(2);
const opt = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};
const BASE = opt("--base", "https://care-pi.rithviknishad.dev").replace(/\/$/, "");
const ADDRESS = opt("--address", "192.0.2.10");
const RESOLVER = opt("--resolver", "1.1.1.1");
const AUTHORITATIVE = opt("--authoritative", "ray.ns.cloudflare.com");
const DIG = !args.includes("--no-dig");
const KEEP = args.includes("--keep");

const b64 = (bytes) => Buffer.from(bytes).toString("base64");
const sha256Hex = (text) => createHash("sha256").update(text, "utf8").digest("hex");
const log = (line) => console.log(line);
let tick = 0;

function check(cond, message) {
  if (!cond) throw new Error(`FAILED: ${message}`);
}

async function newKey() {
  const pair = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
  const pub = new Uint8Array(await crypto.subtle.exportKey("raw", pair.publicKey));
  return { privateKey: pair.privateKey, publicB64: b64(pub) };
}

async function request(method, path, { body, headers = {} } = {}) {
  const res = await fetch(`${BASE}${path}`, { method, headers, body });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    json = null;
  }
  return { status: res.status, json, text };
}

async function signed(key, method, path, value) {
  const body = value === undefined ? "" : JSON.stringify(value);
  // A new timestamp for each request: two identical requests in the same second are a replay.
  const ts = String(Math.floor(Date.now() / 1000) - (tick++ % 60));
  const canonical = ["CARE-PI-DNS-V1", method, path, ts, sha256Hex(body)].join("\n");
  const sig = new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, key.privateKey, new TextEncoder().encode(canonical)));
  const headers = { "x-care-pi-timestamp": ts, "x-care-pi-signature": b64(sig) };
  if (body) headers["content-type"] = "application/json";
  return request(method, path, body ? { body, headers } : { headers });
}

async function dig(type, name, server) {
  const { stdout } = await run("dig", ["+short", type, name, `@${server}`]);
  return stdout.trim().split("\n").filter(Boolean).map((l) => l.replace(/^"|"$/g, ""));
}

async function waitDig(type, name, server, expected, seconds = 90) {
  const deadline = Date.now() + seconds * 1000;
  for (;;) {
    const got = await dig(type, name, server);
    const ok = expected === null ? got.length === 0 : got.includes(expected);
    if (ok || Date.now() > deadline) {
      log(`$ dig +short ${type} ${name} @${server}\n${got.length ? got.join("\n") : "(no answer)"}`);
      check(ok, `dig ${type} ${name} @${server}: expected ${expected ?? "no answer"}`);
      return;
    }
    await new Promise((r) => setTimeout(r, 3000));
  }
}

const txt = () => randomBytes(32).toString("base64url");

let key = null;
let id = null;
try {
  log(`# care-pi DNS smoke test against ${BASE} at ${new Date().toISOString()}`);
  const home = await request("GET", "/");
  log(`GET / -> ${home.status} (${home.text.length} bytes)`);
  check(home.status === 200, "GET / is 200");
  const cert = await request("GET", "/cert");
  log(`GET /cert -> ${cert.status}`);
  check(cert.status === 200, "GET /cert is 200");

  const nonce = await request("GET", "/v1/nonce");
  log(`GET /v1/nonce -> ${nonce.status} ${JSON.stringify(nonce.json)}`);
  check(nonce.status === 200, "nonce");

  key = await newKey();
  const reg = await signed(key, "POST", "/v1/register", { hostname_hint: "smoke-test", pubkey: key.publicB64, nonce: nonce.json.nonce, listing_opt_in: false });
  const shown = reg.json && reg.json.acme ? { ...reg.json, acme: { ...reg.json.acme, password: "<redacted>" } } : reg.json;
  log(`POST /v1/register -> ${reg.status} ${JSON.stringify(shown)}`);
  check(reg.status === 201, "register is 201");
  id = reg.json.id;
  let acme = reg.json.acme;
  const name = reg.json.fulldomain;

  const addr = await signed(key, "PUT", `/v1/clinics/${id}/address`, { ipv4: ADDRESS, ipv6: null });
  log(`PUT /v1/clinics/${id}/address -> ${addr.status} ${JSON.stringify(addr.json)}`);
  check(addr.status === 200, "address is 200");
  if (DIG) await waitDig("A", name, RESOLVER, ADDRESS);

  const value = txt();
  const upd = await request("POST", "/update", {
    body: JSON.stringify({ subdomain: acme.subdomain, txt: value }),
    headers: { "x-api-user": acme.username, "x-api-key": acme.password, "content-type": "application/json" },
  });
  log(`POST /update (acme-dns) -> ${upd.status} ${JSON.stringify(upd.json)}`);
  check(upd.status === 200, "acme update is 200");
  if (DIG) await waitDig("TXT", acme.fulldomain, RESOLVER, value);

  const wrong = await request("POST", "/update", {
    body: JSON.stringify({ subdomain: acme.subdomain, txt: txt() }),
    headers: { "x-api-user": acme.username, "x-api-key": "wrong", "content-type": "application/json" },
  });
  log(`POST /update with a wrong key -> ${wrong.status} ${JSON.stringify(wrong.json)}`);
  check(wrong.status === 401, "wrong key is 401");

  const rot = await signed(key, "POST", `/v1/clinics/${id}/rotate`, {});
  log(`POST /v1/clinics/${id}/rotate -> ${rot.status} (new username ${rot.json?.acme?.username})`);
  check(rot.status === 200, "rotate is 200");
  const old = await request("POST", "/update", {
    body: JSON.stringify({ subdomain: acme.subdomain, txt: txt() }),
    headers: { "x-api-user": acme.username, "x-api-key": acme.password, "content-type": "application/json" },
  });
  log(`POST /update with the old credentials -> ${old.status}`);
  check(old.status === 401, "old credentials are 401");
  acme = rot.json.acme;
  const fresh = await request("POST", "/update", {
    body: JSON.stringify({ subdomain: acme.subdomain, txt: txt() }),
    headers: { "x-api-user": acme.username, "x-api-key": acme.password, "content-type": "application/json" },
  });
  log(`POST /update with the new credentials -> ${fresh.status}`);
  check(fresh.status === 200, "new credentials are 200");

  const status = await signed(key, "GET", `/v1/clinics/${id}`);
  log(`GET /v1/clinics/${id} -> ${status.status} ${JSON.stringify(status.json)}`);
  check(status.status === 200, "status is 200");
} catch (err) {
  console.error(String(err));
  process.exitCode = 1;
} finally {
  if (key && id && !KEEP) {
    const del = await signed(key, "DELETE", `/v1/clinics/${id}`);
    log(`DELETE /v1/clinics/${id} -> ${del.status} ${JSON.stringify(del.json)}`);
    if (del.status !== 200 || del.json?.dns_cleanup !== "done") process.exitCode = 1;
    const gone = await signed(key, "GET", `/v1/clinics/${id}`);
    log(`GET /v1/clinics/${id} after DELETE -> ${gone.status} ${gone.json?.error ?? ""}`);
    if (DIG && del.status === 200) {
      try {
        const base = BASE.replace(/^https?:\/\//, "");
        await waitDig("A", `${id}.${base}`, AUTHORITATIVE, null, 30);
        await waitDig("TXT", `_acme-challenge.${id}.${base}`, AUTHORITATIVE, null, 30);
      } catch (err) {
        console.error(String(err));
        process.exitCode = 1;
      }
    }
  }
  log(process.exitCode ? "# RESULT: FAIL" : "# RESULT: PASS");
}
