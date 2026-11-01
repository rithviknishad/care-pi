// The DNS operations of the service. The handlers and the scheduled handler use them.

import { CfDns, CfDnsError } from "./cf-dns";
import { deleteTxt, getAddress, insertTxt, listTxt, putAddress, type AddressRow } from "./db";
import { challengeName, clinicName, recordTtl, type Env } from "./env";
import { HttpError } from "./http";

// The service writes an unchanged address again after this time, to repair a record that was changed by hand.
export const ADDRESS_REFRESH_SECONDS = 3600;
// acme-dns keeps the two newest TXT values: one certificate order can need two (a name and its wildcard).
export const TXT_KEEP = 2;
// The scheduled handler deletes TXT records older than this. Let's Encrypt checks the record in minutes.
export const TXT_MAX_AGE_SECONDS = 3600;

export function dnsClient(env: Env): CfDns {
  if (!env.CF_DNS_API_TOKEN) {
    throw new HttpError(503, "dns_unconfigured", "The service has no Cloudflare DNS API token (CF_DNS_API_TOKEN).");
  }
  return env.CF_API_BASE
    ? new CfDns({ token: env.CF_DNS_API_TOKEN, zoneId: env.ZONE_ID, baseUrl: env.CF_API_BASE })
    : new CfDns({ token: env.CF_DNS_API_TOKEN, zoneId: env.ZONE_ID });
}

function comment(id: string): string {
  return `care-pi-dns ${id}`;
}

export interface AddressResult {
  row: AddressRow;
  changed: boolean;
}

// Sets the A and AAAA records of a clinic. A null address removes the record.
export async function applyAddress(
  env: Env,
  dns: CfDns,
  id: string,
  ipv4: string | null,
  ipv6: string | null,
  now: number,
): Promise<AddressResult> {
  const current = await getAddress(env.DB, id);
  const name = clinicName(env, id);
  const ttl = recordTtl(env);
  const stale = current === null || now - current.updated_at >= ADDRESS_REFRESH_SECONDS;
  const changed = (current?.ipv4 ?? null) !== ipv4 || (current?.ipv6 ?? null) !== ipv6;
  let aId = current?.a_record_id ?? null;
  let aaaaId = current?.aaaa_record_id ?? null;
  let wrote = false;

  if (ipv4 !== null) {
    if (stale || aId === null || current?.ipv4 !== ipv4) {
      aId = (await dns.upsert({ type: "A", name, content: ipv4, ttl, comment: comment(id) }, aId)).id;
      wrote = true;
    }
  } else if (aId !== null || current?.ipv4) {
    if (aId === null || !(await dns.delete(aId))) await dns.deleteByName("A", name);
    aId = null;
    wrote = true;
  }

  if (ipv6 !== null) {
    if (stale || aaaaId === null || current?.ipv6 !== ipv6) {
      aaaaId = (await dns.upsert({ type: "AAAA", name, content: ipv6, ttl, comment: comment(id) }, aaaaId)).id;
      wrote = true;
    }
  } else if (aaaaId !== null || current?.ipv6) {
    if (aaaaId === null || !(await dns.delete(aaaaId))) await dns.deleteByName("AAAA", name);
    aaaaId = null;
    wrote = true;
  }

  const row: AddressRow = {
    id,
    ipv4,
    ipv6,
    a_record_id: aId,
    aaaa_record_id: aaaaId,
    updated_at: wrote || current === null ? now : current.updated_at,
  };
  if (wrote || current === null) await putAddress(env.DB, row);
  return { row, changed };
}

// Adds a TXT value at _acme-challenge.<id>.<base domain> and keeps the TXT_KEEP newest values.
export async function addChallenge(env: Env, dns: CfDns, id: string, value: string, now: number): Promise<void> {
  const existing = await listTxt(env.DB, id);
  if (existing.some((r) => r.value === value)) return;
  const input = { type: "TXT" as const, name: challengeName(env, id), content: value, ttl: recordTtl(env), comment: comment(id) };
  let recordId: string;
  try {
    recordId = (await dns.create(input)).id;
  } catch (err) {
    // 81058: an identical record exists in the zone but not in D1 (for example after a D1 restore). Adopt it.
    if (!(err instanceof CfDnsError && err.codes.includes(81058))) throw err;
    const same = (await dns.list("TXT", input.name)).find((r) => r.content === value || r.content === `"${value}"`);
    if (same === undefined) throw err;
    recordId = same.id;
  }
  await insertTxt(env.DB, { record_id: recordId, id, value, created_at: now });
  for (const old of existing.slice(TXT_KEEP - 1)) {
    await dns.delete(old.record_id);
    await deleteTxt(env.DB, old.record_id);
  }
}

// Deletes all DNS records of a clinic: A, AAAA, and TXT. The database rows go after the DNS records,
// so that the scheduled handler can try again after a failure.
export async function removeAllRecords(env: Env, dns: CfDns, id: string): Promise<void> {
  const address = await getAddress(env.DB, id);
  if (address?.a_record_id) await dns.delete(address.a_record_id);
  if (address?.aaaa_record_id) await dns.delete(address.aaaa_record_id);
  await dns.deleteByName("A", clinicName(env, id));
  await dns.deleteByName("AAAA", clinicName(env, id));
  for (const txt of await listTxt(env.DB, id)) await dns.delete(txt.record_id);
  await dns.deleteByName("TXT", challengeName(env, id));
  await env.DB.batch([
    env.DB.prepare("DELETE FROM txt_records WHERE id = ?").bind(id),
    env.DB.prepare("DELETE FROM addresses WHERE id = ?").bind(id),
  ]);
}

