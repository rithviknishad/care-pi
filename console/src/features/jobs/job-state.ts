import type { Job, JobLogLine, JobProgress, JobStep, Problem } from "@/api/types";

export type JobEventName = "step" | "log" | "progress" | "error" | "done" | "heartbeat";

export interface JobEvent {
  id: number | null;
  event: JobEventName;
  data: unknown;
}

export interface JobLogEntry extends JobLogLine {
  id: number;
}

export interface JobView {
  // Ordered by index. The reducer appends a new step and replaces a known step in place.
  steps: JobStep[];
  logs: JobLogEntry[];
  droppedLogs: number;
  progress: Record<number, JobProgress>;
  errors: Problem[];
  job: Job | null;
  lastEventId: number;
  done: boolean;
}

export const MAX_JOB_LOGS = 500;

export function emptyJobView(job: Job | null = null): JobView {
  return { steps: [], logs: [], droppedLogs: 0, progress: {}, errors: [], job, lastEventId: 0, done: false };
}

function upsertStep(steps: JobStep[], step: JobStep): JobStep[] {
  const at = steps.findIndex((s) => s.index === step.index);
  if (at >= 0) {
    const next = steps.slice();
    next[at] = step;
    return next;
  }
  // Steps arrive in index order. A late step goes after the last step with a lower index.
  let insert = steps.length;
  while (insert > 0 && (steps[insert - 1]?.index ?? -1) > step.index) insert--;
  return [...steps.slice(0, insert), step, ...steps.slice(insert)];
}

export function applyJobEvent(view: JobView, ev: JobEvent): JobView {
  if (ev.event === "heartbeat") return view;
  if (ev.id !== null && ev.id <= view.lastEventId) return view;
  const lastEventId = ev.id ?? view.lastEventId;
  switch (ev.event) {
    case "step":
      return { ...view, lastEventId, steps: upsertStep(view.steps, ev.data as JobStep) };
    case "log": {
      const logs = [...view.logs, { ...(ev.data as JobLogLine), id: lastEventId }];
      const drop = Math.max(0, logs.length - MAX_JOB_LOGS);
      return { ...view, lastEventId, logs: drop ? logs.slice(drop) : logs, droppedLogs: view.droppedLogs + drop };
    }
    case "progress": {
      const p = ev.data as JobProgress;
      return { ...view, lastEventId, progress: { ...view.progress, [p.step_index]: p } };
    }
    case "error":
      return { ...view, lastEventId, errors: [...view.errors, ev.data as Problem] };
    case "done": {
      const job = ev.data as Job;
      let steps = view.steps;
      for (const s of job.steps ?? []) steps = upsertStep(steps, s);
      return { ...view, lastEventId, job, steps, done: true };
    }
    default:
      return view;
  }
}

export function stepProgress(p: JobProgress | undefined): number | null {
  if (!p) return null;
  if (p.unit === "percent") return Math.max(0, Math.min(100, p.current));
  if (!p.total) return null;
  return Math.max(0, Math.min(100, (p.current / p.total) * 100));
}

export function isFinished(job: Pick<Job, "state"> | null | undefined): boolean {
  return job?.state === "succeeded" || job?.state === "failed" || job?.state === "cancelled";
}

// Expected durations from the measurements on a Pi 5 (plans/09-handoff.md section 3).
// The key is the action of a step (the first word of a plan step).
export const EXPECTED_SECONDS: Partial<Record<string, number>> = {
  "ensure-fe": 180,
  "start-target": 200,
  "wait-healthy": 200,
  "full-cycle": 45,
  restart: 45,
};
