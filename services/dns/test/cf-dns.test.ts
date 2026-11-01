import { afterEach, describe, expect, it, vi } from "vitest";
import { CfDns, CfDnsError } from "../src/cf-dns";
import { dnsClient } from "../src/dns-ops";
import { FakeCloudflare, testEnv } from "./helpers";

const ZONE = "f2b1e99594afa341b94411c77ae8624d";

function client(fake: FakeCloudflare): CfDns {
  return new CfDns({ token: "test-token", zoneId: ZONE, fetch: (input, init) => fake.fetch(input, init) });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("CfDns", () => {
  it("creates an A record that is not proxied, with the bearer token", async () => {
    const fake = new FakeCloudflare(ZONE);
    const rec = await client(fake).create({ type: "A", name: "a-k3x9.care-pi.rithviknishad.dev", content: "192.168.1.23", ttl: 60, comment: "c" });
    expect(rec.id).toMatch(/^rec1-/);
    expect(fake.calls).toHaveLength(1);
    const c = fake.calls[0]!;
    expect(c.method).toBe("POST");
    expect(c.url).toBe(`https://api.cloudflare.com/client/v4/zones/${ZONE}/dns_records`);
    expect(c.authorization).toBe("Bearer test-token");
    expect(c.body).toEqual({ type: "A", name: "a-k3x9.care-pi.rithviknishad.dev", content: "192.168.1.23", ttl: 60, proxied: false, comment: "c" });
  });

  it("sends TXT content without quotes and without proxied", async () => {
    const fake = new FakeCloudflare(ZONE);
    await client(fake).create({ type: "TXT", name: "_acme-challenge.a-k3x9.care-pi.rithviknishad.dev", content: "abc", ttl: 60 });
    expect(fake.calls[0]!.body).toEqual({ type: "TXT", name: "_acme-challenge.a-k3x9.care-pi.rithviknishad.dev", content: "abc", ttl: 60 });
  });

  it("lists by exact name and type", async () => {
    const fake = new FakeCloudflare(ZONE);
    const dns = client(fake);
    await dns.create({ type: "A", name: "x.example", content: "10.0.0.1", ttl: 60 });
    await dns.create({ type: "AAAA", name: "x.example", content: "fd00::1", ttl: 60 });
    const list = await dns.list("A", "x.example");
    expect(list.map((r) => r.content)).toEqual(["10.0.0.1"]);
    const url = new URL(fake.calls.at(-1)!.url);
    expect(url.searchParams.get("type")).toBe("A");
    expect(url.searchParams.get("name.exact")).toBe("x.example");
  });

  it("upserts: PATCH the known id, else search by name, else create", async () => {
    const fake = new FakeCloudflare(ZONE);
    const dns = client(fake);
    const first = await dns.upsert({ type: "A", name: "u.example", content: "10.0.0.1", ttl: 60 }, null);
    expect(fake.calls.map((c) => c.method)).toEqual(["GET", "POST"]);
    fake.calls = [];
    await dns.upsert({ type: "A", name: "u.example", content: "10.0.0.2", ttl: 60 }, first.id);
    expect(fake.calls.map((c) => c.method)).toEqual(["PATCH"]);
    expect(fake.records.get(first.id)!.content).toBe("10.0.0.2");
    // The known id is gone (deleted by hand): search by name, then create.
    fake.records.clear();
    fake.calls = [];
    const again = await dns.upsert({ type: "A", name: "u.example", content: "10.0.0.3", ttl: 60 }, first.id);
    expect(fake.calls.map((c) => c.method)).toEqual(["PATCH", "GET", "POST"]);
    expect(again.id).not.toBe(first.id);
  });

  it("upsert removes duplicate records of the same name", async () => {
    const fake = new FakeCloudflare(ZONE);
    const dns = client(fake);
    await dns.create({ type: "A", name: "d.example", content: "10.0.0.1", ttl: 60 });
    await dns.create({ type: "A", name: "d.example", content: "10.0.0.2", ttl: 60 });
    await dns.upsert({ type: "A", name: "d.example", content: "10.0.0.9", ttl: 60 });
    expect(fake.byName("A", "d.example").map((r) => r.content)).toEqual(["10.0.0.9"]);
  });

  it("delete returns false for a missing record and throws for other errors", async () => {
    const fake = new FakeCloudflare(ZONE);
    const dns = client(fake);
    expect(await dns.delete("missing")).toBe(false);
    fake.failNext(500, 1000, "boom");
    await expect(dns.delete("x")).rejects.toBeInstanceOf(CfDnsError);
  });

  it("maps an error envelope to CfDnsError with the codes", async () => {
    const fake = new FakeCloudflare(ZONE);
    fake.failNext(400, 81058, "An identical record already exists.");
    const err = await client(fake)
      .create({ type: "A", name: "e.example", content: "10.0.0.1", ttl: 60 })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(CfDnsError);
    expect((err as CfDnsError).status).toBe(400);
    expect((err as CfDnsError).codes).toEqual([81058]);
    expect((err as CfDnsError).message).toContain("81058");
  });

  it("rejects a response that is not JSON", async () => {
    const dns = new CfDns({ token: "t", zoneId: ZONE, fetch: async () => new Response("<html>bad gateway</html>", { status: 502 }) });
    await expect(dns.list("A", "x")).rejects.toMatchObject({ status: 502 });
  });

  it("deleteByName deletes every match", async () => {
    const fake = new FakeCloudflare(ZONE);
    const dns = client(fake);
    await dns.create({ type: "TXT", name: "t.example", content: "a", ttl: 60 });
    await dns.create({ type: "TXT", name: "t.example", content: "b", ttl: 60 });
    expect(await dns.deleteByName("TXT", "t.example")).toBe(2);
    expect(fake.records.size).toBe(0);
  });

  it("dnsClient uses the zone of the configuration and CF_API_BASE for local development", async () => {
    const urls: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input));
      return new Response(JSON.stringify({ success: true, errors: [], result: [] }));
    });
    await dnsClient(testEnv()).list("A", "x");
    await dnsClient(testEnv({ CF_API_BASE: "http://127.0.0.1:8788/client/v4" })).list("A", "x");
    expect(urls[0]).toMatch(new RegExp(`^https://api\\.cloudflare\\.com/client/v4/zones/${ZONE}/dns_records\\?`));
    expect(urls[1]).toMatch(/^http:\/\/127\.0\.0\.1:8788\/client\/v4\/zones\//);
    expect(() => dnsClient(testEnv({ CF_DNS_API_TOKEN: "" }))).toThrowError(/CF_DNS_API_TOKEN/);
  });
});
