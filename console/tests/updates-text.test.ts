import { describe, expect, it } from "vitest";
import type { UpdatesOverview, UpdatesSummary } from "@/api/types";
import { bundleBadge, isAutomatic, kindBadge, planEffect, planTitle, sourceBadge } from "@/features/plugs/plug-text";
import {
  blockerText,
  compareVersions,
  historyVersions,
  offerKey,
  outcomeBadge,
  pitrOfferOfJob,
  previousText,
  restoreLink,
  summaryText,
  trackBadge,
  updateStatus,
  versionText,
} from "@/features/updates/update-text";

const OFFER = {
  available: true,
  reason: null,
  checkpoint_at: "2026-11-22T02:05:12Z",
  target_time: "2026-11-22T02:05:40Z",
  restore_point: "care-pi-20261122T020300.000Z-update-apply",
  restore: { kind: "pitr" as const, time: "2026-11-22T02:05:40Z" },
};

type StatusInput = Parameters<typeof updateStatus>[0];
const base: StatusInput = {
  state: "idle",
  blocked_by: null,
  update_available: false,
  target: null,
  track: "stable",
  available: [],
};
const target = { staged: false } as UpdatesOverview["target"];

describe("the texts of the Updates page", () => {
  it("gives each track a badge; custom is not supported", () => {
    expect(trackBadge("stable")).toEqual({ text: "Stable", variant: "success", unsupported: false });
    expect(trackBadge("pinned")).toEqual({ text: "Pinned", variant: "info", unsupported: false });
    expect(trackBadge("custom")).toEqual({ text: "Custom (Not supported)", variant: "warning", unsupported: true });
    expect(trackBadge(undefined).text).toBe("—");
  });

  it("names the running version", () => {
    expect(versionText({ version: "2026.11.0", source: "lock" })).toBe("2026.11.0");
    expect(versionText({ version: null, source: "solution-refs" })).toBe("No release");
    expect(versionText({ version: null, source: "custom" })).toBe("Custom versions");
    expect(versionText(null)).toBe("—");
  });

  it("gives each result of the history a badge, with the rollback marked", () => {
    expect(outcomeBadge("succeeded")).toEqual({ text: "Succeeded", variant: "success" });
    expect(outcomeBadge("rolled-back")).toEqual({ text: "Rolled back", variant: "warning" });
    expect(outcomeBadge("failed").variant).toBe("destructive");
    expect(outcomeBadge("rollback-failed")).toEqual({ text: "Rollback failed", variant: "destructive" });
    expect(historyVersions({ from_version: "2026.11.0", to_version: "2026.11.1" })).toBe("2026.11.0 → 2026.11.1");
    expect(historyVersions({ from_version: null, to_version: "2026.11.0" })).toBe("No release → 2026.11.0");
  });

  it("tells the state of the update machine and what the operator can do", () => {
    expect(updateStatus(base).text).toBe("Up to date");
    expect(updateStatus({ ...base, update_available: true, target }).text).toBe("Available. Not downloaded.");
    expect(updateStatus({ ...base, update_available: true, target: { ...target!, staged: true } })).toEqual({
      text: "Downloaded. Ready to install.",
      variant: "success",
    });
    expect(updateStatus({ ...base, state: "applying" }).text).toBe("Installing the update");
    expect(updateStatus({ ...base, state: "failed" }).variant).toBe("destructive");
    expect(
      updateStatus({ ...base, blocked_by: { code: "agent-too-old", detail: "", min_agent_version: "v0.5.0" } }).text,
    ).toBe("Blocked: this version needs agent v0.5.0 or newer");
    expect(updateStatus({ ...base, track: "pinned", available: [{} as never] }).text).toBe(
      "Pinned. Newer versions exist.",
    );
    expect(blockerText({ code: "index-unavailable", min_agent_version: null })).toBe(
      "the device cannot download the list of versions",
    );
  });

  it("gives the dashboard one line for the update", () => {
    const sum = {
      track: "stable",
      update_available: true,
      target_version: "2026.11.1",
      latest_version: "2026.11.1",
      staged_version: null,
      blocked_by: null,
    } as UpdatesSummary;
    expect(summaryText(sum)).toEqual({ text: "2026.11.1 is available", variant: "primary", link: true });
    expect(summaryText({ ...sum, staged_version: "2026.11.1" }).text).toBe("2026.11.1 is ready to install");
    expect(summaryText({ ...sum, update_available: false, blocked_by: "agent-too-old" }).text).toBe(
      "2026.11.1 is blocked: the agent is too old",
    );
    expect(summaryText({ ...sum, update_available: false })).toEqual({
      text: "Up to date",
      variant: "neutral",
      link: false,
    });
  });

  it("compares lock versions as numbers", () => {
    expect(compareVersions("2026.11.10", "2026.11.9")).toBeGreaterThan(0);
    expect(compareVersions("2026.9.0", "2026.11.0")).toBeLessThan(0);
    expect(compareVersions("2026.11.1", "2026.11.1")).toBe(0);
  });

  it("finds the PITR offer of a job and links it to the Restore page", () => {
    expect(pitrOfferOfJob({ result: { pitr_offer: OFFER } as never, error: null })).toBe(OFFER);
    expect(pitrOfferOfJob({ result: null, error: { pitr_offer: OFFER } as never })).toBe(OFFER);
    expect(pitrOfferOfJob({ result: { kind: "update-check" } as never, error: null })).toBeNull();
    expect(pitrOfferOfJob(null)).toBeNull();
    expect(restoreLink(OFFER)).toBe("/restore?time=2026-11-22T02%3A05%3A40Z");
    expect(offerKey(OFFER)).toBe("care-pi-20261122T020300.000Z-update-apply");
  });

  it("names the previous version of a rollback", () => {
    const r = { available: true, reason: null, images_present: true, pitr_offer: null };
    expect(previousText({ rollback: { ...r, to_lock: "20261115T020741Z", to_version: "2026.11.0" } }, "UTC")).toBe(
      "2026.11.0",
    );
    expect(previousText({ rollback: { ...r, to_lock: "20261108T120000Z", to_version: null } }, "Asia/Kolkata")).toBe(
      "Lock of Sun 2026-11-08 17:30",
    );
    expect(previousText({ rollback: { ...r, available: false, to_lock: null, to_version: null } }, "UTC")).toBe("None");
  });
});

describe("the texts of the plugs", () => {
  it("gives badges for the kind, the source, and the bundle", () => {
    expect(kindBadge("frontend").text).toBe("Frontend");
    expect(kindBadge("both").text).toBe("Backend and frontend");
    expect(sourceBadge("solution").text).toBe("Solution default");
    expect(sourceBadge("operator")).toEqual({ text: "Your choice", variant: "primary" });
    expect(bundleBadge("present")).toEqual({ text: "On the device", variant: "success" });
    expect(bundleBadge("external")).toEqual({ text: "External", variant: "warning" });
    expect(bundleBadge("missing").variant).toBe("neutral");
    expect(isAutomatic({ auto: "when_multihost" })).toBe(true);
    expect(isAutomatic({ auto: null })).toBe(false);
  });

  it("tells the effect of a change before it runs (docs/api/README.md section 14.7)", () => {
    expect(planTitle({ enabled: true }, "Filly")).toBe("Turn on Filly?");
    expect(planTitle({ enabled: false }, "Filly")).toBe("Turn off Filly?");
    expect(planTitle({ enabled: null }, "Filly")).toBe("Use the solution default for Filly?");
    expect(planTitle({ settings: {} }, "Filly")).toBe("Change the settings of Filly?");
    expect(planEffect({ path: "plug-config", estimated_downtime_s: 0 })).toBe(
      "This changes the frontend plug list. Care keeps running. Browsers use the change at the next page load.",
    );
    expect(planEffect({ path: "restart", estimated_downtime_s: 20 })).toBe(
      "This restarts the Care services (api, worker, beat). The API stops for about 20 s.",
    );
    expect(planEffect({ path: "cutover", estimated_downtime_s: 120 })).toContain("Care stops for about 2 min.");
    expect(planEffect({ path: "cutover", estimated_downtime_s: null })).toContain("about 1 min");
  });
});
