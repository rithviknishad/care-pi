import { BuildingIcon, SlidersHorizontalIcon, UserRoundIcon, UsersRoundIcon, type LucideIcon } from "lucide-react";
import { useId, useState } from "react";
import type { Preset, PresetList, PresetName, SizeStep } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { ChoiceCards, type Choice } from "@/components/v2/choice-cards";
import { Panel } from "@/components/v2/panel";
import { StatusChip } from "@/components/v2/status-chip";
import { fmt, strings } from "@/lib/strings";
import { CUSTOM_KEYS, customRanges, nearestPreset, type CustomKey } from "./size-logic";
import type { SetupMode } from "./wizard";

const p = strings.presets;

const ICONS: Record<PresetName, LucideIcon> = {
  solo: UserRoundIcon,
  "small-clinic": UsersRoundIcon,
  "busy-counter": BuildingIcon,
  custom: SlidersHorizontalIcon,
};

const CUSTOM_LABELS: Record<CustomKey, string> = {
  gunicorn_workers: strings.size.workers,
  celery_concurrency: strings.size.concurrency,
  pg_shared_buffers_mb: strings.size.buffers,
};

// The text of a warning code of a preset (busy-counter-low-ram), or the code for a code that the console does not know.
function warningText(code: string | null, ramBytes: number): string | null {
  if (!code) return null;
  // The memory in whole GB, the unit of the fit texts ("A 2 GB device is enough").
  if (code === "busy-counter-low-ram")
    return fmt(p.lowRam, { ram: `${Math.max(1, Math.round(ramBytes / 2 ** 30))} GB` });
  return p.warnings[code] ?? code;
}

function choiceOf(preset: Preset, ramBytes: number, withFacts: boolean): Choice {
  const warning = warningText(preset.warning, ramBytes);
  const facts =
    withFacts && preset.name !== "custom"
      ? `${preset.gunicorn_workers} · ${preset.celery_concurrency} · ${preset.pg_shared_buffers_mb} MB`
      : null;
  return {
    value: preset.name,
    title: p.titles[preset.name] ?? preset.name,
    fit: warning ? <span className="text-warn-fg">{warning}</span> : (p.fit[preset.name] ?? ""),
    icon: ICONS[preset.name],
    recommended: preset.recommended,
    disabled: Boolean(warning),
    meta: facts ?? p.meta[preset.name],
  };
}

// The easy size step (S65): the 3 preset cards with plain words and "Recommended" from GET /config/presets. A preset
// with a warning for this device (busy-counter on 2 GB) is dimmed with the reason.
function EasySize({
  step,
  onChange,
  presets,
}: {
  step: SizeStep;
  onChange: (s: SizeStep) => void;
  presets: PresetList;
}) {
  const id = useId();
  const choices = presets.items
    .filter((x) => x.name !== "custom")
    .map((x) => choiceOf(x, presets.host_ram_bytes, false));
  return (
    <div data-testid="size-presets">
      <ChoiceCards
        name={`${id}-preset`}
        legend={p.legend}
        choices={choices}
        value={step.preset ?? null}
        onChange={(v) => onChange({ preset: v as PresetName })}
        columns={1}
        recommendedLabel={strings.size.recommended}
      />
    </div>
  );
}

// The advanced size step: start from a preset, then change each setting. The hint says which preset the values are
// near ("≈ Small clinic").
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
  const fromPreset = (name: PresetName): Record<CustomKey, string> => {
    const preset = presets.items.find((x) => x.name === name);
    return {
      gunicorn_workers: String(preset?.gunicorn_workers ?? step.custom?.gunicorn_workers ?? 2),
      celery_concurrency: String(preset?.celery_concurrency ?? step.custom?.celery_concurrency ?? 1),
      pg_shared_buffers_mb: String(preset?.pg_shared_buffers_mb ?? step.custom?.pg_shared_buffers_mb ?? 128),
    };
  };
  const [knobs, setKnobs] = useState<Record<CustomKey, string>>(() =>
    current === "custom"
      ? {
          gunicorn_workers: String(step.custom?.gunicorn_workers ?? 2),
          celery_concurrency: String(step.custom?.celery_concurrency ?? 1),
          pg_shared_buffers_mb: String(step.custom?.pg_shared_buffers_mb ?? 128),
        }
      : fromPreset(current),
  );
  const n = (v: string) => (v.trim() === "" ? Number.NaN : Number(v));
  const values = {
    gunicorn_workers: n(knobs.gunicorn_workers),
    celery_concurrency: n(knobs.celery_concurrency),
    pg_shared_buffers_mb: n(knobs.pg_shared_buffers_mb),
  };
  const near = nearestPreset(values, presets.items);
  const choices = presets.items.map((x) => choiceOf(x, presets.host_ram_bytes, true));
  return (
    <div className="flex flex-col gap-6" data-testid="size-advanced">
      <ChoiceCards
        name={`${id}-preset`}
        legend={p.advancedLegend}
        choices={choices.map((c) => ({ ...c, disabled: false }))}
        value={current}
        onChange={(v) => {
          const name = v as PresetName;
          if (name === "custom") {
            onChange({ preset: "custom", custom: values });
            return;
          }
          setKnobs(fromPreset(name));
          onChange({ preset: name });
        }}
        columns={2}
        recommendedLabel={strings.size.recommended}
      />
      <Panel
        variant="tint"
        as="h3"
        title={p.knobs}
        action={
          <StatusChip tone={near ? "primary" : "neutral"} size="sm" testId="size-near">
            {near ? fmt(p.near, { preset: p.titles[near] ?? near }) : p.nearNone}
          </StatusChip>
        }
      >
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
                value={knobs[key]}
                className="type-data"
                onChange={(e) => {
                  const next = { ...knobs, [key]: e.target.value };
                  setKnobs(next);
                  onChange({
                    preset: "custom",
                    custom: {
                      gunicorn_workers: n(next.gunicorn_workers),
                      celery_concurrency: n(next.celery_concurrency),
                      pg_shared_buffers_mb: n(next.pg_shared_buffers_mb),
                    },
                  });
                }}
                aria-invalid={errors[key] ? true : undefined}
              />
            </FormField>
          ))}
        </div>
      </Panel>
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
      <div className="flex flex-col gap-3 pt-10" aria-busy="true">
        <Skeleton className="h-24 rounded-card" />
        <Skeleton className="h-24 rounded-card" />
        <Skeleton className="h-24 rounded-card" />
      </div>
    );
  }
  return mode === "easy" ? (
    <EasySize step={step} onChange={onChange} presets={presets} />
  ) : (
    <AdvancedSize step={step} onChange={onChange} presets={presets} schema={schema} errors={errors} />
  );
}
