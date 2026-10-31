import { describe, expect, it } from "vitest";
import {
  describeWindow,
  dstNote,
  formatDays,
  formatOffset,
  formatZoned,
  hasDst,
  parseTime,
  sameWindow,
  secondsBetween,
  validateWindow,
  windowMinutes,
  zoneOffsetMinutes,
  zoneText,
} from "@/features/updates/window";

describe("the update window", () => {
  it("computes the length: same day, next day, and 24 hours", () => {
    expect(windowMinutes("02:00", "05:00")).toBe(180);
    expect(windowMinutes("23:00", "02:00")).toBe(180);
    expect(windowMinutes("02:00", "02:00")).toBe(1440);
    expect(windowMinutes("22:30", "00:15")).toBe(105);
    expect(windowMinutes("2:00", "05:00")).toBeNull();
    expect(parseTime("24:00")).toBeNull();
    expect(parseTime("23:59")).toBe(1439);
  });

  it("describes the window in plain words", () => {
    expect(describeWindow({ days: ["Sun"], start: "02:00", end: "05:00" })).toBe("Sun, 02:00 to 05:00 (3 h)");
    expect(describeWindow({ days: ["Sun", "Sat"], start: "23:00", end: "02:00" })).toBe(
      "Sat, Sun, 23:00 to 02:00 the next day (3 h)",
    );
    expect(describeWindow({ days: ["Wed"], start: "01:00", end: "01:00" })).toBe("Wed, from 01:00 for 24 h");
    expect(describeWindow({ days: ["Mon"], start: "22:30", end: "00:15" })).toBe(
      "Mon, 22:30 to 00:15 the next day (1 h 45 min)",
    );
    expect(formatDays(["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"])).toBe("Each day");
  });

  it("validates the editor values", () => {
    expect(validateWindow({ days: [], start: "02:00", end: "5" })).toEqual({
      days: "Select 1 day or more.",
      end: "Use the format HH:MM.",
    });
    expect(validateWindow({ days: ["Sun"], start: "02:00", end: "05:00" })).toEqual({});
    expect(
      sameWindow(
        { days: ["Sun", "Sat"], start: "02:00", end: "05:00" },
        { days: ["Sat", "Sun"], start: "02:00", end: "05:00" },
      ),
    ).toBe(true);
  });

  it("tells the time zone of the device with its offset", () => {
    const at = new Date("2026-11-22T20:30:00Z");
    expect(zoneOffsetMinutes("Asia/Kolkata", at)).toBe(330);
    expect(zoneOffsetMinutes("America/New_York", at)).toBe(-300);
    expect(zoneOffsetMinutes("Not/AZone", at)).toBeNull();
    expect(formatOffset(330)).toBe("UTC+05:30");
    expect(formatOffset(-300)).toBe("UTC-05:00");
    expect(formatOffset(0)).toBe("UTC");
    expect(zoneText("Asia/Kolkata", at)).toBe("Asia/Kolkata (UTC+05:30)");
    expect(zoneText("Not/AZone", at)).toBe("Not/AZone");
  });

  it("tells when the zone has daylight saving time (docs/api/README.md section 14.4)", () => {
    expect(hasDst("Asia/Kolkata", 2026)).toBe(false);
    expect(hasDst("Europe/Berlin", 2026)).toBe(true);
    expect(hasDst("UTC", 2026)).toBe(false);
    const at = new Date("2026-11-22T20:30:00Z");
    expect(dstNote("Asia/Kolkata", at)).toBe("Asia/Kolkata has no daylight saving time.");
    expect(dstNote("Europe/Berlin", at)).toMatch(
      /^Europe\/Berlin uses daylight saving time\. .*1 hour shorter or longer/,
    );
    expect(dstNote("Europe/Berlin", at)).toContain("skipped hour moves 1 hour later");
  });

  it("formats times in the zone of the device, not of the browser", () => {
    expect(formatZoned("2026-11-22T20:30:00Z", "Asia/Kolkata")).toBe("Mon 2026-11-23 02:00");
    expect(formatZoned("2026-11-22T20:30:00Z", "Asia/Kolkata", "time")).toBe("02:00");
    expect(formatZoned("2026-11-22T20:30:00Z", "Asia/Kolkata", "date")).toBe("Mon 2026-11-23");
    expect(formatZoned("2026-11-22T20:30:00Z", "UTC")).toBe("Sun 2026-11-22 20:30");
    expect(formatZoned("2026-11-22T20:30:00Z", "Not/AZone")).toBe("Sun 2026-11-22 20:30");
    expect(formatZoned(null, "UTC")).toBe("—");
    expect(formatZoned("not a time", "UTC")).toBe("—");
  });

  it("measures a job", () => {
    expect(secondsBetween("2026-11-22T02:03:00Z", "2026-11-22T02:09:31Z")).toBe(391);
    expect(secondsBetween(null, "2026-11-22T02:09:31Z")).toBeNull();
  });
});
