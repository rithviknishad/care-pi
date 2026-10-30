import type { PresetName, SetupState, SetupStepId, SetupStateName } from "@/api/types";

export type SetupMode = "easy" | "advanced";
export type WizardPage = Exclude<SetupStepId, "account"> | "review";

// The order of the steps (docs/api/README.md section 5). The claim does the step account.
export const STEP_ORDER: SetupStepId[] = [
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

// The pages of the wizard for a path. The easy path skips env (README section 5).
export function wizardPages(mode: SetupMode): WizardPage[] {
  const steps = STEP_ORDER.filter((s): s is Exclude<SetupStepId, "account"> => s !== "account");
  return [...(mode === "easy" ? steps.filter((s) => s !== "env") : steps), "review"];
}

export function isWizardPage(value: string | undefined): value is WizardPage {
  return value === "review" || ((STEP_ORDER as string[]).includes(value ?? "") && value !== "account");
}

export function stepStatus(setup: SetupState, id: SetupStepId) {
  return setup.steps.find((s) => s.id === id);
}

function settled(setup: SetupState, id: SetupStepId): boolean {
  const s = stepStatus(setup, id);
  return s?.status === "done" || s?.status === "skipped";
}

// The page after `from`. On the easy path the wizard skips env automatically.
export function nextPage(mode: SetupMode, from: WizardPage): WizardPage {
  const pages = wizardPages(mode);
  const i = pages.indexOf(from);
  return pages[Math.min(pages.length - 1, i + 1)] ?? "review";
}

export function previousPage(mode: SetupMode, from: WizardPage): WizardPage | null {
  const pages = wizardPages(mode);
  const i = pages.indexOf(from);
  return i > 0 ? (pages[i - 1] ?? null) : null;
}

// The page to open: the current step of the agent, or the review when each step is settled.
export function resumePage(setup: SetupState, mode: SetupMode): WizardPage {
  const current = setup.current_step;
  if (!current || current === "account") return "review";
  if (current === "env" && mode === "easy") {
    const after = STEP_ORDER.slice(STEP_ORDER.indexOf("env") + 1).find((s) => !settled(setup, s));
    return (after as WizardPage | undefined) ?? "review";
  }
  return current;
}

// On the easy path, env must be skipped before the finish (unless it is done).
export function needsEnvSkip(setup: SetupState, mode: SetupMode): boolean {
  return mode === "easy" && !settled(setup, "env");
}

// The steps that block the finish, as the agent counts them.
export function blockingSteps(setup: SetupState): SetupStepId[] {
  return setup.steps
    .filter((s) => s.required && s.status !== "done" && !(s.status === "skipped" && s.skippable))
    .map((s) => s.id);
}

export type Route = "/claim" | "/login" | "/setup" | "/dashboard";

// The start page for a setup state. A null state means: no session.
export function homeFor(state: SetupStateName | null, claimed: boolean): Route {
  if (state === null) return claimed ? "/login" : "/claim";
  if (state === "ready") return "/dashboard";
  return "/setup";
}

// Plain-language answers -> a preset (S7, P13). 0, 1, 2 are the indexes of the options.
export interface SizeAnswers {
  staff: 0 | 1 | 2;
  peak: 0 | 1 | 2;
}

const BY_LEVEL: PresetName[] = ["solo", "small-clinic", "busy-counter"];
export const LOW_RAM_BYTES = 3 * 1024 ** 3;

export function presetFromAnswers(answers: SizeAnswers, hostRamBytes: number): { preset: PresetName; capped: boolean } {
  const level = Math.max(answers.staff, answers.peak);
  // On less than 3 GiB, the easy path hides busy-counter (plans/05 section 3).
  if (level === 2 && hostRamBytes < LOW_RAM_BYTES) return { preset: "small-clinic", capped: true };
  return { preset: BY_LEVEL[level] ?? "small-clinic", capped: false };
}

export function answersFromPreset(preset: string | undefined): SizeAnswers {
  if (preset === "solo") return { staff: 0, peak: 0 };
  if (preset === "busy-counter") return { staff: 2, peak: 2 };
  return { staff: 1, peak: 1 };
}

const MODE_KEY = "care-pi.setup-mode";

export function loadMode(): SetupMode {
  try {
    return localStorage.getItem(MODE_KEY) === "advanced" ? "advanced" : "easy";
  } catch {
    return "easy";
  }
}

export function saveMode(mode: SetupMode): void {
  try {
    localStorage.setItem(MODE_KEY, mode);
  } catch {
    // Private mode: the choice lasts for this page only.
  }
}
