import { ArrowLeftIcon } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { asProblem } from "@/api/problem";
import { usePhaseProbe, useStatus } from "@/api/queries";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatZoned } from "@/features/updates/window";
import { fmt, strings } from "@/lib/strings";
import { NotAvailable } from "@/features/unavailable/unavailable-page";

const r = strings.restore;

// The target of the PITR offer of an update: /restore?time=<RFC 3339 time> (docs/console-ui.md section 10.4).
// The Restore page of Phase 3 replaces this page. In this version the agent answers GET /restore/points with
// 501 not-implemented-in-this-version, so the page tells the time of the offer and what to do.
export default function RestorePage() {
  const [params] = useSearchParams();
  const raw = params.get("time");
  const time = raw && !Number.isNaN(Date.parse(raw)) ? raw : null;
  const probe = usePhaseProbe("restore");
  const status = useStatus(false);
  const tz = status.data?.clock.timezone ?? "UTC";
  const problem = asProblem(probe.error);
  const offered = time ? (
    <div className="flex flex-col gap-1 text-sm" data-testid="restore-time" data-time={time}>
      <p className="font-semibold">{fmt(r.offered, { time: formatZoned(time, tz) })}</p>
      <p className="text-soft-foreground">{fmt(r.utc, { time: formatZoned(time, "UTC") })}</p>
    </div>
  ) : (
    <p className="text-sm text-soft-foreground">{r.noTime}</p>
  );
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={r.title} />
      {probe.isPending ? (
        <Skeleton className="h-56" />
      ) : problem?.is("not-implemented-in-this-version") ? (
        <NotAvailable phase={problem.problem.phase}>
          {offered}
          <p className="max-w-prose text-sm text-soft-foreground">{r.later}</p>
        </NotAvailable>
      ) : probe.isError ? (
        <ProblemAlert error={probe.error} />
      ) : (
        <div className="flex flex-col gap-3 rounded-lg border p-4">
          {offered}
          <p className="max-w-prose text-sm text-soft-foreground">{r.later}</p>
        </div>
      )}
      <Button asChild variant="outline" className="self-start">
        <Link to="/updates">
          <ArrowLeftIcon data-icon="inline-start" />
          {r.back}
        </Link>
      </Button>
    </div>
  );
}
