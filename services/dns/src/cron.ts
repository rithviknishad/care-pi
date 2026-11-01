// The scheduled handler: housekeeping every 15 minutes (wrangler.toml [triggers]).

import { dnsClient, removeAllRecords, TXT_MAX_AGE_SECONDS } from "./dns-ops";
import type { Env } from "./env";
import { log } from "./handlers/common";

// A run must stay under the subrequest limit of the Free plan: 50 per invocation, D1 queries included.
// Worst case: 3 (database cleanup and queries) + TXT_BATCH + 1 + REVOKED_BATCH * ~12 = about 40.
export const TXT_BATCH = 12;
export const REVOKED_BATCH = 2;
export const RATE_KEEP_SECONDS = 2 * 86400;

export interface HousekeepingResult {
  txtDeleted: number;
  revokedCleaned: number;
  errors: number;
}

export async function housekeeping(env: Env, now: number): Promise<HousekeepingResult> {
  const db = env.DB;
  const result: HousekeepingResult = { txtDeleted: 0, revokedCleaned: 0, errors: 0 };
  await db.batch([
    db.prepare("DELETE FROM nonces WHERE expires_at < ?").bind(now),
    db.prepare("DELETE FROM replay WHERE expires_at < ?").bind(now),
    db.prepare("DELETE FROM rate WHERE window_start < ?").bind(now - RATE_KEEP_SECONDS),
  ]);
  if (!env.CF_DNS_API_TOKEN) return result;
  const dns = dnsClient(env);

  const old = await db
    .prepare("SELECT record_id, id FROM txt_records WHERE created_at < ? ORDER BY created_at LIMIT ?")
    .bind(now - TXT_MAX_AGE_SECONDS, TXT_BATCH)
    .all<{ record_id: string; id: string }>();
  const gone: string[] = [];
  for (const row of old.results) {
    try {
      await dns.delete(row.record_id);
      gone.push(row.record_id);
    } catch (err) {
      result.errors++;
      log("housekeeping_txt_failed", { id: row.id, error: String(err) });
    }
  }
  if (gone.length > 0) {
    await db.batch(gone.map((recordId) => db.prepare("DELETE FROM txt_records WHERE record_id = ?").bind(recordId)));
    result.txtDeleted = gone.length;
  }

  // Revoked clinics that still have DNS records: DELETE /v1/clinics/{id} failed half-way, or an operator
  // revoked the clinic with SQL (docs/dns-service.md, "Revoke a clinic by hand").
  const revoked = await db
    .prepare(
      "SELECT c.id FROM clinics c WHERE c.revoked_at IS NOT NULL AND " +
        "(EXISTS (SELECT 1 FROM addresses a WHERE a.id = c.id) OR EXISTS (SELECT 1 FROM txt_records t WHERE t.id = c.id)) LIMIT ?",
    )
    .bind(REVOKED_BATCH)
    .all<{ id: string }>();
  for (const row of revoked.results) {
    try {
      await removeAllRecords(env, dns, row.id);
      result.revokedCleaned++;
    } catch (err) {
      result.errors++;
      log("housekeeping_revoke_failed", { id: row.id, error: String(err) });
    }
  }
  if (result.txtDeleted || result.revokedCleaned || result.errors) log("housekeeping", { ...result });
  return result;
}
