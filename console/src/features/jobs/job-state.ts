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

// lastStepLog gives the last output line of a step, or undefined.
export function lastStepLog(view: Pick<JobView, "logs">, index: number): string | undefined {
  for (let i = view.logs.length - 1; i >= 0; i--) {
    const l = view.logs[i];
    if (l?.step_index === index) return l.line.trim() || undefined;
  }
  return undefined;
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

// In the setup job, the full cycle of care.target is the first start of Care: a new database runs the
// migrations. On lumine (Pi 5, 2 GB) the step took 219 s, not 45 s (plans/08-spike-log.md, Phase 2 exit test).
const SETUP_EXPECTED_SECONDS: Partial<Record<string, number>> = {
  "full-cycle": 220,
  restart: 220,
};

// jobStartedAt is the start of the job for the elapsed time. The job object arrives with the done event (or
// from the page that started the job). Before it, the start of the first step is the start of the job: on lumine
// the progress page of the setup job showed 00:00 for 7 minutes (wave 4c).
export function jobStartedAt(view: Pick<JobView, "job" | "steps">): string | null {
  return view.job?.started_at ?? view.job?.created_at ?? view.steps[0]?.started_at ?? null;
}

// jobKindFromId reads the kind from a job id: "<UTC time>-<kind>" (docs/console-server.md, section 6).
// The console uses it before the first job object arrives (for example after a page reload).
export function jobKindFromId(id: string): string | undefined {
  return /^\d{8}T\d{6}(?:\.\d+)?Z-([a-z-]+)$/.exec(id)?.[1];
}

// The steps of the backup and restore jobs, from the measurements of spike SP6 on a Pi 5 with a 46 MB database
// (plans/08-spike-log.md): a full backup 4 s, an incremental backup (the checkpoint) 4-6 s, a restore 7-8 s, the
// recovery and the promote 3-5 s, pgbackrest verify 0.7 s, a cold start of the stack 44 s. They grow with the size.
const BACKUP_EXPECTED_SECONDS: Partial<Record<string, number>> = {
  checkpoint: 6,
  "backup-incr": 6,
  "backup-full": 4,
  "restore-db": 8,
  "wait-promote": 5,
  "verify-local": 1,
  "drill-restore": 8,
  "drill-start": 5,
  "stop-app": 10,
  "start-app": 10,
};

const KIND_EXPECTED_SECONDS: Record<string, Partial<Record<string, number>>> = {
  setup: SETUP_EXPECTED_SECONDS,
  // After a restore, Care starts on a migrated database: the cold start of SP6 (44 s, with start-app).
  "restore-pitr": { "wait-healthy": 35 },
  "recovery-key-rotate": { "wait-healthy": 35 },
  "restore-dr": { "start-target": 60, "wait-healthy": 60 },
};

// expectedSeconds gives the expected duration of a step of a job of this kind, or undefined.
export function expectedSeconds(action: string | null | undefined, jobKind?: string): number | undefined {
  if (!action) return undefined;
  const byKind = jobKind ? KIND_EXPECTED_SECONDS[jobKind]?.[action] : undefined;
  if (byKind !== undefined) return byKind;
  return BACKUP_EXPECTED_SECONDS[action] ?? EXPECTED_SECONDS[action];
}
