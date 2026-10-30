import { useMutation } from "@tanstack/react-query";
import { InfoIcon } from "lucide-react";
import { api, call } from "@/api/client";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { strings } from "@/lib/strings";
import { StepActions, StepIntro, type StepProps } from "../step-frame";

// Phase 3 implements backups. In Phase 2 only a dev build can skip the step (docs/api/README.md section 5).
export function BackupsStep({ setup, onDone, onBack }: StepProps) {
  const step = setup.steps.find((s) => s.id === "backups");
  const canSkip = Boolean(step?.skippable && setup.dev_build);
  const skip = useMutation({
    mutationFn: () => call(api.POST("/setup/steps/{step}/skip", { params: { path: { step: "backups" } } })),
    onSuccess: onDone,
  });
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (step?.status === "skipped" || step?.status === "done") onDone(setup);
        else skip.mutate();
      }}
    >
      <StepIntro>{strings.backups.intro}</StepIntro>
      <Alert variant={canSkip ? "info" : "warning"}>
        <InfoIcon />
        <AlertDescription>{canSkip ? strings.backups.devBuild : strings.backups.releaseBuild}</AlertDescription>
      </Alert>
      {skip.error ? <ProblemAlert error={skip.error} /> : null}
      <StepActions
        onBack={onBack}
        busy={skip.isPending}
        disabled={!canSkip && step?.status !== "skipped"}
        submitLabel={step?.status === "skipped" ? strings.common.continue : strings.common.skip}
      />
    </form>
  );
}
