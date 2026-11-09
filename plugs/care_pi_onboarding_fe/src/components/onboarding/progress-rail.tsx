import {
  Check,
  CircleAlert,
  Circle,
  CircleDashed,
  SkipForward,
} from "lucide-react";

import { useTranslation } from "@/hooks/use-translation";
import { cn } from "@/lib/utils";
import type { Screen, StepStatus } from "@/lib/wizard";

const ICON = {
  done: Check,
  skipped: SkipForward,
  failed: CircleAlert,
  pending: Circle,
} as const;

export function ProgressRail({
  steps,
  screen,
  busy,
  onGo,
}: {
  steps: StepStatus[];
  screen: Screen | null;
  busy: boolean;
  onGo: (screen: Screen) => void;
}) {
  const { t } = useTranslation();
  const finished = steps.filter(
    (s) => s.state === "done" || s.state === "skipped",
  ).length;
  return (
    <nav aria-label={t("progress")} className="flex flex-col gap-3">
      <p className="text-muted-foreground text-sm" data-testid="progress-count">
        {t("progress_count", { done: finished, total: steps.length })}
      </p>
      <ol className="flex flex-col gap-1">
        {steps.map((step, index) => {
          const Icon = step.allowed ? ICON[step.state] : CircleDashed;
          const current = step.step === screen;
          return (
            <li key={step.step}>
              <button
                type="button"
                onClick={() => onGo(step.step)}
                disabled={busy || !step.allowed}
                aria-current={current ? "step" : undefined}
                data-testid={`rail-${step.step}`}
                data-state={step.state}
                className={cn(
                  "flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors",
                  current
                    ? "bg-primary-100 text-primary-900 font-semibold"
                    : "hover:bg-muted",
                  !step.allowed &&
                    "text-muted-foreground cursor-not-allowed opacity-60",
                )}
              >
                <Icon
                  aria-hidden="true"
                  className={cn(
                    "size-4 shrink-0",
                    step.state === "done" && "text-primary-700",
                    step.state === "failed" && "text-destructive",
                  )}
                />
                <span className="flex-1">
                  {index + 1}. {t(`step_${step.step}`)}
                  {step.optional && (
                    <span className="text-muted-foreground font-normal">
                      {" "}
                      ({t("optional")})
                    </span>
                  )}
                </span>
                <span className="sr-only">
                  {t(`state_${step.allowed ? step.state : "locked"}`)}
                </span>
              </button>
            </li>
          );
        })}
        <li>
          <button
            type="button"
            onClick={() => onGo("done")}
            disabled={busy}
            aria-current={screen === "done" ? "step" : undefined}
            data-testid="rail-done"
            className={cn(
              "flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm",
              screen === "done"
                ? "bg-primary-100 text-primary-900 font-semibold"
                : "hover:bg-muted",
            )}
          >
            <Check aria-hidden="true" className="size-4 shrink-0" />
            <span>{t("step_done")}</span>
          </button>
        </li>
      </ol>
    </nav>
  );
}
