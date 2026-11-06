import type { ReactNode } from "react";
import { asProblem } from "@/api/problem";
import { ProblemCard } from "@/components/v2/problem-card";
import { useCountdown } from "@/hooks/use-countdown";
import { formatDuration } from "@/lib/format";
import { fmt, problemText, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

// Shows an API error in the v2 ProblemCard (console/design.md, "ProblemAlert -> ProblemCard"): the console text of
// the problem type is the title; the detail of the agent, the field errors, and the time until the next attempt
// (Retry-After) are the description; the type, the status, the detail, and the ids are in "Technical details".
// Each card has "Copy" with the support header (S68).
export function ProblemAlert({
  error,
  className,
  title,
  onRetry,
  retrying,
  steps,
  tone = "danger",
  actions,
}: {
  error: unknown;
  className?: string;
  title?: string;
  onRetry?: () => void;
  retrying?: boolean;
  steps?: ReactNode[];
  tone?: "danger" | "warn";
  actions?: ReactNode;
}) {
  const problem = asProblem(error);
  const wait = useCountdown(problem?.retryAfter, error);
  if (!error) return null;
  if (!problem) {
    return (
      <ProblemCard
        title={title ?? strings.problems.unknown}
        technical={error instanceof Error ? `${error.name}: ${error.message}` : String(error)}
        onRetry={onRetry}
        retrying={retrying}
        tone={tone}
        announce
        className={className}
      />
    );
  }
  const p = problem.problem;
  const text =
    problem.slug === "not-implemented-in-this-version" && p.phase
      ? fmt(strings.unavailable.phase, { phase: p.phase })
      : problemText(problem.slug);
  const showDetail = Boolean(p.detail && p.detail !== text);
  const technical = [
    `type: ${p.type}`,
    `status: ${p.status}`,
    p.title ? `title: ${p.title}` : null,
    p.detail ? `detail: ${p.detail}` : null,
    p.instance ? `instance: ${p.instance}` : null,
    typeof p.job_id === "string" ? `job_id: ${p.job_id}` : null,
  ]
    .filter(Boolean)
    .join("\n");
  const description = (
    <div className="flex flex-col gap-1 break-words">
      {title ? <p>{text}</p> : null}
      {showDetail ? <p>{p.detail}</p> : null}
      {p.errors && p.errors.length > 0 ? (
        <ul className="list-disc pl-5">
          {p.errors.map((e, i) => (
            <li key={`${e.pointer}-${i}`}>
              {e.pointer ? <code className="type-data">{e.pointer}</code> : null} {e.message}
            </li>
          ))}
        </ul>
      ) : null}
      {p.failed_checks && p.failed_checks.length > 0 ? <p>{p.failed_checks.join(", ")}</p> : null}
      {p.missing_steps && p.missing_steps.length > 0 ? (
        <p>{p.missing_steps.map((s) => strings.setup.steps[s] ?? s).join(", ")}</p>
      ) : null}
      {wait > 0 ? <p className="type-data">{fmt(strings.common.tryAgainIn, { time: formatDuration(wait) })}</p> : null}
    </div>
  );
  const hasDescription = Boolean(
    title || showDetail || p.errors?.length || p.failed_checks?.length || p.missing_steps?.length || wait > 0,
  );
  return (
    <div className={cn("min-w-0", className)} data-problem={problem.slug}>
      <ProblemCard
        title={title ?? text}
        description={hasDescription ? description : undefined}
        steps={steps}
        technical={technical}
        onRetry={onRetry}
        retrying={retrying}
        tone={tone}
        actions={actions}
        announce
      />
    </div>
  );
}
