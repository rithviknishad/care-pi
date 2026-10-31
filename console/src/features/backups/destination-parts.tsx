import { useId, useState } from "react";
import type { DestinationRetention } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import {
  INTERVAL_CHOICES,
  INTERVAL_DEFAULT_MIN,
  RETENTION_DEFAULTS,
  effectiveRetention,
  formatRetention,
  formatRpo,
  rpoMinutes,
} from "./backups-format";
import { CHOICES, type Choice } from "./destination-meta";

// "Where should backups go?" (plans/02 section 7): 3 big choices. A choice opens its form.
export function DestinationChoices({ value, onChange }: { value: Choice | null; onChange: (c: Choice) => void }) {
  return (
    <div role="radiogroup" aria-label={strings.backups.destTitle} className="grid gap-3 sm:grid-cols-3">
      {CHOICES.map(({ id, icon: Icon }) => {
        const checked = value === id;
        return (
          <button
            key={id}
            type="button"
            role="radio"
            aria-checked={checked}
            onClick={() => onChange(id)}
            className={cn(
              "flex min-h-28 flex-col items-start gap-2 rounded-lg border p-4 text-left outline-none hover:bg-muted-background focus-visible:outline-2 focus-visible:outline-indigo-500",
              checked && "border-primary bg-primary-100/60 ring-1 ring-primary dark:bg-primary-400/10",
            )}
            data-testid={`dest-choice-${id}`}
          >
            <Icon className="size-6 text-primary" aria-hidden="true" />
            <span className="font-semibold">{strings.backups.choices[id]}</span>
            <span className="text-xs text-soft-foreground">{strings.backups.choiceHelp[id]}</span>
          </button>
        );
      })}
    </div>
  );
}

export interface ScheduleValue {
  interval_min?: number | undefined;
  retention?: DestinationRetention | undefined;
}

const KEEP_FIELDS: { key: keyof DestinationRetention; label: string; max: number }[] = [
  { key: "keep_within_days", label: strings.dest.keepWithin, max: 3650 },
  { key: "keep_daily", label: strings.dest.keepDaily, max: 3650 },
  { key: "keep_weekly", label: strings.dest.keepWeekly, max: 520 },
  { key: "keep_monthly", label: strings.dest.keepMonthly, max: 120 },
];

// The interval and the retention of a destination, with the defaults of the agent and the RPO that they give.
// A value that is the default stays out of config.yaml (the agent fills in the defaults).
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
  const interval = value.interval_min ?? INTERVAL_DEFAULT_MIN;
  const retention = effectiveRetention(value.retention);
  const [open, setOpen] = useState(false);
  const setKeep = (key: keyof DestinationRetention, raw: string) => {
    const n = Math.max(0, Math.min(10_000, Math.round(Number(raw) || 0)));
    const next = { ...retention, [key]: n };
    const same = KEEP_FIELDS.every((f) => next[f.key] === RETENTION_DEFAULTS[f.key]);
    onChange({ ...value, retention: same ? undefined : next });
  };
  return (
    <div className="flex flex-col gap-2" data-testid="dest-schedule">
      <FormField id={`${id}-interval`} label={strings.dest.interval} help={strings.dest.intervalHelp}>
        <NativeSelect
          id={`${id}-interval`}
          value={String(interval)}
          onChange={(e) => {
            const n = Number(e.target.value);
            onChange({ ...value, interval_min: n === INTERVAL_DEFAULT_MIN ? undefined : n });
          }}
          className="max-w-48"
        >
          {INTERVAL_CHOICES.map((m) => (
            <NativeSelectOption key={m} value={String(m)}>
              {strings.dest.intervals[m] ?? `${m} min`}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </FormField>
      <p className="text-sm" data-testid="dest-rpo">
        {fmt(strings.dest.rpo, { rpo: formatRpo(rpoMinutes(interval, walTimeoutMin)) })}
      </p>
      <div className="text-sm">
        <span className="text-soft-foreground">{strings.dest.retention}: </span>
        <span>{formatRetention(retention)}</span>
      </div>
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger className="text-sm text-blue-700 underline underline-offset-4 dark:text-blue-400">
          {strings.dest.retentionEdit}
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div className="mt-2 grid gap-x-4 sm:grid-cols-2">
            {KEEP_FIELDS.map((f) => (
              <FormField key={f.key} id={`${id}-${f.key}`} label={f.label}>
                <Input
                  id={`${id}-${f.key}`}
                  type="number"
                  inputMode="numeric"
                  min={0}
                  max={f.max}
                  value={String(retention[f.key])}
                  onChange={(e) => setKeep(f.key, e.target.value)}
                  className="max-w-32"
                />
              </FormField>
            ))}
          </div>
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}
