import { describe, expect, it } from "vitest";
import { everyText, retentionSummary, retentionZones, spanOfDays, spanOfMonths, spanOfWeeks } from "../retention";

describe("retention explainer text", () => {
  it("gives the plain sentence of the default policy (S66 item 2)", () => {
    expect(
      retentionSummary({ interval_min: 15, keep_within_days: 2, keep_daily: 14, keep_weekly: 8, keep_monthly: 6 }),
    ).toBe(
      "Every 15 minutes we save a copy. You can go back to any moment in the last 2 days, to any day in the last 2 weeks, " +
        "to any week in the last 2 months, and to any month in the last 6 months.",
    );
  });

  it("drops a zone with no copies and joins 2 clauses with 'and'", () => {
    expect(
      retentionSummary({ interval_min: 60, keep_within_days: 1, keep_daily: 7, keep_weekly: 0, keep_monthly: 0 }),
    ).toBe(
      "Every hour we save a copy. You can go back to any moment in the last 24 hours and to any day in the last week.",
    );
  });

  it("says when only the newest copy stays", () => {
    expect(
      retentionSummary({ interval_min: 1440, keep_within_days: 0, keep_daily: 0, keep_weekly: 0, keep_monthly: 0 }),
    ).toBe("Every day we save a copy. You can go back to the newest copy only.");
  });

  it("names the spans in plain units", () => {
    expect(everyText(1)).toBe("Every minute");
    expect(everyText(120)).toBe("Every 2 hours");
    expect(everyText(90)).toBe("Every 90 minutes");
    expect(spanOfDays(1)).toBe("day");
    expect(spanOfDays(10)).toBe("10 days");
    expect(spanOfDays(21)).toBe("3 weeks");
    expect(spanOfWeeks(4)).toBe("month");
    expect(spanOfWeeks(6)).toBe("6 weeks");
    expect(spanOfWeeks(12)).toBe("3 months");
    expect(spanOfMonths(12)).toBe("year");
    expect(spanOfMonths(24)).toBe("2 years");
    expect(spanOfMonths(18)).toBe("18 months");
  });

  it("lists the zones from the newest to the oldest with their counts", () => {
    const zones = retentionZones({
      interval_min: 15,
      keep_within_days: 2,
      keep_daily: 14,
      keep_weekly: 8,
      keep_monthly: 6,
    });
    expect(zones.map((z) => [z.kind, z.count, z.span])).toEqual([
      ["moment", 192, "2 days"],
      ["day", 14, "2 weeks"],
      ["week", 8, "2 months"],
      ["month", 6, "6 months"],
    ]);
  });
});
