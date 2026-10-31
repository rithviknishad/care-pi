// The update window (docs/solutions.md section 10.1, docs/api/README.md section 14.4): pure helpers for the text
// of the window and for times in the time zone of the device. The window uses the time zone of the host, not the
// time zone of the browser, so each time on the Updates page shows in the zone of the device.

import type { UpdateWindowState, WindowDay } from "@/api/types";
import { formatDuration } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";

export const DAYS: WindowDay[] = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const DAY_MINUTES = 24 * 60;
const TIME = /^([01][0-9]|2[0-3]):([0-5][0-9])$/;

export interface WindowValue {
  days: WindowDay[];
  start: string;
  end: string;
}

export function parseTime(hhmm: string): number | null {
  const m = TIME.exec(hhmm);
  return m ? Number(m[1]) * 60 + Number(m[2]) : null;
}

// The length of the window in minutes: end after start is the same day, end before start is the next day,
// end equal to start is 24 hours.
export function windowMinutes(start: string, end: string): number | null {
  const s = parseTime(start);
  const e = parseTime(end);
  if (s === null || e === null) return null;
  if (e > s) return e - s;
  if (e < s) return DAY_MINUTES - s + e;
  return DAY_MINUTES;
}

export function formatLength(minutes: number): string {
  if (minutes >= DAY_MINUTES) return "24 h";
  return formatDuration(minutes * 60);
}

// The days in the order of the week. All 7 days: "Each day".
export function formatDays(days: readonly string[]): string {
  const sorted = DAYS.filter((d) => days.includes(d));
  if (sorted.length === DAYS.length) return strings.updates.eachDay;
  return sorted.join(", ");
}

// "Sun, 02:00 to 05:00 (3 h)", "Sat, 23:00 to 02:00 the next day (3 h)", "Sun, from 02:00 for 24 h".
export function describeWindow(w: Pick<WindowValue, "days" | "start" | "end">): string {
  const length = windowMinutes(w.start, w.end);
  const days = formatDays(w.days);
  if (length === null) return `${days}, ${w.start}–${w.end}`;
  const s = parseTime(w.start) ?? 0;
  const e = parseTime(w.end) ?? 0;
  if (length === DAY_MINUTES) return fmt(strings.updates.windowFullDay, { days, start: w.start });
  const template = e > s ? strings.updates.windowSameDay : strings.updates.windowNextDay;
  return fmt(template, { days, start: w.start, end: w.end, length: formatLength(length) });
}

export interface WindowErrors {
  days?: string;
  start?: string;
  end?: string;
}

export function validateWindow(w: WindowValue): WindowErrors {
  const errors: WindowErrors = {};
  if (w.days.length === 0) errors.days = strings.updates.noDays;
  if (parseTime(w.start) === null) errors.start = strings.updates.badTime;
  if (parseTime(w.end) === null) errors.end = strings.updates.badTime;
  return errors;
}

export function windowValue(w: Pick<UpdateWindowState, "days" | "start" | "end">): WindowValue {
  return { days: DAYS.filter((d) => w.days.includes(d)), start: w.start, end: w.end };
}

export function sameWindow(a: WindowValue, b: WindowValue): boolean {
  return a.start === b.start && a.end === b.end && formatDays(a.days) === formatDays(b.days);
}

interface ZonedParts {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  second: number;
  weekday: string;
}

function zonedParts(date: Date, timeZone: string): ZonedParts | null {
  try {
    const parts = new Intl.DateTimeFormat("en-US", {
      timeZone,
      hourCycle: "h23",
      weekday: "short",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    }).formatToParts(date);
    const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "";
    return {
      year: Number(get("year")),
      month: Number(get("month")),
      day: Number(get("day")),
      hour: Number(get("hour")) % 24,
      minute: Number(get("minute")),
      second: Number(get("second")),
      weekday: get("weekday"),
    };
  } catch {
    return null;
  }
}

// The offset of the zone from UTC in minutes at a time (+330 for Asia/Kolkata). null for an unknown zone.
export function zoneOffsetMinutes(timeZone: string, at: Date): number | null {
  const p = zonedParts(at, timeZone);
  if (!p) return null;
  const asUtc = Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second);
  const whole = Math.floor(at.getTime() / 1000) * 1000;
  return Math.round((asUtc - whole) / 60000);
}

// +330 -> "UTC+05:30", -300 -> "UTC-05:00", 0 -> "UTC".
export function formatOffset(minutes: number): string {
  if (minutes === 0) return "UTC";
  const sign = minutes > 0 ? "+" : "-";
  const abs = Math.abs(minutes);
  return `UTC${sign}${String(Math.floor(abs / 60)).padStart(2, "0")}:${String(abs % 60).padStart(2, "0")}`;
}

// The zone has daylight saving time when its offset in January differs from its offset in July.
export function hasDst(timeZone: string, year: number): boolean {
  const jan = zoneOffsetMinutes(timeZone, new Date(Date.UTC(year, 0, 1, 12)));
  const jul = zoneOffsetMinutes(timeZone, new Date(Date.UTC(year, 6, 1, 12)));
  return jan !== null && jul !== null && jan !== jul;
}

// "Asia/Kolkata (UTC+05:30)".
export function zoneText(timeZone: string, at: Date): string {
  const offset = zoneOffsetMinutes(timeZone, at);
  if (offset === null) return timeZone;
  return fmt(strings.updates.timezoneText, { zone: timeZone, offset: formatOffset(offset) });
}

// The note about daylight saving time for the window editor (docs/api/README.md section 14.4).
export function dstNote(timeZone: string, at: Date): string {
  return fmt(hasDst(timeZone, at.getUTCFullYear()) ? strings.updates.dst : strings.updates.noDst, {
    zone: timeZone,
  });
}

const pad = (n: number) => String(n).padStart(2, "0");

// A time in the zone of the device: "Sun 2026-11-22 02:00", "02:00" (time), or "Sun 2026-11-22" (date).
// An unknown zone uses UTC.
export function formatZoned(
  iso: string | null | undefined,
  timeZone: string | null | undefined,
  style: "full" | "time" | "date" = "full",
): string {
  if (!iso) return "—";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  const p = zonedParts(date, timeZone || "UTC") ?? zonedParts(date, "UTC");
  if (!p) return "—";
  const time = `${pad(p.hour)}:${pad(p.minute)}`;
  if (style === "time") return time;
  const day = `${p.year}-${pad(p.month)}-${pad(p.day)}`;
  if (style === "date") return `${p.weekday} ${day}`;
  return `${p.weekday} ${day} ${time}`;
}

// The time from start to end in seconds, or null.
export function secondsBetween(start: string | null | undefined, end: string | null | undefined): number | null {
  if (!start || !end) return null;
  const s = Date.parse(start);
  const e = Date.parse(end);
  if (Number.isNaN(s) || Number.isNaN(e)) return null;
  return Math.max(0, Math.round((e - s) / 1000));
}
