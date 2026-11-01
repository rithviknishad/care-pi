import { describe, expect, it } from "vitest";
import type { BackupBanner, Checkpoint, SnapshotInfo } from "@/api/types";
import {
  calendarText,
  destinationId,
  formatLocalRetention,
  formatRetention,
  formatRpo,
  fromLocalInput,
  intervalText,
  isoSeconds,
  nearestSnapshot,
  restoreOffer,
  rpoMinutes,
  toLocalInput,
  undoCheckpoint,
} from "@/features/backups/backups-format";
import { destinationPlace, nextDiskLabel, onRootDisk } from "@/features/backups/destination-meta";
import {
  bannerAction,
  bannerText,
  destinationBadge,
  healthSummary,
  lastOffsiteCopy,
  localBadge,
  severityVariant,
} from "@/features/backups/health";

describe("retention and RPO", () => {
  it("shows the defaults of the agent, and a changed value", () => {
    expect(formatRetention(undefined)).toBe(
      "All copies of the last 14 days, then 14 daily, 8 weekly, and 6 monthly copies.",
    );
    expect(formatRetention({ keep_daily: 30, keep_monthly: 12 })).toBe(
      "All copies of the last 14 days, then 30 daily, 8 weekly, and 12 monthly copies.",
    );
    expect(formatLocalRetention(2)).toBe("2 full backups with their changes (about 2 weeks of points in time)");
  });

  it("adds the archive timeout to the interval of a destination", () => {
    expect(rpoMinutes(15, 5)).toBe(20);
    expect(rpoMinutes(undefined, undefined)).toBe(20);
    expect(rpoMinutes(1440, 5)).toBe(1445);
    expect(formatRpo(20)).toBe("20 min");
    expect(formatRpo(65)).toBe("1 h 5 min");
    expect(formatRpo(null)).toBe("—");
    expect(intervalText(15)).toBe("Each 15 min");
    expect(intervalText(60)).toBe("Each 1 h");
  });

  it("shows the systemd calendar of the schedule in plain words", () => {
    expect(calendarText("Sun 01:00")).toBe("Each Sunday at 01:00");
    expect(calendarText("Sat 03:00:00")).toBe("Each Saturday at 03:00");
    expect(calendarText("daily 02:30")).toBe("Each day at 02:30");
    expect(calendarText("*-*-* 02:00")).toBe("Each day at 02:00");
    expect(calendarText("*-*-01 04:00")).toBe("Day 1 of each month at 04:00");
    expect(calendarText("Mon..Fri 22:00")).toBe("Mon..Fri 22:00");
    expect(calendarText(null)).toBe("—");
  });
});

describe("restore helpers", () => {
  const snap = (time: string, contents: SnapshotInfo["contents"]): SnapshotInfo => ({
    destination: "usb",
    snapshot_id: "4f1e9a2c",
    time,
    contents,
    size_bytes: 1,
    hostname: "care",
    key_id: "1a2b3c4d",
  });

  it("finds the files snapshot nearest to a time", () => {
    const list = [
      snap("2026-10-30T10:45:00Z", ["db", "config"]),
      snap("2026-10-30T02:30:00Z", ["files", "config"]),
      snap("2026-10-29T02:30:00Z", ["files", "config"]),
    ];
    const n = nearestSnapshot(list, Date.parse("2026-10-30T09:40:00Z"), "files");
    expect(n?.snapshot.time).toBe("2026-10-30T02:30:00Z");
    expect(n?.diffS).toBe(7 * 3600 + 10 * 60);
    expect(nearestSnapshot([list[0] as SnapshotInfo], 0, "files")).toBeNull();
  });

  it("offers the undo of a restore for 24 hours", () => {
    const cp = (undo: string, kind: Checkpoint["job_kind"] = "restore-pitr"): Checkpoint => ({
      label: "20261026-010002F_20261030-104705I",
      undo_point: undo,
      timeline: 1,
      job_id: "20261030T104700.000Z-restore-pitr",
      job_kind: kind,
      reason: null,
    });
    const now = Date.parse("2026-10-30T12:00:00Z");
    expect(undoCheckpoint([cp("2026-10-30T10:47:10Z")], now)?.until).toBe(Date.parse("2026-10-31T10:47:10Z"));
    expect(undoCheckpoint([cp("2026-10-29T10:00:00Z")], now)).toBeNull();
    expect(undoCheckpoint([cp("2026-10-30T10:47:10Z", null)], now)).toBeNull();
    expect(undoCheckpoint([], now)).toBeNull();
  });

  it("converts the local time of the time field", () => {
    const ms = Date.parse("2026-10-30T09:40:12Z");
    expect(fromLocalInput(toLocalInput(ms))).toBe(ms);
    expect(fromLocalInput("2026-10-30T09:40")).not.toBeNull();
    expect(fromLocalInput("not a time")).toBeNull();
    expect(isoSeconds(Date.parse("2026-10-30T09:40:12.345Z"))).toBe("2026-10-30T09:40:12Z");
  });
});

describe("destinations", () => {
  it("makes a unique id from a hint", () => {
    expect(destinationId("care-usb1", [])).toBe("usb1");
    expect(destinationId("nas.lan", [])).toBe("nas");
    expect(destinationId("nas", ["nas", "nas-2"])).toBe("nas-3");
    expect(destinationId("Sunrise Care!", [])).toBe("sunrise-care");
    expect(destinationId("", [])).toBe("backup");
  });

  it("proposes the next free disk label", () => {
    expect(nextDiskLabel([])).toBe("care-usb1");
    expect(nextDiskLabel(["care-usb1", null, "care-usb2"])).toBe("care-usb3");
  });

  it("shows the place of a destination in one line", () => {
    expect(destinationPlace({ id: "usb", type: "posix", disk_label: "care-usb1" })).toBe("care-usb1");
    expect(destinationPlace({ id: "nas", type: "sftp", host: "nas.lan", user: "care", path: "/volume1/care" })).toBe(
      "care@nas.lan:/volume1/care",
    );
    expect(
      destinationPlace({ id: "b2", type: "s3", endpoint: "https://s3.example.com", bucket: "clinic", prefix: "pi" }),
    ).toBe("clinic/pi (s3.example.com)");
  });

  it("finds the root-disk warning in a test result", () => {
    const check = (ok: boolean) => ({ name: "root-disk", level: "advised" as const, ok, detail: "" });
    const result = (ok: boolean) => ({ checks: [check(ok)] }) as unknown as Parameters<typeof onRootDisk>[0];
    expect(onRootDisk(result(false))).toBe(true);
    expect(onRootDisk(result(true))).toBe(false);
    expect(onRootDisk(null)).toBe(false);
  });
});

describe("health", () => {
  const banner = (code: string, severity: BackupBanner["severity"], extra: Partial<BackupBanner> = {}): BackupBanner =>
    ({ code, severity, destination_id: null, since: null, message: `agent text of ${code}`, ...extra }) as BackupBanner;

  it("maps each severity to an alert variant", () => {
    expect(severityVariant("error")).toBe("destructive");
    expect(severityVariant("warning")).toBe("warning");
    expect(severityVariant("info")).toBe("info");
    expect(severityVariant("ok")).toBe("success");
  });

  it("puts the worst banner first and counts the others", () => {
    const s = healthSummary({
      state: "error",
      banners: [
        banner("backups-paused", "warning"),
        banner("local-repository-legacy-key", "info"),
        banner("no-destination", "error"),
      ],
      generated_at: "2026-10-30T10:00:00Z",
    });
    expect(s?.top?.code).toBe("no-destination");
    expect(s?.more).toBe(2);
    expect(s?.banners.map((b) => b.code)).toEqual(["no-destination", "backups-paused", "local-repository-legacy-key"]);
    expect(healthSummary({ state: "ok", banners: [], generated_at: "" })).toMatchObject({ top: null, more: 0 });
    expect(healthSummary(null)).toBeNull();
  });

  it("gives the console text of each banner, and the agent text of an unknown code", () => {
    expect(bannerText(banner("no-destination", "error"))).toBe(
      "No backup destination. The backups stay on this device only.",
    );
    expect(bannerText(banner("destination-unreachable", "error", { destination_id: "b2" }))).toBe(
      "b2: the destination does not answer.",
    );
    expect(
      bannerText(banner("destination-unreachable", "error", { destination_id: "b2", since: "2026-10-29T08:45:00Z" })),
    ).toMatch(/^b2: no successful backup for \d+ d( \d+ h)?\.$/);
    expect(
      bannerText(
        banner("destination-unreachable", "error", { destination_id: "b2", since: "2026-10-29T08:45:00Z" }),
        Date.parse("2026-10-30T10:45:00Z"),
      ),
    ).toBe("b2: no successful backup for 1 d 2 h.");
    expect(bannerText(banner("a-later-code", "warning"))).toBe("agent text of a-later-code");
  });

  it("links each banner to the place that fixes it", () => {
    expect(bannerAction(banner("no-destination", "error"))).toEqual({
      to: "/backups?add=1",
      label: "Add a destination",
    });
    expect(bannerAction(banner("recovery-key-unconfirmed", "warning"))?.to).toBe("/backups#recovery-key");
    expect(bannerAction(banner("destination-unreachable", "error", { destination_id: "nas" }))).toEqual({
      to: "/backups#destination-nas",
      label: "Show the destination",
    });
    expect(bannerAction(banner("verify-failed", "error"))).toEqual({ to: "/logs?unit=backup", label: "Show the log" });
    expect(bannerAction(banner("backups-paused", "warning"))?.to).toBe("/settings?tab=backups");
    expect(bannerAction(banner("a-later-code", "warning"))).toBeNull();
  });

  it("maps the states of the destinations and of the local repository to badges", () => {
    expect(destinationBadge("ok")).toEqual({ text: "OK", variant: "success" });
    expect(destinationBadge("degraded")).toEqual({ text: "Degraded", variant: "warning" });
    expect(destinationBadge("error")).toEqual({ text: "Unreachable", variant: "destructive" });
    expect(destinationBadge("pending").variant).toBe("info");
    expect(destinationBadge("later" as "ok")).toEqual({ text: "later", variant: "neutral" });
    expect(localBadge("none")).toEqual({ text: "No backup yet", variant: "info" });
    expect(localBadge("error").variant).toBe("destructive");
  });

  it("finds the newest copy off the device", () => {
    const d = (t: string | null) => ({ last_db_snapshot_at: t }) as never;
    expect(lastOffsiteCopy({ destinations: [d("2026-10-30T10:00:00Z"), d(null), d("2026-10-30T10:45:00Z")] })).toBe(
      "2026-10-30T10:45:00Z",
    );
    expect(lastOffsiteCopy({ destinations: [] })).toBeNull();
    expect(lastOffsiteCopy(undefined)).toBeNull();
  });
});

describe("the PITR offer of an update", () => {
  it("reads /restore?time=<RFC 3339 time>", () => {
    expect(restoreOffer("2026-11-22T02:05:40Z")).toBe(Date.UTC(2026, 10, 22, 2, 5, 40));
    expect(restoreOffer("2026-11-22T07:35:40+05:30")).toBe(Date.UTC(2026, 10, 22, 2, 5, 40));
    expect(restoreOffer("2026-11-22T02:05:40.500Z")).toBe(Date.UTC(2026, 10, 22, 2, 5, 40, 500));
  });

  it("ignores a missing or a wrong time", () => {
    expect(restoreOffer(null)).toBeNull();
    expect(restoreOffer("")).toBeNull();
    expect(restoreOffer("2026-11-22")).toBeNull();
    expect(restoreOffer("2026-11-22T02:05:40")).toBeNull();
    expect(restoreOffer("tomorrow")).toBeNull();
    expect(restoreOffer("2026-13-45T99:99:99Z")).toBeNull();
  });
});
