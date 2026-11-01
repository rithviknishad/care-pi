// GET /lookup: the clinics that registered from the public IP address of the caller and opted in to the listing.

import { clinicName } from "../env";
import { HttpError, json } from "../http";
import { rfc3339 } from "../encoding";
import { networkKey } from "../ip";
import { enforce } from "../ratelimit";
import type { Ctx } from "./common";

export const LOOKUP_MAX = 50;

export async function lookup(ctx: Ctx): Promise<Response> {
  const { env, request, now, ipKey } = ctx;
  await enforce(env.DB, env, "lookup", ipKey, now);
  if (ipKey === "unknown") throw new HttpError(400, "unknown_ip", "The service cannot read the IP address of the caller.");
  const asked = new URL(request.url).searchParams.get("ip");
  // The service lists only the network of the caller. This stops the enumeration of other networks.
  if (asked !== null && asked !== "" && networkKey(asked) !== ipKey) {
    throw new HttpError(403, "ip_mismatch", "The ip parameter must be your own public IP address (or be absent).", { your_ip: ctx.ip });
  }
  const rows = await env.DB.prepare(
    "SELECT id, created_at FROM clinics WHERE listing_opt_in = 1 AND revoked_at IS NULL AND (created_ip = ?1 OR last_ip = ?1) " +
      "ORDER BY created_at DESC LIMIT ?2",
  )
    .bind(ipKey, LOOKUP_MAX)
    .all<{ id: string; created_at: number }>();
  return json(200, {
    ip: ctx.ip,
    network: ipKey,
    clinics: rows.results.map((r) => ({ id: r.id, fulldomain: clinicName(env, r.id), registered_at: rfc3339(r.created_at) })),
  });
}
