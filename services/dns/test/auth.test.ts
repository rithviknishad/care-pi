import { describe, expect, it } from "vitest";
import { canonicalString, decodePublicKey, decodeSignature, readSignedHeaders, verifyEd25519 } from "../src/auth";
import { sha256Hex, toHex, utf8 } from "../src/encoding";
import vectors from "../testdata/vectors.json";

describe("signature test vectors (testdata/vectors.json)", () => {
  it("has the RFC 8032 test keys", () => {
    expect(vectors.keys.current.public_hex).toBe("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a");
    expect(toHex(decodePublicKey(vectors.keys.current.public_base64)!)).toBe(vectors.keys.current.public_hex);
    expect(toHex(decodePublicKey(vectors.keys.new.public_base64)!)).toBe(vectors.keys.new.public_hex);
  });

  it("empty body hash and acme password hash", async () => {
    expect(await sha256Hex("")).toBe(vectors.empty_body_sha256);
    expect(await sha256Hex(vectors.acme_password_hash.password)).toBe(vectors.acme_password_hash.sha256_hex);
  });

  for (const v of vectors.requests) {
    it(`verifies the vector "${v.name}"`, async () => {
      expect(await sha256Hex(v.body)).toBe(v.body_sha256);
      const canonical = canonicalString(v.method, v.path, v.timestamp, v.body_sha256);
      expect(canonical).toBe(v.canonical);
      expect(toHex(utf8(canonical))).toBe(v.canonical_hex);
      const current = decodePublicKey(vectors.keys.current.public_base64)!;
      expect(await verifyEd25519(current, decodeSignature(v.signature)!, canonical)).toBe(true);
      expect(await verifyEd25519(current, decodeSignature(v.signature)!, `${canonical} `)).toBe(false);
      if ("signature_new" in v && typeof v.signature_new === "string") {
        const next = decodePublicKey(vectors.keys.new.public_base64)!;
        expect(await verifyEd25519(next, decodeSignature(v.signature_new)!, canonical)).toBe(true);
        expect(await verifyEd25519(current, decodeSignature(v.signature_new)!, canonical)).toBe(false);
      }
    });
  }
});

describe("readSignedHeaders", () => {
  const now = 1_800_000_000;
  const sig = vectors.requests[0]!.signature;
  const req = (headers: Record<string, string>) => new Request("https://x/v1/register", { method: "POST", headers });

  it("needs both headers", () => {
    expect(() => readSignedHeaders(req({}), now)).toThrowError(/X-Care-Pi-Timestamp/);
    expect(() => readSignedHeaders(req({ "x-care-pi-timestamp": String(now) }), now)).toThrow();
  });

  it("checks the timestamp format and the window of 300 seconds", () => {
    expect(() => readSignedHeaders(req({ "x-care-pi-timestamp": "1.5e9", "x-care-pi-signature": sig }), now)).toThrowError(/decimal/);
    expect(() => readSignedHeaders(req({ "x-care-pi-timestamp": String(now - 301), "x-care-pi-signature": sig }), now)).toThrowError(/300/);
    expect(() => readSignedHeaders(req({ "x-care-pi-timestamp": String(now + 301), "x-care-pi-signature": sig }), now)).toThrowError(/300/);
    expect(readSignedHeaders(req({ "x-care-pi-timestamp": String(now - 300), "x-care-pi-signature": sig }), now).timestamp).toBe(String(now - 300));
  });

  it("needs a 64-byte signature in standard base64", () => {
    expect(() => readSignedHeaders(req({ "x-care-pi-timestamp": String(now), "x-care-pi-signature": "AAAA" }), now)).toThrowError(/64 bytes/);
    const url = sig.replace(/\+/g, "-").replace(/\//g, "_");
    if (url !== sig) {
      expect(() => readSignedHeaders(req({ "x-care-pi-timestamp": String(now), "x-care-pi-signature": url }), now)).toThrow();
    }
  });
});
