import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Illustration } from "./illustration";
import type { IllustrationName } from "./illustrations";
import { StatusChip } from "./status-chip";
import { v2Strings } from "./strings";

export interface Choice {
  value: string;
  title: string;
  /** One line: who or what this choice fits. "For 1 or 2 people at a time." */
  fit: ReactNode;
  icon?: LucideIcon;
  illustration?: IllustrationName;
  recommended?: boolean;
  disabled?: boolean;
  /** A line of data under the fit (for example "2 GB memory"). Geist Mono. */
  meta?: ReactNode;
  testId?: string;
}

export interface ChoiceCardsProps {
  name: string;
  legend: ReactNode;
  description?: ReactNode;
  choices: Choice[];
  value: string | null;
  onChange: (value: string) => void;
  /** The columns from 640 px. A phone has 1 column. */
  columns?: 1 | 2 | 3;
  recommendedLabel?: string;
  className?: string;
}

const COLS = { 1: "", 2: "sm:grid-cols-2", 3: "sm:grid-cols-2 lg:grid-cols-3" };

// Big radio cards (the presets of the easy setup, S65; the backup destinations). Native radio inputs: the arrow
// keys move the choice, and each screen reader knows them. The selected card has a primary border and the
// tint; the border width does not change (a ring), so the selection moves nothing.
export function ChoiceCards({
  name,
  legend,
  description,
  choices,
  value,
  onChange,
  columns = 3,
  recommendedLabel = v2Strings.recommended,
  className,
}: ChoiceCardsProps) {
  return (
    <fieldset className={cn("flex min-w-0 flex-col gap-3", className)}>
      <legend className="mb-1 type-h3 text-ink">{legend}</legend>
      {description ? <p className="-mt-1 type-small text-ink-muted">{description}</p> : null}
      <div className={cn("grid gap-3", COLS[columns], choices.some((c) => c.recommended) && "pt-3")}>
        {choices.map((c) => {
          const Icon = c.icon;
          return (
            <label
              key={c.value}
              data-value={c.value}
              data-testid={c.testId}
              className={cn(
                "group relative flex min-h-24 cursor-pointer gap-4 rounded-card border border-line bg-surface-card p-4 shadow-soft outline-none",
                "motion-ok:transition-[background-color,box-shadow,border-color] motion-ok:duration-(--duration-fast)",
                "hover:border-line-stronger hover:bg-surface-tint",
                "has-checked:border-primary-solid has-checked:bg-surface-tint has-checked:ring-1 has-checked:ring-primary-solid",
                "has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-indigo-500",
                "has-disabled:cursor-not-allowed has-disabled:opacity-60",
              )}
            >
              <input
                type="radio"
                name={name}
                value={c.value}
                checked={value === c.value}
                disabled={c.disabled}
                onChange={() => onChange(c.value)}
                className="peer sr-only"
              />
              {c.recommended ? (
                // On the top edge of the card, so it takes no width from the text.
                <StatusChip tone="primary" size="sm" className="absolute -top-3 left-4">
                  {recommendedLabel}
                </StatusChip>
              ) : null}
              {c.illustration ? (
                <Illustration name={c.illustration} size="sm" className="w-16" />
              ) : Icon ? (
                <span className="grid size-11 shrink-0 place-items-center rounded-lg bg-surface-tint-strong text-primary-solid">
                  <Icon className="size-5" aria-hidden="true" />
                </span>
              ) : null}
              <span className="flex min-w-0 flex-1 flex-col gap-1 pr-7">
                <span className="type-body font-semibold text-ink">{c.title}</span>
                <span className="type-small text-ink-muted">{c.fit}</span>
                {c.meta ? <span className="type-data text-ink-soft">{c.meta}</span> : null}
              </span>
              <span
                aria-hidden="true"
                className="border-line-input absolute top-4 right-4 grid size-5 place-items-center rounded-full border-2 bg-surface-card peer-checked:border-primary-solid"
              >
                <span className="size-2.5 scale-0 rounded-full bg-primary-solid group-has-checked:scale-100 motion-ok:transition-transform motion-ok:duration-(--duration-fast)" />
              </span>
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}
