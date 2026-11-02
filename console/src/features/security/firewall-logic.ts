import type { FirewallState } from "@/api/types";

/** The seconds until the rollback of the policy drop, or null when no timer runs. */
export function secondsLeft(fw: Pick<FirewallState, "pending_until"> | undefined, now: number): number | null {
  if (!fw?.pending_until) return null;
  return Math.max(0, Math.ceil((Date.parse(fw.pending_until) - now) / 1000));
}
