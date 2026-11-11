import { useId, useState } from "react";
import type { DestinationRetention } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Explainer } from "@/components/v2/explainer";
import { fmt, strings } from "@/lib/strings";
import {
  FREQUENCY_CHOICES,
  INTERVAL_DEFAULT_MIN,
  KEEP_DAYS_MAX,
  dailyRetention,
  effectiveRetention,
  formatRpoMinutes,
  rpoMinutes,
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

// The schedule of a destination (S66, item 2): "How often we save a copy" (a frequency), "How far back you can go"
// (one box: the days to keep the daily copies), the RPO in one sentence, and the Explainer that draws the policy
// and says it in plain words. It updates live. The default frequency stays out of config.yaml. The numbers that the
// operator sets are always written: the defaults of a destination come from backups.retention, so they can change.
export function DestinationSchedule({
  value,
  onChange,
  walTimeoutMin,
  defaults,
}: {
  value: ScheduleValue;
  onChange: (v: ScheduleValue) => void;
  walTimeoutMin: number | undefined;
  /** The retention of a destination without its own numbers (retentionDefaults of the config). */
  defaults?: Required<DestinationRetention>;
}) {
  const id = useId();
  const d = strings.dest;
  const interval = value.interval_min ?? INTERVAL_DEFAULT_MIN;
  const retention = effectiveRetention(value.retention, defaults);
  const frequencies = FREQUENCY_CHOICES.includes(interval) ? FREQUENCY_CHOICES : [...FREQUENCY_CHOICES, interval];
  // The draft lets the operator clear the box and type a new number. A valid number applies at once.
  const [draft, setDraft] = useState<string | null>(null);
  const setDays = (raw: string) => {
    setDraft(raw);
    const n = Number(raw);
    if (Number.isInteger(n) && n >= 1 && n <= KEEP_DAYS_MAX) onChange({ ...value, retention: dailyRetention(n) });
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
      <FormField id={`${id}-days`} label={d.keepDays} help={d.keepDaysHelp}>
        <Input
          id={`${id}-days`}
          type="number"
          inputMode="numeric"
          min={1}
          max={KEEP_DAYS_MAX}
          value={draft ?? String(retention.keep_daily)}
          onChange={(e) => setDays(e.target.value)}
          onBlur={() => setDraft(null)}
          className="max-w-32 type-data"
          data-testid="dest-keep-days"
        />
      </FormField>
      <Explainer policy={{ interval_min: interval, ...retention }} testId="dest-explainer" />
    </div>
  );
}
