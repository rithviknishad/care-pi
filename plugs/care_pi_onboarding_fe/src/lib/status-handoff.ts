import type { Status } from "@/lib/wizard";

let handed: { status: Status; at: number } | null = null;

/**
 * The dashboard redirect gives the status that it read to the wizard. The wizard then opens
 * without a second GET status in the same page load.
 */
export function handOffStatus(status: Status, now = Date.now()) {
  handed = { status, at: now };
}

/** The status from the redirect, once, when it is at most maxAgeMs old. Else null. */
export function takeHandedStatus(
  maxAgeMs = 10_000,
  now = Date.now(),
): Status | null {
  const value = handed;
  handed = null;
  return value && now - value.at <= maxAgeMs ? value.status : null;
}
