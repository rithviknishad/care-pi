import { env } from "cloudflare:workers";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  acmeUpdate,
  bodyOf,
  call,
  FakeCloudflare,
  freshIp,
  getNonce,
  newKey,
  registerClinic,
  signedCall,
  signedHeaders,
  testEnv,
  txtValue,
} from "./helpers";

const BASE = "care-pi.rithviknishad.dev";
let cf: FakeCloudflare;

beforeEach(() => {
  cf = new FakeCloudflare(env.ZONE_ID).install();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("GET /v1/nonce", () => {
  it("gives a single-use nonce and the server time", async () => {
    const res = await call("GET", "/v1/nonce", { ip: freshIp() });
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");
    const body = await bodyOf<{ nonce: string; expires_in: number; server_time: number }>(res);
    expect(body.nonce).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(body.expires_in).toBe(300);
    expect(Math.abs(body.server_time - Date.now() / 1000)).toBeLessThan(5);
  });

  it("is rate limited per network", async () => {
    const e = testEnv({ RATE_LIMITS: '{"nonce":{"limit":2,"window":3600}}' });
    const ip = freshIp();
    expect((await call("GET", "/v1/nonce", { ip, env: e })).status).toBe(200);
    expect((await call("GET", "/v1/nonce", { ip, env: e })).status).toBe(200);
    const res = await call("GET", "/v1/nonce", { ip, env: e });
    expect(res.status).toBe(429);
    expect(Number(res.headers.get("retry-after"))).toBeGreaterThan(0);
    expect((await bodyOf(res)).error).toBe("rate_limited");
  });
});

describe("POST /v1/register", () => {
  it("registers a clinic and returns acme-dns credentials", async () => {
    const r = await registerClinic({ hint: "Lumine Clinic" });
    expect(r.id).toMatch(/^lumine-clinic-[a-z2-7]{4}$/);
    expect(r.response.fulldomain).toBe(`${r.id}.${BASE}`);
    expect(r.response.listing_opt_in).toBe(false);
    expect(r.acme.username).toMatch(/^[0-9a-f-]{36}$/);
    expect(r.acme.password).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(r.acme.subdomain).toBe(r.id);
    expect(r.acme.fulldomain).toBe(`_acme-challenge.${r.id}.${BASE}`);
    expect(r.acme.server_url).toBe(`https://${BASE}`);
    expect(r.response).not.toHaveProperty("ddns_token");
    const row = await env.DB.prepare("SELECT * FROM clinics WHERE id = ?").bind(r.id).first<Record<string, unknown>>();
    expect(row?.pubkey).toBe(r.key.publicB64);
    expect(row?.acme_key_hash).toMatch(/^[0-9a-f]{64}$/);
    expect(JSON.stringify(row)).not.toContain(r.acme.password);
  });

  it("rejects a signature by another key", async () => {
    const key = await newKey();
    const other = await newKey();
    const ip = freshIp();
    const body = JSON.stringify({ hostname_hint: "x", pubkey: key.publicB64, nonce: await getNonce(ip) });
    const res = await call("POST", "/v1/register", { ip, body, headers: await signedHeaders(other, "POST", "/v1/register", body) });
    expect(res.status).toBe(401);
    expect((await bodyOf(res)).error).toBe("bad_signature");
  });

  it("rejects a used or unknown nonce", async () => {
    const key = await newKey();
    const ip = freshIp();
    const nonce = await getNonce(ip);
    const body = { hostname_hint: "x", pubkey: key.publicB64, nonce };
    expect((await signedCall(key, "POST", "/v1/register", body, { ip })).status).toBe(201);
    // The same nonce again, with a new timestamp so that the signature differs.
    const again = await signedCall(key, "POST", "/v1/register", body, { ip, timestamp: Math.floor(Date.now() / 1000) - 1 });
    expect(again.status).toBe(401);
    expect((await bodyOf(again)).error).toBe("bad_nonce");
    const unknown = await signedCall(key, "POST", "/v1/register", { ...body, nonce: "A".repeat(43) }, { ip });
    expect((await bodyOf(unknown)).error).toBe("bad_nonce");
  });

  it("rejects an expired nonce", async () => {
    const key = await newKey();
    const ip = freshIp();
    const nonce = "E".repeat(43);
    await env.DB.prepare("INSERT INTO nonces (nonce, expires_at) VALUES (?, ?)").bind(nonce, Math.floor(Date.now() / 1000) - 1).run();
    const res = await signedCall(key, "POST", "/v1/register", { hostname_hint: "x", pubkey: key.publicB64, nonce }, { ip });
    expect((await bodyOf(res)).error).toBe("bad_nonce");
  });

  it("rejects a stale timestamp and tells the server time", async () => {
    const key = await newKey();
    const ip = freshIp();
    const body = { hostname_hint: "x", pubkey: key.publicB64, nonce: await getNonce(ip) };
    const res = await signedCall(key, "POST", "/v1/register", body, { ip, timestamp: Math.floor(Date.now() / 1000) - 400 });
    expect(res.status).toBe(401);
    const out = await bodyOf(res);
    expect(out.error).toBe("stale_timestamp");
    expect(typeof out.server_time).toBe("number");
  });

  it("validates the body", async () => {
    const key = await newKey();
    const ip = freshIp();
    const nonce = await getNonce(ip);
    const cases: [unknown, string][] = [
      [{ pubkey: key.publicB64, nonce }, "hostname_hint"],
      [{ hostname_hint: "x".repeat(64), pubkey: key.publicB64, nonce }, "hostname_hint"],
      [{ hostname_hint: "x", pubkey: "AAAA", nonce }, "pubkey"],
      [{ hostname_hint: "x", pubkey: key.publicB64, nonce: "short" }, "nonce"],
      [{ hostname_hint: "x", pubkey: key.publicB64, nonce, listing_opt_in: "yes" }, "listing_opt_in"],
    ];
    for (const [body, field] of cases) {
      const res = await signedCall(key, "POST", "/v1/register", body, { ip });
      expect(res.status, field).toBe(400);
      expect((await bodyOf<{ message: string }>(res)).message).toContain(field);
    }
    const notJson = await call("POST", "/v1/register", { ip, body: "{not json" });
    expect(notJson.status).toBe(400);
    const big = await call("POST", "/v1/register", { ip, body: JSON.stringify({ x: "y".repeat(5000) }) });
    expect(big.status).toBe(413);
  });

  it("limits registrations per public IP per day", async () => {
    const ip = freshIp();
    for (let i = 0; i < 5; i++) await registerClinic({ ip });
    const key = await newKey();
    const res = await signedCall(key, "POST", "/v1/register", { hostname_hint: "x", pubkey: key.publicB64, nonce: await getNonce(ip) }, { ip });
    expect(res.status).toBe(429);
    const body = await bodyOf<{ bucket: string; retry_after: number }>(res);
    expect(body.bucket).toBe("register");
    expect(body.retry_after).toBeGreaterThan(0);
    expect(body.retry_after).toBeLessThanOrEqual(86400);
    // Another network is not affected.
    await registerClinic({ ip: freshIp() });
  });

  it("stops a caller after many failed signatures", async () => {
    const e = testEnv({ RATE_LIMITS: '{"authfail":{"limit":3,"window":3600}}' });
    const ip = freshIp();
    const key = await newKey();
    const other = await newKey();
    for (let i = 0; i < 3; i++) {
      const body = JSON.stringify({ hostname_hint: "x", pubkey: key.publicB64, nonce: await getNonce(ip, e) });
      const res = await call("POST", "/v1/register", { ip, env: e, body, headers: await signedHeaders(other, "POST", "/v1/register", body) });
      expect(res.status).toBe(401);
    }
    const ok = { hostname_hint: "x", pubkey: key.publicB64, nonce: await getNonce(ip, e) };
    const res = await signedCall(key, "POST", "/v1/register", ok, { ip, env: e });
    expect(res.status).toBe(429);
    expect((await bodyOf(res)).bucket).toBe("authfail");
  });
});

describe("signed clinic requests", () => {
  it("GET /v1/clinics/{id} gives the status", async () => {
    const r = await registerClinic();
    const res = await signedCall(r.key, "GET", `/v1/clinics/${r.id}`, undefined, { ip: r.ip });
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ id: r.id, fulldomain: `${r.id}.${BASE}`, listing_opt_in: false, address: null });
    expect((body.acme as Record<string, unknown>).username).toBe(r.acme.username);
    expect(body.acme).not.toHaveProperty("password");
  });

  it("rejects a signature by another key, a changed body, and a changed path", async () => {
    const r = await registerClinic();
    const other = await newKey();
    const path = `/v1/clinics/${r.id}/address`;
    const wrongKey = await signedCall(other, "PUT", path, { ipv4: "192.168.1.5" });
    expect(wrongKey.status).toBe(401);
    expect((await bodyOf(wrongKey)).error).toBe("bad_signature");
    const body = JSON.stringify({ ipv4: "192.168.1.5" });
    const tampered = await call("PUT", path, {
      body,
      headers: await signedHeaders(r.key, "PUT", path, body, { signedBody: JSON.stringify({ ipv4: "192.168.1.6" }) }),
    });
    expect(tampered.status).toBe(401);
    const otherPath = await call("PUT", path, {
      body,
      headers: await signedHeaders(r.key, "PUT", path, body, { signedPath: `/v1/clinics/${r.id}` }),
    });
    expect(otherPath.status).toBe(401);
    const noHeaders = await call("PUT", path, { body });
    expect((await bodyOf(noHeaders)).error).toBe("missing_signature");
    expect(cf.calls).toHaveLength(0);
  });

  it("rejects a replayed request", async () => {
    const r = await registerClinic();
    const path = `/v1/clinics/${r.id}`;
    const headers = await signedHeaders(r.key, "GET", path, "");
    expect((await call("GET", path, { headers })).status).toBe(200);
    const replay = await call("GET", path, { headers });
    expect(replay.status).toBe(401);
    expect((await bodyOf(replay)).error).toBe("replayed");
  });

  it("rejects a stale timestamp", async () => {
    const r = await registerClinic();
    const res = await signedCall(r.key, "GET", `/v1/clinics/${r.id}`, undefined, { timestamp: Math.floor(Date.now() / 1000) + 600 });
    expect((await bodyOf(res)).error).toBe("stale_timestamp");
  });

  it("gives 404 for an unknown or malformed id", async () => {
    const key = await newKey();
    expect((await signedCall(key, "GET", "/v1/clinics/nobody-aaaa", undefined)).status).toBe(404);
    expect((await signedCall(key, "GET", "/v1/clinics/NOT_AN_ID", undefined)).status).toBe(404);
    expect((await signedCall(key, "GET", "/v1/clinics/%E0%A4%A", undefined)).status).toBe(404);
  });
});

describe("PUT /v1/clinics/{id}/address (DDNS)", () => {
  it("creates, keeps, changes, and removes the A and AAAA records", async () => {
    const r = await registerClinic();
    const path = `/v1/clinics/${r.id}/address`;
    const name = `${r.id}.${BASE}`;

    let res = await signedCall(r.key, "PUT", path, { ipv4: "192.0.2.10" });
    expect(res.status).toBe(200);
    expect(await bodyOf(res)).toMatchObject({ id: r.id, fulldomain: name, ipv4: "192.0.2.10", ipv6: null, ttl: 60, changed: true });
    expect(cf.byName("A", name)).toEqual([
      expect.objectContaining({ type: "A", name, content: "192.0.2.10", ttl: 60, proxied: false, comment: `care-pi-dns ${r.id}` }),
    ]);

    // The same address again: no DNS API call.
    cf.calls = [];
    res = await signedCall(r.key, "PUT", path, { ipv4: "192.0.2.10", ipv6: null });
    expect((await bodyOf(res)).changed).toBe(false);
    expect(cf.calls).toHaveLength(0);

    // A new IPv4 address and an IPv6 address.
    res = await signedCall(r.key, "PUT", path, { ipv4: "192.168.1.23", ipv6: "FD00:0:0:0::23" });
    expect(await bodyOf(res)).toMatchObject({ ipv4: "192.168.1.23", ipv6: "fd00::23", changed: true });
    expect(cf.byName("A", name).map((x) => x.content)).toEqual(["192.168.1.23"]);
    expect(cf.byName("AAAA", name).map((x) => x.content)).toEqual(["fd00::23"]);

    // IPv6 only: the A record goes.
    res = await signedCall(r.key, "PUT", path, { ipv6: "fd00::23" });
    expect(await bodyOf(res)).toMatchObject({ ipv4: null, ipv6: "fd00::23" });
    expect(cf.byName("A", name)).toHaveLength(0);
    expect(cf.byName("AAAA", name)).toHaveLength(1);
  });

  it("repairs a record that was deleted by hand when the address is stale", async () => {
    const r = await registerClinic();
    const path = `/v1/clinics/${r.id}/address`;
    await signedCall(r.key, "PUT", path, { ipv4: "10.1.2.3" });
    cf.records.clear();
    await env.DB.prepare("UPDATE addresses SET updated_at = updated_at - 7200 WHERE id = ?").bind(r.id).run();
    const res = await signedCall(r.key, "PUT", path, { ipv4: "10.1.2.3" });
    expect((await bodyOf(res)).changed).toBe(false);
    expect(cf.byName("A", `${r.id}.${BASE}`).map((x) => x.content)).toEqual(["10.1.2.3"]);
  });

  it("rejects addresses outside the policy", async () => {
    const r = await registerClinic();
    const path = `/v1/clinics/${r.id}/address`;
    const bad: unknown[] = [{}, { ipv4: null, ipv6: null }, { ipv4: "8.8.8.8" }, { ipv4: "127.0.0.1" }, { ipv4: "192.168.001.1" }, { ipv4: 5 }, { ipv6: "fe80::1" }];
    for (const body of bad) {
      const res = await signedCall(r.key, "PUT", path, body, { env: testEnv({ RATE_LIMITS: '{"address":{"limit":100,"window":60}}' }) });
      expect(res.status, JSON.stringify(body)).toBe(400);
      expect((await bodyOf(res)).error).toBe("bad_address");
    }
    expect(cf.calls).toHaveLength(0);
    const anyPolicy = await signedCall(r.key, "PUT", path, { ipv4: "8.8.8.8" }, {
      env: testEnv({ ADDRESS_POLICY: "any", RATE_LIMITS: '{"address":{"limit":100,"window":60}}' }),
    });
    expect(anyPolicy.status).toBe(200);
  });

  it("limits address updates per clinic per minute", async () => {
    const r = await registerClinic();
    const e = testEnv({ RATE_LIMITS: '{"address":{"limit":6,"window":3600}}' });
    const path = `/v1/clinics/${r.id}/address`;
    for (let i = 0; i < 6; i++) {
      expect((await signedCall(r.key, "PUT", path, { ipv4: `10.0.0.${i + 1}` }, { env: e })).status).toBe(200);
    }
    const res = await signedCall(r.key, "PUT", path, { ipv4: "10.0.0.99" }, { env: e });
    expect(res.status).toBe(429);
    expect((await bodyOf(res)).bucket).toBe("address");
  });

  it("gives 503 without a DNS token and 502 when the DNS API fails", async () => {
    const r = await registerClinic();
    const path = `/v1/clinics/${r.id}/address`;
    const noToken = await signedCall(r.key, "PUT", path, { ipv4: "10.0.0.1" }, { env: testEnv({ CF_DNS_API_TOKEN: "" }) });
    expect(noToken.status).toBe(503);
    expect((await bodyOf(noToken)).error).toBe("dns_unconfigured");
    cf.failNext(500, 1000, "internal");
    const failed = await signedCall(r.key, "PUT", path, { ipv4: "10.0.0.1" });
    expect(failed.status).toBe(502);
    expect((await bodyOf(failed)).error).toBe("dns_error");
  });
});

describe("POST /update (acme-dns)", () => {
  it("writes the TXT record at _acme-challenge.<id> and keeps the two newest values", async () => {
    const r = await registerClinic();
    const name = `_acme-challenge.${r.id}.${BASE}`;
    const res = await acmeUpdate(r.acme, { subdomain: r.acme.subdomain, txt: txtValue(1) });
    expect(res.status).toBe(200);
    expect(await bodyOf(res)).toEqual({ txt: txtValue(1) });
    expect(cf.byName("TXT", name)).toEqual([expect.objectContaining({ content: txtValue(1), ttl: 60 })]);

    await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(2) });
    await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(3) });
    expect(cf.byName("TXT", name).map((x) => x.content).sort()).toEqual([txtValue(2), txtValue(3)].sort());

    // The same value again does nothing.
    cf.calls = [];
    expect((await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(3) })).status).toBe(200);
    expect(cf.calls).toHaveLength(0);
  });

  it("adopts an identical TXT record that is in the zone but not in D1", async () => {
    const r = await registerClinic();
    const name = `_acme-challenge.${r.id}.${BASE}`;
    cf.records.set("manual-1", { id: "manual-1", type: "TXT", name, content: txtValue(7), ttl: 60 });
    const res = await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(7) });
    expect(res.status).toBe(200);
    expect(cf.byName("TXT", name).map((x) => x.id)).toEqual(["manual-1"]);
    const row = await env.DB.prepare("SELECT record_id FROM txt_records WHERE id = ?").bind(r.id).first<{ record_id: string }>();
    expect(row?.record_id).toBe("manual-1");
  });

  it("rejects a wrong key, an unknown user, another subdomain, and missing headers", async () => {
    const a = await registerClinic();
    const b = await registerClinic();
    const txt = txtValue(9);
    const cases = [
      await acmeUpdate({ username: a.acme.username, password: b.acme.password }, { subdomain: a.id, txt }),
      await acmeUpdate({ username: crypto.randomUUID(), password: a.acme.password }, { subdomain: a.id, txt }),
      await acmeUpdate(a.acme, { subdomain: b.id, txt }),
      await call("POST", "/update", { ip: freshIp(), body: JSON.stringify({ subdomain: a.id, txt }) }),
    ];
    for (const res of cases) {
      expect(res.status).toBe(401);
      expect((await bodyOf(res)).error).toBe("forbidden");
    }
    expect(cf.calls).toHaveLength(0);
  });

  it("rejects a bad txt and a body that is not JSON", async () => {
    const r = await registerClinic();
    const res = await acmeUpdate(r.acme, { subdomain: r.id, txt: "too short" });
    expect(res.status).toBe(400);
    expect((await bodyOf(res)).error).toBe("bad_txt");
    const bad = await acmeUpdate(r.acme, "{");
    expect(bad.status).toBe(400);
    expect((await bodyOf(bad)).error).toBe("malformed_json_payload");
  });

  it("limits TXT updates per clinic per minute", async () => {
    const r = await registerClinic();
    const e = testEnv({ RATE_LIMITS: '{"acme":{"limit":6,"window":3600}}' });
    for (let i = 0; i < 6; i++) {
      expect((await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(20 + i) }, freshIp(), e)).status).toBe(200);
    }
    const res = await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(40) }, freshIp(), e);
    expect(res.status).toBe(429);
  });

  it("keeps open registration closed and answers /health", async () => {
    const res = await call("POST", "/register", { body: "" });
    expect(res.status).toBe(403);
    expect((await bodyOf(res)).error).toBe("registration_closed");
    expect(await bodyOf(await call("GET", "/health"))).toEqual({ status: "ok" });
  });
});

describe("POST /v1/clinics/{id}/rotate", () => {
  it("replaces the acme-dns credentials", async () => {
    const r = await registerClinic();
    const res = await signedCall(r.key, "POST", `/v1/clinics/${r.id}/rotate`, {});
    expect(res.status).toBe(200);
    const body = await bodyOf<{ acme: { username: string; password: string }; pubkey: string }>(res);
    expect(body.acme.username).not.toBe(r.acme.username);
    expect(body.pubkey).toBe(r.key.publicB64);
    expect((await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(1) })).status).toBe(401);
    expect((await acmeUpdate(body.acme, { subdomain: r.id, txt: txtValue(1) })).status).toBe(200);
  });

  it("replaces the signing key when the new key also signs", async () => {
    const r = await registerClinic();
    const next = await newKey();
    const path = `/v1/clinics/${r.id}/rotate`;
    const noProof = await signedCall(r.key, "POST", path, { pubkey: next.publicB64 });
    expect(noProof.status).toBe(401);
    expect((await bodyOf(noProof)).error).toBe("bad_new_signature");
    const wrongProof = await signedCall(r.key, "POST", path, { pubkey: next.publicB64 }, { newKey: await newKey() });
    expect((await bodyOf(wrongProof)).error).toBe("bad_new_signature");
    const ok = await signedCall(r.key, "POST", path, { pubkey: next.publicB64 }, { newKey: next });
    expect(ok.status).toBe(200);
    expect((await bodyOf(ok)).pubkey).toBe(next.publicB64);
    expect((await signedCall(r.key, "GET", `/v1/clinics/${r.id}`, undefined)).status).toBe(401);
    expect((await signedCall(next, "GET", `/v1/clinics/${r.id}`, undefined)).status).toBe(200);
  });
});

describe("DELETE /v1/clinics/{id}", () => {
  it("revokes the clinic and deletes its DNS records", async () => {
    const r = await registerClinic();
    await signedCall(r.key, "PUT", `/v1/clinics/${r.id}/address`, { ipv4: "192.168.1.23", ipv6: "fd00::23" });
    await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(5) });
    expect(cf.records.size).toBe(3);
    const res = await signedCall(r.key, "DELETE", `/v1/clinics/${r.id}`, undefined);
    expect(res.status).toBe(200);
    expect(await bodyOf(res)).toMatchObject({ id: r.id, dns_cleanup: "done" });
    expect(cf.records.size).toBe(0);
    const after = await signedCall(r.key, "GET", `/v1/clinics/${r.id}`, undefined);
    expect(after.status).toBe(410);
    expect((await bodyOf(after)).error).toBe("revoked");
    expect((await acmeUpdate(r.acme, { subdomain: r.id, txt: txtValue(6) })).status).toBe(401);
    const row = await env.DB.prepare("SELECT acme_user, revoked_at FROM clinics WHERE id = ?").bind(r.id).first<Record<string, unknown>>();
    expect(row?.acme_user).toBeNull();
    expect(row?.revoked_at).not.toBeNull();
  });
});

describe("GET /lookup", () => {
  it("lists only opted-in clinics of the caller's network", async () => {
    const ip = freshIp();
    const hidden = await registerClinic({ ip });
    const shown = await registerClinic({ ip, listing: true });
    const later = await registerClinic({ ip });
    let res = await call("GET", "/lookup", { ip });
    let body = await bodyOf<{ ip: string; clinics: { id: string; fulldomain: string }[] }>(res);
    expect(body.ip).toBe(ip);
    expect(body.clinics.map((c) => c.id)).toEqual([shown.id]);
    expect(body.clinics[0]!.fulldomain).toBe(`${shown.id}.${BASE}`);

    // Opt in after registration with PATCH.
    const patch = await signedCall(later.key, "PATCH", `/v1/clinics/${later.id}`, { listing_opt_in: true }, { ip });
    expect(patch.status).toBe(200);
    expect((await bodyOf(patch)).listing_opt_in).toBe(true);
    res = await call("GET", "/lookup", { ip });
    body = await bodyOf(res);
    expect(body.clinics.map((c) => c.id).sort()).toEqual([shown.id, later.id].sort());
    expect(body.clinics.map((c) => c.id)).not.toContain(hidden.id);

    // Another network sees nothing; asking for another IP is refused; asking for your own IP works.
    expect((await bodyOf<{ clinics: unknown[] }>(await call("GET", "/lookup", { ip: freshIp() }))).clinics).toEqual([]);
    const other = await call("GET", `/lookup?ip=${encodeURIComponent(ip)}`, { ip: freshIp() });
    expect(other.status).toBe(403);
    expect((await bodyOf(other)).error).toBe("ip_mismatch");
    expect((await call("GET", `/lookup?ip=${ip}`, { ip })).status).toBe(200);

    // A revoked clinic leaves the list.
    await signedCall(shown.key, "DELETE", `/v1/clinics/${shown.id}`, undefined, { ip });
    body = await bodyOf(await call("GET", "/lookup", { ip }));
    expect(body.clinics.map((c) => c.id)).toEqual([later.id]);
  });

  it("lists a clinic by the network of its last update too", async () => {
    const r = await registerClinic({ listing: true });
    const newIp = freshIp();
    await signedCall(r.key, "GET", `/v1/clinics/${r.id}`, undefined, { ip: newIp });
    const body = await bodyOf<{ clinics: { id: string }[] }>(await call("GET", "/lookup", { ip: newIp }));
    expect(body.clinics.map((c) => c.id)).toEqual([r.id]);
  });

  it("groups IPv6 callers by /64", async () => {
    const r = await registerClinic({ ip: "2001:db8:aa:bb::1", listing: true });
    const body = await bodyOf<{ network: string; clinics: { id: string }[] }>(await call("GET", "/lookup", { ip: "2001:db8:aa:bb:ffff::9" }));
    expect(body.network).toBe("2001:db8:aa:bb::/64");
    expect(body.clinics.map((c) => c.id)).toContain(r.id);
  });

  it("rejects PATCH with a bad body", async () => {
    const r = await registerClinic();
    const res = await signedCall(r.key, "PATCH", `/v1/clinics/${r.id}`, { listing_opt_in: "yes" });
    expect(res.status).toBe(400);
  });
});

describe("routing and headers", () => {
  it("gives 404 and 405 with Allow", async () => {
    const nf = await call("GET", "/nope");
    expect(nf.status).toBe(404);
    expect((await bodyOf(nf)).error).toBe("not_found");
    const na = await call("DELETE", "/v1/nonce");
    expect(na.status).toBe(405);
    expect(na.headers.get("allow")).toBe("GET, HEAD");
    const na2 = await call("GET", "/update");
    expect(na2.headers.get("allow")).toBe("POST");
  });

  it("sets the common security headers on every response", async () => {
    for (const res of [await call("GET", "/"), await call("GET", "/v1/nonce", { ip: freshIp() }), await call("GET", "/nope")]) {
      expect(res.headers.get("x-content-type-options")).toBe("nosniff");
      expect(res.headers.get("referrer-policy")).toBe("no-referrer");
      expect(res.headers.get("strict-transport-security")).toBe("max-age=31536000");
    }
  });
});
