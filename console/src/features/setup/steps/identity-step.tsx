import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, call } from "@/api/client";
import { ProblemAlert } from "@/components/app/problem-alert";
import { IdentityForm } from "../identity-form";
import { validateIdentity, type IdentityValue } from "../identity-logic";
import { StepActions, type StepProps } from "../step-frame";

export function IdentityStep({ setup, mode, onDone, onBack, next }: StepProps) {
  const [value, setValue] = useState<IdentityValue>({
    display_name: setup.draft.identity.display_name ?? "",
    hostname: setup.draft.identity.hostname ?? "care",
  });
  const [touched, setTouched] = useState(false);
  const errors = touched ? validateIdentity(value) : {};
  const save = useMutation({
    mutationFn: (body: IdentityValue) => call(api.PUT("/setup/steps/identity", { body })),
    onSuccess: onDone,
  });
  return (
    <form
      className="flex flex-col gap-6"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        setTouched(true);
        const v = { ...value, display_name: value.display_name.trim() };
        if (Object.keys(validateIdentity(v)).length === 0) save.mutate(v);
      }}
    >
      <IdentityForm value={value} onChange={setValue} editableHostname={mode === "advanced"} errors={errors} />
      {save.error ? <ProblemAlert error={save.error} /> : null}
      <StepActions onBack={onBack} busy={save.isPending} next={next} />
    </form>
  );
}
