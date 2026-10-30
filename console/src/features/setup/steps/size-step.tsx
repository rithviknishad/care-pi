import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, call } from "@/api/client";
import { useConfigSchema, usePresets } from "@/api/queries";
import type { SizeStep as SizeValue } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { strings } from "@/lib/strings";
import { SizeForm } from "../size-form";
import { validateSize } from "../size-logic";
import { StepActions, StepIntro, type StepProps } from "../step-frame";
import { presetFromAnswers, answersFromPreset } from "../wizard";

export function SizeStep({ setup, mode, onDone, onBack }: StepProps) {
  const presets = usePresets();
  const schema = useConfigSchema();
  const [value, setValue] = useState<SizeValue>(() => setup.draft.size ?? { preset: "small-clinic" });
  const [touched, setTouched] = useState(false);
  const errors = touched ? validateSize(value, schema.data) : {};
  const save = useMutation({
    mutationFn: (body: SizeValue) => call(api.PUT("/setup/steps/size", { body })),
    onSuccess: onDone,
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setTouched(true);
    // The easy path sends the preset of the answers, also when the operator did not change an answer.
    const body =
      mode === "easy" && presets.data
        ? { preset: presetFromAnswers(answersFromPreset(value.preset), presets.data.host_ram_bytes).preset }
        : value;
    if (Object.keys(validateSize(body, schema.data)).length === 0) save.mutate(body);
  };

  const warnings = setup.steps.find((s) => s.id === "size")?.warnings ?? [];
  return (
    <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
      <StepIntro>{strings.size.intro}</StepIntro>
      <SizeForm
        mode={mode}
        step={value}
        onChange={setValue}
        presets={presets.data}
        schema={schema.data}
        errors={errors}
      />
      {warnings.map((w) => (
        <p key={w.code} className="text-sm text-amber-700 dark:text-amber-300">
          {w.message}
        </p>
      ))}
      {presets.error ? <ProblemAlert error={presets.error} /> : null}
      {save.error ? <ProblemAlert error={save.error} /> : null}
      <StepActions onBack={onBack} busy={save.isPending} disabled={!presets.data} />
    </form>
  );
}
