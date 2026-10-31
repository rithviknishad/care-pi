import type { ReactNode } from "react";
import type { SetupState } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { strings } from "@/lib/strings";
import type { SetupMode } from "./wizard";

export interface StepProps {
  setup: SetupState;
  mode: SetupMode;
  // The agent accepted the step. The wizard stores the state and opens the next page.
  onDone: (setup: SetupState) => void;
  onBack: (() => void) | null;
}

export function StepActions({
  onBack,
  busy = false,
  disabled = false,
  submitLabel = strings.common.continue,
  extra,
  sticky = false,
}: {
  onBack: (() => void) | null;
  busy?: boolean;
  disabled?: boolean;
  submitLabel?: string;
  extra?: ReactNode;
  // sticky keeps the buttons in view on a long step. Below 640 px (sm) they are in a fixed bar at the bottom of
  // the window, and a spacer keeps the end of the page above it. From 640 px the bar is sticky at the bottom of
  // the card (the card of the step must not clip its content: overflow visible).
  sticky?: boolean;
}) {
  if (sticky) {
    return (
      <>
        <div className="h-16 sm:hidden" aria-hidden="true" />
        <div
          className="z-20 flex flex-wrap items-center gap-2 border-t max-sm:fixed max-sm:inset-x-0 max-sm:bottom-0 max-sm:bg-background max-sm:px-4 max-sm:pt-3 max-sm:pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:sticky sm:bottom-0 sm:-mb-6 sm:bg-card sm:pt-4 sm:pb-6"
          data-testid="step-actions"
        >
          <Button type="button" variant="outline" onClick={onBack ?? undefined} disabled={!onBack || busy}>
            {strings.common.back}
          </Button>
          {extra}
          <Button type="submit" className="ml-auto" disabled={busy || disabled} data-testid="step-submit">
            {busy ? <Spinner data-icon="inline-start" /> : null}
            {submitLabel}
          </Button>
        </div>
      </>
    );
  }
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 border-t pt-4" data-testid="step-actions">
      <Button type="button" variant="outline" onClick={onBack ?? undefined} disabled={!onBack || busy}>
        {strings.common.back}
      </Button>
      {extra}
      <Button type="submit" className="ml-auto" disabled={busy || disabled} data-testid="step-submit">
        {busy ? <Spinner data-icon="inline-start" /> : null}
        {submitLabel}
      </Button>
    </div>
  );
}

export function StepIntro({ children }: { children: ReactNode }) {
  return <p className="text-sm text-soft-foreground">{children}</p>;
}
