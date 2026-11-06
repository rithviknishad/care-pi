import { describe, expect, it } from "vitest";
import type { PublicStatus } from "@/api/types";
import { bannerJob } from "@/app/layout/maintenance";

const job = (step_index: number | null, step_count: number | null) =>
  ({
    maintenance: { active: false, kind: null, job_id: null, step: null, started_at: null },
    job: {
      id: "20261106T100000.000Z-update-apply",
      kind: "update-apply",
      state: "running",
      title: "Install an update",
      step: "migrate",
      step_index,
      step_count,
      started_at: "2026-11-06T10:00:00Z",
    },
  }) as unknown as PublicStatus;

describe("bannerJob", () => {
  it("names the step of the planned step count", () => {
    expect(bannerJob(job(9, 17))?.stepOf).toBe("step 10 of 17");
  });

  it("names only the step of a job without a plan (step_count null)", () => {
    expect(bannerJob(job(2, null))?.stepOf).toBe("step 3");
  });

  it("never names a step after the total, and nothing before the first step", () => {
    expect(bannerJob(job(5, 3))?.stepOf).toBe("step 6 of 6");
    expect(bannerJob(job(null, 17))?.stepOf).toBe("");
  });
});
