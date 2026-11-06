import type { JobStep } from "@/api/types";
import type { StepItem, StepState } from "@/components/v2/step-list";
import type { Tone } from "@/components/v2/tone";
import { formatTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { expectedSeconds, lastStepLog, stepProgress, type JobView } from "./job-state";
import { stepArgument, stepTitle } from "./step-text";

const STATE: Record<JobStep["state"], StepState> = {
  pending: "pending",
  running: "running",
  succeeded: "done",
  failed: "failed",
  skipped: "skipped",
  cancelled: "skipped",
};

export const JOB_STATE_TONE: Record<string, Tone> = {
  queued: "neutral",
  running: "primary",
  succeeded: "ok",
  failed: "danger",
  cancelled: "warn",
};

const epoch = (iso: string | null | undefined) => (iso ? Date.parse(iso) : null);

// The log lines of one step (or of the whole job, index undefined) as plain text: "10:15:03 line".
export function logText(view: Pick<JobView, "logs">, index?: number): string {
  return view.logs
    .filter((l) => index === undefined || l.step_index === index)
    .map((l) => `${formatTime(l.time)}  ${l.line}`)
    .join("\n");
}

// The second line of a step: why it failed, what it does now (the progress message, else the usual duration of the
// StepList, else the last output line), or the short argument of its name. Each row reserves the line (a string or
// null), so a change of state does not change the height of a row.
// The step keys whose argument is plain for an operator ("15 files", "3 images", "usb: database", a time).
const PLAIN_ARGUMENT = new Set([
  "write-files",
  "delete-files",
  "pull-images",
  "set-hostname",
  "snapshot",
  "restore-db",
]);

function plainArgument(step: JobStep): string | null {
  if (!PLAIN_ARGUMENT.has(step.action ?? "")) return null;
  // A list of images is a count for the operator; the names are in the tooltip of the row and in the log.
  if (step.action === "pull-images") {
    const arg = step.name.includes(": ") ? step.name.slice(step.name.indexOf(": ") + 2).trim() : "";
    const refs = arg.split(/\s+/).filter(Boolean);
    if (refs.length > 0 && !/^\d+ image/.test(arg)) return fmt(strings.steps.images, { count: refs.length });
  }
  return stepArgument(step) || null;
}

function detailOf(step: JobStep, view: JobView, kind: string | undefined): string | null {
  if (step.state === "failed" || step.state === "skipped") return step.detail || plainArgument(step);
  if (step.state === "running") {
    const message = view.progress[step.index]?.message;
    if (message) return message;
    if (expectedSeconds(step.action, kind)) return null;
    return lastStepLog(view, step.index) ?? plainArgument(step);
  }
  return step.detail || plainArgument(step);
}

// The StepList items of a job view (console/design.md, "job-progress.tsx" -> StepList).
export function stepItems(view: JobView, kind: string | undefined, withLogs: boolean): StepItem[] {
  return view.steps.map((step) => {
    const title = stepTitle(step, kind);
    const progress = stepProgress(view.progress[step.index]);
    const hasLog = withLogs && view.logs.some((l) => l.step_index === step.index);
    return {
      id: String(step.index),
      title: title.text,
      titleMono: title.raw,
      state: STATE[step.state] ?? "pending",
      detail: detailOf(step, view, kind),
      startedAt: epoch(step.started_at),
      endedAt: epoch(step.finished_at),
      expectedSeconds: expectedSeconds(step.action, kind) ?? null,
      log: hasLog ? () => logText(view, step.index) : undefined,
      raw: step.name,
      testId: "job-step",
      titleTestId: "job-step-title",
      data: { state: step.state, action: step.action ?? undefined },
      progress: progress === null ? null : progress / 100,
    };
  });
}
