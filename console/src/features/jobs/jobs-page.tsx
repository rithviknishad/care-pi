import { ChevronRightIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useJobList } from "@/api/queries";
import type { JobSummary } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/v2/empty-state";
import { Hero } from "@/components/v2/hero";
import { Panel } from "@/components/v2/panel";
import { StatusChip } from "@/components/v2/status-chip";
import { useNow } from "@/components/v2/use-now";
import { formatAgo, formatDateTime } from "@/lib/format";
import { fmt, jobKindText, stepActionText, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { JOB_STATE_TONE } from "./job-steps";

const t = strings.jobsPage;

// One job as a row of 72 px: the state, the title, the step or the time, and a link to its page.
function JobRow({ job, now }: { job: JobSummary; now: number }) {
  const running = job.state === "running" || job.state === "queued";
  const step = job.step ? stepActionText(job.step.split(":")[0]?.trim()) : null;
  const when = running
    ? fmt(t.started, { time: formatAgo(job.started_at ?? job.created_at, now) })
    : fmt(t.ended, { time: formatAgo(job.finished_at ?? job.created_at, now) });
  return (
    <li>
      <Link
        to={`/jobs/${encodeURIComponent(job.id)}`}
        data-testid="job-row"
        data-job={job.id}
        data-state={job.state}
        className="grid min-h-18 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-card px-3 py-3 outline-none hover:bg-surface-tint focus-visible:ring-3 focus-visible:ring-ring/50 sm:grid-cols-[6.5rem_minmax(0,1fr)_auto] sm:px-4"
      >
        <StatusChip
          tone={JOB_STATE_TONE[job.state] ?? "neutral"}
          live={running}
          size="sm"
          width={7}
          className="col-start-1 row-start-1 justify-self-start"
        >
          {strings.progress.states[job.state] ?? job.state}
        </StatusChip>
        <span className="col-span-2 col-start-1 row-start-2 min-w-0 sm:col-span-1 sm:col-start-2 sm:row-start-1">
          <span className="block truncate type-body font-medium text-ink">{jobKindText(job.kind)}</span>
          <span className="block truncate type-small text-ink-soft">
            {running && step ? `${step} · ` : job.error_title ? `${job.error_title} · ` : ""}
            <time dateTime={job.started_at ?? job.created_at} title={formatDateTime(job.started_at ?? job.created_at)}>
              {when}
            </time>
            {" · "}
            {t.from[job.source] ?? job.source}
          </span>
        </span>
        <span className="col-start-2 row-start-1 flex items-center gap-1 justify-self-end type-small font-medium text-primary-ink sm:col-start-3">
          {t.open}
          <ChevronRightIcon aria-hidden="true" className="size-4" />
        </span>
      </Link>
    </li>
  );
}

// The skeleton has the grid and the line heights of JobRow (2 lines below 640 px), so the rows do not move.
function RowSkeleton() {
  return (
    <li aria-hidden="true">
      <div className="grid min-h-18 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-3 py-3 sm:grid-cols-[6.5rem_minmax(0,1fr)_auto] sm:px-4">
        <Skeleton className="col-start-1 row-start-1 h-6 w-20 justify-self-start rounded-full" />
        <span className="col-span-2 col-start-1 row-start-2 flex min-w-0 flex-col sm:col-span-1 sm:col-start-2 sm:row-start-1">
          <span className="relative block type-body">
            <span className="invisible">.</span>
            <Skeleton className="absolute inset-y-1 left-0 w-48 max-w-full" />
          </span>
          <span className="relative block type-small">
            <span className="invisible">.</span>
            <Skeleton className="absolute inset-y-1 left-0 w-64 max-w-full" />
          </span>
        </span>
        <Skeleton className="col-start-2 row-start-1 h-4 w-12 justify-self-end sm:col-start-3" />
      </div>
    </li>
  );
}

function JobRows({
  jobs,
  loading,
  now,
  empty,
  rows = 1,
}: {
  jobs: JobSummary[];
  loading: boolean;
  now: number;
  empty: string;
  rows?: number;
}) {
  return (
    <ul className="-mx-3 flex flex-col divide-y divide-line-soft sm:-mx-4">
      {loading ? (
        Array.from({ length: rows }, (_, i) => <RowSkeleton key={i} />)
      ) : jobs.length === 0 ? (
        <li className="flex min-h-18 items-center px-3 type-body text-ink-muted sm:px-4">{empty}</li>
      ) : (
        jobs.map((j) => <JobRow key={j.id} job={j} now={now} />)
      )}
    </ul>
  );
}

// S67: the Jobs page. Each job of the device, from each browser, each device, and the command line: the jobs that
// run first, then the jobs that ended. Each row opens the page of the job (/jobs/:id).
// The skeleton of "Ended" has the row count of the last visit, so the page does not move when the list arrives.
const RECENT_KEY = "care-pi.jobs.recent";

function rememberedRows(): number {
  try {
    const n = Number(localStorage.getItem(RECENT_KEY));
    return Number.isInteger(n) && n >= 1 && n <= 30 ? n : 3;
  } catch {
    return 3;
  }
}

export default function JobsPage() {
  const active = useJobList("active");
  const recent = useJobList("recent", 30);
  const [rows] = useState(rememberedRows);
  const recentCount = recent.data?.items.length;
  useEffect(() => {
    if (recentCount === undefined) return;
    try {
      localStorage.setItem(RECENT_KEY, String(Math.max(1, recentCount)));
    } catch {
      // Private mode: the next visit uses 3.
    }
  }, [recentCount]);
  const now = useNow(true, 30_000);
  const activeJobs = active.data?.items ?? [];
  const recentJobs = recent.data?.items ?? [];
  const none = !active.isPending && !recent.isPending && activeJobs.length === 0 && recentJobs.length === 0;
  return (
    <div className="flex flex-col gap-6" data-testid="jobs-page">
      <Hero size="compact" title={t.title} description={t.description} illustration="pi-box" testId="jobs-hero" />
      {active.error ? <ProblemAlert error={active.error} onRetry={() => void active.refetch()} /> : null}
      <Panel title={t.active} testId="jobs-active" className={cn(activeJobs.length > 0 && "border-primary-solid/40")}>
        <JobRows jobs={activeJobs} loading={active.isPending} now={now} empty={t.noneActive} />
      </Panel>
      {recent.error ? <ProblemAlert error={recent.error} onRetry={() => void recent.refetch()} /> : null}
      <Panel title={t.recent} testId="jobs-recent">
        {none ? (
          <EmptyState illustration="pi-box" title={t.emptyTitle} description={t.emptyText} className="border-0" />
        ) : (
          <JobRows jobs={recentJobs} loading={recent.isPending} rows={rows} now={now} empty={t.emptyText} />
        )}
      </Panel>
    </div>
  );
}
