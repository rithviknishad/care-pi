import { AlertTriangleIcon } from "lucide-react";
import { asProblem } from "@/api/problem";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { useCountdown } from "@/hooks/use-countdown";
import { formatDuration } from "@/lib/format";
import { fmt, problemText, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

// Shows an API error inline: the console text of the problem type, the detail of the agent,
// the field errors, and the time until the next attempt (Retry-After).
export function ProblemAlert({ error, className, title }: { error: unknown; className?: string; title?: string }) {
  const problem = asProblem(error);
  const wait = useCountdown(problem?.retryAfter, error);
  if (!error) return null;
  if (!problem) {
    return (
      <Alert variant="destructive" className={className}>
        <AlertTriangleIcon />
        <AlertTitle>{title ?? strings.problems.unknown}</AlertTitle>
      </Alert>
    );
  }
  const p = problem.problem;
  const text =
    problem.slug === "not-implemented-in-this-version" && p.phase
      ? fmt(strings.unavailable.phase, { phase: p.phase })
      : problemText(problem.slug);
  const showDetail = p.detail && p.detail !== text;
  return (
    <Alert variant="destructive" className={cn("break-words", className)} data-problem={problem.slug}>
      <AlertTriangleIcon />
      <AlertTitle>{title ?? text}</AlertTitle>
      <AlertDescription>
        {title ? <p>{text}</p> : null}
        {showDetail ? <p>{p.detail}</p> : null}
        {p.errors && p.errors.length > 0 ? (
          <ul className="list-disc pl-4">
            {p.errors.map((e, i) => (
              <li key={`${e.pointer}-${i}`}>
                {e.pointer ? <code className="text-xs">{e.pointer}</code> : null} {e.message}
              </li>
            ))}
          </ul>
        ) : null}
        {p.failed_checks && p.failed_checks.length > 0 ? <p>{p.failed_checks.join(", ")}</p> : null}
        {p.missing_steps && p.missing_steps.length > 0 ? (
          <p>{p.missing_steps.map((s) => strings.setup.steps[s] ?? s).join(", ")}</p>
        ) : null}
        {wait > 0 ? (
          <p className="tabular-nums">{fmt(strings.common.tryAgainIn, { time: formatDuration(wait) })}</p>
        ) : null}
      </AlertDescription>
    </Alert>
  );
}
