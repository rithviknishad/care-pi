import { PuzzleIcon, RefreshCwIcon } from "lucide-react";
import { Link } from "react-router";
import { asProblem } from "@/api/problem";
import { usePlugs, useSolution } from "@/api/queries";
import type { Plug, PlugList, SolutionDetail } from "@/api/types";
import { DataList, DataRow, PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { SectionCard } from "@/components/app/section-card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { trackBadge } from "@/features/updates/update-text";
import { NotAvailable } from "@/features/unavailable/unavailable-page";
import { useStableOrder } from "@/hooks/use-stable-order";
import { strings } from "@/lib/strings";
import { PlugCard } from "./plug-card";

const p = strings.plugs;
const plugKey = (x: Plug) => x.id;

function SolutionHeader({ list, detail }: { list: PlugList | undefined; detail: SolutionDetail | undefined }) {
  const loading = !list;
  const track = trackBadge(list?.track);
  return (
    <SectionCard
      title={p.solution}
      testId="solution-card"
      action={
        <Button asChild variant="outline" size="sm">
          <Link to="/updates">
            <RefreshCwIcon data-icon="inline-start" />
            {p.openUpdates}
          </Link>
        </Button>
      }
    >
      <DataList>
        <DataRow
          label={p.solution}
          loading={loading || (!detail && Boolean(list?.solution))}
          value={detail ? `${detail.title} (${detail.name})` : (list?.solution ?? "")}
          testId="solution-name"
        />
        <DataRow
          label={p.description}
          loading={loading || (!detail && Boolean(list?.solution))}
          value={detail?.description || "—"}
          title={detail?.description}
        />
        <DataRow label={p.version} loading={loading} value={list?.version ?? strings.updates.noRelease} />
        <DataRow
          label={p.track}
          loading={loading}
          value={
            <Badge size="sm" variant={track.variant}>
              {track.text}
            </Badge>
          }
        />
        <DataRow label={p.count} loading={loading} value={list ? String(list.items.length) : ""} />
      </DataList>
      <p className="min-h-5 text-sm text-amber-700 dark:text-amber-300" data-testid="plugs-pending">
        {list?.pending ? p.pendingAll : ""}
      </p>
    </SectionCard>
  );
}

// The Solution and plugs page: GET /plugs and GET /solutions/{name} (docs/api/README.md sections 14.6 and 14.7).
export default function PlugsPage() {
  const plugs = usePlugs();
  const list = plugs.data;
  const solution = useSolution(list?.solution);
  const items = useStableOrder(list?.items, plugKey);
  const problem = asProblem(plugs.error);
  if (problem?.is("not-implemented-in-this-version")) {
    return (
      <div className="flex flex-col gap-4">
        <PageHeader title={p.title} />
        <NotAvailable phase={problem.problem.phase} />
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={p.title} description={p.intro} />
      {plugs.isError ? <ProblemAlert error={plugs.error} /> : null}
      <SolutionHeader list={list} detail={solution.data} />
      {!items ? (
        <div className="flex flex-col gap-4" aria-busy="true">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-56 rounded-xl" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <Empty className="min-h-56 rounded-lg border border-dashed" data-testid="plugs-empty">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <PuzzleIcon />
            </EmptyMedia>
            <EmptyTitle>{p.empty}</EmptyTitle>
            <EmptyDescription>{p.emptyHelp}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ul className="flex flex-col gap-4" aria-label={p.title}>
          {items.map((plug) => (
            <li key={plug.id}>
              <PlugCard plug={plug} hostnameCount={list?.hostname_count ?? 0} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
