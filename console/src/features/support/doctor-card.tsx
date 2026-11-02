import { useQuery } from "@tanstack/react-query";
import { RefreshCwIcon, StethoscopeIcon } from "lucide-react";
import { api, call } from "@/api/client";
import { qk } from "@/api/queries";
import type { DoctorCheck } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { formatDateTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";

const dc = strings.support.doctor;

const VARIANT: Record<DoctorCheck["status"], "success" | "warning" | "destructive" | "neutral"> = {
  ok: "success",
  warn: "warning",
  fail: "destructive",
  skip: "neutral",
};

// Phase 7: the result of care-pi doctor (GET /api/v1/doctor). The agent runs the checks at each read; "Run again"
// reads it again. The failed checks come first.
export function DoctorCard() {
  const doctor = useQuery({ queryKey: qk.doctor, queryFn: () => call(api.GET("/doctor")), staleTime: 60_000 });
  const rep = doctor.data;
  const counts = rep
    ? rep.checks.reduce<Record<string, number>>((m, c) => ({ ...m, [c.status]: (m[c.status] ?? 0) + 1 }), {})
    : {};
  return (
    <Card size="sm" data-testid="doctor-card" data-status={rep?.status ?? ""}>
      <CardHeader>
        <CardTitle>
          <h2 className="flex items-center gap-2">
            <StethoscopeIcon className="size-4" aria-hidden="true" />
            {dc.title}
          </h2>
        </CardTitle>
        <CardDescription>{dc.intro}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          {rep ? (
            <>
              <Badge variant={VARIANT[rep.status]} data-testid="doctor-status">
                {dc.statuses[rep.status]}
              </Badge>
              <span className="text-xs text-soft-foreground">
                {fmt(dc.summary, {
                  fail: counts.fail ?? 0,
                  warn: counts.warn ?? 0,
                  ok: counts.ok ?? 0,
                  time: formatDateTime(rep.generated_at),
                })}
              </span>
            </>
          ) : null}
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="ml-auto"
            onClick={() => void doctor.refetch()}
            disabled={doctor.isFetching}
            data-testid="doctor-run"
          >
            {doctor.isFetching ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
            {dc.run}
          </Button>
        </div>
        {doctor.error ? <ProblemAlert error={doctor.error} /> : null}
        {rep ? (
          <ul className="flex flex-col divide-y rounded-lg border" data-testid="doctor-checks">
            {rep.checks.map((c) => (
              <li key={c.id} className="flex flex-col gap-1 px-3 py-2 text-sm" data-check={c.id} data-status={c.status}>
                <div className="flex min-w-0 items-center gap-2">
                  <Badge variant={VARIANT[c.status]} size="xs" className="shrink-0">
                    {dc.statuses[c.status]}
                  </Badge>
                  <span className="min-w-0 truncate font-medium">{c.title}</span>
                </div>
                <p className="text-xs break-words text-soft-foreground">{c.detail}</p>
                {c.fix ? (
                  <div className="flex min-w-0 items-start gap-1">
                    <code className="min-w-0 flex-1 rounded bg-soft-background px-1.5 py-0.5 text-xs break-words">
                      {c.fix}
                    </code>
                    <CopyButton value={c.fix} />
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        ) : doctor.isLoading ? (
          <div className="flex flex-col gap-2" aria-busy="true">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
