import { fmt } from "@/lib/strings";
import { v2Strings } from "./strings";

// The retention policy of the backups (S66, item 2): a copy each interval_min minutes, all copies of the last
// keep_within_days days, then 1 copy each day, each week, and each month.
export interface RetentionPolicy {
  interval_min: number;
  keep_within_days: number;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
}

export type RetentionZoneKind = "moment" | "day" | "week" | "month";

export interface RetentionZone {
  kind: RetentionZoneKind;
  /** The number of copies that the zone keeps (moment: an estimate from the interval). */
  count: number;
  /** "2 days", "2 weeks", "6 months". */
  span: string;
}

const S = v2Strings.explainer;

export function everyText(intervalMin: number): string {
  const n = Math.max(1, Math.round(intervalMin));
  if (n === 1) return S.every.minute;
  if (n < 60) return fmt(S.every.minutes, { n });
  if (n === 60) return S.every.hour;
  if (n === 1440) return S.every.day;
  if (n % 60 === 0 && n < 1440) return fmt(S.every.hours, { n: n / 60 });
  return fmt(S.every.minutes, { n });
}

export function spanOfDays(days: number): string {
  if (days === 1) return S.span.day;
  if (days === 7) return S.span.week;
  if (days >= 14 && days % 7 === 0) return fmt(S.span.weeks, { n: days / 7 });
  return fmt(S.span.days, { n: days });
}

export function spanOfWeeks(weeks: number): string {
  if (weeks === 1) return S.span.week;
  if (weeks === 4) return S.span.month;
  if (weeks >= 8 && weeks % 4 === 0) return fmt(S.span.months, { n: weeks / 4 });
  return fmt(S.span.weeks, { n: weeks });
}

export function spanOfMonths(months: number): string {
  if (months === 1) return S.span.month;
  if (months === 12) return S.span.year;
  if (months >= 24 && months % 12 === 0) return fmt(S.span.years, { n: months / 12 });
  return fmt(S.span.months, { n: months });
}

// The "moment" span: the last day is "24 hours" (clearer than "the last day").
function spanOfMoments(days: number): string {
  return days === 1 ? S.span.hours24 : spanOfDays(days);
}

// The zones from the newest to the oldest. A zone with 0 copies is not in the list.
export function retentionZones(p: RetentionPolicy): RetentionZone[] {
  const zones: RetentionZone[] = [];
  if (p.keep_within_days > 0) {
    const perDay = Math.floor(1440 / Math.max(1, p.interval_min));
    zones.push({ kind: "moment", count: perDay * p.keep_within_days, span: spanOfMoments(p.keep_within_days) });
  }
  if (p.keep_daily > 0) zones.push({ kind: "day", count: p.keep_daily, span: spanOfDays(p.keep_daily) });
  if (p.keep_weekly > 0) zones.push({ kind: "week", count: p.keep_weekly, span: spanOfWeeks(p.keep_weekly) });
  if (p.keep_monthly > 0) zones.push({ kind: "month", count: p.keep_monthly, span: spanOfMonths(p.keep_monthly) });
  return zones;
}

// The plain summary. Policy {15, 2, 14, 8, 6} gives:
// "Every 15 minutes we save a copy. You can go back to any moment in the last 2 days, to any day in the last
// 2 weeks, to any week in the last 2 months, and to any month in the last 6 months."
export function retentionSummary(p: RetentionPolicy): string {
  const first = fmt(S.save, { every: everyText(p.interval_min) });
  const clauses = retentionZones(p).map((z) => fmt(S.clause[z.kind], { span: z.span }));
  if (clauses.length === 0) return `${first} ${S.onlyNewest}`;
  const joined =
    clauses.length === 1
      ? clauses[0]
      : `${clauses.slice(0, -1).join(", ")}${clauses.length > 2 ? "," : ""} ${S.and} ${clauses[clauses.length - 1]}`;
  return `${first} ${fmt(S.canGoBack, { clauses: joined })}`;
}
