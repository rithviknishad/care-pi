import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, call } from "@/api/client";
import { useConfigSchema, usePresets } from "@/api/queries";
import type { PresetList, SizeStep as SizeValue } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { ProblemCard } from "@/components/v2/problem-card";
import { strings } from "@/lib/strings";
import { SizeForm } from "../size-form";
import { validateSize } from "../size-logic";
import { StepActions, type StepProps } from "../step-frame";

// The preset of the draft, else the preset that the agent recommends for this device. On the easy path a preset with
// a warning (busy-counter on 2 GB) is not a choice: the recommended preset takes its place.
function initialPreset(draft: SizeValue | null | undefined, presets: PresetList | undefined, easy: boolean): SizeValue {
  const recommended = presets?.items.find((p) => p.recommended)?.name ?? presets?.default ?? "small-clinic";
  if (!draft?.preset) return { preset: recommended };
  const item = presets?.items.find((p) => p.name === draft.preset);
  if (easy && (draft.preset === "custom" || item?.warning)) return { preset: recommended };
  return draft;
}

// The size step (S65). Easy: the 3 preset cards. Advanced: the presets and the knobs with the "≈ preset" hint.
export function SizeStep({ setup, mode, onDone, onBack, next }: StepProps) {
  const presets = usePresets();
  const schema = useConfigSchema();
  const [chosen, setChosen] = useState<SizeValue | null>(null);
  const value = chosen ?? initialPreset(setup.draft.size, presets.data, mode === "easy");
  const [touched, setTouched] = useState(false);
  const errors = touched ? validateSize(value, schema.data) : {};
  const save = useMutation({
    mutationFn: (body: SizeValue) => call(api.PUT("/setup/steps/size", { body })),
    onSuccess: onDone,
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setTouched(true);
    const body = mode === "easy" ? { preset: value.preset } : value;
    if (Object.keys(validateSize(body, schema.data)).length === 0) save.mutate(body);
  };

  const warnings = (setup.steps.find((s) => s.id === "size")?.warnings ?? []).filter(
    (w) => !(mode === "easy" && w.code === "busy-counter-low-ram" && value.preset !== "busy-counter"),
  );
  return (
    <form className="flex flex-col gap-6" noValidate onSubmit={submit}>
      <SizeForm
        key={presets.data ? "ready" : "loading"}
        mode={mode}
        step={value}
        onChange={setChosen}
        presets={presets.data}
        schema={schema.data}
        errors={errors}
      />
      {warnings.map((w) => (
        <ProblemCard
          key={w.code}
          tone="warn"
          title={strings.presets.warnings[w.code] ?? w.message}
          technical={`${w.code}: ${w.message}`}
        />
      ))}
      {presets.error ? <ProblemAlert error={presets.error} onRetry={() => void presets.refetch()} /> : null}
      {save.error ? <ProblemAlert error={save.error} /> : null}
      <StepActions onBack={onBack} busy={save.isPending} disabled={!presets.data} next={next} />
    </form>
  );
}
