import { HistoryIcon, PuzzleIcon, RotateCcwIcon } from "lucide-react";
import { useUpdateHistory } from "@/api/queries";
import type { UpdateHistoryEntry } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { SectionCard } from "@/components/app/section-card";
import { Button } from "@/components/ui/button";
import { StatusChip } from "@/components/v2/status-chip";
import { toneSolid, type Tone } from "@/components/v2/tone";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { formatDuration } from "@/lib/format";
import { fmt, problemText, strings } from "@/lib/strings";
import { slugOf } from "@/api/problem";
import { cn } from "@/lib/utils";
import { historyVersions, outcomeBadge, outcomeSentence } from "./update-text";
import { formatZoned, secondsBetween } from "./window";

const u = strings.updates;
// The list reserves the height of 3 rows, so the first entries do not move the page.
const RESERVED_ROWS = 3;

const OUTCOME_TONE: Record<string, Tone> = {
  succeeded: "ok",
  "rolled-back": "warn",
  failed: "danger",
  "rollback-failed": "danger",
};

function KindIcon({ e }: { e: UpdateHistoryEntry }) {
  const cls = "size-4 shrink-0";
  if (e.kind === "rollback" || e.outcome === "rolled-back") return <RotateCcwIcon className={cls} aria-hidden="true" />;
  if (e.kind === "plug-change") return <PuzzleIcon className={cls} aria-hidden="true" />;
  return <HistoryIcon className={cls} aria-hidden="true" />;
}

function Row({ e, tz, last }: { e: UpdateHistoryEntry; tz: string; last: boolean }) {
  const badge = outcomeBadge(e.outcome);
  const tone = OUTCOME_TONE[e.outcome] ?? "neutral";
  const took = secondsBetween(e.started_at, e.finished_at);
  const kind = u.historyKinds[e.kind] ?? e.kind;
  const what =
    e.kind === "os-update"
      ? kind
      : e.kind === "plug-change" && e.plug_id
        ? `${kind}: ${fmt(u.plugOf, { plug: e.plug_id })}`
        : `${kind}: ${historyVersions(e)}`;
  const recovery = (e.reason ?? "").startsWith("recovery ");
  const details = [
    took !== null ? fmt(u.took, { time: formatDuration(took) }) : null,
    e.downtime_s ? fmt(u.downtimeWas, { time: formatDuration(e.downtime_s) }) : null,
    recovery ? u.recoveryOf : (u.sourcesOfJob[e.source] ?? e.source),
  ].filter(Boolean);
  const sentence = outcomeSentence(e);
  const problem = e.problem && !sentence ? problemText(slugOf(e.problem.type)) : null;
  return (
    <li
      className="relative grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-3 pb-5 last:pb-0 sm:grid-cols-[9.5rem_1.5rem_minmax(0,1fr)_auto]"
      data-testid="history-row"
      data-kind={e.kind}
      data-outcome={e.outcome}
      title={e.problem?.detail ?? e.reason ?? undefined}
    >
      {last ? null : (
        <span
          aria-hidden="true"
          className="absolute top-7 bottom-1 left-[0.6875rem] w-0.5 rounded-full bg-line sm:left-[calc(9.5rem+0.75rem+0.6875rem)]"
        />
      )}
      <time
        dateTime={e.finished_at}
        className="col-start-2 row-start-1 type-data text-ink-soft sm:col-start-1 sm:pt-0.5 sm:text-right"
      >
        {formatZoned(e.finished_at, tz)}
      </time>
      <span
        className={cn(
          "col-start-1 row-span-2 row-start-1 grid size-6 place-items-center rounded-full text-white sm:col-start-2 dark:text-black/80",
          toneSolid[tone],
        )}
      >
        <KindIcon e={e} />
      </span>
      <div className="col-start-2 min-w-0 sm:col-start-3 sm:row-start-1">
        <p className="type-body font-medium break-words text-ink" data-testid="history-what">
          {what}
        </p>
        {sentence ? (
          <p className="type-body text-ink" data-testid="history-sentence">
            {sentence}
          </p>
        ) : null}
        <p className="type-small text-ink-muted">{details.join(" · ")}</p>
        {problem ? <p className="type-small text-danger-fg">{problem}</p> : null}
      </div>
      <div className="col-start-2 mt-2 flex flex-wrap items-start gap-1 sm:col-start-4 sm:row-start-1 sm:mt-0 sm:flex-col sm:items-end">
        <StatusChip tone={tone} size="sm" testId="history-outcome">
          {badge.text}
        </StatusChip>
        {e.pitr_offer ? (
          <StatusChip tone="warn" size="sm">
            {u.pitrOffered}
          </StatusChip>
        ) : null}
      </div>
    </li>
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
        className="flex flex-col"
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
          <li className="flex flex-1 items-center justify-center type-body text-ink-muted">{u.historyEmpty}</li>
        ) : (
          items.map((e, i) => <Row key={e.job_id} e={e} tz={tz} last={i === items.length - 1} />)
        )}
      </ol>
      {history.error ? <ProblemAlert error={history.error} /> : null}
    </SectionCard>
  );
}
