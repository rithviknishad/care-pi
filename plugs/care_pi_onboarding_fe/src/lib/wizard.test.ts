import { describe, expect, it } from "vitest";

import {
  initialState,
  nextScreen,
  type Status,
  type StepName,
  type StepStatus,
  visibleSteps,
  wizardReducer,
} from "@/lib/wizard";

const FACILITY_STEPS: StepName[] = [
  "facility",
  "staff",
  "questionnaires",
  "catalog",
  "demo_data",
];

function step(name: StepName, patch: Partial<StepStatus> = {}): StepStatus {
  const scope = FACILITY_STEPS.includes(name) ? "facility" : "instance";
  return {
    step: name,
    title: name,
    description: "",
    optional: name === "catalog" || name === "demo_data",
    scope,
    allowed: scope === "instance" || name === "facility",
    state: "pending",
    runs: 0,
    applied_at: null,
    created: {},
    existing: {},
    detail: "",
    exists: {},
    ...patch,
  };
}

function freshStatus(patch: Partial<Status> = {}): Status {
  return {
    can_manage: true,
    is_superuser: true,
    facility: null,
    steps: [
      "organization",
      "facility",
      "roles",
      "staff",
      "valuesets",
      "questionnaires",
      "catalog",
      "demo_data",
    ].map((s) => step(s as StepName)),
    next_step: "organization",
    wizard: { closed: false, reason: "", redirect: true },
    ...patch,
  };
}

describe("wizardReducer", () => {
  it("opens the first pending step that the user may apply", () => {
    const state = wizardReducer(initialState, {
      type: "status_loaded",
      status: freshStatus(),
    });
    expect(state.phase).toBe("ready");
    expect(state.screen).toBe("organization");
    expect(state.facility).toBeNull();
  });

  it("denies a user who cannot manage the setup", () => {
    const state = wizardReducer(initialState, {
      type: "status_loaded",
      status: freshStatus({ can_manage: false, steps: [] }),
    });
    expect(state.phase).toBe("denied");
  });

  it("keeps the facility of the status", () => {
    const state = wizardReducer(initialState, {
      type: "status_loaded",
      status: freshStatus({ facility: { id: "f1", name: "Clinic" } }),
    });
    expect(state.facility).toBe("f1");
  });

  it("a facility result sets the facility and opens the facility steps", () => {
    let state = wizardReducer(initialState, {
      type: "status_loaded",
      status: freshStatus(),
    });
    state = wizardReducer(state, { type: "go", screen: "facility" });
    state = wizardReducer(state, { type: "request" });
    expect(state.busy).toBe(true);
    state = wizardReducer(state, {
      type: "result",
      result: {
        step: "facility",
        status: "done",
        created: { facility: 1 },
        existing: {},
        detail: "made",
        result: { facility: { id: "f9", name: "Kochi Clinic" } },
      },
    });
    expect(state.busy).toBe(false);
    expect(state.facility).toBe("f9");
    expect(state.status?.facility?.name).toBe("Kochi Clinic");
    const staff = state.status?.steps.find((s) => s.step === "staff");
    expect(staff?.allowed).toBe(true);
    const facility = state.status?.steps.find((s) => s.step === "facility");
    expect(facility?.state).toBe("done");
    expect(facility?.runs).toBe(1);
  });

  it("a failed result marks the step failed and keeps the screen", () => {
    let state = wizardReducer(initialState, {
      type: "status_loaded",
      status: freshStatus(),
    });
    state = wizardReducer(state, { type: "request" });
    state = wizardReducer(state, {
      type: "result",
      result: {
        step: "organization",
        status: "failed",
        created: {},
        existing: {},
        detail: "bad",
      },
    });
    expect(state.screen).toBe("organization");
    expect(state.status?.steps[0].state).toBe("failed");
    expect(state.results.organization?.detail).toBe("bad");
  });

  it("removing the demo data makes the step pending again", () => {
    const status = freshStatus({
      facility: { id: "f1", name: "C" },
      steps: freshStatus().steps.map((s) =>
        s.step === "demo_data"
          ? { ...s, allowed: true, state: "done", runs: 1 }
          : s,
      ),
    });
    let state = wizardReducer(initialState, { type: "status_loaded", status });
    state = wizardReducer(state, {
      type: "result",
      result: {
        step: "demo_data",
        status: "removed",
        created: {},
        existing: {},
        detail: "gone",
      },
    });
    const demo = state.status?.steps.find((s) => s.step === "demo_data");
    expect(demo?.state).toBe("pending");
    expect(demo?.runs).toBe(1);
  });

  it("ignores navigation while a request runs", () => {
    let state = wizardReducer(initialState, {
      type: "status_loaded",
      status: freshStatus(),
    });
    state = wizardReducer(state, { type: "request" });
    state = wizardReducer(state, { type: "go", screen: "roles" });
    expect(state.screen).toBe("organization");
    state = wizardReducer(state, { type: "next" });
    expect(state.screen).toBe("organization");
  });

  it("a request error clears busy and shows the error", () => {
    let state = wizardReducer(initialState, {
      type: "status_loaded",
      status: freshStatus(),
    });
    state = wizardReducer(state, { type: "request" });
    state = wizardReducer(state, {
      type: "request_failed",
      error: "Too many requests",
    });
    expect(state.busy).toBe(false);
    expect(state.error).toBe("Too many requests");
  });
});

describe("nextScreen", () => {
  it("skips done, skipped, and locked steps, then finishes", () => {
    const status = freshStatus({
      steps: freshStatus().steps.map((s) =>
        ["organization", "facility"].includes(s.step)
          ? { ...s, state: "done" }
          : s.step === "roles"
            ? { ...s, state: "skipped" }
            : s,
      ),
    });
    // staff is locked (no facility yet), so valuesets is next.
    expect(nextScreen(status, null)).toBe("valuesets");
    expect(nextScreen(status, "valuesets")).toBe("done");
  });

  it("returns a failed step again", () => {
    const status = freshStatus({
      steps: freshStatus().steps.map((s) =>
        s.step === "organization" ? { ...s, state: "failed" } : s,
      ),
    });
    expect(nextScreen(status, null)).toBe("organization");
  });
});

describe("visibleSteps", () => {
  it("hides the demo step when the backend turned it off", () => {
    const status = freshStatus({
      steps: freshStatus().steps.map((s) =>
        s.step === "demo_data" ? { ...s, available: false } : s,
      ),
    });
    expect(visibleSteps(status).map((s) => s.step)).not.toContain("demo_data");
  });

  it("hides the instance steps from a facility admin", () => {
    const status = freshStatus({
      is_superuser: false,
      facility: { id: "f1", name: "C" },
      steps: freshStatus().steps.map((s) => ({
        ...s,
        allowed: s.scope === "facility",
      })),
    });
    expect(visibleSteps(status).map((s) => s.step)).toEqual([
      "facility",
      "staff",
      "questionnaires",
      "catalog",
      "demo_data",
    ]);
  });
});
