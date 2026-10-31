import { useMutation, useQueryClient } from "@tanstack/react-query";
import { BanIcon } from "lucide-react";
import { useCallback, useState } from "react";
import { api, call } from "@/api/client";
import { asProblem, ProblemError } from "@/api/problem";
import { qk, useStatus, useUpdates } from "@/api/queries";
import type { Job } from "@/api/types";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { JobProgress } from "@/features/jobs/job-progress";
import { jobKindFromId } from "@/features/jobs/job-state";
import { NotAvailable } from "@/features/unavailable/unavailable-page";
import { formatTime } from "@/lib/format";
import { fmt, jobKindText, strings } from "@/lib/strings";
import { CurrentCard } from "./current-card";
import { HistoryCard } from "./history-card";
import { NewVersionCard } from "./new-version-card";
import { OsCard } from "./os-card";
import { PitrOfferCard } from "./pitr-offer";
import { RollbackCard } from "./rollback-card";
import { TrackCard } from "./track-card";
import { offerKey, pitrOfferOfJob } from "./update-text";
import { WindowCard } from "./window-card";

const u = strings.updates;
const DISMISSED = "care-pi.pitr-dismissed";

function readDismissed(): string[] {
  try {
    const v = JSON.parse(sessionStorage.getItem(DISMISSED) ?? "[]") as unknown;
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

// The job of the page: the job that the operator started here, else the job of GET /updates (a timer, the CLI,
// or another tab). The steps append; the card stays after the end until "Close".
function JobArea({
  jobId,
  initial,
  done,
  failed,
  onDone,
  onStep,
  onClose,
}: {
  jobId: string;
  initial: Job | null;
  done: boolean;
  failed: Job["error"];
  onDone: (job: Job) => void;
  onStep: () => void;
  onClose: () => void;
}) {
  const kind = initial?.kind ?? jobKindFromId(jobId);
  const cancel = useMutation({
    mutationFn: () => call(api.POST("/jobs/{job_id}/cancel", { params: { path: { job_id: jobId } } })),
  });
  return (
    <Card size="sm" data-testid="update-job" data-kind={kind}>
      <CardContent>
        <JobProgress
          jobId={jobId}
          initial={initial}
          title={fmt(u.jobTitle, { kind: jobKindText(kind) })}
          onDone={onDone}
          onStepChange={onStep}
        >
          {failed ? <ProblemAlert error={new ProblemError(failed)} /> : null}
          {cancel.error ? <ProblemAlert error={cancel.error} /> : null}
          {done ? (
            <Button type="button" variant="outline" className="self-start" onClick={onClose} data-testid="job-close">
              {strings.common.close}
            </Button>
          ) : kind === "update-stage" ? (
            <Button
              type="button"
              variant="outline"
              className="self-start"
              onClick={() => cancel.mutate()}
              disabled={cancel.isPending}
            >
              <BanIcon data-icon="inline-start" />
              {strings.jobs.cancel}
            </Button>
          ) : null}
        </JobProgress>
      </CardContent>
    </Card>
  );
}

export default function UpdatesPage() {
  const qc = useQueryClient();
  const updates = useUpdates();
  const status = useStatus(30_000);
  const o = updates.data;
  const tz = o?.window.timezone ?? status.data?.clock.timezone ?? "UTC";
  const [job, setJob] = useState<Job | null>(null);
  const [doneJob, setDoneJob] = useState<Job | null>(null);
  const [dismissed, setDismissed] = useState<string[]>(readDismissed);
  const problem = asProblem(updates.error);

  const refresh = useCallback(() => {
    void qc.invalidateQueries({ queryKey: qk.updates });
    void qc.invalidateQueries({ queryKey: qk.status });
    void qc.invalidateQueries({ queryKey: qk.publicStatus });
    void qc.invalidateQueries({ queryKey: qk.plugs });
  }, [qc]);

  // A step of an update job can turn the maintenance page on or off: read the state of the top bar again.
  const onStep = useCallback(() => {
    void qc.invalidateQueries({ queryKey: qk.publicStatus });
    void qc.invalidateQueries({ queryKey: qk.updates, exact: true });
  }, [qc]);

  if (problem?.is("not-implemented-in-this-version")) {
    return (
      <div className="flex flex-col gap-4">
        <PageHeader title={u.title} />
        <NotAvailable phase={problem.problem.phase} />
      </div>
    );
  }

  const start = (j: Job) => {
    setJob(j);
    setDoneJob(null);
    void qc.invalidateQueries({ queryKey: qk.publicStatus });
  };
  const jobId = job?.id ?? o?.job?.id ?? null;
  const done = Boolean(jobId && doneJob?.id === jobId);
  const busy = Boolean((jobId && !done) || o?.job);
  const offer = pitrOfferOfJob(doneJob) ?? o?.last_result?.pitr_offer ?? null;
  const showOffer = offer !== null && !dismissed.includes(offerKey(offer));

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={u.title}
        description={
          <span className="tabular-nums" data-testid="updates-updated">
            {fmt(u.updated, { time: o ? formatTime(new Date(updates.dataUpdatedAt).toISOString()) : "--:--:--" })}
          </span>
        }
      />
      {updates.isError ? <ProblemAlert error={updates.error} /> : null}
      <div className="grid gap-4 lg:grid-cols-2">
        <CurrentCard o={o} status={status.data} tz={tz} />
        <NewVersionCard o={o} status={status.data} tz={tz} busy={busy} onJob={start} />
      </div>
      {jobId ? (
        <JobArea
          jobId={jobId}
          initial={job}
          done={done}
          failed={done && doneJob?.state === "failed" ? doneJob.error : null}
          onDone={(j) => {
            setJob(j);
            setDoneJob(j);
            refresh();
          }}
          onStep={onStep}
          onClose={() => {
            setJob(null);
            refresh();
          }}
        />
      ) : null}
      {showOffer && offer ? (
        <PitrOfferCard
          offer={offer}
          version={o?.current?.version ?? ""}
          tz={tz}
          onDismiss={() => {
            const next = [...dismissed, offerKey(offer)];
            setDismissed(next);
            try {
              sessionStorage.setItem(DISMISSED, JSON.stringify(next));
            } catch {
              // The browser blocks the storage: the choice lasts until the next page load.
            }
          }}
        />
      ) : null}
      <div className="grid gap-4 lg:grid-cols-2">
        <TrackCard o={o} busy={busy} onJob={start} />
        <WindowCard o={o} tz={tz} />
        <RollbackCard o={o} tz={tz} busy={busy} onJob={start} />
        <OsCard o={o} tz={tz} busy={busy} onJob={start} />
      </div>
      <HistoryCard tz={tz} />
    </div>
  );
}
