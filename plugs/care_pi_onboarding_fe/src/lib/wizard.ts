export const STEP_ORDER = [
  "organization",
  "facility",
  "roles",
  "staff",
  "valuesets",
  "questionnaires",
  "catalog",
  "demo_data",
] as const;

export type StepName = (typeof STEP_ORDER)[number];
export type StepState = "pending" | "done" | "skipped" | "failed";
export type Counts = Record<string, number>;

export interface StepStatus {
  step: StepName;
  title: string;
  description: string;
  optional: boolean;
  scope: "instance" | "facility";
  allowed: boolean;
  available?: boolean;
  state: StepState;
  runs: number;
  applied_at: string | null;
  created: Counts;
  existing: Counts;
  detail: string;
  exists: Counts;
}

export interface FacilityRef {
  id: string;
  name: string;
}

export interface Status {
  can_manage: boolean;
  is_superuser: boolean;
  locale?: string;
  facility: FacilityRef | null;
  geo_organization?: { id: string; name: string; path: string[] } | null;
  steps: StepStatus[];
  next_step: StepName | null;
  wizard: { closed: boolean; reason: string; redirect: boolean };
}

export interface StepResult {
  step: StepName;
  status: "done" | "skipped" | "failed" | "removed";
  created: Counts;
  existing: Counts;
  detail: string;
  result?: Record<string, any>;
  errors?: string[];
  secrets?: { credentials?: { username: string; password: string }[] };
  removed?: Counts;
}

export type Screen = StepName | "done";

export interface WizardState {
  phase: "loading" | "ready" | "denied" | "error";
  status: Status | null;
  screen: Screen | null;
  facility: string | null;
  busy: boolean;
  results: Partial<Record<StepName, StepResult>>;
  error: string | null;
}

export type WizardAction =
  | { type: "status_loaded"; status: Status }
  | { type: "status_failed"; error: string }
  | { type: "go"; screen: Screen }
  | { type: "request" }
  | { type: "request_failed"; error: string }
  | { type: "result"; result: StepResult }
  | { type: "next" };

export const initialState: WizardState = {
  phase: "loading",
  status: null,
  screen: null,
  facility: null,
  busy: false,
  results: {},
  error: null,
};

/** The steps that the wizard shows: the steps the user may apply, and the steps that ran. */
export function visibleSteps(status: Status | null): StepStatus[] {
  if (!status) return [];
  return status.steps.filter(
    (s) =>
      (s.allowed || s.state !== "pending" || s.scope === "facility") &&
      s.available !== false,
  );
}

/** The first step after `after` (or from the start) that is not done or skipped and that the user may apply. */
export function nextScreen(status: Status, after: Screen | null): Screen {
  const steps = visibleSteps(status);
  const start =
    after && after !== "done"
      ? steps.findIndex((s) => s.step === after) + 1
      : 0;
  for (const step of steps.slice(start)) {
    if (!step.allowed) continue;
    if (step.state === "pending" || step.state === "failed") {
      return step.step;
    }
  }
  return "done";
}

export function totals(counts: Counts | undefined): number {
  return Object.values(counts ?? {}).reduce((sum, n) => sum + n, 0);
}

function withStep(status: Status, result: StepResult): Status {
  const state: StepState =
    result.status === "removed"
      ? "pending"
      : result.status === "failed"
        ? "failed"
        : result.status;
  return {
    ...status,
    steps: status.steps.map((s) =>
      s.step === result.step
        ? {
            ...s,
            state,
            runs: s.runs + (result.status === "removed" ? 0 : 1),
            created: result.created ?? {},
            existing: result.existing ?? {},
            detail: result.detail,
          }
        : s,
    ),
  };
}

export function wizardReducer(
  state: WizardState,
  action: WizardAction,
): WizardState {
  switch (action.type) {
    case "status_loaded": {
      const { status } = action;
      if (!status.can_manage) {
        return { ...state, phase: "denied", status, busy: false };
      }
      const facility = state.facility ?? status.facility?.id ?? null;
      return {
        ...state,
        phase: "ready",
        status,
        facility,
        busy: false,
        error: null,
        screen: state.screen ?? nextScreen(status, null),
      };
    }
    case "status_failed":
      return { ...state, phase: "error", busy: false, error: action.error };
    case "go":
      if (state.busy) return state;
      return { ...state, screen: action.screen, error: null };
    case "request":
      return { ...state, busy: true, error: null };
    case "request_failed":
      return { ...state, busy: false, error: action.error };
    case "result": {
      const { result } = action;
      if (!state.status) return { ...state, busy: false };
      let facility = state.facility;
      const made = result.result?.facility as FacilityRef | undefined;
      let status = withStep(state.status, result);
      if (result.step === "facility" && result.status === "done" && made) {
        facility = made.id;
        // The maker of a facility is its Facility Admin, so the facility steps open now.
        // The server checks each call again.
        status = {
          ...status,
          facility: made,
          steps: status.steps.map((s) =>
            s.scope === "facility" && s.available !== false
              ? { ...s, allowed: true }
              : s,
          ),
        };
      }
      return {
        ...state,
        busy: false,
        status,
        facility,
        results: { ...state.results, [result.step]: result },
        error: null,
      };
    }
    case "next": {
      if (!state.status || state.busy) return state;
      return { ...state, screen: nextScreen(state.status, state.screen) };
    }
    default:
      return state;
  }
}
