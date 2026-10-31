// Pure helpers for the texts and badges of the Updates page and the dashboard (unit tests: tests/updates.test.ts).

import type {
  InstalledLock,
  Job,
  PitrOffer,
  Problem,
  UpdateBlocker,
  UpdateHistoryEntry,
  UpdatesOverview,
  UpdatesSummary,
  UpdateTrack,
} from "@/api/types";
import { fmt, strings } from "@/lib/strings";
import { formatZoned } from "./window";

const u = strings.updates;

export type BadgeVariant = "success" | "info" | "warning" | "destructive" | "neutral" | "primary";

export interface BadgeSpec {
  text: string;
  variant: BadgeVariant;
}

// stable: green; pinned: blue; custom: orange with "Not supported" (03 section 3.2).
export function trackBadge(track: UpdateTrack | null | undefined): BadgeSpec & { unsupported: boolean } {
  if (!track) return { text: "—", variant: "neutral", unsupported: false };
  const text = u.tracks[track] ?? track;
  if (track === "custom") return { text: `${text} (${u.unsupported})`, variant: "warning", unsupported: true };
  return { text, variant: track === "stable" ? "success" : "info", unsupported: false };
}

// The running version: the lock version, else what the device runs without a release.
export function versionText(current: Pick<InstalledLock, "version" | "source"> | null | undefined): string {
  if (!current) return "—";
  if (current.version) return current.version;
  return current.source === "custom" ? u.customVersions : u.noRelease;
}

export function outcomeBadge(outcome: UpdateHistoryEntry["outcome"]): BadgeSpec {
  const text = u.outcomes[outcome] ?? outcome;
  switch (outcome) {
    case "succeeded":
      return { text, variant: "success" };
    case "rolled-back":
      return { text, variant: "warning" };
    default:
      return { text, variant: "destructive" };
  }
}

export function blockerText(blocker: Pick<UpdateBlocker, "code" | "min_agent_version"> | null | undefined): string {
  if (!blocker) return "";
  const template = u.blockers[blocker.code];
  return template ? fmt(template, { version: blocker.min_agent_version ?? "?" }) : blocker.code;
}

// The state line of the new-version card: what the update machine does, or what the operator can do.
export function updateStatus(
  o: Pick<UpdatesOverview, "state" | "blocked_by" | "update_available" | "target" | "track" | "available">,
): BadgeSpec {
  switch (o.state) {
    case "checking":
    case "staging":
    case "applying":
    case "rolling-back":
      return { text: u.states[o.state] ?? o.state, variant: "info" };
    case "failed":
      return { text: u.states.failed ?? o.state, variant: "destructive" };
  }
  if (o.blocked_by)
    return { text: fmt(u.states.blocked ?? "", { reason: blockerText(o.blocked_by) }), variant: "warning" };
  if (o.update_available && o.target?.staged) return { text: u.states.staged ?? "", variant: "success" };
  if (o.update_available) return { text: u.states.available ?? "", variant: "primary" };
  if (o.track === "pinned" && o.available.length > 0) return { text: u.states.pinned ?? "", variant: "info" };
  return { text: u.states.idle ?? "", variant: "neutral" };
}

// The update line of the dashboard (Status.updates).
export function summaryText(s: UpdatesSummary): BadgeSpec & { link: boolean } {
  const d = strings.dashboard;
  const version = s.target_version ?? s.latest_version ?? "";
  if (s.blocked_by) {
    const reason = u.blockersShort[s.blocked_by] ?? s.blocked_by;
    return { text: fmt(d.updatesBlocked, { version, reason }), variant: "warning", link: true };
  }
  if (s.update_available && s.staged_version)
    return { text: fmt(d.updatesStaged, { version: s.staged_version }), variant: "success", link: true };
  if (s.update_available) return { text: fmt(d.updatesAvailable, { version }), variant: "primary", link: true };
  return { text: d.updatesNone, variant: "neutral", link: false };
}

// LockVersion values compare as 3 numbers: 2026.11.10 is newer than 2026.11.9.
export function compareVersions(a: string, b: string): number {
  const pa = a.split(".").map(Number);
  const pb = b.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d;
  }
  return 0;
}

// The PITR offer of a job that the page followed: the result of a rollback, or the error of a failed update.
export function pitrOfferOfJob(job: Pick<Job, "result" | "error"> | null | undefined): PitrOffer | null {
  if (!job) return null;
  const result = job.result as { pitr_offer?: PitrOffer | null } | null;
  if (result?.pitr_offer) return result.pitr_offer;
  return (job.error as Problem | null)?.pitr_offer ?? null;
}

// The link to the Restore page of Phase 3 with the time of the offer (docs/console-ui.md section 10.4).
export function restoreLink(offer: Pick<PitrOffer, "target_time">): string {
  return `/restore?time=${encodeURIComponent(offer.target_time)}`;
}

// The key of an offer, for "Not now".
export function offerKey(offer: Pick<PitrOffer, "target_time" | "restore_point">): string {
  return offer.restore_point ?? offer.target_time;
}

// "2026.11.0 → 2026.11.1". A version without a release shows as "No release".
export function historyVersions(e: Pick<UpdateHistoryEntry, "from_version" | "to_version">): string {
  return fmt(u.versions, { from: e.from_version ?? u.noRelease, to: e.to_version ?? u.noRelease });
}

// "20261108T120000Z" -> the ISO time of the lock file.
function lockTime(id: string): string {
  const m = /^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z$/.exec(id);
  return m ? `${m[1]}-${m[2]}-${m[3]}T${m[4]}:${m[5]}:${m[6]}Z` : id;
}

// The previous version of a rollback: its lock version, else the time of its lock file.
export function previousText(o: Pick<UpdatesOverview, "rollback">, tz: string): string {
  const r = o.rollback;
  if (r.to_version) return r.to_version;
  if (r.to_lock) return fmt(u.previousLock, { time: formatZoned(lockTime(r.to_lock), tz) });
  return u.rollbackNone;
}
