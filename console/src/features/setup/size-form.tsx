import { useId, useState } from "react";
import type { Preset, PresetList, PresetName, SizeStep } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { formatMiB } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { CUSTOM_KEYS, customRanges, type CustomKey } from "./size-logic";
import { answersFromPreset, presetFromAnswers, type SetupMode, type SizeAnswers } from "./wizard";

const CUSTOM_LABELS: Record<CustomKey, string> = {
  gunicorn_workers: strings.size.workers,
  celery_concurrency: strings.size.concurrency,
  pg_shared_buffers_mb: strings.size.buffers,
};

function PresetFacts({ preset }: { preset: Preset | undefined }) {
  if (!preset || preset.name === "custom") return null;
  return (
    <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 text-xs leading-6 text-soft-foreground">
      <dt>{strings.size.workers}</dt>
      <dd className="text-right">{preset.gunicorn_workers}</dd>
      <dt>{strings.size.concurrency}</dt>
      <dd className="text-right">{preset.celery_concurrency}</dd>
      <dt>{strings.size.buffers}</dt>
      <dd className="text-right">{preset.pg_shared_buffers_mb}</dd>
      {preset.memory?.["care-api"] ? (
        <>
          <dt>care-api</dt>
          <dd className="text-right">{formatMiB(preset.memory["care-api"].max_mb)}</dd>
        </>
      ) : null}
    </dl>
  );
}

function Question({
  name,
  question,
  options,
  value,
  onChange,
}: {
  name: string;
  question: string;
  options: string[];
  value: number;
  onChange: (v: 0 | 1 | 2) => void;
}) {
  const id = useId();
  return (
    <fieldset className="flex flex-col gap-3">
      <legend className="mb-2 font-semibold">{question}</legend>
      <RadioGroup name={name} value={String(value)} onValueChange={(v) => onChange(Number(v) as 0 | 1 | 2)}>
        {options.map((label, i) => (
          <Label key={label} htmlFor={`${id}-${i}`} className="flex items-center gap-3 font-normal">
            <RadioGroupItem id={`${id}-${i}`} value={String(i)} />
            {label}
          </Label>
        ))}
      </RadioGroup>
    </fieldset>
  );
}

function EasySize({
  step,
  onChange,
  presets,
}: {
  step: SizeStep;
  onChange: (s: SizeStep) => void;
  presets: PresetList;
}) {
  const [answers, setAnswers] = useState<SizeAnswers>(() => answersFromPreset(step.preset));
  const result = presetFromAnswers(answers, presets.host_ram_bytes);
  const update = (next: SizeAnswers) => {
    setAnswers(next);
    onChange({ preset: presetFromAnswers(next, presets.host_ram_bytes).preset });
  };
  const chosen = presets.items.find((p) => p.name === result.preset);
  return (
    <div className="flex flex-col gap-6">
      <Question
        name="staff"
        question={strings.size.q1}
        options={strings.size.q1Options}
        value={answers.staff}
        onChange={(staff) => update({ ...answers, staff })}
      />
      <Question
        name="peak"
        question={strings.size.q2}
        options={strings.size.q2Options}
        value={answers.peak}
        onChange={(peak) => update({ ...answers, peak })}
      />
      <div
        className="flex flex-col gap-2 rounded-lg border bg-soft-background p-4"
        aria-live="polite"
        data-testid="size-result"
      >
        <p className="font-semibold">{fmt(strings.size.result, { preset: result.preset })}</p>
        <p className="text-sm text-soft-foreground">{strings.size.presets[result.preset]}</p>
        <p className="min-h-5 text-sm text-amber-700 dark:text-amber-300">{result.capped ? strings.size.capped : ""}</p>
        <PresetFacts preset={chosen} />
      </div>
    </div>
  );
}

function AdvancedSize({
  step,
  onChange,
  presets,
  schema,
  errors,
}: {
  step: SizeStep;
  onChange: (s: SizeStep) => void;
  presets: PresetList;
  schema: unknown;
  errors: Partial<Record<CustomKey, string>>;
}) {
  const id = useId();
  const ranges = customRanges(schema);
  const current = (step.preset ?? presets.default) as PresetName;
  const [custom, setCustom] = useState<Record<CustomKey, string>>(() => ({
    gunicorn_workers: String(step.custom?.gunicorn_workers ?? 2),
    celery_concurrency: String(step.custom?.celery_concurrency ?? 1),
    pg_shared_buffers_mb: String(step.custom?.pg_shared_buffers_mb ?? 128),
  }));
  const emit = (preset: PresetName, values = custom) => {
    if (preset !== "custom") {
      onChange({ preset });
      return;
    }
    const n = (v: string) => (v.trim() === "" ? Number.NaN : Number(v));
    onChange({
      preset,
      custom: {
        gunicorn_workers: n(values.gunicorn_workers),
        celery_concurrency: n(values.celery_concurrency),
        pg_shared_buffers_mb: n(values.pg_shared_buffers_mb),
      },
    });
  };
  return (
    <div className="flex flex-col gap-6">
      <fieldset>
        <legend className="mb-3 font-semibold">{strings.size.presetLabel}</legend>
        <RadioGroup value={current} onValueChange={(v) => emit(v as PresetName)} className="grid gap-3 sm:grid-cols-2">
          {presets.items.map((p) => (
            <Label
              key={p.name}
              htmlFor={`${id}-${p.name}`}
              className={cn(
                "flex min-h-36 cursor-pointer flex-col items-stretch gap-2 rounded-lg border p-4 font-normal",
                current === p.name && "border-primary-600 bg-primary-50 dark:bg-primary-400/10",
              )}
            >
              <span className="flex items-center gap-3">
                <RadioGroupItem id={`${id}-${p.name}`} value={p.name} />
                <span className="font-semibold">{p.name}</span>
                {p.recommended ? (
                  <Badge variant="success" size="sm">
                    {strings.size.recommended}
                  </Badge>
                ) : null}
              </span>
              <span className="text-xs text-soft-foreground">{strings.size.presets[p.name]}</span>
              {p.warning ? <span className="text-xs text-amber-700 dark:text-amber-300">{p.warning}</span> : null}
              <PresetFacts preset={p} />
            </Label>
          ))}
        </RadioGroup>
      </fieldset>
      {current === "custom" ? (
        <div className="grid gap-x-4 sm:grid-cols-3">
          {CUSTOM_KEYS.map((key) => (
            <FormField
              key={key}
              id={`${id}-${key}`}
              label={CUSTOM_LABELS[key]}
              help={fmt(strings.size.range, ranges[key])}
              error={errors[key]}
            >
              <Input
                id={`${id}-${key}`}
                type="number"
                inputMode="numeric"
                min={ranges[key].min}
                max={ranges[key].max}
                value={custom[key]}
                onChange={(e) => {
                  const next = { ...custom, [key]: e.target.value };
                  setCustom(next);
                  emit("custom", next);
                }}
                aria-invalid={errors[key] ? true : undefined}
              />
            </FormField>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function SizeForm({
  mode,
  step,
  onChange,
  presets,
  schema,
  errors,
}: {
  mode: SetupMode;
  step: SizeStep;
  onChange: (s: SizeStep) => void;
  presets: PresetList | undefined;
  schema: unknown;
  errors: Partial<Record<CustomKey, string>>;
}) {
  if (!presets) {
    return (
      <div className="flex flex-col gap-4" aria-busy="true">
        <Skeleton className="h-28" />
        <Skeleton className="h-28" />
        <Skeleton className="h-32" />
      </div>
    );
  }
  return mode === "easy" ? (
    <EasySize step={step} onChange={onChange} presets={presets} />
  ) : (
    <AdvancedSize step={step} onChange={onChange} presets={presets} schema={schema} errors={errors} />
  );
}
