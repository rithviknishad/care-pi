// Generates testdata/vectors.json: the signature test vectors of API.md section 3.
// Run: npm run vectors. Ed25519 is deterministic, so the output is stable.
//
// The keys are the RFC 8032 section 7.1 test keys (TEST 1 and TEST 2). Never use them for a real clinic.

import { createHash, createPrivateKey, createPublicKey, sign } from "node:crypto";
import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

const PKCS8_ED25519_PREFIX = Buffer.from("302e020100300506032b657004220420", "hex");

function keyFromSeed(seedHex) {
  const seed = Buffer.from(seedHex, "hex");
  const privateKey = createPrivateKey({ key: Buffer.concat([PKCS8_ED25519_PREFIX, seed]), format: "der", type: "pkcs8" });
  const spki = createPublicKey(privateKey).export({ format: "der", type: "spki" });
  const pub = spki.subarray(spki.length - 32);
  return { seedHex, privateKey, pubHex: pub.toString("hex"), pubB64: pub.toString("base64") };
}

const keyA = keyFromSeed("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60");
const keyB = keyFromSeed("4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb");

const TIMESTAMP = "1798761600"; // 2027-01-01T00:00:00Z
const NONCE = Buffer.from(Array.from({ length: 32 }, (_, i) => i + 1)).toString("base64url");
const ID = "lumine-k3x9";

function sha256Hex(text) {
  return createHash("sha256").update(Buffer.from(text, "utf8")).digest("hex");
}

function canonical(method, path, timestamp, body) {
  return ["CARE-PI-DNS-V1", method, path, timestamp, sha256Hex(body)].join("\n");
}

function vector(name, method, path, body, extra = {}) {
  const c = canonical(method, path, TIMESTAMP, body);
  const out = {
    name,
    method,
    path,
    timestamp: TIMESTAMP,
    body,
    body_sha256: sha256Hex(body),
    canonical: c,
    canonical_hex: Buffer.from(c, "utf8").toString("hex"),
    signature: sign(null, Buffer.from(c, "utf8"), keyA.privateKey).toString("base64"),
  };
  if (extra.newKey) out.signature_new = sign(null, Buffer.from(c, "utf8"), extra.newKey.privateKey).toString("base64");
  return out;
}

const vectors = {
  description:
    "Test vectors for the care-pi DNS service request signature (services/dns/API.md section 3). " +
    "The keys are the RFC 8032 section 7.1 test keys: never use them for a real clinic.",
  scheme: "CARE-PI-DNS-V1",
  canonical_format: "CARE-PI-DNS-V1\\nMETHOD\\nPATH\\nTIMESTAMP\\nhex(SHA-256(body))",
  keys: {
    current: { seed_hex: keyA.seedHex, public_hex: keyA.pubHex, public_base64: keyA.pubB64 },
    new: { seed_hex: keyB.seedHex, public_hex: keyB.pubHex, public_base64: keyB.pubB64 },
  },
  empty_body_sha256: sha256Hex(""),
  acme_password_hash: {
    password: "q0kJ3Fh5mQzVb7pX2sLr9tYwN4cGd8eA1uZo6iHjKlM",
    sha256_hex: sha256Hex("q0kJ3Fh5mQzVb7pX2sLr9tYwN4cGd8eA1uZo6iHjKlM"),
  },
  requests: [
    vector(
      "register",
      "POST",
      "/v1/register",
      JSON.stringify({ hostname_hint: "lumine", pubkey: keyA.pubB64, nonce: NONCE, listing_opt_in: false }),
    ),
    vector("status", "GET", `/v1/clinics/${ID}`, ""),
    vector("address", "PUT", `/v1/clinics/${ID}/address`, JSON.stringify({ ipv4: "192.168.1.23", ipv6: null })),
    vector("settings", "PATCH", `/v1/clinics/${ID}`, JSON.stringify({ listing_opt_in: true })),
    vector("rotate_with_new_key", "POST", `/v1/clinics/${ID}/rotate`, JSON.stringify({ pubkey: keyB.pubB64 }), { newKey: keyB }),
    vector("rotate", "POST", `/v1/clinics/${ID}/rotate`, "{}"),
    vector("delete", "DELETE", `/v1/clinics/${ID}`, ""),
  ],
};

const target = join(here, "..", "testdata", "vectors.json");
writeFileSync(target, `${JSON.stringify(vectors, null, 2)}\n`);
console.log(`wrote ${target}`);
