import { PencilIcon, PlayIcon, ShieldCheckIcon, Trash2Icon } from "lucide-react";
import { Link } from "react-router";
import type { BackupsOverview, DestinationState, LocalBackupState } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { DataList, DataRow } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { formatBytes, formatDateTime, formatDuration, formatRelative } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import {
  calendarText,
  formatLocalRetention,
  formatMinute,
  formatMinutes,
  formatRetention,
  formatRpo,
  intervalText,
} from "./backups-format";
import { TYPE_ICONS, destinationPlace } from "./destination-meta";
import { destinationBadge, localBadge } from "./health";

const p = strings.backupsPage;

// "5 min ago (2026-10-30 10:45)". Below 640 px only "5 min ago": the value must not wrap (docs/console-ui.md 5.1).
// now is generated_at of GET /backups: the ages use the clock of the device, not the clock of the browser.
function when(iso: string | null | undefined, now: number): React.ReactNode {
  if (!iso) return p.never;
  return (
    <>
      {formatRelative(iso, now)}
      <span className="hidden sm:inline"> ({formatMinute(iso)})</span>
    </>
  );
}

// The restore window without the year (it is about 2 weeks), so it fits one line. The title has the full times.
function windowText(from: string, to: string): string {
  const short = (iso: string) => formatMinute(iso).slice(5);
  return fmt(p.windowValue, { from: short(from), to: short(to) });
}

// The local pgBackRest repository and the restore window (plans/02 section 7: local PITR health).
export function LocalCard({
  local,
  now,
  onBackup,
  busy,
}: {
  local: LocalBackupState | undefined;
  now: number;
  onBackup: (type: "incr" | "full") => void;
  busy: boolean;
}) {
  const loading = !local;
  const badge = local ? localBadge(local.state) : null;
  return (
    <Card size="sm" data-testid="local-card">
      <CardHeader>
        <CardTitle>
          <h2>{p.local}</h2>
        </CardTitle>
        <CardDescription>{p.localIntro}</CardDescription>
        <CardAction className="flex w-32 justify-end">
          {badge ? (
            <Badge variant={badge.variant} data-testid="local-state">
              {badge.text}
            </Badge>
          ) : (
            <Skeleton className="h-6 w-16" />
          )}
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataList>
          <DataRow
            label={p.window}
            loading={loading}
            testId="local-window"
            title={local?.window_from && local.window_to ? `${local.window_from} – ${local.window_to}` : undefined}
            value={local?.window_from && local.window_to ? windowText(local.window_from, local.window_to) : p.none}
          />
          <DataRow label={p.lastFull} loading={loading} value={when(local?.last_full_at, now)} />
          <DataRow label={p.lastIncr} loading={loading} value={when(local?.last_incr_at, now)} />
          <DataRow
            label={p.lastWal}
            loading={loading}
            value={when(local?.last_wal_archived_at, now)}
            testId="local-wal"
          />
          <DataRow label={p.size} loading={loading} value={formatBytes(local?.size_bytes)} />
          <DataRow label={p.free} loading={loading} value={formatBytes(local?.free_bytes)} />
          <DataRow label={p.key} loading={loading} value={local ? (p.keys[local.key] ?? local.key) : ""} />
        </DataList>
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            size="sm"
            onClick={() => onBackup("incr")}
            disabled={loading || busy}
            data-testid="local-backup"
          >
            <PlayIcon data-icon="inline-start" />
            {p.backupNow}
          </Button>
          <Button type="button" size="sm" variant="outline" onClick={() => onBackup("full")} disabled={loading || busy}>
            {p.fullNow}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// One destination (plans/02 section 7): the type icon, the state, the last copies, the size, the free space, and
// the actions. Each card has the same rows and a reserved line for the last error, so a refresh does not move it.
export function DestinationCard({
  d,
  now,
  onRun,
  onVerify,
  onEdit,
  onRemove,
  busy,
}: {
  d: DestinationState;
  now: number;
  onRun: () => void;
  onVerify: () => void;
  onEdit: () => void;
  onRemove: () => void;
  busy: boolean;
}) {
  const Icon = TYPE_ICONS[d.type] ?? ShieldCheckIcon;
  const badge = destinationBadge(d.state);
  return (
    <Card size="sm" id={`destination-${d.id}`} data-testid="destination-card" data-id={d.id} data-state={d.state}>
      <CardHeader>
        <CardTitle className="flex h-6 min-w-0 items-center gap-2">
          <Icon className="size-5 shrink-0 text-primary" aria-hidden="true" />
          <h3 className="max-w-[60%] shrink-0 truncate">{d.id}</h3>
          <span className="truncate text-sm font-normal text-soft-foreground">{strings.dest.types[d.type]}</span>
        </CardTitle>
        <CardDescription className="h-5 truncate" title={d.location}>
          {destinationPlace(d.destination)}
        </CardDescription>
        <CardAction className="flex w-28 justify-end">
          <Badge variant={badge.variant} data-testid="destination-state">
            {badge.text}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <DataList>
          <DataRow label={p.lastDb} value={when(d.last_db_snapshot_at, now)} testId="destination-last-db" />
          <DataRow label={p.lastFiles} value={when(d.last_files_snapshot_at, now)} />
          <DataRow label={p.size} value={formatBytes(d.size_bytes)} />
          <DataRow label={p.free} value={formatBytes(d.free_bytes)} />
          <DataRow
            label={p.next}
            value={
              d.next_db_run_at
                ? `${formatRelative(d.next_db_run_at, now)} · ${intervalText(d.destination.interval_min)}`
                : p.paused
            }
          />
          <DataRow label={p.rpo} value={formatRpo(d.rpo_minutes)} />
        </DataList>
        <p
          className="line-clamp-2 min-h-8 text-xs break-words text-destructive"
          title={d.last_error ?? undefined}
          data-testid="destination-error"
        >
          {d.last_error ? `${p.lastError}: ${d.last_error}` : "\u00a0"}
        </p>
        <DestinationActions
          onRun={onRun}
          onVerify={onVerify}
          onEdit={onEdit}
          onRemove={onRemove}
          disabled={busy || d.running}
        />
      </CardContent>
    </Card>
  );
}

function DestinationActions({
  onRun,
  onVerify,
  onEdit,
  onRemove,
  disabled,
  placeholder = false,
}: {
  onRun?: () => void;
  onVerify?: () => void;
  onEdit?: () => void;
  onRemove?: () => void;
  disabled: boolean;
  // The skeleton: the same buttons, invisible, so that they wrap the same way.
  placeholder?: boolean;
}) {
  const ph = placeholder ? { tabIndex: -1 } : {};
  return (
    <div
      className={placeholder ? "invisible flex flex-wrap gap-2" : "flex flex-wrap gap-2"}
      aria-hidden={placeholder || undefined}
    >
      <Button
        type="button"
        size="sm"
        onClick={onRun}
        disabled={disabled}
        data-testid={placeholder ? undefined : "destination-run"}
        {...ph}
      >
        <PlayIcon data-icon="inline-start" />
        {p.backupNow}
      </Button>
      <Button type="button" size="sm" variant="outline" onClick={onVerify} disabled={disabled} {...ph}>
        {p.verify}
      </Button>
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={onEdit}
        data-testid={placeholder ? undefined : "destination-edit"}
        {...ph}
      >
        <PencilIcon data-icon="inline-start" />
        {p.edit}
      </Button>
      <Button
        type="button"
        size="sm"
        variant="ghost"
        onClick={onRemove}
        data-testid={placeholder ? undefined : "destination-remove"}
        {...ph}
      >
        <Trash2Icon data-icon="inline-start" />
        {p.remove}
      </Button>
    </div>
  );
}

// The skeleton of a destination card: the same header, rows, error line, and buttons.
export function DestinationCardSkeleton() {
  return (
    <Card size="sm" data-testid="destination-skeleton">
      <CardHeader>
        <CardTitle className="flex h-6 items-center">
          <Skeleton className="h-5 w-32" />
        </CardTitle>
        <CardDescription className="flex h-5 items-center">
          <Skeleton className="h-4 w-48" />
        </CardDescription>
        <CardAction className="flex w-28 justify-end">
          <Skeleton className="h-6 w-16" />
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <DataList>
          {[p.lastDb, p.lastFiles, p.size, p.free, p.next, p.rpo].map((l) => (
            <DataRow key={l} label={l} loading value="" />
          ))}
        </DataList>
        <p className="min-h-8" />
        <DestinationActions disabled placeholder />
      </CardContent>
    </Card>
  );
}

// The schedule, the retention, and the RPO (plans/02 section 7, docs/api/README.md section 13.7).
// rows: the number of schedule rows of the skeleton (4 local timers, 1 for each destination, and 1 for the files).
export function ScheduleCard({ overview, rows }: { overview: BackupsOverview | undefined; rows: number }) {
  const loading = !overview;
  const rpo = overview?.rpo;
  const firstDest = overview?.destinations[0];
  const scheduleLabel = (kind: string, destination: string | null) =>
    fmt(p.schedules[kind] ?? kind, { destination: destination ?? "" });
  return (
    <Card size="sm" data-testid="schedule-card">
      <CardHeader>
        <CardTitle>
          <h2>{p.schedule}</h2>
        </CardTitle>
        <CardDescription>{p.scheduleHelp}</CardDescription>
        <CardAction className="flex justify-end">
          <Button asChild variant="outline" size="sm">
            <Link to="/settings?tab=backups" data-testid="schedule-edit">
              {p.change}
            </Link>
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataList>
          {loading
            ? Array.from({ length: rows }, (_, i) => (
                <DataRow key={i} label={<Skeleton className="inline-block h-3 w-24 align-middle" />} loading value="" />
              ))
            : overview.schedule
                .filter((e) => e.kind !== "snapshot-files" || e.destination_id === firstDest?.id)
                .map((e) => (
                  <DataRow
                    key={e.id}
                    label={scheduleLabel(e.kind, e.destination_id)}
                    value={
                      <span className={e.enabled ? "" : "text-soft-foreground"}>
                        {e.interval_min ? intervalText(e.interval_min) : calendarText(e.calendar)}
                        {e.enabled ? "" : ` (${p.paused})`}
                      </span>
                    }
                  />
                ))}
        </DataList>
        <section className="relative flex flex-col gap-1 text-sm" aria-busy={loading} data-testid="retention">
          <p className={loading ? "invisible" : ""}>
            <span className="text-soft-foreground">{p.localRetention}: </span>
            {formatLocalRetention(overview?.retention.db_full ?? 2)}
          </p>
          <p className={loading ? "invisible" : ""}>
            <span className="text-soft-foreground">{p.destRetention}: </span>
            {formatRetention(firstDest?.destination.retention)}
          </p>
          {loading ? <Skeleton className="absolute inset-0" /> : null}
        </section>
        <section className="flex flex-col gap-1 rounded-lg border bg-soft-background p-3 text-sm" data-testid="rpo">
          <h3 className="font-semibold">{p.rpoTitle}</h3>
          {/* Before the data, the same text with the defaults keeps the height (monospace: the same wrap). */}
          <div className="relative flex flex-col gap-1" aria-busy={!rpo}>
            <p className={rpo ? "" : "invisible"}>
              {fmt(p.rpoLocal, {
                local: formatMinutes(rpo?.local_minutes ?? 5),
                timeout: formatMinutes(rpo?.wal_archive_timeout_min ?? 5),
              })}
            </p>
            <p className={rpo ? "" : "invisible"}>
              {!rpo || (rpo.offsite_minutes && firstDest)
                ? fmt(p.rpoOffsite, {
                    offsite: formatMinutes(rpo?.offsite_minutes ?? 20),
                    interval: formatMinutes((rpo?.offsite_minutes ?? 20) - (rpo?.wal_archive_timeout_min ?? 5)),
                    timeout: formatMinutes(rpo?.wal_archive_timeout_min ?? 5),
                  })
                : p.rpoNoOffsite}
            </p>
            {rpo ? null : <Skeleton className="absolute inset-0" />}
          </div>
        </section>
      </CardContent>
    </Card>
  );
}

// The last verify and the last restore drill, with "Verify now" and "Test restore (drill)".
export function VerifyCard({
  overview,
  onVerify,
  onDrill,
  busy,
}: {
  overview: BackupsOverview | undefined;
  onVerify: () => void;
  onDrill: () => void;
  busy: boolean;
}) {
  const loading = !overview;
  const v = overview?.verify;
  const d = overview?.drill;
  const verifyText = v?.last
    ? `${v.last.ok ? p.ok : fmt(p.failedText, { detail: v.last.detail ?? "" })} · ${formatMinute(v.last.finished_at)}`
    : p.never;
  const drillText = d?.last
    ? `${p.outcomes[d.last.outcome] ?? d.last.outcome}${d.last.duration_s !== null ? ` (${formatDuration(d.last.duration_s)})` : ""} · ${formatMinute(d.last.finished_at)}`
    : p.never;
  return (
    <Card size="sm" data-testid="verify-card">
      <CardHeader>
        <CardTitle>
          <h2>{p.verifyTitle}</h2>
        </CardTitle>
        <CardDescription>{p.drillHelp}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataList>
          <DataRow label={p.lastVerify} loading={loading} value={verifyText} testId="verify-last" />
          <DataRow
            label={p.nextVerify}
            loading={loading}
            value={v?.next_run_at ? formatDateTime(v.next_run_at) : p.paused}
          />
          <DataRow label={p.lastDrill} loading={loading} value={drillText} testId="drill-last" />
          <DataRow
            label={p.nextDrill}
            loading={loading}
            value={d?.enabled ? (d.next_run_at ? formatDateTime(d.next_run_at) : p.paused) : p.drillOff}
          />
        </DataList>
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" onClick={onVerify} disabled={loading || busy}>
            {p.verifyNow}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={onDrill}
            disabled={loading || busy}
            data-testid="drill"
          >
            {p.drill}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// The SSH public key of the device for the network folder servers (GET /backups/ssh-key; the same in GET /backups).
export function SshKeyCard({ overview }: { overview: BackupsOverview | undefined }) {
  const key = overview?.ssh_key ?? null;
  return (
    <Card size="sm" data-testid="ssh-card">
      <CardHeader>
        <CardTitle>
          <h2>{p.sshTitle}</h2>
        </CardTitle>
        <CardDescription>{p.sshHelp}</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex min-h-14 items-start gap-2">
          {!overview ? (
            <Skeleton className="h-14 flex-1" />
          ) : key ? (
            <>
              <code className="line-clamp-3 min-w-0 flex-1 text-xs break-all" title={key.authorized_keys_line}>
                {key.authorized_keys_line}
              </code>
              <CopyButton value={key.authorized_keys_line} label={strings.dest.sftp.publicKey} />
            </>
          ) : (
            <p className="text-sm text-soft-foreground">{p.none}</p>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
