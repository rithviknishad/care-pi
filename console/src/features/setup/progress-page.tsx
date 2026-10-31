import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronRightIcon, RotateCwIcon, ScrollTextIcon, XCircleIcon } from "lucide-react";
import { useCallback, useState } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { slugOf } from "@/api/problem";
import { qk, useSetup } from "@/api/queries";
import type { Job } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { JobProgress } from "@/features/jobs/job-progress";
import { logUnitOf, stepTitle } from "@/features/jobs/step-text";
import { fmt, problemText, strings } from "@/lib/strings";
import { failedStep, failureDetails } from "./failure-logic";

const p = strings.progress;

function SetupFailed({ job, onRetry }: { job: Job; onRetry: (job: Job) => void }) {
  const qc = useQueryClient();
  const retry = useMutation({
    mutationFn: () => call(api.POST("/setup/finish")),
    onSuccess: (next) => {
      void qc.invalidateQueries({ queryKey: qk.setup });
      onRetry(next);
    },
  });
  const step = failedStep(job);
  const problem = job.error;
  const detail = problem?.detail || (problem ? problemText(slugOf(problem.type)) : step?.detail) || "";
  const unit = logUnitOf(`${problem?.detail ?? ""} ${step?.detail ?? ""}`);
  const technical = failureDetails(job);
  return (
    <div className="flex flex-col gap-3" data-testid="setup-failed">
      <Alert variant="destructive">
        <XCircleIcon />
        <AlertTitle>{p.failed}</AlertTitle>
        <AlertDescription className="flex flex-col gap-2">
          <p data-testid="setup-failed-step">
            {step ? fmt(p.failedStep, { step: stepTitle({ ...step, state: "failed" }).text }) : p.failedNoStep}
          </p>
          {detail ? (
            <p className="break-words" data-testid="setup-failed-detail">
              {detail}
            </p>
          ) : null}
          <p>{p.failedHelp}</p>
        </AlertDescription>
      </Alert>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" onClick={() => retry.mutate()} disabled={retry.isPending}>
          <RotateCwIcon data-icon="inline-start" />
          {p.retry}
        </Button>
        <CopyButton value={technical} label={p.copyDetails} withLabel />
        <Button asChild variant="outline" size="sm">
          <Link to={`/logs?unit=${unit ?? "agent"}`} data-testid="setup-failed-log">
            <ScrollTextIcon data-icon="inline-start" />
            {unit && unit !== "agent" ? fmt(p.openLog, { unit: `care-${unit}` }) : p.openAgentLog}
          </Link>
        </Button>
        <Button asChild variant="link" size="sm">
          <Link to="/setup/review">{p.backToSetup}</Link>
        </Button>
      </div>
      {retry.error ? <ProblemAlert error={retry.error} /> : null}
      <Collapsible className="rounded-lg border">
        <CollapsibleTrigger className="group flex w-full items-center gap-2 px-3 py-2 text-left text-sm">
          <ChevronRightIcon
            className="size-4 transition-transform group-data-[panel-open]:rotate-90"
            aria-hidden="true"
          />
          {p.technical}
        </CollapsibleTrigger>
        <CollapsibleContent>
          <pre
            className="max-h-64 overflow-auto border-t px-3 py-2 text-xs break-words whitespace-pre-wrap"
            data-testid="setup-failed-technical"
          >
            {technical}
          </pre>
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}

export default function ProgressPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const initial = (location.state as { job?: Job } | null)?.job ?? null;
  const setup = useSetup();
  const [failed, setFailed] = useState<Job | null>(null);

  const onDone = useCallback(
    (job: Job) => {
      void qc.invalidateQueries({ queryKey: qk.session });
      void qc.invalidateQueries({ queryKey: qk.setup });
      void qc.invalidateQueries({ queryKey: qk.status });
      if (job.state === "succeeded") navigate("/setup/done", { replace: true, state: { job } });
      else setFailed(job);
    },
    [qc, navigate],
  );

  // Keep the job id after the job ends. GET /setup clears job_id when the state becomes ready. Without the kept
  // id, the page goes to /setup/done without the job, and the hand-off card has no job result (the Care user
  // name, the console URL after a host name change). Lumine, wave 4c.
  const fromSetup = setup.data?.job_id ?? null;
  const [kept, setKept] = useState<{ id: string; job: Job | null } | null>(
    initial ? { id: initial.id, job: initial } : null,
  );
  if (kept === null && fromSetup !== null) setKept({ id: fromSetup, job: null });
  const jobId = kept?.id ?? fromSetup;
  const onRetry = useCallback(
    (job: Job) => {
      setFailed(null);
      setKept({ id: job.id, job });
      navigate("/setup/progress", { replace: true, state: { job } });
    },
    [navigate],
  );
  if (!jobId) {
    if (setup.isPending) return <Skeleton className="h-96" />;
    if (setup.data?.state === "ready") return <Navigate to="/setup/done" replace />;
    return <Navigate to="/setup" replace />;
  }

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title={p.title} description={p.intro} />
      <Card>
        <CardContent>
          <JobProgress jobId={jobId} initial={kept?.job ?? initial} onDone={onDone}>
            {failed && failed.id === jobId ? <SetupFailed job={failed} onRetry={onRetry} /> : null}
          </JobProgress>
        </CardContent>
      </Card>
    </div>
  );
}
