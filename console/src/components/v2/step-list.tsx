import { CheckIcon, MinusIcon, ScrollTextIcon, XIcon } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { fmt } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { aboutMinutes, clock } from "./duration";
import { MonoBlock } from "./mono-block";
import { v2Strings } from "./strings";
import { useNow } from "./use-now";

export type StepState = "pending" | "running" | "done" | "failed" | "skipped";

export interface StepItem {
  id: string;
  /** The operator text of the step (docs/console-ui.md section 9), never the internal name. */
  title: string;
  state: StepState;
  /** One plain line under the title: what the step does now, or why it failed. Give null (not undefined) to
   *  reserve the line before the text comes, so the row does not grow. */
  detail?: ReactNode;
  /** Epoch ms. The time column shows the elapsed time (running) or the duration (done). */
  startedAt?: number | null;
  endedAt?: number | null;
  /** The usual duration of the step in seconds: "Usually about 4 min" while it runs. */
  expectedSeconds?: number | null;
  /** The log of the step. The log button opens it; its Copy has the support header (S68). */
  log?: string | (() => string);
  /** The internal name (the tooltip of the row, for support). */
  raw?: string;
}

export interface StepListProps {
  steps: StepItem[];
  /** The number of rows to reserve before the steps come (the skeleton of a job that starts). */
  reserve?: number;
  /** Reserve the log button of each row (default: a step has a log). Give true when logs come later. */
  withLogs?: boolean;
  className?: string;
  testId?: string;
}

const STATE_TEXT: Record<StepState, string> = v2Strings.steps;

// The steps of a job. Append only: a new step goes to the end, a row never moves (layout rule 5).
// A row that can have a second line (the detail, or "Usually about …" while it runs) reserves it in each state,
// so a change of state does not change its height. The running row has an indeterminate bar at its bottom edge (absolute: no height).
// A step that ends while the page is open pops its check mark.
export function StepList({ steps, reserve = 0, withLogs, className, testId }: StepListProps) {
  const running = steps.some((s) => s.state === "running");
  const now = useNow(running);
  const ghosts = Math.max(0, reserve - steps.length);
  const logs = withLogs ?? steps.some((s) => s.log !== undefined);
  return (
    <ol className={cn("flex flex-col", className)} data-testid={testId} aria-busy={running || undefined}>
      {steps.map((step, i) => (
        <StepRow key={step.id} step={step} now={now} logs={logs} last={i === steps.length - 1 && ghosts === 0} />
      ))}
      {Array.from({ length: ghosts }, (_, i) => (
        <li
          key={`ghost-${i}`}
          aria-hidden="true"
          className="grid min-h-14 grid-cols-[1.5rem_minmax(0,1fr)] content-start gap-x-3 py-3"
        >
          <span className="mt-0.5 size-6 rounded-full border-2 border-dashed border-line-strong" />
          <span className="mt-1 h-4 w-2/5 rounded bg-muted" />
        </li>
      ))}
    </ol>
  );
}

function StepIcon({ state, pop }: { state: StepState; pop: boolean }) {
  const base = "relative z-10 grid size-6 shrink-0 place-items-center rounded-full";
  switch (state) {
    case "done":
      return (
        <span
          className={cn(base, "bg-primary-solid text-white dark:text-primary-950", pop && "motion-ok:animate-v2-pop")}
        >
          <CheckIcon className="size-4" strokeWidth={3} aria-hidden="true" />
        </span>
      );
    case "failed":
      return (
        <span className={cn(base, "bg-danger-solid text-white dark:text-red-950")}>
          <XIcon className="size-4" strokeWidth={3} aria-hidden="true" />
        </span>
      );
    case "skipped":
      return (
        <span className={cn(base, "border-2 border-line-strong bg-surface-card text-ink-soft")}>
          <MinusIcon className="size-3.5" strokeWidth={3} aria-hidden="true" />
        </span>
      );
    case "running":
      return (
        <span className={cn(base, "bg-surface-card")}>
          <svg viewBox="0 0 24 24" className="size-6 motion-ok:animate-v2-spin" aria-hidden="true">
            <circle cx="12" cy="12" r="10" fill="none" strokeWidth="3" className="stroke-primary-track" />
            <path
              d="M12 2a10 10 0 0 1 10 10"
              fill="none"
              strokeWidth="3"
              strokeLinecap="round"
              className="stroke-primary-solid"
            />
          </svg>
        </span>
      );
    default:
      return <span className={cn(base, "border-2 border-line-strong bg-surface-card")} />;
  }
}

function StepRow({ step, now, logs, last }: { step: StepItem; now: number; logs: boolean; last: boolean }) {
  const [open, setOpen] = useState(false);
  const logId = useId();
  // Pop the check mark only when the step ends while the page is open, not at each load.
  const [prevState, setPrevState] = useState(step.state);
  const [pop, setPop] = useState(false);
  if (prevState !== step.state) {
    setPrevState(step.state);
    if (step.state === "done") setPop(true);
  }

  const end = step.endedAt ?? (step.state === "running" ? now : null);
  const elapsed = step.startedAt != null && end != null ? end - step.startedAt : null;
  const second =
    step.detail ??
    (step.state === "running" && step.expectedSeconds
      ? fmt(v2Strings.steps.usually, { time: aboutMinutes(step.expectedSeconds) })
      : null);
  const logText = open && step.log ? (typeof step.log === "function" ? step.log() : step.log) : "";
  // The second line is reserved when the step can have one (a usual duration or a detail), in each state.
  const twoLines = Boolean(step.expectedSeconds) || step.detail !== undefined;

  return (
    <li
      className="relative grid min-h-14 grid-cols-[1.5rem_minmax(0,1fr)_auto] content-start gap-x-3 py-3"
      data-state={step.state}
      title={step.raw}
    >
      {last ? null : (
        <span
          aria-hidden="true"
          className={cn(
            "absolute top-[2.375rem] -bottom-[0.875rem] left-[0.6875rem] w-0.5 rounded-full",
            step.state === "done" ? "bg-primary-solid/50" : "bg-line",
          )}
        />
      )}
      <span className="mt-0.5">
        <StepIcon state={step.state} pop={pop} />
      </span>
      <div className="min-w-0">
        <p
          className={cn(
            "line-clamp-2 type-body",
            step.state === "running" && "font-semibold text-ink",
            step.state === "pending" && "text-ink-soft",
            step.state === "failed" && "font-semibold text-danger-fg",
            (step.state === "done" || step.state === "skipped") && "text-ink",
          )}
        >
          <span className="sr-only">{STATE_TEXT[step.state]}: </span>
          {step.title}
        </p>
        {twoLines ? (
          <p
            className={cn(
              "mt-0.5 line-clamp-2 min-h-5 type-small",
              step.state === "failed" ? "text-ink" : "text-ink-soft",
            )}
          >
            {second}
          </p>
        ) : null}
      </div>
      <div className="flex items-start gap-1">
        <span className="mt-0.5 w-[7ch] text-right type-data text-ink-soft">
          {step.state === "pending" ? "" : clock(elapsed)}
        </span>
        {logs ? (
          step.log ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              className="-mt-1 text-ink-muted no-underline"
              aria-expanded={open}
              aria-controls={logId}
              aria-label={`${open ? v2Strings.steps.hideLog : v2Strings.steps.showLog}: ${step.title}`}
              title={open ? v2Strings.steps.hideLog : v2Strings.steps.showLog}
              onClick={() => setOpen((o) => !o)}
            >
              <ScrollTextIcon />
            </Button>
          ) : (
            <span aria-hidden="true" className="size-8" />
          )
        ) : null}
      </div>
      {open ? (
        <div id={logId} className="col-span-full mt-3 sm:col-start-2">
          <MonoBlock text={logText} mode="pre" lines={10} copy="report" copyTitle={step.title} />
        </div>
      ) : null}
      {step.state === "running" ? (
        <span
          aria-hidden="true"
          className="absolute inset-x-0 bottom-0 h-0.5 overflow-hidden rounded-full bg-primary-track"
        >
          <span className="block h-full w-1/3 rounded-full bg-primary-solid motion-reduce:w-full motion-reduce:opacity-40 motion-ok:animate-v2-slide" />
        </span>
      ) : null}
    </li>
  );
}
