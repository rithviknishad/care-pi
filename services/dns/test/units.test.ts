import { describe, expect, it } from "vitest";
import { fromBase64Strict, toBase64, toBase64Url } from "../src/encoding";
import { BASE32, isClinicId, newClinicId, slugify } from "../src/ids";
import { checkIPv4, checkIPv6, classifyIPv4, classifyIPv6, formatIPv6, networkKey, parseIPv4, parseIPv6 } from "../src/ip";
import { limitsFor, DEFAULT_LIMITS } from "../src/ratelimit";
import { testEnv } from "./helpers";

describe("base64", () => {
  it("accepts canonical standard base64 only", () => {
    const bytes = Uint8Array.from([0, 1, 2, 250, 251, 252]);
    expect(fromBase64Strict(toBase64(bytes))).toEqual(bytes);
    expect(fromBase64Strict(toBase64Url(bytes))).toBeNull();
    expect(fromBase64Strict("AAE")).toBeNull(); // no padding
    expect(fromBase64Strict("AAF=")).toBeNull(); // non-zero padding bits
    expect(fromBase64Strict(" AAE=")).toBeNull();
    expect(fromBase64Strict("")).toEqual(new Uint8Array(0));
  });
});

describe("ip", () => {
  it("parses IPv4 strictly", () => {
    expect(parseIPv4("192.168.1.23")).toEqual(Uint8Array.from([192, 168, 1, 23]));
    expect(parseIPv4("192.168.01.23")).toBeNull();
    expect(parseIPv4("256.1.1.1")).toBeNull();
    expect(parseIPv4("1.2.3")).toBeNull();
  });

  it("parses and formats IPv6 (RFC 5952)", () => {
    const cases: [string, string][] = [
      ["2001:DB8:0:0:0:0:0:1", "2001:db8::1"],
      ["fd00::1:0:0:1", "fd00::1:0:0:1"],
      ["2001:db8:0:0:1:0:0:1", "2001:db8::1:0:0:1"],
      ["::", "::"],
      ["::1", "::1"],
      ["fe80::1", "fe80::1"],
      ["::ffff:192.0.2.1", "::ffff:c000:201"],
      ["2001:db8:1:2:3:4:5:6", "2001:db8:1:2:3:4:5:6"],
    ];
    for (const [input, out] of cases) {
      const b = parseIPv6(input);
      expect(b, input).not.toBeNull();
      expect(formatIPv6(b as Uint8Array)).toBe(out);
    }
    for (const bad of ["1::2::3", "12345::", "1:2:3:4:5:6:7:8:9", "fe80::1%eth0", "", "1:2:3:4:5:6:7::8"]) {
      expect(parseIPv6(bad), bad).toBeNull();
    }
  });

  it("classifies addresses", () => {
    expect(classifyIPv4(parseIPv4("10.1.2.3")!)).toBe("private");
    expect(classifyIPv4(parseIPv4("172.31.0.1")!)).toBe("private");
    expect(classifyIPv4(parseIPv4("172.32.0.1")!)).toBe("public");
    expect(classifyIPv4(parseIPv4("100.100.1.1")!)).toBe("cgnat");
    expect(classifyIPv4(parseIPv4("192.0.2.10")!)).toBe("documentation");
    expect(classifyIPv4(parseIPv4("127.0.0.1")!)).toBe("loopback");
    expect(classifyIPv4(parseIPv4("169.254.1.1")!)).toBe("link-local");
    expect(classifyIPv4(parseIPv4("0.0.0.0")!)).toBe("unspecified");
    expect(classifyIPv4(parseIPv4("224.0.0.1")!)).toBe("multicast");
    expect(classifyIPv4(parseIPv4("8.8.8.8")!)).toBe("public");
    expect(classifyIPv6(parseIPv6("fd12:3456::1")!)).toBe("ula");
    expect(classifyIPv6(parseIPv6("2400:1234::1")!)).toBe("global");
    expect(classifyIPv6(parseIPv6("2001:db8::1")!)).toBe("documentation");
    expect(classifyIPv6(parseIPv6("fe80::1")!)).toBe("link-local");
    expect(classifyIPv6(parseIPv6("::ffff:10.0.0.1")!)).toBe("ipv4-mapped");
  });

  it("applies the address policy", () => {
    expect(checkIPv4("192.168.1.23", "private")).toEqual({ ok: true, value: "192.168.1.23" });
    expect(checkIPv4("192.0.2.10", "private")).toEqual({ ok: true, value: "192.0.2.10" });
    expect(checkIPv4("8.8.8.8", "private").ok).toBe(false);
    expect(checkIPv4("8.8.8.8", "any")).toEqual({ ok: true, value: "8.8.8.8" });
    expect(checkIPv4("127.0.0.1", "any").ok).toBe(false);
    expect(checkIPv6("FD00::0001")).toEqual({ ok: true, value: "fd00::1" });
    expect(checkIPv6("fe80::1").ok).toBe(false);
    expect(checkIPv6("::1").ok).toBe(false);
  });

  it("groups IPv6 callers by /64", () => {
    expect(networkKey("203.0.113.9")).toBe("203.0.113.9");
    expect(networkKey("2001:db8:1:2:aaaa::1")).toBe("2001:db8:1:2::/64");
    expect(networkKey("2001:db8:1:2:bbbb::7")).toBe("2001:db8:1:2::/64");
    expect(networkKey("not an ip")).toBe("unknown");
  });
});

describe("ids", () => {
  it("slugifies the hostname hint", () => {
    expect(slugify("Lumine Clinic")).toBe("lumine-clinic");
    expect(slugify("  --CARE.local--  ")).toBe("care-local");
    expect(slugify("xn--caf-dma")).toBe("xn-caf-dma");
    expect(slugify("Ünïcôdé Hospital")).toBe("unicode-hospital");
    expect(slugify("Sjøvik")).toBe("sj-vik");
    expect(slugify("!!!")).toBe("clinic");
    expect(slugify("a".repeat(40))).toBe("a".repeat(20));
    expect(slugify("abcdefghijklmnopqrs-tuvw")).toBe("abcdefghijklmnopqrs");
  });

  it("makes ids that are DNS labels", () => {
    for (let i = 0; i < 200; i++) {
      const id = newClinicId("Lumine Clinic");
      expect(id).toMatch(/^lumine-clinic-[a-z2-7]{4}$/);
      expect(isClinicId(id)).toBe(true);
    }
    expect(BASE32).toHaveLength(32);
    expect(isClinicId("lumine--k3x9")).toBe(false);
    expect(isClinicId("-lumine-k3x9")).toBe(false);
    expect(isClinicId("lumine-k3x1")).toBe(false); // "1" is not base32
    expect(isClinicId("lumine")).toBe(false);
    expect(isClinicId("_acme-challenge")).toBe(false);
  });
});

describe("rate limit configuration", () => {
  it("merges RATE_LIMITS over the defaults and ignores bad entries", () => {
    const limits = limitsFor(testEnv({ RATE_LIMITS: '{"register":{"limit":9,"window":60},"acme":{"limit":-1,"window":60},"nope":{}}' }));
    expect(limits.register).toEqual({ limit: 9, window: 60 });
    expect(limits.acme).toEqual(DEFAULT_LIMITS.acme);
    expect(limitsFor(testEnv({ RATE_LIMITS: "not json" }))).toEqual(DEFAULT_LIMITS);
  });
});
