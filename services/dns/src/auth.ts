// Request authentication: Ed25519 request signatures (the /v1 API) and the acme-dns key (POST /update).

import { fromBase64Strict, sha256Hex, utf8 } from "./encoding";
import { HttpError } from "./http";

export const SIGNATURE_SCHEME = "CARE-PI-DNS-V1";
export const REPLAY_WINDOW_SECONDS = 300;
export const HEADER_TIMESTAMP = "x-care-pi-timestamp";
export const HEADER_SIGNATURE = "x-care-pi-signature";
export const HEADER_SIGNATURE_NEW = "x-care-pi-signature-new";

// The string that the client signs (API.md section 3):
//   CARE-PI-DNS-V1 \n METHOD \n PATH \n TIMESTAMP \n hex(SHA-256(body))
export function canonicalString(method: string, path: string, timestamp: string, bodySha256Hex: string): string {
  return [SIGNATURE_SCHEME, method.toUpperCase(), path, timestamp, bodySha256Hex].join("\n");
}

export async function canonicalFor(request: Request, body: Uint8Array, timestamp: string): Promise<string> {
  const path = new URL(request.url).pathname;
  return canonicalString(request.method, path, timestamp, await sha256Hex(body));
}

export function decodePublicKey(text: unknown): Uint8Array | null {
  if (typeof text !== "string") return null;
  const key = fromBase64Strict(text);
  return key !== null && key.byteLength === 32 ? key : null;
}

export function decodeSignature(text: string | null): Uint8Array | null {
  if (text === null) return null;
  const sig = fromBase64Strict(text.trim());
  return sig !== null && sig.byteLength === 64 ? sig : null;
}

export async function verifyEd25519(publicKey: Uint8Array, signature: Uint8Array, message: string): Promise<boolean> {
  try {
    const key = await crypto.subtle.importKey("raw", publicKey, { name: "Ed25519" }, false, ["verify"]);
    return await crypto.subtle.verify({ name: "Ed25519" }, key, signature, utf8(message));
  } catch {
    return false;
  }
}

export interface SignedHeaders {
  timestamp: string;
  signature: Uint8Array;
}

// Reads and checks X-Care-Pi-Timestamp and X-Care-Pi-Signature. Throws a 401 HttpError.
export function readSignedHeaders(request: Request, now: number): SignedHeaders {
  const timestamp = request.headers.get(HEADER_TIMESTAMP);
  const signatureText = request.headers.get(HEADER_SIGNATURE);
  if (timestamp === null || signatureText === null) {
    throw new HttpError(401, "missing_signature", "The request must have the X-Care-Pi-Timestamp and X-Care-Pi-Signature headers.");
  }
  if (!/^[0-9]{1,12}$/.test(timestamp)) {
    throw new HttpError(401, "bad_timestamp", "X-Care-Pi-Timestamp must be Unix time in decimal seconds.");
  }
  const ts = Number(timestamp);
  if (Math.abs(now - ts) > REPLAY_WINDOW_SECONDS) {
    throw new HttpError(401, "stale_timestamp", `X-Care-Pi-Timestamp must be within ${REPLAY_WINDOW_SECONDS} seconds of the server time.`, {
      server_time: now,
    });
  }
  const signature = decodeSignature(signatureText);
  if (signature === null) {
    throw new HttpError(401, "bad_signature", "X-Care-Pi-Signature must be 64 bytes in standard base64.");
  }
  return { timestamp, signature };
}

// Records a signed request in the replay cache. A second request with the same clinic id and the same
// canonical string in the replay window fails with 401 "replayed".
export async function rememberRequest(db: D1Database, id: string, canonical: string, timestamp: string): Promise<void> {
  const digest = await sha256Hex(`${id}\n${canonical}`);
  const expiresAt = Number(timestamp) + REPLAY_WINDOW_SECONDS + 60;
  const row = await db
    .prepare("INSERT INTO replay (digest, expires_at) VALUES (?, ?) ON CONFLICT (digest) DO NOTHING RETURNING digest")
    .bind(digest, expiresAt)
    .first<{ digest: string }>();
  if (row === null) {
    throw new HttpError(401, "replayed", "The service already accepted this signed request. Sign the retry with a new timestamp.");
  }
}

// Compares the SHA-256 of an acme-dns password with the stored hash in constant time.
export async function acmeKeyMatches(password: string, storedHashHex: string | null): Promise<boolean> {
  if (storedHashHex === null || storedHashHex.length !== 64) return false;
  const given = utf8(await sha256Hex(password));
  const stored = utf8(storedHashHex);
  return crypto.subtle.timingSafeEqual(given, stored);
}

