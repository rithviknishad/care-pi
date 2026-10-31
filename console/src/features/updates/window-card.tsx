import { useQueryClient } from "@tanstack/react-query";
import { CalendarClockIcon } from "lucide-react";
import { useId, useState } from "react";
import { qk } from "@/api/queries";
import type { UpdatesOverview } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { DataList, DataRow } from "@/components/app/page";
import { SectionCard } from "@/components/app/section-card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { ConfigChange } from "@/features/settings/config-change";
import { describedBy } from "@/lib/form";
import { fmt, strings } from "@/lib/strings";
import {
  DAYS,
  describeWindow,
  dstNote,
  formatZoned,
  sameWindow,
  validateWindow,
  windowValue,
  zoneText,
  type WindowValue,
} from "./window";

const u = strings.updates;

// The editor of updates.window and updates.auto. The change goes through the config flow of Settings:
// PUT /config?dry_run=true with If-Match (the plan: the timers change), then PUT /config?apply=true.
function WindowEditor({ o, onApplied }: { o: UpdatesOverview; onApplied: () => void }) {
  const id = useId();
  const [value, setValue] = useState<WindowValue>(() => windowValue(o.window));
  const [auto, setAuto] = useState(o.auto);
  const errors = validateWindow(value);
  const valid = Object.keys(errors).length === 0;
  const changed = !sameWindow(value, windowValue(o.window)) || auto !== o.auto;
  return (
    <div className="flex flex-col gap-3 border-t pt-4" data-testid="window-editor">
      <ConfigChange
        edit={(file) => ({
          ...file,
          updates: { ...file.updates, auto, window: { days: value.days, start: value.start, end: value.end } },
        })}
        valid={valid && changed}
        onApplied={onApplied}
      >
        <div className="flex flex-col gap-3">
          <p className="text-xs text-soft-foreground">{u.windowIntro}</p>
          <fieldset className="flex flex-col gap-2" aria-describedby={errors.days ? `${id}-days-error` : undefined}>
            <legend className="mb-2 text-sm font-medium">{u.days}</legend>
            <ToggleGroup
              multiple
              variant="outline"
              size="sm"
              spacing={1}
              value={value.days}
              onValueChange={(v) => setValue({ ...value, days: DAYS.filter((d) => v.includes(d)) })}
              aria-label={u.days}
              className="flex-wrap"
              data-testid="window-days"
            >
              {DAYS.map((d) => (
                <ToggleGroupItem key={d} value={d} aria-label={u.dayNames[d]} className="min-w-11">
                  {d}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <p id={`${id}-days-error`} className="min-h-5 text-sm text-destructive" aria-live="polite">
              {errors.days ?? ""}
            </p>
          </fieldset>
          <div className="grid gap-x-4 sm:grid-cols-2">
            <FormField id={`${id}-start`} label={u.start} error={errors.start}>
              <Input
                id={`${id}-start`}
                type="time"
                value={value.start}
                onChange={(e) => setValue({ ...value, start: e.target.value })}
                aria-invalid={errors.start ? true : undefined}
                aria-describedby={describedBy(`${id}-start`, false, errors.start)}
                data-testid="window-start"
              />
            </FormField>
            <FormField id={`${id}-end`} label={u.end} error={errors.end}>
              <Input
                id={`${id}-end`}
                type="time"
                value={value.end}
                onChange={(e) => setValue({ ...value, end: e.target.value })}
                aria-invalid={errors.end ? true : undefined}
                aria-describedby={describedBy(`${id}-end`, false, errors.end)}
                data-testid="window-end"
              />
            </FormField>
          </div>
          <p className="text-xs text-soft-foreground">{u.endHelp}</p>
          <p className="text-sm font-semibold" data-testid="window-preview">
            {valid ? fmt(u.preview, { window: describeWindow(value) }) : "\u00a0"}
          </p>
          <Label htmlFor={`${id}-auto`} className="flex items-start gap-3 font-normal">
            <Switch id={`${id}-auto`} checked={auto} onCheckedChange={setAuto} data-testid="window-auto" />
            <span className="flex flex-col gap-0.5">
              <span>{u.auto}</span>
              <span className="text-xs text-soft-foreground">{u.autoHelp}</span>
            </span>
          </Label>
        </div>
      </ConfigChange>
    </div>
  );
}

// The update window in the time zone of the device, its next times, and the editor.
export function WindowCard({ o, tz }: { o: UpdatesOverview | undefined; tz: string }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const w = o?.window;
  // The reference time of the zone texts: the next opening (a value of the agent, not the clock of the browser).
  const at = w ? new Date(w.next_open_at) : null;
  return (
    <SectionCard
      title={u.windowTitle}
      testId="window-card"
      action={
        w ? (
          <Badge size="sm" variant={w.open ? "success" : "neutral"} data-testid="window-open">
            {w.open ? u.open : u.closed}
          </Badge>
        ) : (
          <Skeleton className="h-5 w-16" />
        )
      }
      footer={
        <Button
          type="button"
          variant="outline"
          onClick={() => setEditing((v) => !v)}
          aria-expanded={editing}
          disabled={!o}
          data-testid="window-edit"
        >
          <CalendarClockIcon data-icon="inline-start" />
          {u.editWindow}
        </Button>
      }
    >
      <div className="flex min-h-6 items-center text-sm font-semibold" data-testid="window-summary">
        {w ? describeWindow(w) : <Skeleton className="h-4 w-48" />}
      </div>
      <DataList>
        <DataRow label={u.timezone} loading={!o} value={w && at ? zoneText(w.timezone, at) : ""} testId="window-zone" />
        <DataRow
          label={w?.open ? u.closesAt : u.nextOpen}
          loading={!o}
          value={formatZoned(w?.open ? w.closes_at : w?.next_open_at, tz)}
        />
        <DataRow label={u.nextCheck} loading={!o} value={formatZoned(o?.next_check_at, tz)} />
        <DataRow
          label={u.nextApply}
          loading={!o}
          value={o?.next_apply_at ? formatZoned(o.next_apply_at, tz) : u.nextApplyNone}
          title={o?.next_apply_at ? undefined : u.nextApplyNone}
        />
        <DataRow
          label={u.autoLabel}
          loading={!o}
          value={o ? (o.auto ? u.autoOn : u.autoOff) : ""}
          testId="window-auto-state"
        />
      </DataList>
      <p className="min-h-8 text-xs text-soft-foreground" data-testid="dst-note">
        {w && at ? dstNote(w.timezone, at) : ""}
      </p>
      {editing && o ? (
        <WindowEditor o={o} onApplied={() => void qc.invalidateQueries({ queryKey: qk.updates })} />
      ) : null}
    </SectionCard>
  );
}
