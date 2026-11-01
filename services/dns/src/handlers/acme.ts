// The acme-dns-compatible API (https://github.com/joohoi/acme-dns#api), as used by libdns/acmedns and
// the Caddy module dns.providers.acmedns.
//
// Difference from acme-dns: the TXT record is at _acme-challenge.<id>.<base domain> directly, so the clinic
// needs no CNAME. The "subdomain" of the credentials is the clinic id.

import { acmeKeyMatches } from "../auth";
import { getClinicByAcmeUser, touchClinic } from "../db";
import { addChallenge, dnsClient } from "../dns-ops";
import { HttpError, json, parseJsonObject, readBody } from "../http";
import { countAuthFailure, enforce, gateAuthFailures } from "../ratelimit";
import { log, type Ctx } from "./common";

// An ACME DNS-01 value is the base64url SHA-256 digest of the key authorization: 43 characters.
const TXT_VALUE = /^[A-Za-z0-9_-]{43}$/;

function forbidden(): HttpError {
  return new HttpError(401, "forbidden", "The X-Api-User, X-Api-Key, and subdomain do not match a clinic.");
}

// POST /update
export async function update(ctx: Ctx): Promise<Response> {
  const { env, request, now, ipKey } = ctx;
  await gateAuthFailures(env.DB, env, ipKey, now);
  const user = request.headers.get("x-api-user");
  const key = request.headers.get("x-api-key");
  const body = await readBody(request);
  const input = parseJsonObject(body, "malformed_json_payload");
  if (user === null || key === null) throw forbidden();

  const clinic = await getClinicByAcmeUser(env.DB, user);
  const keyOk = await acmeKeyMatches(key, clinic?.acme_key_hash ?? null);
  if (clinic === null || !keyOk || input.subdomain !== clinic.id) {
    await countAuthFailure(env.DB, env, ipKey, now);
    throw forbidden();
  }
  if (typeof input.txt !== "string" || !TXT_VALUE.test(input.txt)) {
    throw new HttpError(400, "bad_txt", "txt must be 43 base64url characters (an ACME DNS-01 value).");
  }
  await enforce(env.DB, env, "acme", clinic.id, now);
  await addChallenge(env, dnsClient(env), clinic.id, input.txt, now);
  await touchClinic(env.DB, clinic.id, now, ipKey);
  log("acme_txt", { id: clinic.id });
  return json(200, { txt: input.txt });
}

// POST /register: closed. A Pi gets acme-dns credentials from POST /v1/register.
export function registerClosed(): Response {
  return json(403, {
    error: "registration_closed",
    message: "Open registration is off. A care-pi device registers with POST /v1/register (signed) and gets its acme-dns credentials there.",
  });
}

// GET /health
export function health(): Response {
  return json(200, { status: "ok" });
}
