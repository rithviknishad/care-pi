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
  /** "2 days", "2 weeks", "6 months", "year": the form of "in the last {span}". */
  span: string;
  /** The span with its number for one unit too: "1 year", "6 months" (the axis label "1 year ago", the legend). */
  length: string;
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

// counted: the form with the number for one unit too ("1 year"), else the form of a sentence ("year").
export function spanOfDays(days: number, counted = false): string {
  if (days === 1) return counted ? S.span.oneDay : S.span.day;
  if (days === 7) return counted ? S.span.oneWeek : S.span.week;
  if (days >= 14 && days % 7 === 0) return fmt(S.span.weeks, { n: days / 7 });
  return fmt(S.span.days, { n: days });
}

export function spanOfWeeks(weeks: number, counted = false): string {
  if (weeks === 1) return counted ? S.span.oneWeek : S.span.week;
  if (weeks === 4) return counted ? S.span.oneMonth : S.span.month;
  if (weeks >= 8 && weeks % 4 === 0) return fmt(S.span.months, { n: weeks / 4 });
  return fmt(S.span.weeks, { n: weeks });
}

export function spanOfMonths(months: number, counted = false): string {
  if (months === 1) return counted ? S.span.oneMonth : S.span.month;
  if (months === 12) return counted ? S.span.oneYear : S.span.year;
  if (months >= 24 && months % 12 === 0) return fmt(S.span.years, { n: months / 12 });
  return fmt(S.span.months, { n: months });
}

// The "moment" span: the last day is "24 hours" (clearer than "the last day").
function spanOfMoments(days: number, counted = false): string {
  return days === 1 ? S.span.hours24 : spanOfDays(days, counted);
}

// The zones from the newest to the oldest. A zone with 0 copies is not in the list.
export function retentionZones(p: RetentionPolicy): RetentionZone[] {
  const zones: RetentionZone[] = [];
  const zone = (kind: RetentionZoneKind, count: number, n: number, span: (n: number, counted?: boolean) => string) =>
    zones.push({ kind, count, span: span(n), length: span(n, true) });
  if (p.keep_within_days > 0) {
    const perDay = Math.floor(1440 / Math.max(1, p.interval_min));
    zone("moment", perDay * p.keep_within_days, p.keep_within_days, spanOfMoments);
  }
  if (p.keep_daily > 0) zone("day", p.keep_daily, p.keep_daily, spanOfDays);
  if (p.keep_weekly > 0) zone("week", p.keep_weekly, p.keep_weekly, spanOfWeeks);
  if (p.keep_monthly > 0) zone("month", p.keep_monthly, p.keep_monthly, spanOfMonths);
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
