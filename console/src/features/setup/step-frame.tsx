import { ArrowLeftIcon, ArrowRightIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { SetupState } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { WhatNext } from "@/components/v2/panel";
import { strings } from "@/lib/strings";
import type { SetupMode } from "./wizard";

export interface StepProps {
  setup: SetupState;
  mode: SetupMode;
  // The agent accepted the step. The wizard stores the state and opens the next page.
  onDone: (setup: SetupState) => void;
  onBack: (() => void) | null;
  /** "What happens next" under the primary button (console/design.md, Stage). */
  next?: string;
}

// The footer of a step in the Stage (console/design.md section 8.2): Back at the left (ghost), the primary button at
// the right with "what happens next" under it. On a phone the primary button is first and full width, Back is last.
// The footer is in the form of the step, so the primary button submits it.
export function StepActions({
  onBack,
  busy = false,
  disabled = false,
  submitLabel = strings.common.continue,
  extra,
  next,
}: {
  onBack: (() => void) | null;
  busy?: boolean;
  disabled?: boolean;
  submitLabel?: string;
  extra?: ReactNode;
  next?: ReactNode;
  /** Kept for the callers of v1: the Stage footer is always in view at the end of the step. */
  sticky?: boolean;
}) {
  return (
    <div
      className="mt-2 flex flex-col-reverse gap-3 border-t border-line-soft pt-5 sm:flex-row sm:items-start sm:justify-between"
      data-testid="step-actions"
    >
      <div className="flex flex-col-reverse gap-2 sm:flex-row sm:items-center">
        <Button
          type="button"
          variant="ghost"
          className="no-underline"
          onClick={onBack ?? undefined}
          disabled={!onBack || busy}
        >
          <ArrowLeftIcon data-icon="inline-start" />
          {strings.common.back}
        </Button>
        {extra}
      </div>
      <div className="flex flex-col gap-2 sm:items-end">
        <Button
          type="submit"
          size="lg"
          className="w-full sm:w-auto"
          disabled={busy || disabled}
          data-testid="step-submit"
        >
          {busy ? <Spinner data-icon="inline-start" /> : null}
          {submitLabel}
          {busy ? null : <ArrowRightIcon data-icon="inline-end" />}
        </Button>
        {next ? <WhatNext className="sm:max-w-xs sm:text-right">{next}</WhatNext> : null}
      </div>
    </div>
  );
}

export function StepIntro({ children }: { children: ReactNode }) {
  return <p className="max-w-prose type-body text-ink-muted">{children}</p>;
}
