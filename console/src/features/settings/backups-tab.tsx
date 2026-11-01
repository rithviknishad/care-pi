import { useId, useState } from "react";
import type { Config } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { formatMinutes } from "@/features/backups/backups-format";
import { fmt, strings } from "@/lib/strings";
import { ConfigChange } from "./config-change";

const st = strings.settings;
const CALENDAR = /^[A-Za-z0-9 :*,./-]{1,64}$/;

type Schedule = { db_full: string; db_incr: string; files: string; verify: string; drill: string };
interface BackupsValue {
  wal: string;
  schedule: Schedule;
  dbFull: string;
  daily: string;
  weekly: string;
  alertAfter: string;
  paused: boolean;
  drillEnabled: boolean;
}

function fromConfig(config: Config): BackupsValue {
  const b = config.backups ?? {};
  return {
    wal: String(b.wal_archive_timeout_min ?? 5),
    schedule: {
      db_full: b.schedule?.db_full ?? "Sun 01:00",
      db_incr: b.schedule?.db_incr ?? "daily 02:00",
      files: b.schedule?.files ?? "daily 02:30",
      verify: b.schedule?.verify ?? "Sat 03:00",
      drill: b.schedule?.drill ?? "*-*-01 04:00",
    },
    dbFull: String(b.retention?.db_full ?? 2),
    daily: String(b.retention?.files_keep_daily ?? 14),
    weekly: String(b.retention?.files_keep_weekly ?? 8),
    alertAfter: String(b.alert_after_hours ?? 24),
    paused: b.paused ?? false,
    drillEnabled: b.drill_enabled ?? true,
  };
}

function intIn(raw: string, min: number, max: number): number | null {
  const n = Number(raw);
  return /^\d+$/.test(raw) && n >= min && n <= max ? n : null;
}

const RANGES = {
  wal: [1, 60],
  dbFull: [1, 52],
  daily: [0, 3650],
  weekly: [0, 520],
  alertAfter: [1, 720],
} as const;

function validateBackups(v: BackupsValue): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const [k, [min, max]] of Object.entries(RANGES)) {
    if (intIn(v[k as keyof typeof RANGES], min, max) === null) errors[k] = fmt(st.range, { min, max });
  }
  for (const [k, cal] of Object.entries(v.schedule)) if (!CALENDAR.test(cal)) errors[k] = st.calendarInvalid;
  return errors;
}

// Settings > Backups: the schedule, the retention, the archive timeout, and the pause (backups.* of config.yaml,
// docs/config.md). The destinations stay as they are: the Backups page changes them.
export function BackupsTab({ config }: { config: Config }) {
  const id = useId();
  const [v, setV] = useState<BackupsValue>(() => fromConfig(config));
  const errors = validateBackups(v);
  const destinations = config.backups?.destinations ?? [];
  const minInterval = destinations.length ? Math.min(...destinations.map((d) => d.interval_min ?? 15)) : null;
  const wal = intIn(v.wal, 1, 60);
  const num = (k: keyof typeof RANGES, label: string, help?: string) => (
    <FormField id={`${id}-${k}`} label={label} help={help} error={errors[k]}>
      <Input
        id={`${id}-${k}`}
        type="number"
        inputMode="numeric"
        min={RANGES[k][0]}
        max={RANGES[k][1]}
        value={v[k]}
        onChange={(e) => setV({ ...v, [k]: e.target.value })}
        className="max-w-32"
        aria-invalid={errors[k] ? true : undefined}
      />
    </FormField>
  );
  const cal = (k: keyof Schedule, label: string) => (
    <FormField id={`${id}-${k}`} label={label} error={errors[k]}>
      <Input
        id={`${id}-${k}`}
        value={v.schedule[k]}
        onChange={(e) => setV({ ...v, schedule: { ...v.schedule, [k]: e.target.value } })}
        className="max-w-56 font-mono"
        spellCheck={false}
        aria-invalid={errors[k] ? true : undefined}
      />
    </FormField>
  );
  return (
    <ConfigChange
      valid={Object.keys(errors).length === 0}
      edit={(file) => ({
        ...file,
        backups: {
          ...file.backups,
          wal_archive_timeout_min: Number(v.wal),
          schedule: { ...file.backups?.schedule, ...v.schedule },
          retention: {
            ...file.backups?.retention,
            db_full: Number(v.dbFull),
            files_keep_daily: Number(v.daily),
            files_keep_weekly: Number(v.weekly),
          },
          alert_after_hours: Number(v.alertAfter),
          paused: v.paused,
          drill_enabled: v.drillEnabled,
        },
      })}
    >
      <div className="flex flex-col gap-4" data-testid="backups-settings">
        <p className="text-sm text-soft-foreground">{st.backupsIntro}</p>
        {num("wal", st.walTimeout, st.walTimeoutHelp)}
        <p className="text-sm" data-testid="settings-rpo">
          {wal
            ? minInterval
              ? fmt(st.rpoNow, { local: formatMinutes(wal), offsite: formatMinutes(wal + minInterval) })
              : fmt(st.rpoNoOffsite, { local: formatMinutes(wal) })
            : "\u00a0"}
        </p>
        <fieldset className="flex flex-col">
          <legend className="mb-1 font-semibold">{st.scheduleTitle}</legend>
          <p className="mb-3 text-xs text-soft-foreground">{st.calendarHelp}</p>
          <div className="grid gap-x-4 sm:grid-cols-2">
            {cal("db_full", st.dbFull)}
            {cal("db_incr", st.dbIncr)}
            {cal("files", st.filesCopy)}
            {cal("verify", st.verify)}
            {cal("drill", st.drill)}
          </div>
          <Label className="flex items-center gap-3 font-normal">
            <Switch checked={v.drillEnabled} onCheckedChange={(c) => setV({ ...v, drillEnabled: c })} />
            {st.drillEnabled}
          </Label>
        </fieldset>
        <fieldset className="flex flex-col">
          <legend className="mb-3 font-semibold">{st.retentionTitle}</legend>
          <div className="grid gap-x-4 sm:grid-cols-2">
            {num("dbFull", st.dbFullKeep, st.dbFullKeepHelp)}
            {num("alertAfter", st.alertAfter, st.alertAfterHelp)}
            {num("daily", st.filesDaily)}
            {num("weekly", st.filesWeekly)}
          </div>
        </fieldset>
        <Label className="flex items-start gap-3 font-normal">
          <Switch checked={v.paused} onCheckedChange={(c) => setV({ ...v, paused: c })} />
          <span className="flex flex-col">
            <span>{st.paused}</span>
            <span className="text-xs text-soft-foreground">{st.pausedHelp}</span>
          </span>
        </Label>
      </div>
    </ConfigChange>
  );
}
