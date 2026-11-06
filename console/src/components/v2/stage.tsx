import { CheckIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { fmt } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { Illustration } from "./illustration";
import type { IllustrationName } from "./illustrations";
import { WhatNext } from "./panel";
import { ProgressRing } from "./progress-ring";
import { v2Strings } from "./strings";

export interface StageStep {
  id: string;
  label: string;
  /** A done step can link back to its page. */
  href?: string;
}

export interface StageProps {
  steps: StageStep[];
  /** The index of the current step. The steps before it are done. */
  current: number;
  /** The title of the rail ("Set up your Care box"). */
  railTitle: ReactNode;
  /** One line under the rail title ("About 10 minutes in total"). */
  railHint?: ReactNode;
  illustration?: IllustrationName;
  /** The task of this step. */
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  /** The secondary action at the left of the footer (Back). */
  back?: ReactNode;
  /** The primary action at the right of the footer. */
  primary?: ReactNode;
  /** "What happens next": one sentence under the primary button. */
  next?: ReactNode;
  /** Give the task panel view-transition-name: v2-stage (for <Link viewTransition>). Without it, the panel
   *  has a CSS enter animation at each step. Use one of the two, not both. */
  viewTransition?: boolean;
  labels?: Partial<typeof v2Strings.stage>;
  className?: string;
}

function RailStep({ step, index, current }: { step: StageStep; index: number; current: number }) {
  const state = index < current ? "done" : index === current ? "current" : "todo";
  const icon =
    state === "done" ? (
      <span className="relative z-10 grid size-6 place-items-center rounded-full bg-primary-solid text-white dark:text-primary-950">
        <CheckIcon className="size-3.5" strokeWidth={3} aria-hidden="true" />
      </span>
    ) : state === "current" ? (
      <span className="relative z-10 grid size-6 place-items-center rounded-full border-2 border-primary-solid bg-surface-card">
        <span className="size-2.5 rounded-full bg-primary-solid" />
      </span>
    ) : (
      <span className="relative z-10 grid size-6 place-items-center rounded-full border-2 border-line-strong bg-surface-card type-data text-[0.6875rem] text-ink-soft">
        {index + 1}
      </span>
    );
  const text = (
    <span
      className={cn(
        "min-w-0 truncate type-small",
        state === "current" && "font-semibold text-ink",
        state === "done" && "text-ink-muted",
        state === "todo" && "text-ink-soft",
      )}
    >
      {step.label}
    </span>
  );
  const row = "relative flex h-11 items-center gap-3 rounded-lg px-2";
  return (
    <li aria-current={state === "current" ? "step" : undefined} className="relative">
      {state === "done" && step.href ? (
        <Link
          to={step.href}
          className={cn(
            row,
            "outline-none hover:bg-surface-tint focus-visible:outline-2 focus-visible:outline-indigo-500",
          )}
        >
          {icon}
          {text}
        </Link>
      ) : (
        <div className={cn(row, state === "current" && "bg-surface-card shadow-soft")}>
          {icon}
          {text}
        </div>
      )}
    </li>
  );
}

// The wizard layout. Left (from 1024 px): the stage rail, with an illustrated gradient header, the progress, and
// the steps. Right: the one task of this step, its title, and the footer with Back, the primary action, and
// "what happens next". Below 1024 px the rail is a compact band with a segmented progress bar.
export function Stage({
  steps,
  current,
  railTitle,
  railHint,
  illustration = "pi-box",
  title,
  description,
  children,
  back,
  primary,
  next,
  viewTransition = false,
  labels: labelOverrides,
  className,
}: StageProps) {
  const labels = { ...v2Strings.stage, ...labelOverrides };
  const total = steps.length;
  const index = Math.min(Math.max(current, 0), Math.max(total - 1, 0));
  const fraction = total ? index / total : 0;
  const stepOf = fmt(labels.stepOf, { current: index + 1, total });
  const percent = Math.round(fraction * 100);
  const stepId = steps[index]?.id ?? String(index);

  return (
    <div className={cn("grid gap-6 lg:grid-cols-[var(--stage-rail-width)_minmax(0,1fr)] lg:gap-8", className)}>
      <aside className="self-start lg:sticky lg:top-[calc(var(--topbar-height)+2rem)]">
        <div className="overflow-hidden rounded-stage border border-line bg-gradient-rail shadow-soft">
          <div className="relative isolate flex h-32 items-center gap-4 overflow-hidden bg-gradient-hero px-5 on-gradient lg:h-44 lg:items-end lg:pb-5">
            <div aria-hidden="true" className="absolute inset-0 -z-10 bg-scrim-full" />
            <Illustration
              name={illustration}
              size="md"
              className="absolute -top-1 -right-4 w-24 opacity-50 lg:top-3 lg:right-2 lg:w-36 lg:opacity-90"
            />
            <ProgressRing value={fraction} size={56} stroke={5} label={stepOf} onGradient className="shrink-0">
              <span className="text-[0.75rem]">{percent}%</span>
            </ProgressRing>
            <div className="relative min-w-0 pr-14 lg:pr-0">
              <p className="type-h3 text-balance">{railTitle}</p>
              <p className="type-data text-on-gradient-muted">{railHint ?? stepOf}</p>
            </div>
          </div>
          {/* Phones and tablets: segments and the name of the step. */}
          <div className="flex flex-col gap-2 px-4 py-3 lg:hidden">
            <div className="flex gap-1" aria-hidden="true">
              {steps.map((s, i) => (
                <span
                  key={s.id}
                  className={cn(
                    "h-1.5 flex-1 rounded-full",
                    i < index ? "bg-primary-solid" : i === index ? "bg-primary-solid/60" : "bg-primary-track",
                  )}
                />
              ))}
            </div>
            <p className="truncate type-small text-ink-muted">
              <span className="type-data">{stepOf}</span>
              <span aria-hidden="true"> · </span>
              <span className="font-medium text-ink">{steps[index]?.label}</span>
            </p>
          </div>
          <nav aria-label={labels.steps} className="hidden p-3 lg:block">
            <ol className="relative flex flex-col gap-0.5">
              {steps.map((s, i) => (
                <RailStep key={s.id} step={s} index={i} current={index} />
              ))}
            </ol>
          </nav>
        </div>
      </aside>
      <section
        key={stepId}
        aria-labelledby={`stage-title-${stepId}`}
        className={cn(
          "flex min-w-0 flex-col rounded-stage border border-line bg-surface-card p-5 shadow-soft sm:p-8",
          viewTransition ? "[view-transition-name:v2-stage]" : "motion-ok:animate-v2-enter",
        )}
      >
        <header className="flex flex-col gap-2">
          <p className="type-data text-primary-ink">{stepOf}</p>
          <h1 id={`stage-title-${stepId}`} className="type-h1 text-ink">
            {title}
          </h1>
          {description ? <p className="max-w-prose type-body text-ink-muted">{description}</p> : null}
        </header>
        <div className="mt-6 flex flex-col gap-6">{children}</div>
        {back || primary ? (
          <footer className="mt-8 flex flex-col-reverse gap-3 border-t border-line-soft pt-5 sm:flex-row sm:items-start sm:justify-between">
            <div className="flex">{back}</div>
            <div className="flex flex-col gap-2 sm:items-end">
              <div className="flex flex-col sm:flex-row">{primary}</div>
              {next ? <WhatNext className="sm:max-w-xs sm:text-right">{next}</WhatNext> : null}
            </div>
          </footer>
        ) : null}
      </section>
    </div>
  );
}
