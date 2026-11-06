import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { toneText, type Tone } from "./tone";

export interface StatCardProps {
  label: ReactNode;
  icon?: LucideIcon;
  /** The number or a short value, in Geist Mono with tabular figures. */
  value?: ReactNode;
  unit?: ReactNode;
  /** One line under the value: a trend or a hint ("Last copy 12 min ago"). */
  hint?: ReactNode;
  hintTone?: Tone;
  loading?: boolean;
  /** A slot at the right of the label (a status chip). */
  action?: ReactNode;
  className?: string;
  testId?: string;
}

// A fixed-height card (8 rem) for one value. The skeleton has the same rows, so nothing moves when the value comes.
export function StatCard({
  label,
  icon: Icon,
  value,
  unit,
  hint,
  hintTone = "neutral",
  loading = false,
  action,
  className,
  testId,
}: StatCardProps) {
  return (
    <section
      data-testid={testId}
      aria-busy={loading || undefined}
      className={cn(
        "grid h-32 grid-rows-[1.75rem_2.25rem_1.25rem] content-between gap-1 overflow-hidden rounded-card border border-line bg-surface-card p-4 shadow-soft",
        className,
      )}
    >
      <header className="flex min-w-0 items-center gap-2">
        {Icon ? <Icon aria-hidden="true" className="size-4 shrink-0 text-primary-solid" /> : null}
        <h3 className="min-w-0 flex-1 truncate type-small font-medium text-ink-muted">{label}</h3>
        {action}
      </header>
      <div className="flex min-w-0 items-baseline gap-1.5">
        {loading ? (
          <Skeleton className="h-8 w-24 self-center" />
        ) : (
          <>
            <span className="truncate font-mono text-[1.75rem] leading-9 font-semibold tracking-tight text-ink tabular-nums">
              {value ?? "—"}
            </span>
            {unit ? <span className="shrink-0 type-data text-ink-soft">{unit}</span> : null}
          </>
        )}
      </div>
      <div className="min-w-0">
        {loading ? (
          <Skeleton className="h-4 w-32" />
        ) : hint ? (
          <p className={cn("truncate type-small", hintTone === "neutral" ? "text-ink-soft" : toneText[hintTone])}>
            {hint}
          </p>
        ) : null}
      </div>
    </section>
  );
}
