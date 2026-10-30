import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, call } from "@/api/client";
import { useConfigSchema } from "@/api/queries";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { reservedNames } from "@/lib/schema";
import { strings } from "@/lib/strings";
import { RESERVED_BACKEND, RESERVED_FRONTEND } from "../env-defaults";
import { EnvForm } from "../env-form";
import { modelFromStep, stepFromModel, validateEnv, type EnvModel } from "../env-logic";
import { StepActions, StepIntro, type StepProps } from "../step-frame";

export function EnvStep({ setup, onDone, onBack }: StepProps) {
  const schema = useConfigSchema();
  const [model, setModel] = useState<EnvModel>(() => modelFromStep(setup.draft.env));
  const [touched, setTouched] = useState(false);
  const reserved = {
    backend: reservedNames(schema.data, "backend", RESERVED_BACKEND),
    frontend: reservedNames(schema.data, "frontend", RESERVED_FRONTEND),
  };
  const errors = touched ? validateEnv(model, reserved) : {};
  const save = useMutation({
    mutationFn: () => call(api.PUT("/setup/steps/env", { body: stepFromModel(model) })),
    onSuccess: onDone,
  });
  const skip = useMutation({
    mutationFn: () => call(api.POST("/setup/steps/{step}/skip", { params: { path: { step: "env" } } })),
    onSuccess: onDone,
  });
  return (
    <form
      className="flex flex-col gap-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        setTouched(true);
        if (Object.keys(validateEnv(model, reserved)).length === 0) save.mutate();
      }}
    >
      <StepIntro>{strings.env.intro}</StepIntro>
      <EnvForm model={model} onChange={setModel} errors={errors} />
      {save.error ? <ProblemAlert error={save.error} /> : null}
      {skip.error ? <ProblemAlert error={skip.error} /> : null}
      <StepActions
        onBack={onBack}
        busy={save.isPending || skip.isPending}
        extra={
          <Button
            type="button"
            variant="ghost"
            onClick={() => skip.mutate()}
            disabled={save.isPending || skip.isPending}
          >
            {strings.common.skip}
          </Button>
        }
      />
    </form>
  );
}
