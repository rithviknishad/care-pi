// Format helpers of the backups and restore pages. Each helper is a pure function, so the unit tests can check it.

import type { Checkpoint, DestinationRetention, SnapshotInfo } from "@/api/types";
import { formatDateTime, formatDuration } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";

// The defaults of the agent (docs/api/README.md section 13.7, docs/config.md).
export const RETENTION_DEFAULTS: Required<DestinationRetention> = {
  keep_within_days: 14,
  keep_daily: 14,
  keep_weekly: 8,
  keep_monthly: 6,
};
export const INTERVAL_DEFAULT_MIN = 15;
export const WAL_TIMEOUT_DEFAULT_MIN = 5;
export const INTERVAL_CHOICES = [5, 15, 30, 60, 240, 1440];

// The retention of a destination without its own numbers, as the agent takes it (backup.RetentionOf): keep_daily
// and keep_weekly come from backups.retention.files_keep_daily and files_keep_weekly of the same config. Only a
// value that the config does not have takes the constant of the agent.
export function retentionDefaults(
  r: { files_keep_daily?: number | null; files_keep_weekly?: number | null } | null | undefined,
): Required<DestinationRetention> {
  return {
    ...RETENTION_DEFAULTS,
    keep_daily: r?.files_keep_daily ?? RETENTION_DEFAULTS.keep_daily,
    keep_weekly: r?.files_keep_weekly ?? RETENTION_DEFAULTS.keep_weekly,
  };
}

export function effectiveRetention(
  r: DestinationRetention | undefined | null,
  defaults: Required<DestinationRetention> = RETENTION_DEFAULTS,
): Required<DestinationRetention> {
  return { ...defaults, ...(r ?? {}) };
}

// S66, item 2: the operator picks a frequency and a "how far back" preset, not the 4 numbers.
export const FREQUENCY_CHOICES = [5, 15, 30, 60];

export type RetentionPresetName = "standard" | "longer" | "custom";

// Standard: each copy for 2 days, then 1 a day for 2 weeks, 1 a week for 2 months, 1 a month for 6 months.
// Longer: each copy for 1 week, then 1 a day for 30 days, 1 a week for 6 months, 1 a month for 2 years.
export const RETENTION_PRESETS: Record<Exclude<RetentionPresetName, "custom">, Required<DestinationRetention>> = {
  standard: { keep_within_days: 2, keep_daily: 14, keep_weekly: 8, keep_monthly: 6 },
  longer: { keep_within_days: 7, keep_daily: 30, keep_weekly: 24, keep_monthly: 24 },
};

// The preset of a retention, or "custom".
export function retentionPreset(r: DestinationRetention | undefined | null): RetentionPresetName {
  const e = effectiveRetention(r);
  for (const [name, p] of Object.entries(RETENTION_PRESETS)) {
    if ((Object.keys(p) as (keyof DestinationRetention)[]).every((k) => p[k] === e[k]))
      return name as RetentionPresetName;
  }
  return "custom";
}

// The RPO in minutes for a sentence: "20 minutes", "1 hour 5 minutes" (never seconds; console/design.md, "Copy").
export function formatRpoMinutes(minutes: number): string {
  const m = Math.max(1, Math.round(minutes));
  if (m < 60) return m === 1 ? "1 minute" : `${m} minutes`;
  const h = Math.floor(m / 60);
  const rest = m % 60;
  const hours = h === 1 ? "1 hour" : `${h} hours`;
  return rest ? `${hours} ${rest === 1 ? "1 minute" : `${rest} minutes`}` : hours;
}

// "All copies of the last 14 days, then 14 daily, 8 weekly, and 6 monthly copies."
export function formatRetention(r: DestinationRetention | undefined | null): string {
  const e = effectiveRetention(r);
  return fmt(strings.dest.retentionText, {
    within: e.keep_within_days,
    daily: e.keep_daily,
    weekly: e.keep_weekly,
    monthly: e.keep_monthly,
  });
}

// "2 full backups with their changes (about 2 weeks of points in time)". A full backup runs each week.
export function formatLocalRetention(dbFull: number): string {
  return fmt(strings.backupsPage.localRetentionValue, { count: dbFull, weeks: dbFull });
}

// 15 -> "15 min", 60 -> "1 h", 1440 -> "1 d".
export function formatMinutes(minutes: number): string {
  return formatDuration(minutes * 60);
}

// The off-site RPO (docs/api/README.md section 13.7): the interval of the destination plus archive_timeout.
export function rpoMinutes(intervalMin: number | undefined | null, walTimeoutMin: number | undefined | null): number {
  return (intervalMin ?? INTERVAL_DEFAULT_MIN) + (walTimeoutMin ?? WAL_TIMEOUT_DEFAULT_MIN);
}

export function formatRpo(minutes: number | null | undefined): string {
  return minutes === null || minutes === undefined ? "—" : formatMinutes(minutes);
}

const DAYS = new Set(["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]);

// A systemd calendar text of backups.schedule.* in plain words. An unknown form shows as it is.
// "Sun 01:00" -> "Each Sunday at 01:00", "daily 02:30" -> "Each day at 02:30", "*-*-01 04:00" -> "Day 1 of each month at 04:00".
export function calendarText(calendar: string | null | undefined): string {
  if (!calendar) return "—";
  const c = calendar.trim();
  const c1 = strings.backupsPage.calendar;
  let m = /^([A-Z][a-z]{2})\s+(\d{2}:\d{2})(?::00)?$/.exec(c);
  if (m && DAYS.has(m[1] ?? "")) return fmt(c1.weekday, { day: c1.days[m[1] ?? ""] ?? m[1] ?? "", time: m[2] ?? "" });
  m = /^(?:daily|\*-\*-\*)\s+(\d{2}:\d{2})(?::00)?$/.exec(c);
  if (m) return fmt(c1.daily, { time: m[1] ?? "" });
  m = /^\*-\*-(\d{1,2})\s+(\d{2}:\d{2})(?::00)?$/.exec(c);
  if (m) return fmt(c1.monthly, { day: Number(m[1]), time: m[2] ?? "" });
  return c;
}

export function intervalText(minutes: number | null | undefined): string {
  return minutes ? fmt(strings.backupsPage.calendar.interval, { interval: formatMinutes(minutes) }) : "—";
}

// "2026-10-30 09:40" (local time, no seconds).
export function formatMinute(iso: string | null | undefined): string {
  const t = formatDateTime(iso);
  return t === "—" ? t : t.slice(0, 16);
}

// The snapshot with `content` that is nearest to `time`, and the difference in seconds.
export function nearestSnapshot(
  snapshots: readonly SnapshotInfo[],
  time: number,
  content: "db" | "files",
): { snapshot: SnapshotInfo; diffS: number } | null {
  let best: { snapshot: SnapshotInfo; diffS: number } | null = null;
  for (const s of snapshots) {
    if (!s.contents.includes(content)) continue;
    const diffS = Math.round(Math.abs(Date.parse(s.time) - time) / 1000);
    if (!best || diffS < best.diffS) best = { snapshot: s, diffS };
  }
  return best;
}

export const UNDO_HOURS = 24;

// The undo point of the last restore: the newest checkpoint of a restore-pitr job of the last 24 hours.
export function undoCheckpoint(
  checkpoints: readonly Checkpoint[],
  now: number = Date.now(),
): { checkpoint: Checkpoint; until: number } | null {
  for (const c of checkpoints) {
    if (c.job_kind !== "restore-pitr") continue;
    const until = Date.parse(c.undo_point) + UNDO_HOURS * 3600 * 1000;
    return until > now ? { checkpoint: c, until } : null;
  }
  return null;
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

// The value of an <input type="datetime-local"> (local time, seconds) for a time in ms.
export function toLocalInput(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// The time in ms of a datetime-local value, or null.
export function fromLocalInput(value: string): number | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(value)) return null;
  const t = new Date(value).getTime();
  return Number.isNaN(t) ? null : t;
}

// The time of /restore?time=<RFC 3339 time> (the PITR offer of an update, docs/console-ui.md section 11.4) in ms,
// or null when the parameter is not there or is not an RFC 3339 time with a zone.
export function restoreOffer(raw: string | null | undefined): number | null {
  if (!raw || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2})$/.test(raw)) return null;
  const t = Date.parse(raw);
  return Number.isNaN(t) ? null : t;
}

// An ISO time without milliseconds, for the API.
export function isoSeconds(ms: number): string {
  return new Date(Math.floor(ms / 1000) * 1000).toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

// A destination id from a hint (a disk label, a host, a bucket): a-z, 0-9, and -, unique in `taken`.
export function destinationId(hint: string, taken: readonly string[]): string {
  const base =
    hint
      .toLowerCase()
      .replace(/^care-/, "")
      .replace(/\..*$/, "")
      .replace(/[^a-z0-9-]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 24) || "backup";
  let id = base;
  for (let n = 2; taken.includes(id); n++) id = `${base}-${n}`;
  return id;
}

export const DESTINATION_ID = /^[a-z0-9][a-z0-9-]{0,30}$/;
