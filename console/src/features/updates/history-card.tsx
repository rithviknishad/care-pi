import { HistoryIcon, LifeBuoyIcon, PuzzleIcon, RotateCcwIcon } from "lucide-react";
import { useUpdateHistory } from "@/api/queries";
import type { UpdateHistoryEntry } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { SectionCard } from "@/components/app/section-card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Item, ItemActions, ItemContent, ItemDescription, ItemMedia, ItemTitle } from "@/components/ui/item";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { formatDuration } from "@/lib/format";
import { fmt, problemText, strings } from "@/lib/strings";
import { slugOf } from "@/api/problem";
import { historyVersions, outcomeBadge } from "./update-text";
import { formatZoned, secondsBetween } from "./window";

const u = strings.updates;
// The list reserves the height of 3 rows, so the first entries do not move the page.
const RESERVED_ROWS = 3;

function KindIcon({ e }: { e: UpdateHistoryEntry }) {
  const cls = "size-4 shrink-0";
  if (e.kind === "rollback" || e.outcome === "rolled-back")
    return <RotateCcwIcon className={`${cls} text-amber-600`} aria-hidden="true" />;
  if (e.kind === "plug-change") return <PuzzleIcon className={cls} aria-hidden="true" />;
  return <HistoryIcon className={cls} aria-hidden="true" />;
}

function Row({ e, tz }: { e: UpdateHistoryEntry; tz: string }) {
  const badge = outcomeBadge(e.outcome);
  const took = secondsBetween(e.started_at, e.finished_at);
  const kind = u.historyKinds[e.kind] ?? e.kind;
  const what =
    e.kind === "os-update"
      ? kind
      : e.kind === "plug-change" && e.plug_id
        ? `${kind}: ${fmt(u.plugOf, { plug: e.plug_id })}`
        : `${kind}: ${historyVersions(e)}`;
  const details = [
    formatZoned(e.finished_at, tz),
    took !== null ? fmt(u.took, { time: formatDuration(took) }) : null,
    e.downtime_s ? fmt(u.downtimeWas, { time: formatDuration(e.downtime_s) }) : null,
    u.sourcesOfJob[e.source] ?? e.source,
  ].filter(Boolean);
  const problem = e.problem ? problemText(slugOf(e.problem.type)) : null;
  return (
    <Item
      asChild
      size="xs"
      className="min-h-14 flex-nowrap rounded-none px-1"
      data-testid="history-row"
      data-kind={e.kind}
      data-outcome={e.outcome}
      title={e.problem?.detail ?? e.reason ?? undefined}
    >
      <li>
        <ItemMedia variant="icon">
          <KindIcon e={e} />
        </ItemMedia>
        <ItemContent className="min-w-0">
          <ItemTitle
            className="line-clamp-2 w-full font-semibold break-words sm:line-clamp-1"
            data-testid="history-what"
          >
            {what}
          </ItemTitle>
          <ItemDescription className="line-clamp-2 text-xs sm:line-clamp-1">{details.join(" · ")}</ItemDescription>
          {problem ? (
            <ItemDescription className="line-clamp-1 text-xs text-destructive">{problem}</ItemDescription>
          ) : null}
        </ItemContent>
        <ItemActions className="flex-col items-end gap-1">
          <Badge size="sm" variant={badge.variant} data-testid="history-outcome">
            {badge.text}
          </Badge>
          {e.pitr_offer ? (
            <Badge size="sm" variant="warning">
              <LifeBuoyIcon data-icon="inline-start" />
              {u.pitrOffered}
            </Badge>
          ) : null}
        </ItemActions>
      </li>
    </Item>
  );
}

// GET /updates/history: newest first. "Show older entries" adds the next page at the end (next_before).
// An entry never changes after the agent writes it; a new entry comes at the top.
export function HistoryCard({ tz }: { tz: string }) {
  const history = useUpdateHistory();
  const items = history.data?.pages.flatMap((p) => p.items) ?? null;
  return (
    <SectionCard
      title={u.historyTitle}
      testId="history-card"
      footer={
        history.hasNextPage ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => void history.fetchNextPage()}
            disabled={history.isFetchingNextPage}
            data-testid="history-more"
          >
            {history.isFetchingNextPage ? <Spinner data-icon="inline-start" /> : null}
            {u.historyMore}
          </Button>
        ) : null
      }
    >
      <ol
        className="flex flex-col divide-y"
        style={{ minHeight: `${RESERVED_ROWS * 3.5}rem` }}
        aria-label={u.historyTitle}
        data-testid="history-list"
      >
        {items === null ? (
          Array.from({ length: RESERVED_ROWS }, (_, i) => (
            <li key={i} className="flex h-14 flex-col justify-center gap-2 px-1">
              <Skeleton className="h-4 w-56" />
              <Skeleton className="h-3 w-72 max-w-full" />
            </li>
          ))
        ) : items.length === 0 ? (
          <li className="flex flex-1 items-center justify-center text-sm text-soft-foreground">{u.historyEmpty}</li>
        ) : (
          items.map((e) => <Row key={e.job_id} e={e} tz={tz} />)
        )}
      </ol>
      {history.error ? <ProblemAlert error={history.error} /> : null}
    </SectionCard>
  );
}
