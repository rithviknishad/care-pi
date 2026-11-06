import { useId, useState } from "react";
import type { DestinationRetention } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { ChoiceCards } from "@/components/v2/choice-cards";
import { Explainer } from "@/components/v2/explainer";
import { fmt, strings } from "@/lib/strings";
import {
  FREQUENCY_CHOICES,
  INTERVAL_DEFAULT_MIN,
  RETENTION_DEFAULTS,
  RETENTION_PRESETS,
  effectiveRetention,
  formatRpoMinutes,
  retentionPreset,
  rpoMinutes,
  type RetentionPresetName,
} from "./backups-format";
import { CHOICES, type Choice } from "./destination-meta";

// "Where should the copies go?" (plans/02 section 7): 3 big choices (ChoiceCards). A choice opens its form.
export function DestinationChoices({ value, onChange }: { value: Choice | null; onChange: (c: Choice) => void }) {
  const id = useId();
  return (
    <ChoiceCards
      name={`${id}-dest`}
      legend={<span className="sr-only">{strings.backups.destTitle}</span>}
      value={value}
      onChange={(v) => onChange(v as Choice)}
      columns={3}
      choices={CHOICES.map(({ id: c, icon }) => ({
        value: c,
        title: strings.backups.choices[c] ?? c,
        fit: strings.backups.choiceHelp[c] ?? "",
        icon,
        recommended: c === "usb",
        testId: `dest-choice-${c}`,
      }))}
    />
  );
}

export interface ScheduleValue {
  interval_min?: number | undefined;
  retention?: DestinationRetention | undefined;
}

const KEEP_FIELDS: { key: keyof DestinationRetention; max: number }[] = [
  { key: "keep_within_days", max: 3650 },
  { key: "keep_daily", max: 3650 },
  { key: "keep_weekly", max: 520 },
  { key: "keep_monthly", max: 120 },
];

// The schedule of a destination (S66, item 2): "How often we save a copy" (a frequency), "How far back you can go"
// (Standard, Longer, or Custom with the 4 numbers), the RPO in one sentence, and the Explainer that draws the policy
// and says it in plain words. It updates live. A value that is the default of the agent stays out of config.yaml.
export function DestinationSchedule({
  value,
  onChange,
  walTimeoutMin,
}: {
  value: ScheduleValue;
  onChange: (v: ScheduleValue) => void;
  walTimeoutMin: number | undefined;
}) {
  const id = useId();
  const d = strings.dest;
  const interval = value.interval_min ?? INTERVAL_DEFAULT_MIN;
  const retention = effectiveRetention(value.retention);
  const [customOpen, setCustomOpen] = useState(false);
  const preset: RetentionPresetName = customOpen ? "custom" : retentionPreset(retention);
  const frequencies = FREQUENCY_CHOICES.includes(interval) ? FREQUENCY_CHOICES : [...FREQUENCY_CHOICES, interval];
  const setRetention = (next: Required<DestinationRetention>) => {
    const same = KEEP_FIELDS.every((f) => next[f.key] === RETENTION_DEFAULTS[f.key]);
    onChange({ ...value, retention: same ? undefined : next });
  };
  const setKeep = (key: keyof DestinationRetention, raw: string) => {
    const n = Math.max(0, Math.min(10_000, Math.round(Number(raw) || 0)));
    setRetention({ ...retention, [key]: n });
  };
  return (
    <div className="flex min-w-0 flex-col gap-6" data-testid="dest-schedule">
      <fieldset className="flex min-w-0 flex-col gap-2">
        <legend className="mb-1 type-h3 text-ink">{d.interval}</legend>
        <p className="-mt-1 type-small text-ink-muted">{d.intervalHelp}</p>
        <ToggleGroup
          variant="outline"
          value={[String(interval)]}
          onValueChange={(v) => {
            const n = Number(v[0]);
            if (!n) return;
            onChange({ ...value, interval_min: n === INTERVAL_DEFAULT_MIN ? undefined : n });
          }}
          aria-label={d.interval}
          className="flex w-full flex-wrap justify-start"
          data-testid="dest-interval"
        >
          {frequencies.map((m) => (
            <ToggleGroupItem key={m} value={String(m)} className="min-w-0 flex-1 sm:flex-none">
              {d.intervals[m] ?? `${m} min`}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <p className="min-h-6 type-body font-medium text-ink" data-testid="dest-rpo" aria-live="polite">
          {fmt(d.rpoSentence, { minutes: formatRpoMinutes(rpoMinutes(interval, walTimeoutMin)) })}
        </p>
      </fieldset>
      <div className="flex min-w-0 flex-col gap-3">
        <ChoiceCards
          name={`${id}-keep`}
          legend={d.howFar}
          description={d.howFarHelp}
          value={preset}
          columns={3}
          onChange={(v) => {
            if (v === "custom") {
              setCustomOpen(true);
              return;
            }
            setCustomOpen(false);
            const next = RETENTION_PRESETS[v as Exclude<RetentionPresetName, "custom">];
            if (next) setRetention(next);
          }}
          choices={(["standard", "longer", "custom"] as const).map((p) => ({
            value: p,
            title: d.retentionPresets[p] ?? p,
            fit: d.retentionFit[p] ?? "",
            recommended: p === "standard",
            testId: `dest-keep-${p}`,
          }))}
        />
        {preset === "custom" ? (
          <div className="grid gap-x-4 sm:grid-cols-2" data-testid="dest-keep-custom">
            {KEEP_FIELDS.map((f) => (
              <FormField key={f.key} id={`${id}-${f.key}`} label={d.keepFor[f.key] ?? f.key}>
                <Input
                  id={`${id}-${f.key}`}
                  type="number"
                  inputMode="numeric"
                  min={0}
                  max={f.max}
                  value={String(retention[f.key])}
                  onChange={(e) => setKeep(f.key, e.target.value)}
                  className="max-w-32 type-data"
                />
              </FormField>
            ))}
          </div>
        ) : null}
      </div>
      <Explainer policy={{ interval_min: interval, ...retention }} testId="dest-explainer" />
    </div>
  );
}
