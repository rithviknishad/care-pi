// Runs the /cert page script (src/pages/cert-client.ts) in Node with a small DOM stub, and checks it
// against the fixture root certificates: testdata/root-ca.pem (made with OpenSSL), testdata/root-ca-agent.pem (made
// by the agent, agent/internal/ca), and testdata/bad-roots.pem (roots that the page must refuse; Go crypto/x509, the
// subject CN names the defect).

import { X509Certificate } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import vm from "node:vm";
import { describe, expect, it } from "vitest";
import { CERT_JS } from "../../src/pages/cert-client";
import { HOME_JS } from "../../src/pages/home";

const pemText = readFileSync(join(import.meta.dirname, "..", "..", "testdata", "root-ca.pem"), "utf8");
const x509 = new X509Certificate(pemText);
const der = new Uint8Array(x509.raw);
const derB64Url = Buffer.from(der).toString("base64url");

function readPEM(file: string): X509Certificate[] {
  const text = readFileSync(join(import.meta.dirname, "..", "..", "testdata", file), "utf8");
  return text
    .split("-----END CERTIFICATE-----")
    .filter((b) => b.includes("-----BEGIN CERTIFICATE-----"))
    .map((b) => new X509Certificate(`${b.trim()}\n-----END CERTIFICATE-----\n`));
}

const b64url = (c: X509Certificate) => Buffer.from(c.raw).toString("base64url");

interface FakeElement {
  id: string;
  textContent: string;
  hidden: boolean;
  disabled: boolean;
  className: string;
  listeners: Record<string, (() => void)[]>;
  addEventListener(type: string, fn: () => void): void;
  click(): void;
  href?: string;
  download?: string;
  rel?: string;
  remove(): void;
}

function element(id: string): FakeElement {
  const e: FakeElement = {
    id,
    textContent: "",
    hidden: id === "details" || id === "empty",
    disabled: id.startsWith("dl-"),
    className: "",
    listeners: {},
    addEventListener(type, fn) {
      (this.listeners[type] ??= []).push(fn);
    },
    click() {
      for (const fn of this.listeners.click ?? []) fn();
    },
    remove() {},
  };
  return e;
}

interface Run {
  elements: Map<string, FakeElement>;
  downloads: { name: string; type: string; text: string; bytes: Uint8Array }[];
  hook: { api?: Record<string, (...args: never[]) => unknown>; ready?: Promise<unknown> };
}

async function run(hash: string): Promise<Run> {
  const elements = new Map<string, FakeElement>();
  for (const id of ["status", "details", "empty", "subject", "issuer", "not-before", "not-after", "ca", "constraints", "fingerprint", "dl-crt", "dl-pem", "dl-mobileconfig"]) {
    elements.set(id, element(id));
  }
  const downloads: Run["downloads"] = [];
  const blobs = new Map<string, Blob>();
  const pending: Promise<void>[] = [];
  const hook: Run["hook"] = {};
  const context = {
    document: {
      getElementById: (id: string) => elements.get(id) ?? null,
      createElement: () => {
        const a = element("a");
        a.click = () => {
          const blob = blobs.get(a.href ?? "");
          if (!blob) throw new Error("no blob");
          pending.push(
            blob.arrayBuffer().then((buf) => {
              const bytes = new Uint8Array(buf);
              downloads.push({ name: a.download ?? "", type: blob.type, text: Buffer.from(bytes).toString("utf8"), bytes });
            }),
          );
        };
        return a;
      },
      body: { appendChild: () => undefined },
    },
    location: { hash, reload: () => undefined },
    window: { addEventListener: () => undefined },
    URL: {
      createObjectURL: (b: Blob) => {
        const url = `blob:test/${blobs.size}`;
        blobs.set(url, b);
        return url;
      },
      revokeObjectURL: () => undefined,
    },
    setTimeout: () => 0,
    Blob,
    TextDecoder,
    Uint8Array,
    crypto: globalThis.crypto,
    atob,
    btoa,
    __CARE_PI_CERT_TEST__: hook,
  };
  (context as Record<string, unknown>).globalThis = context;
  vm.runInNewContext(CERT_JS, context);
  await hook.ready;
  // Click every download button.
  for (const id of ["dl-crt", "dl-pem", "dl-mobileconfig"]) elements.get(id)!.click();
  await Promise.all(pending);
  return { elements, downloads, hook };
}

describe("/cert page script", () => {
  it("shows the SHA-256 fingerprint, the names, and the dates", async () => {
    const r = await run(`#${derB64Url}`);
    expect(r.elements.get("fingerprint")!.textContent).toBe(x509.fingerprint256);
    expect(r.elements.get("subject")!.textContent).toBe("O=care-pi, CN=care-pi local CA lumine");
    expect(r.elements.get("issuer")!.textContent).toBe("O=care-pi, CN=care-pi local CA lumine");
    expect(r.elements.get("ca")!.textContent).toMatch(/^Yes/);
    const validFrom = new Date(x509.validFrom).toISOString().slice(0, 19).replace("T", " ");
    expect(r.elements.get("not-before")!.textContent).toBe(`${validFrom} UTC`);
    expect(r.elements.get("details")!.hidden).toBe(false);
    expect(r.elements.get("dl-crt")!.disabled).toBe(false);
  });

  it("accepts standard base64, percent-encoded base64, and whitespace", async () => {
    const std = Buffer.from(der).toString("base64");
    for (const hash of [`#${std}`, `#${encodeURIComponent(std)}`, `#${std.slice(0, 40)}%20${std.slice(40)}`]) {
      const r = await run(hash);
      expect(r.elements.get("fingerprint")!.textContent).toBe(x509.fingerprint256);
    }
  });

  it("builds .crt, .pem, and .mobileconfig files on the device", async () => {
    const r = await run(`#${derB64Url}`);
    const byExt = Object.fromEntries(r.downloads.map((d) => [d.name.split(".").pop(), d]));
    const short = x509.fingerprint256.replace(/:/g, "").slice(0, 8).toLowerCase();
    expect(byExt.crt!.name).toBe(`care-pi-${short}.crt`);
    expect(byExt.crt!.type).toBe("application/x-x509-ca-cert");
    expect(Buffer.from(byExt.crt!.bytes).equals(Buffer.from(der))).toBe(true);
    expect(byExt.pem!.text).toBe(x509.toString());
    expect(new X509Certificate(byExt.pem!.text).fingerprint256).toBe(x509.fingerprint256);
    const plist = byExt.mobileconfig!.text;
    expect(byExt.mobileconfig!.type).toBe("application/x-apple-aspen-config");
    expect(plist).toContain("<string>com.apple.security.root</string>");
    expect(plist).toContain("<string>Configuration</string>");
    const data = /<data>\s*([\s\S]*?)\s*<\/data>/.exec(plist)![1]!.replace(/\s+/g, "");
    expect(Buffer.from(data, "base64").equals(Buffer.from(der))).toBe(true);
    const uuids = [...plist.matchAll(/<key>PayloadUUID<\/key>\s*<string>([^<]+)<\/string>/g)].map((m) => m[1]);
    expect(uuids).toHaveLength(2);
    for (const u of uuids) expect(u).toMatch(/^[0-9A-F]{8}-[0-9A-F]{4}-4[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$/);
    // The same certificate gives the same profile ids, so a new download replaces the old profile.
    const again = await run(`#${derB64Url}`);
    expect(again.downloads.find((d) => d.name.endsWith(".mobileconfig"))!.text).toBe(plist);
  });

  it("shows the empty state without a fragment", async () => {
    const r = await run("");
    expect(r.elements.get("empty")!.hidden).toBe(false);
    expect(r.elements.get("details")!.hidden).toBe(true);
    expect(r.downloads).toHaveLength(0);
  });

  it("refuses data that is not a certificate", async () => {
    for (const hash of ["#not*base64", `#${Buffer.from("hello world").toString("base64url")}`, `#${derB64Url.slice(0, 200)}`]) {
      const r = await run(hash);
      expect(r.elements.get("status")!.className).toBe("warn");
      expect(r.elements.get("status")!.textContent).toMatch(/not hold/);
      expect(r.elements.get("details")!.hidden).toBe(true);
    }
  });

  it("shows the name constraints of a care-pi root and offers the files", async () => {
    const r = await run(`#${derB64Url}`);
    expect(r.elements.get("constraints")!.textContent).toBe("DNS: .local, .care-pi.rithviknishad.dev; IP: 192.168.1.23/32; critical");
    expect(r.elements.get("status")!.className).not.toBe("warn");
    expect(r.downloads).toHaveLength(3);
    const [agent] = readPEM("root-ca-agent.pem");
    const a = await run(`#${b64url(agent!)}`);
    expect(a.elements.get("subject")!.textContent).toBe("O=care-pi, CN=care-pi care.local Root CA");
    expect(a.elements.get("constraints")!.textContent).toBe(
      "DNS: .local, .care-pi.rithviknishad.dev; IP: 100.100.1.0/24, 192.168.1.0/24; critical",
    );
    expect(a.elements.get("status")!.textContent).toMatch(/^The certificate is on this device only/);
    expect(a.elements.get("dl-crt")!.disabled).toBe(false);
    expect(a.downloads.map((d) => d.name.split(".").pop()).sort()).toEqual(["crt", "mobileconfig", "pem"]);
  });

  it("refuses a root that is not a name-constrained care-pi root", async () => {
    const want: Record<string, RegExp> = {
      unconstrained: /has no name constraints/,
      "not critical": /not marked critical/,
      "not a CA": /not a certificate authority/,
      "other domain": /DNS name "\.example\.com"/,
      "bare local": /DNS name "local"/,
      "public range": /IP range 8\.8\.8\.0\/24, which is not a private/,
      "wide range": /IP range 192\.0\.0\.0\/8, which is not a private/,
      "ipv6 range": /IPv6 range fd00:0:0:0:0:0:0:0\/8\./,
      "no ip constraint": /permits each IP address/,
      "no dns constraint": /permits each DNS name/,
    };
    const roots = readPEM("bad-roots.pem");
    expect(roots).toHaveLength(Object.keys(want).length);
    for (const root of roots) {
      const cn = /CN=([^\n,]+)/.exec(root.subject)![1]!;
      const r = await run(`#${b64url(root)}`);
      const status = r.elements.get("status")!;
      expect(status.className, cn).toBe("warn");
      expect(status.textContent, cn).toMatch(/^This is not the certificate of a care-pi device\. Do not install it\./);
      expect(status.textContent, cn).toMatch(want[cn]!);
      // The page still shows what the certificate is, with its constraints.
      expect(r.elements.get("details")!.hidden, cn).toBe(false);
      expect(r.elements.get("fingerprint")!.textContent, cn).toBe(root.fingerprint256);
      expect(r.elements.get("constraints")!.textContent, cn).not.toBe("");
      for (const id of ["dl-crt", "dl-pem", "dl-mobileconfig"]) expect(r.elements.get(id)!.disabled, cn).toBe(true);
      expect(r.downloads, cn).toHaveLength(0);
    }
  });

  it("allows exact names below the two domains and each private IPv4 block", async () => {
    const r = await run(`#${derB64Url}`);
    const problems = (r.hook.api as unknown as { problems: (info: unknown) => string[] }).problems;
    const v4 = (s: string) => {
      const [a, bits] = s.split("/");
      return { addr: a!.split(".").map(Number), bits: Number(bits) };
    };
    const info = (dns: string[], ips: string[]) => ({ ca: true, constraints: { critical: true, dns, ips, ipv4: ips.map(v4), other: [] } });
    const good = ["10.1.2.0/24", "172.16.5.0/24", "172.31.255.0/24", "192.168.0.0/16", "100.64.0.0/10", "100.127.1.0/24"];
    expect(problems(info(["care.local", "Clinic-1.care-pi.rithviknishad.dev", ".sub.local"], good))).toEqual([]);
    for (const ip of ["172.32.0.0/16", "172.0.0.0/8", "100.128.0.0/24", "11.0.0.0/8", "0.0.0.0/0", "192.169.0.0/24"]) {
      expect(problems(info([".local"], [ip])), ip).toHaveLength(1);
    }
    for (const d of ["", "local", "care-pi.rithviknishad.dev", "evil.dev", "x.local.evil.dev"]) {
      expect(problems(info([d], ["10.0.0.0/8"])), d).toHaveLength(1);
    }
  });

  it("escapes XML in the certificate name", async () => {
    const r = await run(`#${derB64Url}`);
    const api = r.hook.api as unknown as { mobileconfig: (d: Uint8Array, fp: Uint8Array, cn: string) => string };
    const plist = api.mobileconfig(der, new Uint8Array(32), 'a<b>&"c');
    expect(plist).toContain("a&lt;b&gt;&amp;&quot;c");
    expect(plist).not.toContain("a<b>");
  });
});

describe("home page script", () => {
  it("is valid JavaScript", () => {
    expect(() => new vm.Script(HOME_JS)).not.toThrow();
    expect(() => new vm.Script(CERT_JS)).not.toThrow();
  });
});
