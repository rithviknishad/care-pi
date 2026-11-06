import { ChevronDownIcon, CircleAlertIcon, RotateCcwIcon, TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import { CopyButton } from "./copy-button";
import { MonoBlock } from "./mono-block";
import { v2Strings } from "./strings";

export interface ProblemCardProps {
  /** What happened, in plain words, without blame: "We could not reach the backup disk." */
  title: string;
  /** Why, and what it means for the operator. */
  description?: ReactNode;
  /** What the operator can do, in order. */
  steps?: ReactNode[];
  /** The raw problem (type, status, detail, ids). It shows only in the "Technical details" disclosure. */
  technical?: string;
  onRetry?: () => void;
  retrying?: boolean;
  retryLabel?: string;
  /** More actions (a link to the log, to support). */
  actions?: ReactNode;
  tone?: "danger" | "warn";
  /** role="alert": the screen reader says it at once. Use it for a problem that comes after an action. */
  announce?: boolean;
  className?: string;
  testId?: string;
}

// A problem: plain title and detail, the steps, Retry, Copy (with the support header, S68), and the
// technical details in a disclosure. It takes the place of the content that failed (layout rule 7).
export function ProblemCard({
  title,
  description,
  steps,
  technical,
  onRetry,
  retrying = false,
  retryLabel = v2Strings.retry,
  actions,
  tone = "danger",
  announce = false,
  className,
  testId,
}: ProblemCardProps) {
  const Icon = tone === "danger" ? CircleAlertIcon : TriangleAlertIcon;
  const copyBody = [typeof description === "string" ? description : null, technical ?? null]
    .filter(Boolean)
    .join("\n\n");
  return (
    <section
      role={announce ? "alert" : undefined}
      data-testid={testId}
      data-tone={tone}
      className={cn(
        "flex min-w-0 gap-3 rounded-card border p-4 sm:gap-4 sm:p-5",
        tone === "danger" ? "border-danger-border bg-danger-bg" : "border-warn-border bg-warn-bg",
        className,
      )}
    >
      <Icon
        aria-hidden="true"
        className={cn("mt-0.5 size-6 shrink-0", tone === "danger" ? "text-danger-solid" : "text-warn-solid")}
      />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <h3 className={cn("type-h3", tone === "danger" ? "text-danger-fg" : "text-warn-fg")}>{title}</h3>
        {description ? <div className="type-body text-ink">{description}</div> : null}
        {steps && steps.length > 0 ? (
          <ol className="ml-5 list-decimal space-y-1 type-body text-ink marker:type-data marker:text-ink-soft">
            {steps.map((s, i) => (
              <li key={i}>{s}</li>
            ))}
          </ol>
        ) : null}
        <div className="flex flex-wrap items-center gap-2 pt-2">
          {onRetry ? (
            <Button type="button" size="sm" onClick={onRetry} disabled={retrying}>
              {retrying ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}
              {retryLabel}
            </Button>
          ) : null}
          <CopyButton text={copyBody || title} title={title} report />
          {actions}
        </div>
        {technical ? (
          <details className="group mt-1 min-w-0">
            <summary className="inline-flex h-9 cursor-pointer list-none items-center gap-1.5 rounded-md type-small font-medium text-ink-muted outline-none select-none hover:text-ink focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
              <ChevronDownIcon
                aria-hidden="true"
                className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
              />
              {v2Strings.technicalDetails}
            </summary>
            <MonoBlock text={technical} mode="pre" copy={false} className="mt-2" />
          </details>
        ) : null}
      </div>
    </section>
  );
}
