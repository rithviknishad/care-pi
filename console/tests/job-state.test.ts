import { describe, expect, it } from "vitest";
import type { Job, JobStep } from "@/api/types";
import {
  MAX_JOB_LOGS,
  applyJobEvent,
  emptyJobView,
  expectedSeconds,
  jobKindFromId,
  jobStartedAt,
  stepProgress,
  type JobEvent,
} from "@/features/jobs/job-state";

function step(index: number, state: JobStep["state"]): JobStep {
  return {
    index,
    name: `step ${index}`,
    action: "write-files",
    state,
    started_at: null,
    finished_at: null,
    detail: null,
  };
}

function run(events: JobEvent[]) {
  return events.reduce(applyJobEvent, emptyJobView());
}

describe("job event reducer", () => {
  it("appends new steps and changes a known step in place", () => {
    const v = run([
      { id: 1, event: "step", data: step(0, "running") },
      { id: 2, event: "step", data: step(0, "succeeded") },
      { id: 3, event: "step", data: step(1, "running") },
    ]);
    expect(v.steps.map((s) => [s.index, s.state])).toEqual([
      [0, "succeeded"],
      [1, "running"],
    ]);
    expect(v.lastEventId).toBe(3);
  });

  it("ignores a repeated event after a reconnect and the heartbeat", () => {
    const v = run([
      { id: 1, event: "step", data: step(0, "running") },
      { id: 2, event: "log", data: { time: "t", step_index: 0, stream: "agent", line: "a" } },
      { id: 2, event: "log", data: { time: "t", step_index: 0, stream: "agent", line: "a" } },
      { id: null, event: "heartbeat", data: { time: "t" } },
    ]);
    expect(v.logs).toHaveLength(1);
    expect(v.lastEventId).toBe(2);
  });

  it("keeps the newest log lines only", () => {
    const events: JobEvent[] = Array.from({ length: MAX_JOB_LOGS + 10 }, (_, i) => ({
      id: i + 1,
      event: "log",
      data: { time: "t", step_index: null, stream: "stdout", line: `line ${i}` },
    }));
    const v = run(events);
    expect(v.logs).toHaveLength(MAX_JOB_LOGS);
    expect(v.droppedLogs).toBe(10);
    expect(v.logs[0]?.line).toBe("line 10");
  });

  it("stores progress and the final job", () => {
    const job = {
      id: "j",
      kind: "setup",
      state: "succeeded",
      steps: [step(0, "succeeded"), step(1, "succeeded")],
    } as unknown as Job;
    const v = run([
      { id: 1, event: "step", data: step(0, "running") },
      { id: 2, event: "progress", data: { step_index: 0, current: 50, total: 100, unit: "percent", message: null } },
      { id: 3, event: "done", data: job },
    ]);
    expect(stepProgress(v.progress[0])).toBe(50);
    expect(v.done).toBe(true);
    expect(v.job?.state).toBe("succeeded");
    expect(v.steps).toHaveLength(2);
  });
});

describe("expected durations", () => {
  it("gives the first start of Care in a setup job the long duration", () => {
    // Lumine, wave 4c: the full cycle of the setup job took 219 s, and the console said "about 45 s".
    expect(expectedSeconds("full-cycle", "setup")).toBe(220);
    expect(expectedSeconds("full-cycle", "apply")).toBe(45);
    expect(expectedSeconds("full-cycle")).toBe(45);
    expect(expectedSeconds("ensure-fe", "setup")).toBe(180);
    expect(expectedSeconds("write-files", "setup")).toBeUndefined();
    expect(expectedSeconds(null, "setup")).toBeUndefined();
  });

  it("reads the job kind from the job id", () => {
    expect(jobKindFromId("20261030T163803.767Z-setup")).toBe("setup");
    expect(jobKindFromId("20261030T101500.123Z-fe-build")).toBe("fe-build");
    expect(jobKindFromId("nodash")).toBeUndefined();
  });
});

describe("job start time", () => {
  it("uses the first step until the job object arrives", () => {
    const running = run([
      { id: 1, event: "step", data: { ...step(0, "running"), started_at: "2026-10-30T17:24:31.406Z" } },
    ]);
    expect(running.job).toBeNull();
    expect(jobStartedAt(running)).toBe("2026-10-30T17:24:31.406Z");
    expect(jobStartedAt(emptyJobView())).toBeNull();
    const job = { started_at: "2026-10-30T17:24:31.405Z", created_at: "2026-10-30T17:24:31.405Z" } as Job;
    expect(jobStartedAt({ ...running, job })).toBe("2026-10-30T17:24:31.405Z");
  });
});
