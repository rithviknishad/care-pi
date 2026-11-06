import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckIcon, ChevronDownIcon, CircleAlertIcon, CpuIcon, FolderCheckIcon, TriangleAlertIcon } from "lucide-react";
import { api, call } from "@/api/client";
import { qk, useHostCheck } from "@/api/queries";
import type { HostCheck, HostCheckItem } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { InlineSkeleton } from "@/components/v2/inline-skeleton";
import { KeyValueList } from "@/components/v2/key-value-list";
import { ProblemCard } from "@/components/v2/problem-card";
import { StatusChip } from "@/components/v2/status-chip";
import { formatBytes, formatMbps } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { groupChecks, hardwareWarnings } from "../hardware-logic";
import { StepActions, type StepProps } from "../step-frame";

const h = strings.hardware;

function Facts({ check }: { check: HostCheck | undefined }) {
  const hw = check?.hardware;
  return (
    <KeyValueList
      loading={!hw}
      labelWidth="md"
      items={[
        { id: "model", label: h.model, value: hw?.model ?? strings.common.none },
        { id: "cpu", label: h.cpu, value: hw ? `${hw.cpu_count} × ${hw.arch}` : "", mono: true },
        { id: "ram", label: h.ram, value: formatBytes(hw?.ram_bytes), mono: true },
        { id: "page", label: h.pageSize, value: hw ? formatBytes(hw.page_size_bytes) : "", mono: true },
        {
          id: "disk",
          label: h.disk,
          value: hw
            ? `${h.diskKinds[hw.root_disk.kind] ?? hw.root_disk.kind}, ${formatBytes(hw.root_disk.size_bytes)} (${hw.root_disk.device})`
            : "",
        },
        {
          id: "speed",
          label: h.diskSpeed,
          value: hw?.disk_probe
            ? fmt(h.speed, { write: hw.disk_probe.write_mb_s.toFixed(0), read: hw.disk_probe.read_mb_s.toFixed(0) })
            : h.notMeasured,
        },
        { id: "rtc", label: h.rtc, value: hw?.rtc_present ? h.present : h.absent },
        {
          id: "eth",
          label: h.ethernet,
          value: hw
            ? hw.ethernet.link
              ? `${h.connected}, ${formatMbps(hw.ethernet.speed_mbps)}, ${hw.ethernet.address ?? "—"}`
              : h.notConnected
            : "",
        },
        {
          id: "internet",
          label: h.internet,
          value: hw?.internet_reachable === null ? h.notChecked : hw?.internet_reachable ? h.reachable : h.notReachable,
        },
        { id: "clock", label: h.clock, value: hw?.clock_synchronized ? h.synchronized : h.notSynchronized },
      ]}
    />
  );
}

function CheckRow({ check }: { check: HostCheckItem }) {
  return (
    <li className="grid grid-cols-[1.5rem_minmax(0,1fr)_auto] items-start gap-x-3 py-2.5" data-check={check.name}>
      {check.ok ? (
        <span className="mt-0.5 grid size-5 place-items-center rounded-full bg-ok-solid text-white dark:text-black/80">
          <CheckIcon className="size-3.5" strokeWidth={3} aria-label={h.passed} />
        </span>
      ) : check.level === "required" ? (
        <CircleAlertIcon className="mt-0.5 size-5 text-danger-solid" aria-label={h.failed} />
      ) : (
        <TriangleAlertIcon className="mt-0.5 size-5 text-warn-solid" aria-label={h.failed} />
      )}
      <div className="min-w-0">
        <p className="type-small font-semibold text-ink">{check.name}</p>
        <p className="type-data break-words text-ink-soft">{check.detail}</p>
        {!check.ok && check.fix ? <p className="type-small text-ink">{fmt(h.fix, { fix: check.fix })}</p> : null}
      </div>
      <StatusChip tone={check.level === "required" ? "neutral" : "info"} size="sm">
        {check.level === "required" ? h.required : h.advised}
      </StatusChip>
    </li>
  );
}

// Each check of the agent: the failures first, the passed checks, and one row for the directories.
function Checks({ check }: { check: HostCheck }) {
  const g = groupChecks(check.checks);
  return (
    <div className="flex flex-col gap-2" data-testid="host-checks">
      {g.problems.length > 0 ? (
        <ul className="divide-y divide-line-soft" data-testid="host-check-problems">
          {g.problems.map((c) => (
            <CheckRow key={c.name} check={c} />
          ))}
        </ul>
      ) : null}
      <ul className="divide-y divide-line-soft" data-testid="host-check-passed">
        {g.passed.map((c) => (
          <CheckRow key={c.name} check={c} />
        ))}
        {g.dirs.total > 0 ? (
          <li className="grid grid-cols-[1.5rem_minmax(0,1fr)] items-start gap-x-3 py-2.5" data-check="directories">
            <FolderCheckIcon
              className={cn("mt-0.5 size-5", g.dirs.ok === g.dirs.total ? "text-ok-solid" : "text-warn-solid")}
              aria-hidden="true"
            />
            <div className="min-w-0">
              <p className="type-small font-semibold text-ink">
                {h.directories}:{" "}
                {g.dirs.ok === g.dirs.total
                  ? fmt(h.directoriesOk, { ok: g.dirs.ok })
                  : fmt(h.directoriesSome, { ok: g.dirs.ok, total: g.dirs.total })}
              </p>
              <p className="type-small text-ink-soft">{h.directoriesDetail}</p>
            </div>
          </li>
        ) : null}
      </ul>
    </div>
  );
}

// "Raspberry Pi 5 · 2 GB memory · 58 GB disk · cable": the device in one line of plain words.
function oneLine(check: HostCheck): string {
  const hw = check.hardware;
  return [
    hw.model ?? null,
    fmt(h.summaryRam, { ram: formatBytes(hw.ram_bytes) }),
    fmt(h.summaryDisk, { disk: formatBytes(hw.root_disk.size_bytes) }),
    hw.ethernet.link ? h.summaryCable : h.summaryNoCable,
  ]
    .filter(Boolean)
    .join(" · ");
}

// The hardware step (S66): a short, friendly summary of the device, the notes in plain words, and "Details" with each
// fact and each check of the agent.
export function HardwareStep({ onDone, onBack, next }: StepProps) {
  const qc = useQueryClient();
  const check = useHostCheck();
  const probe = useMutation({
    mutationFn: () => call(api.GET("/host/check", { params: { query: { probe: true } } })),
    onSuccess: (data) => qc.setQueryData(qk.hostCheck, data),
  });
  const ack = useMutation({
    mutationFn: () => call(api.PUT("/setup/steps/hardware", { body: { acknowledged: true } })),
    onSuccess: onDone,
  });
  const data = check.data;
  const warnings = data ? hardwareWarnings(data.hardware) : [];
  const g = data ? groupChecks(data.checks) : null;
  const summary = g
    ? g.ok === g.total
      ? fmt(h.allPassed, { total: g.total })
      : fmt(h.somePassed, { passed: g.ok, total: g.total })
    : "";
  const state = !data ? null : !data.ok ? "failed" : warnings.length > 0 ? "notes" : "ready";

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        ack.mutate();
      }}
    >
      {check.isError ? <ProblemAlert error={check.error} onRetry={() => void check.refetch()} /> : null}
      <section
        className="flex items-start gap-4 rounded-card border border-line-soft bg-surface-tint p-4 sm:p-5"
        data-testid="hardware-summary"
        data-state={state ?? undefined}
        aria-busy={!data}
      >
        <span
          className={cn(
            "grid size-11 shrink-0 place-items-center rounded-lg",
            state === "failed"
              ? "bg-danger-bg text-danger-solid"
              : state === "notes"
                ? "bg-warn-bg text-warn-solid"
                : "bg-surface-tint-strong text-primary-solid",
          )}
        >
          {state === "failed" ? (
            <CircleAlertIcon className="size-5" aria-hidden="true" />
          ) : state === "notes" ? (
            <TriangleAlertIcon className="size-5" aria-hidden="true" />
          ) : (
            <CpuIcon className="size-5" aria-hidden="true" />
          )}
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <p className="min-h-7 type-h3 text-ink">
            {state ? h.summary[state] : <InlineSkeleton className="h-5 w-56" />}
          </p>
          <p className="min-h-6 type-body text-ink-muted">
            {data ? oneLine(data) : <InlineSkeleton className="h-4 w-72 max-w-full" />}
          </p>
          <p className="min-h-5 type-small text-ink-soft" data-testid="host-check-summary">
            {summary}
          </p>
        </div>
      </section>
      {warnings.length > 0 ? (
        <section className="flex flex-col gap-2" aria-labelledby="hardware-notes">
          <h3 id="hardware-notes" className="type-h3 text-ink">
            {h.notes}
          </h3>
          <ul className="flex flex-col gap-2">
            {warnings.map((w) => (
              <li key={w} className="flex items-start gap-2 type-body text-ink">
                <TriangleAlertIcon className="mt-1 size-4 shrink-0 text-warn-solid" aria-hidden="true" />
                {w}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {data && !data.ok ? (
        <ProblemCard
          title={h.failedRequired}
          steps={(g?.problems ?? []).filter((c) => c.level === "required").map((c) => c.fix || c.name)}
          onRetry={() => void check.refetch()}
          technical={(g?.problems ?? []).map((c) => `${c.name}: ${c.detail}`).join("\n")}
        />
      ) : null}
      <details className="group rounded-card border border-line-soft" data-testid="hardware-details">
        <summary className="flex h-12 cursor-pointer list-none items-center gap-2 px-4 type-small font-medium text-ink-muted outline-none select-none hover:text-ink focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
          <ChevronDownIcon
            aria-hidden="true"
            className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
          />
          {h.details}
        </summary>
        <div className="flex flex-col gap-6 px-4 pb-4">
          <section className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="type-h3 text-ink">{h.facts}</h3>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="ml-auto"
                onClick={() => probe.mutate()}
                disabled={probe.isPending || !data}
              >
                {probe.isPending ? <Spinner data-icon="inline-start" /> : null}
                {h.probe}
              </Button>
            </div>
            <p className="type-small text-ink-soft">{h.probeHelp}</p>
            <Facts check={data} />
          </section>
          <section className="flex flex-col gap-2">
            <h3 className="type-h3 text-ink">{h.checks}</h3>
            {data ? <Checks check={data} /> : null}
          </section>
        </div>
      </details>
      {ack.error ? <ProblemAlert error={ack.error} /> : null}
      <StepActions
        onBack={onBack}
        busy={ack.isPending}
        disabled={!data}
        submitLabel={warnings.length > 0 ? h.acknowledge : strings.common.continue}
        next={next}
      />
    </form>
  );
}
