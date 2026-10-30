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
}: {
  onBack: (() => void) | null;
  busy?: boolean;
  disabled?: boolean;
  submitLabel?: string;
  extra?: ReactNode;
}) {
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 border-t pt-4">
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
