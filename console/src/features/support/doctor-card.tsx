import { useQuery } from "@tanstack/react-query";
import { CheckIcon, CircleAlertIcon, MinusIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react";
import { api, call } from "@/api/client";
import { exportDoctor } from "@/api/exports";
import { qk } from "@/api/queries";
import type { DoctorCheck } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/v2/copy-button";
import { MonoBlock } from "@/components/v2/mono-block";
import { Panel } from "@/components/v2/panel";
import { StatusChip } from "@/components/v2/status-chip";
import type { Tone } from "@/components/v2/tone";
import { formatDateTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

const dc = strings.support.doctor;

const TONE: Record<DoctorCheck["status"], Tone> = { ok: "ok", warn: "warn", fail: "danger", skip: "neutral" };

function CheckMark({ status }: { status: DoctorCheck["status"] }) {
  const base = "mt-0.5 grid size-6 shrink-0 place-items-center rounded-full";
  if (status === "ok")
    return (
      <span className={cn(base, "bg-ok-solid text-white dark:text-black/80")}>
        <CheckIcon className="size-4" strokeWidth={3} aria-hidden="true" />
      </span>
    );
  if (status === "fail")
    return <CircleAlertIcon className="mt-0.5 size-6 shrink-0 text-danger-solid" aria-hidden="true" />;
  if (status === "warn")
    return <TriangleAlertIcon className="mt-0.5 size-6 shrink-0 text-warn-solid" aria-hidden="true" />;
  return (
    <span className={cn(base, "border-2 border-line-strong text-ink-soft")}>
      <MinusIcon className="size-3.5" strokeWidth={3} aria-hidden="true" />
    </span>
  );
}

// Phase 7, S66, S68: the result of care-pi doctor (GET /api/v1/doctor) as a checklist. Each check that needs work
// has its fix as a command with Copy; "Copy the result" copies the plain text of the agent (GET /doctor/export).
// The agent runs the checks at each read; "Run again" reads it again. The failed checks come first.
export function DoctorCard() {
  const doctor = useQuery({ queryKey: qk.doctor, queryFn: () => call(api.GET("/doctor")), staleTime: 60_000 });
  const rep = doctor.data;
  const counts = rep
    ? rep.checks.reduce<Record<string, number>>((m, c) => ({ ...m, [c.status]: (m[c.status] ?? 0) + 1 }), {})
    : {};
  return (
    <div data-testid="doctor-card" data-status={rep?.status ?? ""}>
      <Panel
        title={dc.title}
        description={dc.intro}
        action={
          rep ? (
            <StatusChip tone={TONE[rep.status]} testId="doctor-status" width={7}>
              {dc.statuses[rep.status]}
            </StatusChip>
          ) : (
            <Skeleton className="h-7 w-24 rounded-full" />
          )
        }
        footer={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={() => void doctor.refetch()}
              disabled={doctor.isFetching}
              data-testid="doctor-run"
            >
              {doctor.isFetching ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
              {dc.run}
            </Button>
            <CopyButton text={() => exportDoctor()} support label={dc.copy} size="default" />
          </>
        }
      >
        <p className="min-h-5 type-small text-ink-soft">
          {rep
            ? fmt(dc.summary, {
                fail: counts.fail ?? 0,
                warn: counts.warn ?? 0,
                ok: counts.ok ?? 0,
                time: formatDateTime(rep.generated_at),
              })
            : "\u00a0"}
        </p>
        {doctor.error ? <ProblemAlert error={doctor.error} onRetry={() => void doctor.refetch()} /> : null}
        {rep ? (
          <ul className="flex flex-col divide-y divide-line-soft" data-testid="doctor-checks">
            {rep.checks.map((c) => (
              <li
                key={c.id}
                className="grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-3 py-3"
                data-check={c.id}
                data-status={c.status}
              >
                <CheckMark status={c.status} />
                <div className="flex min-w-0 flex-col gap-1">
                  <p className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="min-w-0 type-body font-medium text-ink">{c.title}</span>
                    {c.status !== "ok" ? (
                      <StatusChip tone={TONE[c.status]} size="sm">
                        {dc.statuses[c.status]}
                      </StatusChip>
                    ) : (
                      <span className="sr-only">{dc.statuses[c.status]}</span>
                    )}
                  </p>
                  <p className="type-small break-words text-ink-muted">{c.detail}</p>
                  {c.fix ? (
                    <div className="mt-1 flex flex-col gap-1">
                      <span className="type-small font-medium text-ink">{dc.fix}</span>
                      <MonoBlock text={c.fix} mode="wrap" />
                    </div>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        ) : doctor.isLoading ? (
          <div className="flex flex-col gap-2" aria-busy="true">
            <Skeleton className="h-12" />
            <Skeleton className="h-12" />
            <Skeleton className="h-12" />
          </div>
        ) : null}
      </Panel>
    </div>
  );
}
