import { useMutation } from "@tanstack/react-query";
import { ChevronDownIcon, SquareIcon } from "lucide-react";
import { useEffect, useRef, type ReactNode } from "react";
import { api, call } from "@/api/client";
import { exportJob } from "@/api/exports";
import type { Job, JobStep } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/v2/copy-button";
import { clock } from "@/components/v2/duration";
import { MonoBlock } from "@/components/v2/mono-block";
import { ProblemCard } from "@/components/v2/problem-card";
import { StatusChip } from "@/components/v2/status-chip";
import { StepList, StepProgress } from "@/components/v2/step-list";
import { useNow } from "@/components/v2/use-now";
import { fmt, problemText, strings } from "@/lib/strings";
import { slugOf } from "@/api/problem";
import { cn } from "@/lib/utils";
import { jobKindFromId, jobStartedAt } from "./job-state";
import { JOB_STATE_TONE, logText, stepItems } from "./job-steps";
import { useJobStream } from "./use-job-stream";

interface JobProgressProps {
  jobId: string;
  /** The job from the POST that started it, or from GET /jobs/{id} (with log_tail: the stream resumes, S67). */
  initial?: Job | null;
  title?: string;
  intro?: string;
  onDone?: (job: Job) => void;
  // Called when a step starts or ends (for example to read the maintenance state again), with the running step.
  onStepChange?: (running: JobStep | null) => void;
  children?: ReactNode;
  /** S67: a device without a session follows the public stream (steps only). */
  public?: boolean;
  /** The rows to reserve before the steps come (the step count of the job, when the page knows it). */
  reserve?: number;
  /** Show "Step N of M" and a bar above the steps: the planned step count of the job (job.step_count of the public
   *  status), or null for a job without a plan ("Step N" and an indeterminate bar). undefined: no such line. */
  stepCount?: number | null;
  /** Show "Stop the job" when the job can stop (the Jobs page). */
  cancel?: boolean;
  className?: string;
}

// The progress of a job from its event stream (console/design.md, StepList): steps append, a known step changes in
// place, each running step has its elapsed clock and "Usually about …". The header has the state, the time, and
// "Copy the transcript" (S68: GET /jobs/{id}/export, with the support header of the agent).
export function JobProgress(props: JobProgressProps) {
  return <JobProgressView key={`${props.jobId}-${props.public ? "p" : "s"}`} {...props} />;
}

function JobProgressView({
  jobId,
  initial = null,
  title,
  intro,
  onDone,
  onStepChange,
  children,
  public: isPublic = false,
  reserve,
  stepCount,
  cancel = false,
  className,
}: JobProgressProps) {
  const { view, state, problem } = useJobStream(jobId, initial, { public: isPublic });
  const job = view.job;
  const kind = job?.kind ?? jobKindFromId(jobId);
  const jobState = view.done ? (job?.state ?? "succeeded") : job?.state === "queued" ? "queued" : "running";
  const now = useNow(!view.done);
  const startedAt = jobStartedAt(view);
  const endedAt = view.done && job?.finished_at ? Date.parse(job.finished_at) : now;
  const total = startedAt ? endedAt - Date.parse(startedAt) : null;
  const doneRef = useRef(false);
  const stepChange = useRef(onStepChange);
  const stepKey = view.steps.map((s) => `${s.index}:${s.state}`).join(",");
  const stop = useMutation({
    mutationFn: () => call(api.POST("/jobs/{job_id}/cancel", { params: { path: { job_id: jobId } } })),
  });

  useEffect(() => {
    stepChange.current = onStepChange;
  });

  const runningStep = [...view.steps].reverse().find((s) => s.state === "running") ?? null;
  const runningRef = useRef(runningStep);
  useEffect(() => {
    runningRef.current = runningStep;
  });
  useEffect(() => {
    if (stepKey) stepChange.current?.(runningRef.current);
  }, [stepKey]);

  useEffect(() => {
    if (view.done && job && !doneRef.current) {
      doneRef.current = true;
      onDone?.(job);
    }
  }, [view.done, job, onDone]);

  const items = stepItems(view, kind, !isPublic);
  const canCancel = cancel && !isPublic && !view.done && Boolean(job?.cancellable);
  const older = view.droppedLogs;

  return (
    <section
      className={cn("flex min-w-0 flex-col gap-4", className)}
      aria-busy={!view.done && !problem}
      data-testid="job-progress"
      data-job-state={jobState}
      data-job-id={jobId}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        {title ? <h2 className="type-h3 text-ink">{title}</h2> : null}
        <StatusChip
          tone={JOB_STATE_TONE[jobState] ?? "neutral"}
          live={!view.done}
          width={7}
          size="sm"
          testId="job-state"
        >
          {strings.progress.states[jobState] ?? jobState}
        </StatusChip>
        <span className="w-[7ch] type-data text-ink-soft" aria-label={strings.progress.elapsed}>
          {clock(total)}
        </span>
        {/* The line of a lost connection has its place: in the row from 640 px, below it on a phone. */}
        <p
          className="order-last min-h-5 basis-full type-small text-ink-soft sm:order-none sm:min-h-0 sm:min-w-0 sm:flex-1 sm:basis-0 sm:truncate"
          aria-live="polite"
        >
          {state === "retry" || state === "poll" ? strings.progress.reconnect : ""}
        </p>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {canCancel ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={stop.isPending}
              onClick={() => stop.mutate()}
              data-testid="job-cancel"
            >
              {stop.isPending ? <Spinner data-icon="inline-start" /> : <SquareIcon data-icon="inline-start" />}
              {strings.jobsPage.cancel}
            </Button>
          ) : null}
          {isPublic ? null : (
            <CopyButton
              text={() => exportJob(jobId)}
              support
              label={strings.jobsPage.copyTranscript}
              className="no-underline"
            />
          )}
        </div>
      </div>
      {intro ? <p className="max-w-prose type-body text-ink-muted">{intro}</p> : null}
      {stepCount === undefined ? null : (
        <StepProgress
          steps={items}
          total={stepCount}
          succeeded={view.done ? jobState === "succeeded" : null}
          testId="job-step-progress"
        />
      )}
      <StepList
        steps={items}
        reserve={reserve ?? (view.steps.length === 0 && !problem ? 3 : 0)}
        withLogs={!isPublic}
        testId="job-steps"
        className="rounded-card border border-line-soft px-3 sm:px-4"
      />
      {view.errors.map((p, i) => (
        <ProblemCard
          key={i}
          tone="warn"
          title={p.title || problemText(slugOf(p.type))}
          technical={[p.type, p.detail].filter(Boolean).join("\n")}
        />
      ))}
      {problem ? <ProblemAlert error={problem} /> : null}
      {stop.error ? <ProblemAlert error={stop.error} /> : null}
      {isPublic ? null : (
        <details className="group rounded-card border border-line-soft">
          <summary className="flex h-11 cursor-pointer list-none items-center gap-2 px-3 type-small font-medium text-ink-muted outline-none select-none hover:text-ink focus-visible:ring-3 focus-visible:ring-ring/50 sm:px-4 [&::-webkit-details-marker]:hidden">
            <ChevronDownIcon
              aria-hidden="true"
              className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
            />
            {strings.jobsPage.log}
          </summary>
          <div className="flex flex-col gap-2 px-3 pb-3 sm:px-4 sm:pb-4">
            {older > 0 ? (
              <p className="type-small text-ink-soft">{fmt(strings.jobsPage.olderLines, { count: older })}</p>
            ) : null}
            <MonoBlock
              text={logText(view) || strings.jobsPage.noLog}
              mode="pre"
              lines={12}
              copy="report"
              copyTitle={`${strings.jobsPage.jobTitle} ${jobId}`}
              testId="job-log"
            />
          </div>
        </details>
      )}
      {children}
    </section>
  );
}
