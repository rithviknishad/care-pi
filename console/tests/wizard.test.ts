import { describe, expect, it } from "vitest";
import type { SetupState, SetupStepId } from "@/api/types";
import {
  blockingSteps,
  homeFor,
  isWizardPage,
  needsEnvSkip,
  nextPage,
  presetFromAnswers,
  previousPage,
  resumePage,
  wizardPages,
} from "@/features/setup/wizard";

const GIB = 1024 ** 3;

function setup(
  statuses: Partial<Record<SetupStepId, "done" | "current" | "pending" | "skipped">>,
  current: SetupStepId | null,
): SetupState {
  const ids: SetupStepId[] = [
    "account",
    "recovery-codes",
    "hardware",
    "identity",
    "tls",
    "size",
    "env",
    "backups",
    "care-admin",
  ];
  return {
    state: "setup",
    dev_build: true,
    current_step: current,
    steps: ids.map((id) => ({
      id,
      status: statuses[id] ?? "pending",
      required: id !== "env",
      skippable: id === "env" || id === "backups",
      phase: id === "backups" ? 3 : 2,
      updated_at: null,
      errors: [],
      warnings: [],
    })),
    draft: {} as SetupState["draft"],
    can_finish: false,
    job_id: null,
    claimed_at: null,
    ready_at: null,
  };
}

describe("presets from plain-language answers", () => {
  it("maps the larger answer to a preset", () => {
    expect(presetFromAnswers({ staff: 0, peak: 0 }, 4 * GIB)).toEqual({ preset: "solo", capped: false });
    expect(presetFromAnswers({ staff: 0, peak: 1 }, 4 * GIB)).toEqual({ preset: "small-clinic", capped: false });
    expect(presetFromAnswers({ staff: 2, peak: 1 }, 4 * GIB)).toEqual({ preset: "busy-counter", capped: false });
  });
  it("hides busy-counter on less than 3 GiB of RAM", () => {
    expect(presetFromAnswers({ staff: 2, peak: 2 }, 2 * GIB)).toEqual({ preset: "small-clinic", capped: true });
  });
});

describe("wizard pages", () => {
  it("skips env on the easy path only", () => {
    expect(wizardPages("easy")).not.toContain("env");
    expect(wizardPages("advanced")).toContain("env");
    expect(wizardPages("easy").at(-1)).toBe("review");
    expect(nextPage("easy", "size")).toBe("backups");
    expect(nextPage("advanced", "size")).toBe("env");
    expect(previousPage("easy", "backups")).toBe("size");
    expect(previousPage("easy", "recovery-codes")).toBeNull();
  });
  it("knows the page names", () => {
    expect(isWizardPage("review")).toBe(true);
    expect(isWizardPage("tls")).toBe(true);
    expect(isWizardPage("account")).toBe(false);
    expect(isWizardPage(undefined)).toBe(false);
  });
  it("resumes at the current step of the agent", () => {
    expect(resumePage(setup({ account: "done" }, "recovery-codes"), "easy")).toBe("recovery-codes");
    expect(resumePage(setup({}, null), "easy")).toBe("review");
    const atEnv = setup(
      { account: "done", "recovery-codes": "done", hardware: "done", identity: "done", tls: "done", size: "done" },
      "env",
    );
    expect(resumePage(atEnv, "advanced")).toBe("env");
    expect(resumePage(atEnv, "easy")).toBe("backups");
  });
  it("skips env before the finish on the easy path", () => {
    expect(needsEnvSkip(setup({ env: "pending" }, "env"), "easy")).toBe(true);
    expect(needsEnvSkip(setup({ env: "skipped" }, null), "easy")).toBe(false);
    expect(needsEnvSkip(setup({ env: "pending" }, "env"), "advanced")).toBe(false);
  });
  it("lists the steps that block the finish", () => {
    const s = setup(
      {
        account: "done",
        "recovery-codes": "done",
        hardware: "done",
        identity: "done",
        tls: "done",
        size: "done",
        backups: "skipped",
      },
      "care-admin",
    );
    expect(blockingSteps(s)).toEqual(["care-admin"]);
  });
});

describe("start page", () => {
  it("routes by the setup state", () => {
    expect(homeFor(null, false)).toBe("/claim");
    expect(homeFor(null, true)).toBe("/login");
    expect(homeFor("claimed", true)).toBe("/setup");
    expect(homeFor("applying", true)).toBe("/setup");
    expect(homeFor("ready", true)).toBe("/dashboard");
  });
});
