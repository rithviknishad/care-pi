import {
  ActivityIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  DatabaseBackupIcon,
  GlobeIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { Link } from "react-router";
import { useJobList, useStatus } from "@/api/queries";
import type { Status, UnitStatus } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { DashboardGrid } from "@/components/v2/dashboard-grid";
import { Hero } from "@/components/v2/hero";
import { InlineSkeleton } from "@/components/v2/inline-skeleton";
import { KeyValueList } from "@/components/v2/key-value-list";
import { MetricBar } from "@/components/v2/metric-bar";
import { Panel } from "@/components/v2/panel";
import { StatCard } from "@/components/v2/stat-card";
import { StatusChip } from "@/components/v2/status-chip";
import { Timeline } from "@/components/v2/timeline";
import type { Tone } from "@/components/v2/tone";
import { useNow } from "@/components/v2/use-now";
import { Skeleton } from "@/components/ui/skeleton";
import { maintenanceText } from "@/app/layout/maintenance";
import { BackupHealthRow } from "@/features/backups/backup-banners";
import { lastOffsiteCopy } from "@/features/backups/health";
import { JOB_STATE_TONE } from "@/features/jobs/job-steps";
import { servedNames, type HostMode } from "@/features/connectivity/connectivity-logic";
import { summaryText } from "@/features/updates/update-text";
import { useStableOrder } from "@/hooks/use-stable-order";
import {
  formatAgo,
  formatBytes,
  formatDateTime,
  formatDuration,
  formatLoad,
  formatPercent,
  formatTime,
  percent,
  shortDigest,
  shortSha,
} from "@/lib/format";
import { fmt, jobKindText, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

const d = strings.dashboard;
const unitKey = (u: UnitStatus) => u.unit;

const STATE_TONE: Record<string, Tone> = {
  ok: "ok",
  degraded: "danger",
  maintenance: "warn",
  "clock-wait": "warn",
  starting: "info",
  setup: "info",
};

const MODE_TONE: Record<HostMode, Tone> = { public: "ok", local: "neutral", tailscale: "info" };

// GET /status lists 10 units: care.target, care-clock, and the 8 units of the stack (agent/internal/status).
const UNIT_ROWS = 10;

// The plain name of a service (console/design.md: "service", not "unit").
function serviceName(u: UnitStatus): string {
  const key = u.unit.replace(/\.(service|target)$/, "");
  return d.services[key] ?? key;
}

function unitTone(u: UnitStatus): { tone: Tone; text: string } {
  if (u.active_state !== "active") {
    return {
      tone: u.active_state === "failed" ? "danger" : "warn",
      text: d.unitStates[u.active_state] ?? u.active_state,
    };
  }
  if (u.health === "unhealthy") return { tone: "danger", text: d.unitStates.unhealthy ?? u.health };
  if (u.health === "starting") return { tone: "info", text: d.unitStates.starting ?? u.health };
  return { tone: "ok", text: d.unitStates.running ?? "Running" };
}

// The problems of the device in plain words: the maintenance step, the flags, and the problems of the agent.
function problemsOf(status: Status): { items: string[]; maintenance: string | null } {
  const flags = (Object.entries(status.flags) as [string, boolean][])
    .filter(([k, on]) => on && k !== "degraded")
    .map(([k]) => d.flags[k] ?? k);
  const maintenance = status.maintenance?.active ? maintenanceText(status.maintenance) : null;
  return {
    items: [
      ...(maintenance ? [maintenance] : []),
      ...flags.filter((f) => !maintenance || f !== d.flags.maintenance),
      ...status.problems,
    ],
    maintenance,
  };
}

// The services: one row for each, the plain name, the state in a word, and the memory against its cap (MetricBar).
function Services({ status }: { status: Status | undefined }) {
  const units = useStableOrder(status?.units, unitKey);
  return (
    <Panel title={d.services.title} description={d.services.intro} testId="units-card">
      <ul className="flex flex-col divide-y divide-line-soft" data-testid="units" aria-label={d.units}>
        {units
          ? units.map((u) => {
              const s = unitTone(u);
              return (
                <li
                  key={u.unit}
                  className="grid h-20 content-center gap-2 py-2"
                  data-unit={u.unit}
                  data-active={u.active_state}
                  data-sub={u.sub_state}
                  title={u.unit}
                >
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="min-w-0 flex-1 truncate type-body font-medium text-ink">
                      {u.log_unit ? (
                        <Link
                          to={`/logs?unit=${u.log_unit}`}
                          className="underline-offset-4 hover:underline"
                          aria-label={`${serviceName(u)}: ${d.openLog}`}
                        >
                          {serviceName(u)}
                        </Link>
                      ) : (
                        serviceName(u)
                      )}
                    </span>
                    {u.restarts > 0 ? (
                      <span
                        className="shrink-0 type-data text-ink-soft"
                        title={fmt(d.restartsCount, { count: u.restarts })}
                      >
                        {fmt(d.restartsShort, { count: u.restarts })}
                      </span>
                    ) : null}
                    <StatusChip tone={s.tone} size="sm" width={9}>
                      {s.text}
                    </StatusChip>
                  </div>
                  <MetricBar label={d.memory} used={u.memory_current_bytes} cap={u.memory_max_bytes} />
                </li>
              );
            })
          : Array.from({ length: UNIT_ROWS }, (_, i) => (
              <li key={i} className="grid h-20 content-center gap-2 py-2" aria-hidden="true">
                <Skeleton className="h-5 w-48" />
                <MetricBar label={d.memory} used={null} cap={null} loading />
              </li>
            ))}
      </ul>
    </Panel>
  );
}

// The names of Care with their mode (local, public, Tailscale) and certificate.
function Names({ status }: { status: Status | undefined }) {
  return (
    <Panel
      title={d.names}
      testId="network-card"
      action={<GlobeIcon className="size-5 text-primary-solid" aria-hidden="true" />}
    >
      <ul className="flex flex-col gap-3" aria-label={d.names} data-testid="served-names">
        {status ? (
          servedNames(status).map((n) => (
            <li key={n.host} className="flex min-w-0 items-start gap-3">
              <StatusChip tone={MODE_TONE[n.mode]} size="sm" width={8} className="mt-0.5">
                {d.modes[n.mode]}
              </StatusChip>
              <div className="flex min-w-0 flex-col">
                <span className="truncate type-data text-ink" title={n.host}>
                  {n.host}
                </span>
                <span className="truncate type-small text-ink-soft">{d.certs[n.mode]}</span>
              </div>
            </li>
          ))
        ) : (
          <li className="flex items-start gap-3" aria-hidden>
            <Skeleton className="mt-0.5 h-6 w-20 shrink-0 rounded-full" />
            <div className="flex min-w-0 flex-col">
              <span className="type-data">
                <InlineSkeleton className="h-4 w-32 align-middle" />
              </span>
              <span className="type-small">
                <InlineSkeleton className="h-4 w-40 align-middle" />
              </span>
            </div>
          </li>
        )}
      </ul>
      <KeyValueList
        loading={!status}
        labelWidth="md"
        items={[
          { id: "configured", label: d.configured, value: status?.network.mdns_configured, mono: true },
          {
            id: "advertised",
            label: d.advertised,
            mono: true,
            value: (
              <span className={cn(status?.flags.mdns_collision && "text-warn-fg")}>
                {status?.network.mdns_advertised ?? "—"}
              </span>
            ),
          },
          { id: "addresses", label: d.addresses, value: status?.addresses.join(", "), mono: true },
          {
            id: "internet",
            label: d.internet,
            value:
              status?.network.internet_reachable === null
                ? "—"
                : status?.network.internet_reachable
                  ? d.reachable
                  : d.notReachable,
          },
        ]}
      />
    </Panel>
  );
}

// This device: the facts, with the memory of the device as a bar.
function Device({ status }: { status: Status | undefined }) {
  const mem = status?.host.memory;
  const disk = status?.disks[0];
  return (
    <Panel title={d.host}>
      <div className="flex flex-col gap-4">
        <MetricBar
          label={d.ram}
          used={mem ? mem.total_bytes - mem.available_bytes : null}
          cap={mem?.total_bytes}
          loading={!status}
          testId="mem-bar"
        />
        <MetricBar
          label={disk ? fmt(d.diskOf, { mount: disk.mount }) : d.disks}
          used={disk ? disk.total_bytes - disk.free_bytes : null}
          cap={disk?.total_bytes}
          loading={!status}
        />
      </div>
      <KeyValueList
        loading={!status}
        labelWidth="md"
        items={[
          { id: "model", label: d.model, value: status?.host.model ?? "—", title: status?.host.model ?? undefined },
          { id: "os", label: d.os, value: status?.host.os, title: status?.host.os },
          {
            id: "available",
            label: d.available,
            mono: true,
            testId: "mem-available",
            value: mem
              ? `${formatBytes(mem.available_bytes)} (${formatPercent(percent(mem.available_bytes, mem.total_bytes))})`
              : "",
          },
          { id: "uptime", label: d.uptime, value: status ? formatDuration(status.host.uptime_s) : "" },
          { id: "time", label: d.time, mono: true, value: status ? formatDateTime(status.clock.time) : "" },
          { id: "tz", label: d.timezone, value: status?.clock.timezone },
        ]}
      />
    </Panel>
  );
}

// The facts for support: the versions, the lock, the certificate, the load. A hash only here (console/design.md).
function Technical({ status }: { status: Status | undefined }) {
  const care = status?.images.find((i) => i.name === "care");
  const mem = status?.host.memory;
  return (
    <details className="group rounded-card border border-line bg-surface-card shadow-soft" data-testid="technical">
      <summary className="flex h-14 cursor-pointer list-none items-center gap-2 px-4 type-body font-medium text-ink outline-none select-none hover:bg-surface-tint focus-visible:ring-3 focus-visible:ring-ring/50 sm:px-6 [&::-webkit-details-marker]:hidden">
        <ChevronDownIcon
          aria-hidden="true"
          className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
        />
        {strings.jobsPage.technical}
      </summary>
      <div className="px-4 pb-4 sm:px-6">
        <KeyValueList
          loading={!status}
          labelWidth="md"
          items={[
            { id: "agent", label: d.agent, mono: true, value: status?.agent.version },
            {
              id: "solution",
              label: d.solution,
              value: status?.lock ? `${status.lock.solution.name} (${status.lock.solution.track})` : d.noLock,
            },
            {
              id: "care",
              label: d.careDigest,
              mono: true,
              testId: "care-digest",
              value: shortDigest(care?.pull),
              title: care?.pull,
              copy: care?.pull ?? undefined,
            },
            {
              id: "fe",
              label: d.careFeSha,
              mono: true,
              value: shortSha(status?.care_fe_sha),
              title: status?.care_fe_sha ?? undefined,
            },
            { id: "build", label: d.buildId, mono: true, testId: "fe-build-id", value: status?.fe_build_id ?? "—" },
            { id: "locked", label: d.lockedAt, mono: true, value: formatDateTime(status?.locked_at) },
            {
              id: "fingerprint",
              label: d.fingerprint,
              mono: true,
              value: status?.tls.local_ca.fingerprint_sha256 ? (
                <Link to="/certificate" className="underline-offset-4 hover:underline" data-testid="fingerprint">
                  {shortSha(status.tls.local_ca.fingerprint_sha256, 16)}…
                </Link>
              ) : (
                "—"
              ),
              title: status?.tls.local_ca.fingerprint_sha256 ?? undefined,
            },
            { id: "notafter", label: d.notAfter, mono: true, value: formatDateTime(status?.tls.local_ca.not_after) },
            { id: "kernel", label: d.kernel, mono: true, value: status?.host.kernel },
            {
              id: "cpus",
              label: d.cpus,
              mono: true,
              value: status ? `${status.host.cpu_count} × ${status.host.arch}` : "",
            },
            {
              id: "load",
              label: d.load,
              mono: true,
              testId: "load",
              value: status ? formatLoad(status.host.load_average) : "",
            },
            {
              id: "swap",
              label: d.swap,
              mono: true,
              value: mem
                ? fmt(d.swapUsed, {
                    used: formatBytes(mem.swap_total_bytes - mem.swap_free_bytes),
                    total: formatBytes(mem.swap_total_bytes),
                  })
                : "",
            },
            { id: "policy", label: d.policy, mono: true, value: status?.clock.policy },
            { id: "gate", label: d.gate, mono: true, value: status?.clock.gate },
          ]}
        />
      </div>
    </details>
  );
}

// The day: the jobs that ended, newest first (a Timeline), each one a link to its page.
function Today() {
  const recent = useJobList("recent", 5);
  const items = recent.data?.items ?? [];
  return (
    <Panel
      title={d.today}
      action={
        <Link
          to="/jobs"
          className="flex items-center gap-1 type-small font-medium text-primary-ink underline-offset-4 hover:underline"
        >
          {d.allJobs}
          <ChevronRightIcon className="size-4" aria-hidden="true" />
        </Link>
      }
      testId="today"
    >
      {recent.isPending ? (
        <div className="flex flex-col gap-4" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <p className="type-body text-ink-muted">{d.noJobsToday}</p>
      ) : (
        <Timeline
          items={items.map((j) => ({
            id: j.id,
            time: formatTime(j.finished_at ?? j.created_at).slice(0, 5),
            datetime: j.finished_at ?? j.created_at,
            tone: JOB_STATE_TONE[j.state] ?? "neutral",
            title: (
              <Link to={`/jobs/${encodeURIComponent(j.id)}`} className="underline-offset-4 hover:underline">
                {jobKindText(j.kind)}
              </Link>
            ),
            description: strings.progress.states[j.state] ?? j.state,
          }))}
        />
      )}
    </Panel>
  );
}

// The dashboard (console/design.md section 8.4, Monitor): the compact hero with the state in one sentence, 4 cards of
// a fixed size (health, backups, updates, names), then the services, this device, the names, and the day.
export default function DashboardPage() {
  const status = useStatus(10_000);
  const now = useNow(true, 30_000);
  const s = status.data;
  const loading = !s;
  const problems = s ? problemsOf(s) : null;
  const lastCopy = lastOffsiteCopy(s?.backups);
  const backups = s?.backups;
  const health = backups?.health;
  const backupTone: Tone = !health
    ? "neutral"
    : health.state === "ok"
      ? "ok"
      : health.state === "error"
        ? "danger"
        : "warn";
  const updates = s?.updates ?? null;
  const updateLine = updates ? summaryText(updates) : null;
  const names = s ? servedNames(s) : [];
  const publicName = names.find((n) => n.mode === "public");
  const stateTone = s ? (STATE_TONE[s.state] ?? "neutral") : "neutral";
  return (
    <div className="flex flex-col gap-6">
      <Hero
        size="compact"
        as="h1"
        eyebrow={s ? (s.network.mdns_advertised ?? s.hostnames[0]) : "\u00a0"}
        title={s ? (d.heroTitles[s.state] ?? d.title) : d.title}
        description={
          s ? (
            <>
              {s.state === "maintenance"
                ? d.heroMaintenance
                : problems && problems.items.length > 0
                  ? d.heroProblems
                  : d.heroOk}{" "}
              {/* line-height 1: the monospace font does not make its line taller than the skeleton (360 px). */}
              <span className="type-data" style={{ lineHeight: 1 }} data-testid="status-updated">
                {fmt(d.updatedShort, { time: formatTime(s.generated_at) })}
              </span>
            </>
          ) : undefined
        }
        loading={loading}
        illustration="pi-box"
        testId="dashboard-hero"
      />
      {status.isError ? <ProblemAlert error={status.error} onRetry={() => void status.refetch()} /> : null}
      {/* The banner slot of the backups (plans/02 section 7). Status.backups is GET /backups: no second request. */}
      <BackupHealthRow
        health={s?.backups?.health}
        lastCopy={lastCopy}
        loading={!s}
        unavailable={Boolean(s) && !s?.backups}
      />
      <DashboardGrid>
        <StatCard
          label={d.state}
          icon={ActivityIcon}
          loading={loading}
          value={
            <span data-testid="status-state" data-tone={stateTone}>
              {s ? (d.states[s.state] ?? s.state) : ""}
            </span>
          }
          hint={
            problems
              ? problems.items.length === 0
                ? d.noProblems
                : fmt(d.problemCount, { count: problems.items.length })
              : undefined
          }
          hintTone={problems && problems.items.length > 0 ? "warn" : "ok"}
          testId="stat-health"
        />
        <StatCard
          label={d.backupsCard}
          icon={DatabaseBackupIcon}
          loading={loading}
          value={!backups ? "—" : lastCopy ? formatAgo(lastCopy, now).replace(/ ago$/, "") : d.noCopy}
          unit={lastCopy ? d.ago : undefined}
          hint={!backups ? d.backupsNotAvailable : lastCopy ? d.lastCopy : d.noCopyHint}
          hintTone={backupTone}
          action={
            backups ? (
              <Link
                to="/backups"
                className="type-small font-medium text-primary-ink underline-offset-4 hover:underline"
              >
                {strings.common.open}
              </Link>
            ) : null
          }
          testId="stat-backups"
        />
        <StatCard
          label={d.updates}
          icon={RefreshCwIcon}
          loading={loading}
          value={updates?.current_version ?? "—"}
          hint={
            <span data-testid="updates-line">
              {updateLine ? (
                updateLine.link ? (
                  <Link to="/updates" className="underline-offset-4 hover:underline">
                    {updateLine.text}
                  </Link>
                ) : (
                  updateLine.text
                )
              ) : (
                d.updatesNotAvailable
              )}
            </span>
          }
          hintTone={updateLine?.variant === "warning" ? "warn" : updateLine?.link ? "primary" : "neutral"}
          testId="updates-summary"
        />
        <StatCard
          label={d.names}
          icon={GlobeIcon}
          loading={loading}
          value={<span className="type-data text-xl leading-9">{publicName?.host ?? names[0]?.host ?? "—"}</span>}
          hint={
            <span className="flex gap-1">
              {(["local", "public", "tailscale"] as HostMode[])
                .filter((m) => names.some((n) => n.mode === m))
                .map((m) => (
                  <StatusChip key={m} tone={MODE_TONE[m]} size="sm">
                    {d.modes[m]}
                  </StatusChip>
                ))}
            </span>
          }
          testId="stat-names"
        />
      </DashboardGrid>
      <Panel title={d.problems} testId="problems-card">
        <ul className="flex min-h-12 flex-col gap-2 type-body" aria-live="polite" data-testid="status-problems">
          {!problems ? (
            <li>
              <Skeleton className="h-5 w-64" />
            </li>
          ) : problems.items.length === 0 ? (
            <li className="text-ink-muted">{d.noProblems}</li>
          ) : (
            problems.items.map((p) => (
              <li
                key={p}
                className="flex items-start gap-2 text-ink"
                data-testid={p === problems.maintenance ? "status-maintenance" : undefined}
              >
                <TriangleAlertIcon className="mt-1 size-4 shrink-0 text-warn-solid" aria-hidden="true" />
                {p}
              </li>
            ))
          )}
        </ul>
      </Panel>
      <div className="grid gap-6 lg:grid-cols-2">
        <Services status={s} />
        <div className="flex min-w-0 flex-col gap-6">
          <Device status={s} />
          <Names status={s} />
          <Today />
        </div>
      </div>
      <Technical status={s} />
    </div>
  );
}
