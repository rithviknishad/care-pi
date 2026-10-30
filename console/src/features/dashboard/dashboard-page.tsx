import { AlertTriangleIcon, CheckCircle2Icon } from "lucide-react";
import { Link } from "react-router";
import { useStatus } from "@/api/queries";
import type { Status, UnitStatus } from "@/api/types";
import { DataList, DataRow, PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useStableOrder } from "@/hooks/use-stable-order";
import {
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
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

const d = strings.dashboard;
const unitKey = (u: UnitStatus) => u.unit;

const STATE_VARIANT: Record<string, "success" | "warning" | "destructive" | "info" | "neutral"> = {
  ok: "success",
  degraded: "destructive",
  maintenance: "warning",
  "clock-wait": "warning",
  starting: "info",
  setup: "info",
};

function Section({ title, children, className }: { title: string; children: React.ReactNode; className?: string }) {
  return (
    <Card size="sm" className={className}>
      <CardHeader>
        <CardTitle>
          <h2>{title}</h2>
        </CardTitle>
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

function StateCard({ status }: { status: Status | undefined }) {
  const flags = status
    ? (Object.entries(status.flags) as [string, boolean][])
        .filter(([k, on]) => on && k !== "degraded")
        .map(([k]) => d.flags[k] ?? k)
    : [];
  const items = status ? [...flags, ...status.problems] : [];
  return (
    <Card size="sm">
      <CardContent className="grid gap-4 sm:grid-cols-[14rem_minmax(0,1fr)]">
        <div className="flex flex-col gap-2">
          <span className="text-sm text-soft-foreground">{d.state}</span>
          <div className="flex h-8 items-center">
            {status ? (
              <Badge variant={STATE_VARIANT[status.state] ?? "neutral"} size="lg" data-testid="status-state">
                {d.states[status.state] ?? status.state}
              </Badge>
            ) : (
              <Skeleton className="h-7 w-24" />
            )}
          </div>
        </div>
        <div className="flex flex-col gap-2">
          <span className="text-sm text-soft-foreground">{d.problems}</span>
          <ul className="flex min-h-16 flex-col gap-1 text-sm" aria-live="polite" data-testid="status-problems">
            {!status ? (
              <li>
                <Skeleton className="h-4 w-48" />
              </li>
            ) : items.length === 0 ? (
              <li className="flex items-center gap-2">
                <CheckCircle2Icon className="size-4 text-green-600 dark:text-green-400" aria-hidden="true" />
                {d.noProblems}
              </li>
            ) : (
              items.map((p) => (
                <li key={p} className="flex items-start gap-2">
                  <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-600" aria-hidden="true" />
                  {p}
                </li>
              ))
            )}
          </ul>
        </div>
      </CardContent>
    </Card>
  );
}

function MemoryBar({ used, cap }: { used: number | null; cap: number | null }) {
  const p = percent(used, cap);
  return (
    <div className="flex w-full min-w-40 items-center gap-3">
      <Progress
        value={p ?? 0}
        aria-label={d.memory}
        className={cn(
          "w-20 shrink-0 gap-0",
          p !== null && p > 90 && "[&_[data-slot=progress-indicator]]:bg-destructive",
        )}
      />
      <span className="w-[19ch] truncate text-xs text-soft-foreground">
        {used === null
          ? "—"
          : cap
            ? fmt(d.memoryOf, { used: formatBytes(used), cap: formatBytes(cap) })
            : formatBytes(used)}
      </span>
    </div>
  );
}

// GET /status lists 10 units: care.target, care-clock, and the 8 units of the stack (agent/internal/status).
const UNIT_ROWS = 10;

function UnitsTable({ status }: { status: Status | undefined }) {
  const units = useStableOrder(status?.units, unitKey);
  return (
    <Table data-testid="units">
      <TableHeader>
        <TableRow>
          <TableHead>{d.unit}</TableHead>
          <TableHead className="w-[14ch]">{d.unitState}</TableHead>
          <TableHead className="w-[10ch]">{d.health}</TableHead>
          <TableHead>{d.memory}</TableHead>
          <TableHead className="w-[8ch] text-right">{d.restarts}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {units
          ? units.map((u) => (
              <TableRow key={u.unit} className="h-11" data-unit={u.unit}>
                <TableCell className="max-w-[24ch] truncate">
                  {u.log_unit ? (
                    <Link to={`/logs?unit=${u.log_unit}`} className="underline-offset-4 hover:underline">
                      {u.unit}
                    </Link>
                  ) : (
                    u.unit
                  )}
                </TableCell>
                <TableCell>
                  <span className={cn(u.active_state === "active" ? "" : "text-amber-700 dark:text-amber-300")}>
                    {u.active_state}/{u.sub_state}
                  </span>
                </TableCell>
                <TableCell>
                  {u.health ? (
                    <Badge
                      size="sm"
                      variant={u.health === "healthy" ? "success" : u.health === "starting" ? "info" : "destructive"}
                    >
                      {u.health}
                    </Badge>
                  ) : (
                    <span className="text-soft-foreground">—</span>
                  )}
                </TableCell>
                <TableCell>
                  <MemoryBar used={u.memory_current_bytes} cap={u.memory_max_bytes} />
                </TableCell>
                <TableCell className="text-right">{u.restarts}</TableCell>
              </TableRow>
            ))
          : Array.from({ length: UNIT_ROWS }, (_, i) => (
              <TableRow key={i} className="h-11">
                <TableCell colSpan={5}>
                  <Skeleton className="h-4 w-full" />
                </TableCell>
              </TableRow>
            ))}
      </TableBody>
    </Table>
  );
}

export default function DashboardPage() {
  const status = useStatus(10_000);
  const s = status.data;
  const loading = !s;
  const mem = s?.host.memory;
  const care = s?.images.find((i) => i.name === "care");
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={d.title}
        description={
          <span className="tabular-nums" data-testid="status-updated">
            {fmt(d.updated, { time: s ? formatTime(s.generated_at) : "--:--:--" })}
          </span>
        }
      />
      {status.isError ? <ProblemAlert error={status.error} /> : null}
      <StateCard status={s} />
      <div className="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
        <Section title={d.host}>
          <DataList>
            <DataRow
              label={d.model}
              loading={loading}
              value={s?.host.model ?? "—"}
              title={s?.host.model ?? undefined}
            />
            <DataRow label={d.os} loading={loading} value={s?.host.os} title={s?.host.os} />
            <DataRow label={d.kernel} loading={loading} value={s?.host.kernel} />
            <DataRow label={d.cpus} loading={loading} value={s ? `${s.host.cpu_count} × ${s.host.arch}` : ""} />
            <DataRow label={d.load} loading={loading} value={s ? formatLoad(s.host.load_average) : ""} testId="load" />
            <DataRow label={d.uptime} loading={loading} value={s ? formatDuration(s.host.uptime_s) : ""} />
          </DataList>
        </Section>
        <Section title={d.ram}>
          <DataList>
            <DataRow label={d.ram} loading={loading} value={formatBytes(mem?.total_bytes)} />
            <DataRow
              label={d.available}
              loading={loading}
              value={
                mem
                  ? `${formatBytes(mem.available_bytes)} (${formatPercent(percent(mem.available_bytes, mem.total_bytes))})`
                  : ""
              }
              testId="mem-available"
            />
            <DataRow
              label={d.swap}
              loading={loading}
              value={
                mem
                  ? fmt(d.swapUsed, {
                      used: formatBytes(mem.swap_total_bytes - mem.swap_free_bytes),
                      total: formatBytes(mem.swap_total_bytes),
                    })
                  : ""
              }
            />
            {(s?.disks ?? [null]).map((disk, i) =>
              disk ? (
                <DataRow
                  key={disk.mount}
                  label={`${d.disks} ${disk.mount}`}
                  value={fmt(d.free, { free: formatBytes(disk.free_bytes), total: formatBytes(disk.total_bytes) })}
                />
              ) : (
                <DataRow key={i} label={d.disks} loading value="" />
              ),
            )}
          </DataList>
        </Section>
        <Section title={d.clock}>
          <DataList>
            <DataRow label={d.time} loading={loading} value={s ? formatDateTime(s.clock.time) : ""} />
            <DataRow label={d.timezone} loading={loading} value={s?.clock.timezone} />
            <DataRow label={d.policy} loading={loading} value={s?.clock.policy} />
            <DataRow
              label={d.synchronized}
              loading={loading}
              value={s ? (s.clock.synchronized ? strings.common.yes : strings.common.no) : ""}
            />
            <DataRow label={d.gate} loading={loading} value={s?.clock.gate} />
          </DataList>
        </Section>
        <Section title={d.network}>
          <DataList>
            <DataRow label={d.configured} loading={loading} value={s?.network.mdns_configured} />
            <DataRow
              label={d.advertised}
              loading={loading}
              value={
                <span className={cn(s?.flags.mdns_collision && "text-amber-700 dark:text-amber-300")}>
                  {s?.network.mdns_advertised ?? "—"}
                </span>
              }
            />
            <DataRow label={d.addresses} loading={loading} value={s?.addresses.join(", ")} />
            <DataRow
              label={d.internet}
              loading={loading}
              value={
                s?.network.internet_reachable === null
                  ? "—"
                  : s?.network.internet_reachable
                    ? strings.common.yes
                    : strings.common.no
              }
            />
          </DataList>
        </Section>
        <Section title={d.lock}>
          <DataList>
            <DataRow
              label={d.solution}
              loading={loading}
              value={s?.lock ? `${s.lock.solution.name} (${s.lock.solution.track})` : d.noLock}
            />
            <DataRow
              label={d.careDigest}
              loading={loading}
              value={shortDigest(care?.pull)}
              title={care?.pull}
              testId="care-digest"
            />
            <DataRow
              label={d.careFeSha}
              loading={loading}
              value={shortSha(s?.care_fe_sha)}
              title={s?.care_fe_sha ?? undefined}
            />
            <DataRow label={d.buildId} loading={loading} value={s?.fe_build_id ?? "—"} testId="fe-build-id" />
            <DataRow label={d.lockedAt} loading={loading} value={formatDateTime(s?.locked_at)} />
            <DataRow label={d.agent} loading={loading} value={s?.agent.version} />
          </DataList>
        </Section>
        <Section title={d.certificate}>
          <DataList>
            <DataRow
              label={d.fingerprint}
              loading={loading}
              value={
                s?.tls.local_ca.fingerprint_sha256 ? (
                  <Link to="/certificate" className="underline-offset-4 hover:underline" data-testid="fingerprint">
                    {shortSha(s.tls.local_ca.fingerprint_sha256, 16)}…
                  </Link>
                ) : (
                  "—"
                )
              }
              title={s?.tls.local_ca.fingerprint_sha256 ?? undefined}
            />
            <DataRow label={d.notAfter} loading={loading} value={formatDateTime(s?.tls.local_ca.not_after)} />
            <DataRow
              label={d.runningJob}
              loading={loading}
              value={s?.jobs.running ? `${s.jobs.running.kind}: ${s.jobs.running.step ?? ""}` : d.noJob}
            />
            <DataRow
              label={d.lastJob}
              loading={loading}
              value={
                s?.jobs.last
                  ? `${s.jobs.last.kind} ${s.jobs.last.state} ${formatTime(s.jobs.last.finished_at)}`
                  : d.noJob
              }
            />
          </DataList>
        </Section>
      </div>
      <Section title={d.units}>
        <UnitsTable status={s} />
      </Section>
    </div>
  );
}
