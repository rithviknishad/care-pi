import { ClockIcon } from "lucide-react";
import { asProblem } from "@/api/problem";
import { usePhaseProbe } from "@/api/queries";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { fmt, strings } from "@/lib/strings";

// A page of a later phase. The agent answers 501 not-implemented-in-this-version with the phase.
export function UnavailablePage({ feature, title }: { feature: "backups" | "updates" | "plugs"; title: string }) {
  const probe = usePhaseProbe(feature);
  const problem = asProblem(probe.error);
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={title} />
      {probe.isPending ? (
        <Skeleton className="h-56" />
      ) : problem?.is("not-implemented-in-this-version") ? (
        <Empty className="min-h-56 rounded-lg border border-dashed" data-testid="not-available">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ClockIcon />
            </EmptyMedia>
            <EmptyTitle>{strings.unavailable.title}</EmptyTitle>
            <EmptyDescription>
              {fmt(strings.unavailable.phase, { phase: problem.problem.phase ?? "?" })}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : probe.isError ? (
        <ProblemAlert error={probe.error} />
      ) : (
        <Empty className="min-h-56 rounded-lg border border-dashed">
          <EmptyHeader>
            <EmptyTitle>{strings.unavailable.title}</EmptyTitle>
          </EmptyHeader>
        </Empty>
      )}
    </div>
  );
}
