// Fixed-window rate limits, counted in D1.
//
// The Workers Rate Limiting binding supports periods of 10 s and 60 s only, and it counts per Cloudflare
// location. The registration limit is per day, so the service keeps all counters in D1: one upsert per check.

import type { Env } from "./env";
import { HttpError } from "./http";

export type Bucket = "nonce" | "register" | "address" | "acme" | "manage" | "lookup" | "authfail";

export interface Limit {
  limit: number;
  window: number; // seconds
}

export const DEFAULT_LIMITS: Readonly<Record<Bucket, Limit>> = {
  nonce: { limit: 20, window: 60 }, //       per network key
  register: { limit: 5, window: 86400 }, //  per network key
  address: { limit: 6, window: 60 }, //      per clinic id
  acme: { limit: 6, window: 60 }, //         per clinic id
  manage: { limit: 6, window: 60 }, //       per clinic id: status, settings, rotate, delete
  lookup: { limit: 30, window: 60 }, //      per network key
  authfail: { limit: 20, window: 600 }, //   per network key: failed signatures and acme-dns keys
};

export function limitsFor(env: Env): Record<Bucket, Limit> {
  const out: Record<Bucket, Limit> = { ...DEFAULT_LIMITS };
  if (!env.RATE_LIMITS) return out;
  let parsed: unknown;
  try {
    parsed = JSON.parse(env.RATE_LIMITS);
  } catch {
    return out;
  }
  if (typeof parsed !== "object" || parsed === null) return out;
  for (const [bucket, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (!(bucket in out) || typeof value !== "object" || value === null) continue;
    const { limit, window } = value as Partial<Limit>;
    if (Number.isInteger(limit) && Number.isInteger(window) && (limit as number) > 0 && (window as number) > 0) {
      out[bucket as Bucket] = { limit: limit as number, window: window as number };
    }
  }
  return out;
}

export interface RateResult {
  allowed: boolean;
  count: number;
  retryAfter: number;
}

function windowStart(now: number, window: number): number {
  return now - (now % window);
}

// Adds one to the counter and tells if the request is in the limit.
export async function hit(db: D1Database, bucket: Bucket, key: string, limit: Limit, now: number): Promise<RateResult> {
  const start = windowStart(now, limit.window);
  const row = await db
    .prepare(
      "INSERT INTO rate (bucket, key, window_start, count) VALUES (?, ?, ?, 1) " +
        "ON CONFLICT (bucket, key, window_start) DO UPDATE SET count = count + 1 RETURNING count",
    )
    .bind(bucket, key, start)
    .first<{ count: number }>();
  const count = row?.count ?? 1;
  return { allowed: count <= limit.limit, count, retryAfter: start + limit.window - now };
}

// Reads the counter without a change.
export async function peek(db: D1Database, bucket: Bucket, key: string, limit: Limit, now: number): Promise<RateResult> {
  const start = windowStart(now, limit.window);
  const row = await db
    .prepare("SELECT count FROM rate WHERE bucket = ? AND key = ? AND window_start = ?")
    .bind(bucket, key, start)
    .first<{ count: number }>();
  const count = row?.count ?? 0;
  return { allowed: count < limit.limit, count, retryAfter: start + limit.window - now };
}

export function rateLimited(bucket: Bucket, result: RateResult): HttpError {
  const retry = Math.max(1, result.retryAfter);
  return new HttpError(
    429,
    "rate_limited",
    `Too many requests (${bucket}). Try again in ${retry} seconds.`,
    { bucket, retry_after: retry },
    { "retry-after": String(retry) },
  );
}

export async function enforce(db: D1Database, env: Env, bucket: Bucket, key: string, now: number): Promise<void> {
  const result = await hit(db, bucket, key, limitsFor(env)[bucket], now);
  if (!result.allowed) throw rateLimited(bucket, result);
}

// Rejects a caller that has too many failed authentications in the current window.
export async function gateAuthFailures(db: D1Database, env: Env, key: string, now: number): Promise<void> {
  const result = await peek(db, "authfail", key, limitsFor(env).authfail, now);
  if (!result.allowed) throw rateLimited("authfail", result);
}

export async function countAuthFailure(db: D1Database, env: Env, key: string, now: number): Promise<void> {
  await hit(db, "authfail", key, limitsFor(env).authfail, now);
}
