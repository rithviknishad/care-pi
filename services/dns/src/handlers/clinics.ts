// The /v1 API of the Pi: nonce, registration, status, settings, address (DDNS), rotation, deletion.

import {
  HEADER_SIGNATURE_NEW,
  canonicalFor,
  decodePublicKey,
  decodeSignature,
  readSignedHeaders,
  rememberRequest,
  verifyEd25519,
} from "../auth";
import {
  getAddress,
  getClinic,
  insertClinic,
  insertNonce,
  consumeNonce,
  revokeClinic,
  setCredentials,
  setListing,
  touchClinic,
  type ClinicRow,
} from "../db";
import { addressPolicy, checkIPv4, checkIPv6 } from "../ip";
import { applyAddress, dnsClient, removeAllRecords } from "../dns-ops";
import { fromBase64Strict, rfc3339, toBase64 } from "../encoding";
import { challengeName, clinicName, recordTtl, type Env } from "../env";
import { HttpError, json, parseJsonObject, readBody } from "../http";
import { isClinicId, newAcmeSecret, newClinicId, newNonce } from "../ids";
import { countAuthFailure, enforce, gateAuthFailures, type Bucket } from "../ratelimit";
import { log, type Ctx } from "./common";

export const NONCE_TTL_SECONDS = 300;
const ID_ATTEMPTS = 8;

function acmeBlock(env: Env, id: string, username: string, password?: string): Record<string, unknown> {
  const out: Record<string, unknown> = { username };
  if (password !== undefined) out.password = password;
  out.subdomain = id;
  out.fulldomain = challengeName(env, id);
  out.server_url = env.SERVER_URL;
  out.allowfrom = [];
  return out;
}

// GET /v1/nonce
export async function getNonce(ctx: Ctx): Promise<Response> {
  await enforce(ctx.env.DB, ctx.env, "nonce", ctx.ipKey, ctx.now);
  const nonce = newNonce();
  await insertNonce(ctx.env.DB, nonce, ctx.now + NONCE_TTL_SECONDS);
  return json(200, { nonce, expires_in: NONCE_TTL_SECONDS, server_time: ctx.now });
}

// POST /v1/register
export async function register(ctx: Ctx): Promise<Response> {
  const { env, request, now, ipKey } = ctx;
  await gateAuthFailures(env.DB, env, ipKey, now);
  const body = await readBody(request);
  const input = parseJsonObject(body);

  const hint = typeof input.hostname_hint === "string" ? input.hostname_hint.trim() : "";
  if (hint.length < 1 || hint.length > 63) {
    throw new HttpError(400, "bad_request", "hostname_hint must be a string of 1 to 63 characters.");
  }
  const pubkey = decodePublicKey(input.pubkey);
  if (pubkey === null) {
    throw new HttpError(400, "bad_request", "pubkey must be a 32-byte Ed25519 public key in standard base64.");
  }
  if (typeof input.nonce !== "string" || !/^[A-Za-z0-9_-]{43}$/.test(input.nonce)) {
    throw new HttpError(400, "bad_request", "nonce must be the value from GET /v1/nonce.");
  }
  if (input.listing_opt_in !== undefined && typeof input.listing_opt_in !== "boolean") {
    throw new HttpError(400, "bad_request", "listing_opt_in must be true or false.");
  }
  const listing = input.listing_opt_in === true;

  const signed = readSignedHeaders(request, now);
  const canonical = await canonicalFor(request, body, signed.timestamp);
  if (!(await verifyEd25519(pubkey, signed.signature, canonical))) {
    await countAuthFailure(env.DB, env, ipKey, now);
    throw new HttpError(401, "bad_signature", "The signature does not match the canonical request and the pubkey.");
  }
  if (!(await consumeNonce(env.DB, input.nonce, now))) {
    throw new HttpError(401, "bad_nonce", "The nonce is unknown, used, or expired. Get a new nonce from GET /v1/nonce.");
  }
  await enforce(env.DB, env, "register", ipKey, now);

  const secret = await newAcmeSecret();
  for (let attempt = 0; attempt < ID_ATTEMPTS; attempt++) {
    const id = newClinicId(hint);
    const row: ClinicRow = {
      id,
      pubkey: toBase64(pubkey),
      created_at: now,
      created_ip: ipKey,
      hostname_hint: hint,
      acme_user: secret.username,
      acme_key_hash: secret.passwordHash,
      listing_opt_in: listing ? 1 : 0,
      last_seen: now,
      last_ip: ipKey,
      revoked_at: null,
    };
    if (await insertClinic(env.DB, row)) {
      log("register", { id, ip: ipKey, listing });
      return json(201, {
        id,
        fulldomain: clinicName(env, id),
        acme: acmeBlock(env, id, secret.username, secret.password),
        listing_opt_in: listing,
        created_at: rfc3339(now),
      });
    }
  }
  throw new HttpError(503, "id_exhausted", "The service could not allocate a free id. Try again with another hostname_hint.");
}

interface Authenticated {
  clinic: ClinicRow;
  body: Uint8Array;
  canonical: string;
  timestamp: string;
}

// Authenticates a signed request for /v1/clinics/{id}... and counts it against the rate limit of the bucket.
async function authenticate(ctx: Ctx, bucket: Bucket): Promise<Authenticated> {
  const { env, request, now, ipKey } = ctx;
  const id = ctx.params.id ?? "";
  await gateAuthFailures(env.DB, env, ipKey, now);
  if (!isClinicId(id)) throw new HttpError(404, "unknown_clinic", "No clinic has this id.");
  const body = await readBody(request);
  const signed = readSignedHeaders(request, now);
  const clinic = await getClinic(env.DB, id);
  if (clinic === null) throw new HttpError(404, "unknown_clinic", "No clinic has this id.");
  if (clinic.revoked_at !== null) {
    throw new HttpError(410, "revoked", "This clinic id is revoked. Register again to get a new id.", { revoked_at: rfc3339(clinic.revoked_at) });
  }
  const pubkey = fromBase64Strict(clinic.pubkey);
  const canonical = await canonicalFor(request, body, signed.timestamp);
  if (pubkey === null || !(await verifyEd25519(pubkey, signed.signature, canonical))) {
    await countAuthFailure(env.DB, env, ipKey, now);
    throw new HttpError(401, "bad_signature", "The signature does not match the canonical request and the registered key.");
  }
  await rememberRequest(env.DB, id, canonical, signed.timestamp);
  await enforce(env.DB, env, bucket, id, now);
  await touchClinic(env.DB, id, now, ipKey);
  return { clinic: { ...clinic, last_seen: now, last_ip: ipKey }, body, canonical, timestamp: signed.timestamp };
}

async function status(env: Env, clinic: ClinicRow): Promise<Record<string, unknown>> {
  const address = await getAddress(env.DB, clinic.id);
  return {
    id: clinic.id,
    fulldomain: clinicName(env, clinic.id),
    hostname_hint: clinic.hostname_hint,
    created_at: rfc3339(clinic.created_at),
    last_seen: rfc3339(clinic.last_seen),
    listing_opt_in: clinic.listing_opt_in === 1,
    address:
      address === null ? null : { ipv4: address.ipv4, ipv6: address.ipv6, ttl: recordTtl(env), updated_at: rfc3339(address.updated_at) },
    acme: acmeBlock(env, clinic.id, clinic.acme_user ?? ""),
  };
}

// GET /v1/clinics/{id}
export async function getStatus(ctx: Ctx): Promise<Response> {
  const auth = await authenticate(ctx, "manage");
  return json(200, await status(ctx.env, auth.clinic));
}

// PATCH /v1/clinics/{id}
export async function patchSettings(ctx: Ctx): Promise<Response> {
  const auth = await authenticate(ctx, "manage");
  const input = parseJsonObject(auth.body);
  if (typeof input.listing_opt_in !== "boolean") {
    throw new HttpError(400, "bad_request", "listing_opt_in must be true or false.");
  }
  await setListing(ctx.env.DB, auth.clinic.id, input.listing_opt_in);
  log("settings", { id: auth.clinic.id, listing: input.listing_opt_in });
  return json(200, await status(ctx.env, { ...auth.clinic, listing_opt_in: input.listing_opt_in ? 1 : 0 }));
}

function optionalAddress(value: unknown, field: string): string | null {
  if (value === undefined || value === null) return null;
  if (typeof value !== "string") throw new HttpError(400, "bad_address", `${field} must be a string or null.`);
  return value;
}

// PUT /v1/clinics/{id}/address
export async function putAddress(ctx: Ctx): Promise<Response> {
  const auth = await authenticate(ctx, "address");
  const input = parseJsonObject(auth.body, "bad_address");
  const ipv4Text = optionalAddress(input.ipv4, "ipv4");
  const ipv6Text = optionalAddress(input.ipv6, "ipv6");
  if (ipv4Text === null && ipv6Text === null) {
    throw new HttpError(400, "bad_address", "Send ipv4, ipv6, or both.");
  }
  let ipv4: string | null = null;
  let ipv6: string | null = null;
  if (ipv4Text !== null) {
    const r = checkIPv4(ipv4Text, addressPolicy(ctx.env.ADDRESS_POLICY));
    if (!r.ok) throw new HttpError(400, "bad_address", r.reason);
    ipv4 = r.value;
  }
  if (ipv6Text !== null) {
    const r = checkIPv6(ipv6Text);
    if (!r.ok) throw new HttpError(400, "bad_address", r.reason);
    ipv6 = r.value;
  }
  const dns = dnsClient(ctx.env);
  const result = await applyAddress(ctx.env, dns, auth.clinic.id, ipv4, ipv6, ctx.now);
  if (result.changed) log("address", { id: auth.clinic.id, ipv4, ipv6 });
  return json(200, {
    id: auth.clinic.id,
    fulldomain: clinicName(ctx.env, auth.clinic.id),
    ipv4: result.row.ipv4,
    ipv6: result.row.ipv6,
    ttl: recordTtl(ctx.env),
    changed: result.changed,
    updated_at: rfc3339(result.row.updated_at),
  });
}

// POST /v1/clinics/{id}/rotate
export async function rotate(ctx: Ctx): Promise<Response> {
  const auth = await authenticate(ctx, "manage");
  const input = parseJsonObject(auth.body);
  let pubkey = auth.clinic.pubkey;
  if (input.pubkey !== undefined) {
    const next = decodePublicKey(input.pubkey);
    if (next === null) throw new HttpError(400, "bad_request", "pubkey must be a 32-byte Ed25519 public key in standard base64.");
    const proof = decodeSignature(ctx.request.headers.get(HEADER_SIGNATURE_NEW));
    if (proof === null || !(await verifyEd25519(next, proof, auth.canonical))) {
      await countAuthFailure(ctx.env.DB, ctx.env, ctx.ipKey, ctx.now);
      throw new HttpError(401, "bad_new_signature", "X-Care-Pi-Signature-New must be the signature of the canonical request with the new key.");
    }
    pubkey = toBase64(next);
  }
  const secret = await newAcmeSecret();
  await setCredentials(ctx.env.DB, auth.clinic.id, secret.username, secret.passwordHash, pubkey);
  log("rotate", { id: auth.clinic.id, new_key: pubkey !== auth.clinic.pubkey });
  return json(200, {
    id: auth.clinic.id,
    fulldomain: clinicName(ctx.env, auth.clinic.id),
    pubkey,
    acme: acmeBlock(ctx.env, auth.clinic.id, secret.username, secret.password),
  });
}

// DELETE /v1/clinics/{id}
export async function deleteClinic(ctx: Ctx): Promise<Response> {
  const auth = await authenticate(ctx, "manage");
  const id = auth.clinic.id;
  await revokeClinic(ctx.env.DB, id, ctx.now);
  let cleanup: "done" | "pending" = "pending";
  if (ctx.env.CF_DNS_API_TOKEN) {
    try {
      await removeAllRecords(ctx.env, dnsClient(ctx.env), id);
      cleanup = "done";
    } catch (err) {
      log("revoke_cleanup_failed", { id, error: String(err) });
    }
  }
  log("revoke", { id, cleanup });
  return json(200, { id, revoked_at: rfc3339(ctx.now), dns_cleanup: cleanup });
}
