import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangleIcon, CheckCircle2Icon, XCircleIcon } from "lucide-react";
import { api, call } from "@/api/client";
import { qk, useHostCheck } from "@/api/queries";
import type { HostCheck } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { formatBytes, formatMbps } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { StepActions, StepIntro, type StepProps } from "../step-frame";
import { hardwareWarnings } from "../hardware-logic";

function Facts({ check }: { check: HostCheck | undefined }) {
  const h = strings.hardware;
  const hw = check?.hardware;
  const loading = !hw;
  return (
    <DataList>
      <DataRow label={h.model} loading={loading} value={hw?.model ?? strings.common.none} />
      <DataRow label={h.cpu} loading={loading} value={hw ? `${hw.cpu_count} × ${hw.arch}` : ""} />
      <DataRow label={h.ram} loading={loading} value={formatBytes(hw?.ram_bytes)} />
      <DataRow label={h.pageSize} loading={loading} value={hw ? `${hw.page_size_bytes / 1024} KiB` : ""} />
      <DataRow
        label={h.disk}
        loading={loading}
        value={
          hw
            ? `${h.diskKinds[hw.root_disk.kind] ?? hw.root_disk.kind}, ${formatBytes(hw.root_disk.size_bytes)} (${hw.root_disk.device})`
            : ""
        }
      />
      <DataRow
        label={h.diskSpeed}
        loading={loading}
        value={
          hw?.disk_probe
            ? fmt(h.speed, { write: hw.disk_probe.write_mb_s.toFixed(0), read: hw.disk_probe.read_mb_s.toFixed(0) })
            : h.notMeasured
        }
      />
      <DataRow label={h.rtc} loading={loading} value={hw?.rtc_present ? h.present : h.absent} />
      <DataRow
        label={h.ethernet}
        loading={loading}
        value={
          hw
            ? hw.ethernet.link
              ? `${h.connected}, ${formatMbps(hw.ethernet.speed_mbps)}, ${hw.ethernet.address ?? "—"}`
              : h.notConnected
            : ""
        }
      />
      <DataRow
        label={h.internet}
        loading={loading}
        value={hw?.internet_reachable === null ? h.notChecked : hw?.internet_reachable ? h.reachable : h.notReachable}
      />
      <DataRow label={h.clock} loading={loading} value={hw?.clock_synchronized ? h.synchronized : h.notSynchronized} />
    </DataList>
  );
}

export function HardwareStep({ onDone, onBack }: StepProps) {
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
  const h = strings.hardware;
  const data = check.data;
  const warnings = data ? hardwareWarnings(data.hardware) : [];

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        ack.mutate();
      }}
    >
      <StepIntro>{h.intro}</StepIntro>
      {check.isError ? <ProblemAlert error={check.error} /> : null}
      <section className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="font-semibold">{h.facts}</h3>
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
        <p className="text-right text-xs text-soft-foreground">{h.probeHelp}</p>
        <Facts check={data} />
      </section>
      {warnings.length > 0 ? (
        <Alert variant="warning">
          <AlertTriangleIcon />
          <AlertDescription>
            <ul className="flex list-disc flex-col gap-1 pl-4">
              {warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      ) : null}
      <section className="flex flex-col gap-2">
        <h3 className="font-semibold">{h.checks}</h3>
        <ul className="divide-y rounded-lg border" data-testid="host-checks">
          {data
            ? data.checks.map((c) => (
                <li key={c.name} className="grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-start gap-x-3 px-3 py-2">
                  {c.ok ? (
                    <CheckCircle2Icon
                      className="mt-0.5 size-5 text-green-600 dark:text-green-400"
                      aria-label={h.passed}
                    />
                  ) : (
                    <XCircleIcon
                      className={cn("mt-0.5 size-5", c.level === "required" ? "text-destructive" : "text-amber-600")}
                      aria-label={h.failed}
                    />
                  )}
                  <div className="min-w-0">
                    <p className="text-sm font-semibold">{c.name}</p>
                    <p className="text-xs break-words text-soft-foreground">{c.detail}</p>
                    {!c.ok && c.fix ? <p className="text-xs">{fmt(h.fix, { fix: c.fix })}</p> : null}
                  </div>
                  <Badge variant={c.level === "required" ? "neutral" : "info"} size="sm">
                    {c.level === "required" ? h.required : h.advised}
                  </Badge>
                </li>
              ))
            : [0, 1, 2, 3, 4, 5].map((i) => (
                <li key={i} className="px-3 py-2">
                  <Skeleton className="h-10" />
                </li>
              ))}
        </ul>
      </section>
      {data && !data.ok ? (
        <Alert variant="destructive">
          <XCircleIcon />
          <AlertDescription>{h.failedRequired}</AlertDescription>
        </Alert>
      ) : null}
      {ack.error ? <ProblemAlert error={ack.error} /> : null}
      <StepActions onBack={onBack} busy={ack.isPending} disabled={!data} submitLabel={h.acknowledge} />
    </form>
  );
}
