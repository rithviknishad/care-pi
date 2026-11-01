import { env } from "cloudflare:workers";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { housekeeping } from "../src/cron";
import { acmeUpdate, call, FakeCloudflare, registerClinic, signedCall, testEnv, txtValue } from "./helpers";

const BASE = "care-pi.rithviknishad.dev";
let cf: FakeCloudflare;

beforeEach(() => {
  cf = new FakeCloudflare(env.ZONE_ID).install();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("pages", () => {
  it("GET / is static HTML with a strict CSP and no external assets", async () => {
    const res = await call("GET", "/");
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("text/html; charset=utf-8");
    const csp = res.headers.get("content-security-policy") ?? "";
    expect(csp).toContain("default-src 'none'");
    expect(csp).toContain("script-src 'self'");
    expect(csp).not.toMatch(/unsafe-(inline|eval)/);
    const html = await res.text();
    expect(html).toContain(`&lt;id&gt;.${BASE}`);
    expect(html).not.toMatch(/(src|href)="(https?:)?\/\/(?!github\.com)/);
    expect(html).not.toMatch(/<script>(?!<\/script>)/); // no inline script
    expect(html).not.toMatch(/style="/);
  });

  it("GET /cert reads the fragment only and makes no network request", async () => {
    const res = await call("GET", "/cert");
    expect(res.status).toBe(200);
    const csp = res.headers.get("content-security-policy") ?? "";
    expect(csp).toContain("connect-src 'none'");
    expect(csp).toContain("frame-ancestors 'none'");
    const html = await res.text();
    expect(html).toContain('<script src="/static/cert.js" defer></script>');
    expect(html).not.toMatch(/(src|href)="(https?:)?\/\//);
    expect(html).not.toMatch(/<script>/);
    for (const id of ["status", "details", "fingerprint", "dl-crt", "dl-pem", "dl-mobileconfig", "empty", "subject", "not-after"]) {
      expect(html).toContain(`id="${id}"`);
    }
    const js = await (await call("GET", "/static/cert.js")).text();
    expect(js).toContain("location.hash");
    expect(js).toContain('crypto.subtle.digest("SHA-256"');
    expect(js).toContain("com.apple.security.root");
    for (const forbidden of ["fetch(", "XMLHttpRequest", "sendBeacon", "WebSocket", "EventSource", "import(", ".src =", "location.search"]) {
      expect(js, forbidden).not.toContain(forbidden);
    }
  });

  it("serves the assets with the right types, and HEAD without a body", async () => {
    expect((await call("GET", "/static/site.css")).headers.get("content-type")).toBe("text/css; charset=utf-8");
    expect((await call("GET", "/static/home.js")).headers.get("content-type")).toBe("text/javascript; charset=utf-8");
    expect(await (await call("GET", "/robots.txt")).text()).toContain("Disallow: /v1/");
    const head = await call("HEAD", "/cert");
    expect(head.status).toBe(200);
    expect(await head.text()).toBe("");
  });

  it("the home script talks to /lookup only", async () => {
    const js = await (await call("GET", "/static/home.js")).text();
    expect(js).toContain('fetch("/lookup"');
    expect(js).not.toContain("innerHTML");
  });
});

describe("scheduled housekeeping", () => {
  it("deletes old TXT records, expired rows, and the records of revoked clinics", async () => {
    const now = Math.floor(Date.now() / 1000);
    const a = await registerClinic();
    await acmeUpdate(a.acme, { subdomain: a.id, txt: txtValue(1) });
    await acmeUpdate(a.acme, { subdomain: a.id, txt: txtValue(2) });
    await env.DB.prepare("UPDATE txt_records SET created_at = ? WHERE id = ? AND value = ?").bind(now - 7200, a.id, txtValue(1)).run();
    await env.DB.prepare("INSERT INTO nonces (nonce, expires_at) VALUES ('old-nonce', ?)").bind(now - 10).run();
    await env.DB.prepare("INSERT INTO replay (digest, expires_at) VALUES ('old-digest', ?)").bind(now - 10).run();
    await env.DB.prepare("INSERT INTO rate (bucket, key, window_start, count) VALUES ('nonce', 'x', ?, 1)").bind(now - 3 * 86400).run();

    // A clinic that an operator revoked with SQL: its records stay until housekeeping runs.
    const b = await registerClinic();
    await signedCall(b.key, "PUT", `/v1/clinics/${b.id}/address`, { ipv4: "10.9.8.7" });
    await env.DB.prepare("UPDATE clinics SET revoked_at = ?, acme_user = NULL, acme_key_hash = NULL WHERE id = ?").bind(now, b.id).run();
    expect(cf.byName("A", `${b.id}.${BASE}`)).toHaveLength(1);

    const result = await housekeeping(testEnv(), now);
    expect(result.errors).toBe(0);
    expect(result.txtDeleted).toBeGreaterThanOrEqual(1);
    expect(result.revokedCleaned).toBeGreaterThanOrEqual(1);
    expect(cf.byName("TXT", `_acme-challenge.${a.id}.${BASE}`).map((r) => r.content)).toEqual([txtValue(2)]);
    expect(cf.byName("A", `${b.id}.${BASE}`)).toHaveLength(0);
    expect(await env.DB.prepare("SELECT COUNT(*) AS n FROM nonces WHERE nonce = 'old-nonce'").first("n")).toBe(0);
    expect(await env.DB.prepare("SELECT COUNT(*) AS n FROM replay WHERE digest = 'old-digest'").first("n")).toBe(0);
    expect(await env.DB.prepare("SELECT COUNT(*) AS n FROM rate WHERE key = 'x'").first("n")).toBe(0);
    expect(await env.DB.prepare("SELECT COUNT(*) AS n FROM addresses WHERE id = ?").bind(b.id).first("n")).toBe(0);
  });

  it("only cleans the database without a DNS token", async () => {
    const result = await housekeeping(testEnv({ CF_DNS_API_TOKEN: "" }), Math.floor(Date.now() / 1000));
    expect(result).toEqual({ txtDeleted: 0, revokedCleaned: 0, errors: 0 });
    expect(cf.calls).toHaveLength(0);
  });

  it("DELETE reports pending cleanup when the DNS API fails, and housekeeping finishes it", async () => {
    const r = await registerClinic();
    await signedCall(r.key, "PUT", `/v1/clinics/${r.id}/address`, { ipv4: "10.1.1.1" });
    cf.failNext(500, 1000, "down");
    const res = await signedCall(r.key, "DELETE", `/v1/clinics/${r.id}`, undefined);
    expect(res.status).toBe(200);
    expect(((await res.json()) as { dns_cleanup: string }).dns_cleanup).toBe("pending");
    await housekeeping(testEnv(), Math.floor(Date.now() / 1000));
    expect(cf.byName("A", `${r.id}.${BASE}`)).toHaveLength(0);
  });
});
