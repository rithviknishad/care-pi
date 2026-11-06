import { ArrowLeftIcon, ChevronDownIcon, LogInIcon } from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { isProblem, useJob, usePublicStatus, useSession } from "@/api/queries";
import type { JobKind } from "@/api/types";
import { PublicBar } from "@/app/layout/top-bar";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { FocusBackdrop, FocusCard } from "@/components/v2/focus";
import { InlineSkeleton } from "@/components/v2/inline-skeleton";
import { KeyValueList } from "@/components/v2/key-value-list";
import { Panel, WhatNext } from "@/components/v2/panel";
import { ProblemCard } from "@/components/v2/problem-card";
import { StepList } from "@/components/v2/step-list";
import { fmt, jobKindText, strings } from "@/lib/strings";
import { JobProgress } from "./job-progress";
import { isFinished, jobKindFromId } from "./job-state";

const t = strings.jobsPage;
const UPDATE_KINDS = new Set(["update-apply", "update-rollback", "os-update", "plug-change", "update-stage"]);

function TechnicalDetails({ id, kind, source }: { id: string; kind: string; source: string | undefined }) {
  return (
    <details className="group rounded-card border border-line-soft">
      <summary className="flex h-11 cursor-pointer list-none items-center gap-2 px-4 type-small font-medium text-ink-muted outline-none select-none hover:text-ink focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
        <ChevronDownIcon
          aria-hidden="true"
          className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
        />
        {t.technical}
      </summary>
      <div className="px-4 pb-2">
        <KeyValueList
          labelWidth="sm"
          items={[
            { id: "id", label: t.idLabel, value: id, mono: true, copy: id, testId: "job-id" },
            { id: "kind", label: t.kindLabel, value: kind, mono: true },
            { id: "source", label: t.sourceLabel, value: source ? (t.from[source] ?? source) : undefined },
          ]}
        />
      </div>
    </details>
  );
}

// /jobs/:id with a session: the steps with the live stream, resumed after a reload from GET /jobs/{id} (log_tail and
// last_event_id, S67), the log, "Copy the transcript" (S68), and "Stop the job" when the job can stop.
function SessionJobPage({ id }: { id: string }) {
  const job = useJob(id);
  const kind = job.data?.kind ?? jobKindFromId(id) ?? "";
  const notFound = isProblem(job.error, "job-not-found", "not-found");
  const sentence = !job.data
    ? undefined
    : !isFinished(job.data)
      ? t.running
      : job.data.state === "succeeded"
        ? t.succeeded
        : job.data.state === "cancelled"
          ? t.cancelled
          : t.failed;
  return (
    <div className="flex flex-col gap-6" data-testid="job-page">
      <div>
        <Button asChild variant="ghost" size="sm" className="-ml-2 no-underline">
          <Link to="/jobs">
            <ArrowLeftIcon data-icon="inline-start" />
            {t.back}
          </Link>
        </Button>
      </div>
      <header className="flex flex-col gap-1">
        <h1 className="type-h1 text-ink" data-testid="job-title">
          {kind ? jobKindText(kind) : <InlineSkeleton className="h-7 w-64" />}
        </h1>
        <p className="min-h-6 max-w-prose type-body text-ink-muted">
          {sentence ?? <InlineSkeleton className="h-4 w-80 max-w-full" />}
        </p>
      </header>
      {notFound ? (
        <ProblemCard
          title={t.notFoundTitle}
          description={t.notFoundText}
          technical={`job: ${id}\n${job.error instanceof Error ? job.error.message : ""}`}
          actions={
            <Button asChild variant="outline" size="sm">
              <Link to="/jobs">{t.back}</Link>
            </Button>
          }
        />
      ) : job.error ? (
        <ProblemAlert error={job.error} onRetry={() => void job.refetch()} />
      ) : (
        <Panel>
          {job.data ? (
            <JobProgress jobId={id} initial={job.data} cancel />
          ) : (
            <div className="flex flex-col gap-4" aria-busy="true">
              <Skeleton className="h-7 w-48" />
              <StepList steps={[]} reserve={5} />
            </div>
          )}
        </Panel>
      )}
      {notFound ? null : <TechnicalDetails id={id} kind={kind} source={job.data?.source} />}
    </div>
  );
}

function publicTitle(kind: string | undefined) {
  if (kind === "setup") return { title: strings.publicJob.setupTitle, text: strings.publicJob.setupText };
  if (kind && UPDATE_KINDS.has(kind))
    return { title: strings.publicJob.updateTitle, text: strings.publicJob.updateText };
  return { title: strings.publicJob.otherTitle, text: strings.publicJob.otherText };
}

// /jobs/:id without a session (S67): a Focus page with the steps of the public stream (no log lines) and a link to
// the login. A second device that opens the console during the setup finish or an update lands here.
export function PublicJobView({ id }: { id: string }) {
  const pub = usePublicStatus(true);
  const kind = (jobKindFromId(id) ?? pub.data?.job?.kind) as JobKind | undefined;
  // The planned step count of this job (null: the job has no plan). It stays after the job leaves the public status.
  const live = pub.data?.job?.id === id ? (pub.data.job.step_count ?? null) : null;
  const [planned, setPlanned] = useState<number | null>(null);
  if (live !== null && live !== planned) setPlanned(live);
  const { title, text } = publicTitle(kind);
  const next = `/login?next=${encodeURIComponent(`/jobs/${id}`)}`;
  return (
    <FocusBackdrop top={<PublicBar />}>
      <FocusCard
        width="md"
        illustration={kind === "setup" ? "pi-box" : "update-arrow"}
        title={title}
        description={text}
        footer={
          <span data-testid="public-job-kind" className="type-small text-ink-soft">
            {kind ? fmt(strings.publicJob.kind, { kind: jobKindText(kind) }) : ""}
          </span>
        }
      >
        <div data-testid="public-job">
          <JobProgress jobId={id} public reserve={planned ?? 4} stepCount={planned} />
        </div>
        <div className="flex flex-col gap-2">
          <Button asChild size="lg" className="w-full">
            <Link to={next} data-testid="public-job-login">
              <LogInIcon data-icon="inline-start" />
              {strings.publicJob.login}
            </Link>
          </Button>
          <WhatNext>{strings.publicJob.afterLogin}</WhatNext>
        </div>
      </FocusCard>
    </FocusBackdrop>
  );
}

export default function JobPage() {
  const { id = "" } = useParams();
  const session = useSession();
  if (session.isPending) return null;
  if (session.data) return <SessionJobPage key={id} id={id} />;
  if (session.isError) return <ProblemAlert error={session.error} />;
  return <PublicJobView key={id} id={id} />;
}
