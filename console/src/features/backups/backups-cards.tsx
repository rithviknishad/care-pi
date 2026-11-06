import { ChevronDownIcon, PencilIcon, PlayIcon, ShieldCheckIcon, Trash2Icon } from "lucide-react";
import { useEffect } from "react";
import { Link } from "react-router";
import type { BackupsOverview, DestinationState, LocalBackupState } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { InlineSkeleton } from "@/components/v2/inline-skeleton";
import { Explainer } from "@/components/v2/explainer";
import { KeyValueList } from "@/components/v2/key-value-list";
import { MonoBlock } from "@/components/v2/mono-block";
import { Panel } from "@/components/v2/panel";
import { everyText, retentionSummary, retentionZones, type RetentionPolicy } from "@/components/v2/retention";
import { StatusChip } from "@/components/v2/status-chip";
import type { Tone } from "@/components/v2/tone";
import { formatAgo, formatBytes, formatDateTime, formatDuration } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import {
  INTERVAL_DEFAULT_MIN,
  calendarText,
  effectiveRetention,
  formatLocalRetention,
  formatMinute,
  formatRpoMinutes,
  intervalText,
} from "./backups-format";
import { TYPE_ICONS, destinationPlace } from "./destination-meta";
import { destinationBadge, localBadge, type BadgeVariant } from "./health";

const p = strings.backupsPage;

const TONE: Record<BadgeVariant, Tone> = {
  success: "ok",
  warning: "warn",
  destructive: "danger",
  info: "info",
  neutral: "neutral",
};

// "12 minutes ago". now is generated_at of GET /backups: the ages use the clock of the device. The title has the time.
function when(iso: string | null | undefined, now: number): React.ReactNode {
  if (!iso) return p.never;
  return <span title={formatMinute(iso)}>{formatAgo(iso, now)}</span>;
}

// "in 12 minutes" for the next copy.
function inMinutes(iso: string, now: number): string {
  const min = Math.max(0, Math.round((Date.parse(iso) - now) / 60_000));
  return min < 1 ? p.soon : fmt(p.inMinutes, { time: formatRpoMinutes(min) });
}

// "Every 15 minutes · back to 6 months": the policy of a destination in one short line (the explainer has the rest).
function shortPolicy(policy: RetentionPolicy): string {
  const oldest = retentionZones(policy).at(-1);
  return oldest
    ? `${everyText(policy.interval_min)} · ${fmt(p.backTo, { span: oldest.length })}`
    : everyText(policy.interval_min);
}

// The policy of the explainer of the last visit: the invisible explainer of the loading state has the same height.
const POLICY_KEY = "care-pi.backups.policy";

function rememberedPolicy(): RetentionPolicy {
  try {
    const v = JSON.parse(localStorage.getItem(POLICY_KEY) ?? "null") as RetentionPolicy | null;
    if (v && typeof v.interval_min === "number") return v;
  } catch {
    // Private mode: the default policy.
  }
  return { interval_min: INTERVAL_DEFAULT_MIN, ...effectiveRetention(null) };
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
    <Panel
      title={p.local}
      description={p.localIntro}
      testId="local-card"
      action={
        <span className="flex w-32 justify-end">
          {badge ? (
            <StatusChip tone={TONE[badge.variant]} testId="local-state">
              {badge.text}
            </StatusChip>
          ) : (
            <Skeleton className="h-7 w-20 rounded-full" />
          )}
        </span>
      }
      footer={
        <>
          <Button type="button" onClick={() => onBackup("incr")} disabled={loading || busy} data-testid="local-backup">
            <PlayIcon data-icon="inline-start" />
            {p.backupNow}
          </Button>
          <Button type="button" variant="outline" onClick={() => onBackup("full")} disabled={loading || busy}>
            {p.fullNow}
          </Button>
        </>
      }
    >
      <KeyValueList
        loading={loading}
        items={[
          {
            id: "window",
            label: p.window,
            mono: true,
            testId: "local-window",
            title: local?.window_from && local.window_to ? `${local.window_from} – ${local.window_to}` : undefined,
            value: local?.window_from && local.window_to ? windowText(local.window_from, local.window_to) : p.none,
          },
          { id: "full", label: p.lastFull, value: when(local?.last_full_at, now) },
          { id: "incr", label: p.lastIncr, value: when(local?.last_incr_at, now) },
          { id: "wal", label: p.lastWal, value: when(local?.last_wal_archived_at, now), testId: "local-wal" },
          { id: "size", label: p.size, mono: true, value: formatBytes(local?.size_bytes) },
          { id: "free", label: p.free, mono: true, value: formatBytes(local?.free_bytes) },
          { id: "key", label: p.key, value: local ? (p.keys[local.key] ?? local.key) : "" },
        ]}
      />
    </Panel>
  );
}

// One destination (plans/02 section 7, S66): the type, the plain state, the place, the last and the next copy, the
// schedule in one sentence, and the actions. Each card has the same rows and a reserved line for the last error, so
// a refresh does not move it.
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
  const policy = {
    interval_min: d.destination.interval_min ?? INTERVAL_DEFAULT_MIN,
    ...effectiveRetention(d.destination.retention),
  };
  return (
    <section
      id={`destination-${d.id}`}
      data-testid="destination-card"
      data-id={d.id}
      data-state={d.state}
      className="flex min-w-0 flex-col gap-4 rounded-card border border-line bg-surface-card p-4 shadow-soft sm:p-6"
    >
      <header className="flex min-w-0 items-start gap-3">
        <span className="grid size-11 shrink-0 place-items-center rounded-lg bg-surface-tint-strong text-primary-solid">
          <Icon className="size-5" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h3 className="truncate type-h3 text-ink">
            {strings.dest.types[d.type]} <span className="type-data font-normal text-ink-soft">· {d.id}</span>
          </h3>
          <p className="h-5 truncate type-data text-ink-soft" title={d.location}>
            {destinationPlace(d.destination)}
          </p>
        </div>
        <StatusChip tone={TONE[badge.variant]} width={9} testId="destination-state">
          {badge.text}
        </StatusChip>
      </header>
      <p className="line-clamp-2 min-h-10 type-small text-ink-muted" title={retentionSummary(policy)}>
        {shortPolicy(policy)}
      </p>
      <KeyValueList
        items={[
          { id: "db", label: p.lastDb, value: when(d.last_db_snapshot_at, now), testId: "destination-last-db" },
          { id: "files", label: p.lastFiles, value: when(d.last_files_snapshot_at, now) },
          {
            id: "next",
            label: p.next,
            value: d.next_db_run_at
              ? `${inMinutes(d.next_db_run_at, now)} · ${intervalText(d.destination.interval_min)}`
              : p.paused,
          },
          {
            id: "rpo",
            label: p.rpo,
            value: d.rpo_minutes ? formatRpoMinutes(d.rpo_minutes) : "—",
          },
          { id: "size", label: p.size, mono: true, value: formatBytes(d.size_bytes) },
          { id: "free", label: p.free, mono: true, value: formatBytes(d.free_bytes) },
        ]}
      />
      <p
        className="line-clamp-2 min-h-10 type-small break-words text-danger-fg"
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
    </section>
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
      className={
        placeholder
          ? "invisible flex flex-wrap gap-2 border-t border-line-soft pt-4"
          : "flex flex-wrap gap-2 border-t border-line-soft pt-4"
      }
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
        className="no-underline"
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

// The skeleton of a destination card: the same header, sentence, rows, error line, and buttons.
export function DestinationCardSkeleton() {
  return (
    <section
      data-testid="destination-skeleton"
      aria-busy="true"
      className="flex min-w-0 flex-col gap-4 rounded-card border border-line bg-surface-card p-4 shadow-soft sm:p-6"
    >
      <header className="flex min-w-0 items-start gap-3">
        <Skeleton className="size-11 shrink-0 rounded-lg" />
        <div className="min-w-0 flex-1">
          <h3 className="type-h3">
            <InlineSkeleton className="h-5 w-40 align-middle" />
          </h3>
          <p className="h-5">
            <InlineSkeleton className="h-4 w-48 align-middle" />
          </p>
        </div>
        <Skeleton className="h-7 w-24 rounded-full" />
      </header>
      <Skeleton className="h-10" />
      <KeyValueList
        loading
        items={[p.lastDb, p.lastFiles, p.next, p.rpo, p.size, p.free].map((l) => ({ id: l, label: l }))}
      />
      <p className="min-h-10" />
      <DestinationActions disabled placeholder />
    </section>
  );
}

// How we keep your copies (S66, item 2): the Explainer of the first destination, the RPO in one sentence, and the
// timers of the device in "Each time" (plans/02 section 7, docs/api/README.md section 13.7).
// rows: the number of timer rows of the skeleton.
export function ScheduleCard({ overview, rows }: { overview: BackupsOverview | undefined; rows: number }) {
  const loading = !overview;
  const rpo = overview?.rpo;
  const firstDest = overview?.destinations[0];
  const policy = {
    interval_min: firstDest?.destination.interval_min ?? INTERVAL_DEFAULT_MIN,
    ...effectiveRetention(firstDest?.destination.retention),
  };
  const scheduleLabel = (kind: string, destination: string | null) =>
    fmt(p.schedules[kind] ?? kind, { destination: destination ?? "" });
  useEffect(() => {
    if (!firstDest) return;
    try {
      localStorage.setItem(POLICY_KEY, JSON.stringify(policy));
    } catch {
      // Private mode.
    }
  });
  return (
    <Panel
      title={p.schedule}
      description={p.scheduleHelp}
      testId="schedule-card"
      action={
        <Button asChild variant="outline" size="sm">
          <Link to="/settings?tab=backups" data-testid="schedule-edit">
            {p.change}
          </Link>
        </Button>
      }
    >
      <section
        className="flex flex-col gap-1 rounded-card border border-line-soft bg-surface-tint p-4"
        data-testid="rpo"
        aria-busy={!rpo}
      >
        <p className="line-clamp-2 min-h-12 type-body font-medium text-ink">
          {!rpo
            ? "\u00a0"
            : rpo.offsite_minutes && firstDest
              ? fmt(strings.dest.rpoSentence, { minutes: formatRpoMinutes(rpo.offsite_minutes) })
              : p.rpoNoOffsite}
        </p>
        <p className="line-clamp-3 min-h-15 type-small text-ink-muted sm:min-h-10">
          {rpo ? fmt(p.rpoLocalShort, { minutes: formatRpoMinutes(rpo.local_minutes ?? 5) }) : "\u00a0"}
        </p>
      </section>
      {loading ? (
        // The explainer of the last visit, invisible: the card has its final height before the data comes.
        <div className="invisible" aria-hidden="true">
          <Explainer policy={rememberedPolicy()} title={fmt(p.explainerTitle, { name: "usb" })} />
        </div>
      ) : firstDest ? (
        <Explainer policy={policy} testId="schedule-explainer" title={fmt(p.explainerTitle, { name: firstDest.id })} />
      ) : null}
      <details className="group rounded-card border border-line-soft" data-testid="retention">
        <summary className="flex h-11 cursor-pointer list-none items-center gap-2 px-4 type-small font-medium text-ink-muted outline-none select-none hover:text-ink focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
          <ChevronDownIcon
            aria-hidden="true"
            className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
          />
          {p.timers}
        </summary>
        <div className="flex flex-col gap-3 px-4 pb-4">
          <KeyValueList
            loading={loading}
            items={
              loading
                ? Array.from({ length: rows }, (_, i) => ({ id: String(i), label: "\u00a0" }))
                : overview.schedule
                    .filter((e) => e.kind !== "snapshot-files" || e.destination_id === firstDest?.id)
                    .map((e) => ({
                      id: e.id,
                      label: scheduleLabel(e.kind, e.destination_id),
                      value: (
                        <span className={e.enabled ? "" : "text-ink-soft"}>
                          {e.interval_min ? intervalText(e.interval_min) : calendarText(e.calendar)}
                          {e.enabled ? "" : ` (${p.paused})`}
                        </span>
                      ),
                    }))
            }
          />
          <p className="type-small text-ink-muted">
            {p.localRetention}: {formatLocalRetention(overview?.retention.db_full ?? 2)}
          </p>
        </div>
      </details>
    </Panel>
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
    <Panel
      title={p.verifyTitle}
      description={p.drillHelp}
      testId="verify-card"
      footer={
        <>
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
        </>
      }
    >
      <KeyValueList
        loading={loading}
        items={[
          { id: "verify", label: p.lastVerify, value: verifyText, testId: "verify-last" },
          { id: "next-verify", label: p.nextVerify, value: v?.next_run_at ? formatDateTime(v.next_run_at) : p.paused },
          { id: "drill", label: p.lastDrill, value: drillText, testId: "drill-last" },
          {
            id: "next-drill",
            label: p.nextDrill,
            value: d?.enabled ? (d.next_run_at ? formatDateTime(d.next_run_at) : p.paused) : p.drillOff,
          },
        ]}
      />
    </Panel>
  );
}

// The SSH public key of the device for the network folder servers (GET /backups/ssh-key; the same in GET /backups).
export function SshKeyCard({ overview }: { overview: BackupsOverview | undefined }) {
  const key = overview?.ssh_key ?? null;
  return (
    <Panel title={p.sshTitle} description={p.sshHelp} testId="ssh-card">
      {!overview || key ? (
        <MonoBlock
          text={key?.authorized_keys_line ?? ""}
          label={strings.dest.sftp.publicKey}
          lines={3}
          copy={Boolean(key)}
          testId="ssh-key"
        />
      ) : (
        <p className="type-body text-ink-muted">{p.none}</p>
      )}
    </Panel>
  );
}
