import { BanIcon, CheckCircle2Icon, CircleIcon, MinusCircleIcon, TerminalIcon, XCircleIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { Job, JobStep } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Spinner } from "@/components/ui/spinner";
import { useElapsed } from "@/hooks/use-countdown";
import { formatAbout, formatElapsed, formatTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { expectedSeconds, jobKindFromId, jobStartedAt, stepProgress, type JobView } from "./job-state";
import { useJobStream } from "./use-job-stream";

const STATE_BADGE: Record<string, "neutral" | "info" | "success" | "destructive" | "warning"> = {
  queued: "neutral",
  pending: "neutral",
  running: "info",
  succeeded: "success",
  failed: "destructive",
  cancelled: "warning",
  skipped: "neutral",
};

function StepIcon({ state }: { state: JobStep["state"] }) {
  switch (state) {
    case "running":
      return <Spinner className="size-5 text-primary" />;
    case "succeeded":
      return <CheckCircle2Icon className="size-5 text-green-600 dark:text-green-400" />;
    case "failed":
      return <XCircleIcon className="size-5 text-destructive" />;
    case "skipped":
      return <MinusCircleIcon className="size-5 text-soft-foreground" />;
    case "cancelled":
      return <BanIcon className="size-5 text-amber-600" />;
    default:
      return <CircleIcon className="size-5 text-soft-foreground" />;
  }
}

function StepRow({ step, view, kind }: { step: JobStep; view: JobView; kind: string | undefined }) {
  const running = step.state === "running";
  const elapsed = useElapsed(step.started_at, step.finished_at, running);
  const progress = stepProgress(view.progress[step.index]);
  const expected = expectedSeconds(step.action, kind);
  const message = view.progress[step.index]?.message;
  const secondary =
    step.detail ??
    message ??
    (running && expected ? fmt(strings.progress.expected, { time: formatAbout(expected) }) : "");
  return (
    <li
      className="relative grid h-14 grid-cols-[1.25rem_1fr_auto] items-center gap-x-3 border-b px-1 last:border-b-0"
      data-state={step.state}
      data-testid="job-step"
    >
      <StepIcon state={step.state} />
      <div className="min-w-0">
        <p className={cn("truncate text-sm", running && "font-semibold")}>{step.name}</p>
        <p className={cn("truncate text-xs", step.state === "failed" ? "text-destructive" : "text-soft-foreground")}>
          {secondary || "\u00a0"}
        </p>
      </div>
      <span className="w-[7ch] text-right text-xs text-soft-foreground tabular-nums">
        {step.started_at ? formatElapsed(elapsed) : ""}
      </span>
      {running && progress !== null ? (
        <Progress
          value={progress}
          aria-label={step.name}
          className="absolute inset-x-0 bottom-0 gap-0 [&_[data-slot=progress-track]]:h-0.5 [&_[data-slot=progress-track]]:rounded-none"
        />
      ) : null}
    </li>
  );
}

interface JobProgressProps {
  jobId: string;
  initial?: Job | null;
  title?: string;
  intro?: string;
  onDone?: (job: Job) => void;
  children?: React.ReactNode;
}

// The progress of a job from its event stream: steps append, a known step changes in place.
export function JobProgress(props: JobProgressProps) {
  return <JobProgressView key={props.jobId} {...props} />;
}

function JobProgressView({ jobId, initial = null, title, intro, onDone, children }: JobProgressProps) {
  const { view, state, problem } = useJobStream(jobId, initial);
  const [showLogs, setShowLogs] = useState(false);
  const job = view.job;
  const jobState = view.done ? (job?.state ?? "succeeded") : (job?.state ?? "running");
  const total = useElapsed(jobStartedAt(view), view.done ? job?.finished_at : null, !view.done);
  const doneRef = useRef(false);
  const logBox = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (view.done && job && !doneRef.current) {
      doneRef.current = true;
      onDone?.(job);
    }
  }, [view.done, job, onDone]);

  useEffect(() => {
    const box = logBox.current;
    if (box) box.scrollTop = box.scrollHeight;
  }, [view.logs.length, showLogs]);

  return (
    <section
      className="flex flex-col gap-4"
      aria-busy={!view.done && !problem}
      data-testid="job-progress"
      data-job-state={jobState}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        {title ? <h2 className="text-lg font-semibold">{title}</h2> : null}
        <Badge variant={STATE_BADGE[jobState] ?? "neutral"} data-testid="job-state">
          {strings.progress.states[jobState] ?? jobState}
        </Badge>
        <span className="text-sm text-soft-foreground tabular-nums" aria-label={strings.progress.elapsed}>
          {formatElapsed(total)}
        </span>
        <span className="text-xs text-soft-foreground">{fmt(strings.progress.job, { id: jobId })}</span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="ml-auto"
          aria-expanded={showLogs}
          onClick={() => setShowLogs((v) => !v)}
        >
          <TerminalIcon data-icon="inline-start" />
          {showLogs ? strings.progress.hideLogs : strings.progress.showLogs}
        </Button>
      </div>
      {intro ? <p className="text-sm text-soft-foreground">{intro}</p> : null}
      <p className="min-h-5 text-xs text-soft-foreground" aria-live="polite">
        {state === "retry" || state === "poll" ? strings.progress.reconnect : ""}
      </p>
      {showLogs ? (
        <div
          ref={logBox}
          className="h-56 overflow-y-auto rounded-lg border bg-soft-background p-3 text-xs leading-5"
          role="log"
          aria-label={strings.progress.logs}
        >
          {view.droppedLogs > 0 ? (
            <p className="text-soft-foreground">{fmt(strings.progress.dropped, { count: view.droppedLogs })}</p>
          ) : null}
          {view.logs.map((l) => (
            <p key={l.id} className={cn("break-all whitespace-pre-wrap", l.stream === "stderr" && "text-destructive")}>
              <span className="text-soft-foreground">{formatTime(l.time)} </span>
              {l.line}
            </p>
          ))}
        </div>
      ) : null}
      <ol className="rounded-lg border" aria-label={strings.progress.title}>
        {view.steps.map((s) => (
          <StepRow key={s.index} step={s} view={view} kind={job?.kind ?? jobKindFromId(jobId)} />
        ))}
        {view.steps.length === 0 && !problem ? (
          <li className="flex h-14 items-center gap-3 px-1">
            <Spinner className="size-5" />
          </li>
        ) : null}
      </ol>
      {view.errors.map((p, i) => (
        <p key={i} className="text-sm text-destructive">
          {p.detail}
        </p>
      ))}
      {problem ? <ProblemAlert error={problem} /> : null}
      {children}
    </section>
  );
}
