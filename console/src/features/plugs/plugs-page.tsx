import { RefreshCwIcon } from "lucide-react";
import { Link } from "react-router";
import { asProblem } from "@/api/problem";
import { usePlugs, useSolution } from "@/api/queries";
import type { Plug, PlugList, SolutionDetail } from "@/api/types";
import { PageHeader } from "@/components/app/page";
import { EmptyState } from "@/components/v2/empty-state";
import { KeyValueList } from "@/components/v2/key-value-list";
import { Panel } from "@/components/v2/panel";
import { StatusChip } from "@/components/v2/status-chip";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
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
    <Panel
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
      <KeyValueList
        items={[
          {
            id: "solution",
            label: p.solution,
            loading: loading || (!detail && Boolean(list?.solution)),
            value: detail ? `${detail.title} (${detail.name})` : (list?.solution ?? ""),
            testId: "solution-name",
          },
          {
            id: "description",
            label: p.description,
            loading: loading || (!detail && Boolean(list?.solution)),
            value: detail?.description || "—",
            title: detail?.description,
          },
          { id: "version", label: p.version, loading, mono: true, value: list?.version ?? strings.updates.noRelease },
          {
            id: "track",
            label: p.track,
            loading,
            value: (
              <StatusChip
                size="sm"
                tone={track.variant === "warning" ? "warn" : track.variant === "success" ? "ok" : "primary"}
              >
                {track.text}
              </StatusChip>
            ),
          },
          { id: "count", label: p.count, loading, mono: true, value: list ? String(list.items.length) : "" },
        ]}
      />
      <p className="min-h-5 type-small text-warn-fg" data-testid="plugs-pending">
        {list?.pending ? p.pendingAll : ""}
      </p>
    </Panel>
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
    <div className="flex flex-col gap-6">
      <PageHeader title={p.title} description={p.intro} />
      {plugs.isError ? <ProblemAlert error={plugs.error} /> : null}
      <SolutionHeader list={list} detail={solution.data} />
      {!items ? (
        <div className="flex flex-col gap-4" aria-busy="true">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-56 rounded-card" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <div data-testid="plugs-empty">
          <EmptyState illustration="pi-box" title={p.empty} description={p.emptyHelp} />
        </div>
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
