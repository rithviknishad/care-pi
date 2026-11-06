import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ScrollTextIcon } from "lucide-react";
import { useCallback, useState } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { exportJob } from "@/api/exports";
import { slugOf } from "@/api/problem";
import { qk, useJob, useSetup } from "@/api/queries";
import type { Job, JobStep } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { CopyButton } from "@/components/v2/copy-button";
import { ProblemCard } from "@/components/v2/problem-card";
import { StepList } from "@/components/v2/step-list";
import { JobProgress } from "@/features/jobs/job-progress";
import { jobKindFromId } from "@/features/jobs/job-state";
import { logUnitOf, stepTitle } from "@/features/jobs/step-text";
import { fmt, problemText, strings } from "@/lib/strings";
import { failedStep, failureDetails } from "./failure-logic";
import { loadMode } from "./wizard";
import { WizardStage } from "./wizard-stage";

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
      <ProblemCard
        announce
        title={p.failed}
        description={
          <div className="flex flex-col gap-2">
            <p data-testid="setup-failed-step">
              {step ? fmt(p.failedStep, { step: stepTitle({ ...step, state: "failed" }).text }) : p.failedNoStep}
            </p>
            {detail ? (
              <p className="type-data break-words" data-testid="setup-failed-detail">
                {detail}
              </p>
            ) : null}
            <p>{p.failedHelp}</p>
          </div>
        }
        onRetry={() => retry.mutate()}
        retrying={retry.isPending}
        retryLabel={p.retry}
        technical={technical}
        actions={
          <>
            <CopyButton text={() => exportJob(job.id)} support label={p.copyDetails} />
            <Button asChild variant="outline" size="sm">
              <Link to={`/logs?unit=${unit ?? "agent"}`} data-testid="setup-failed-log">
                <ScrollTextIcon data-icon="inline-start" />
                {unit && unit !== "agent" ? fmt(p.openLog, { unit: `care-${unit}` }) : p.openAgentLog}
              </Link>
            </Button>
            <Button asChild variant="ghost" size="sm" className="no-underline">
              <Link to="/setup/review">{p.backToSetup}</Link>
            </Button>
          </>
        }
      />
      {retry.error ? <ProblemAlert error={retry.error} /> : null}
    </div>
  );
}

// The phase of the setup job in one reassuring sentence (S66): what the device does now, and that it is normal.
const PHASES: [string[], string][] = [
  [["pull-images"], p.phases.download],
  [["full-cycle", "start-target", "ensure-buckets", "ensure-stanza"], p.phases.database],
  [["ensure-fe"], p.phases.web],
  [["wait-healthy", "write-lock"], p.phases.start],
  [["create-care-superuser"], p.phases.admin],
];

function phaseSentence(action: string | null | undefined): string {
  if (!action) return p.phases.prepare;
  return PHASES.find(([actions]) => actions.includes(action))?.[1] ?? p.phases.prepare;
}

export default function ProgressPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const initial = (location.state as { job?: Job } | null)?.job ?? null;
  const setup = useSetup();
  const [failed, setFailed] = useState<Job | null>(null);
  const [action, setAction] = useState<string | null>(null);

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
  // S67: after a reload (or on a second device) the page reads the job first: its steps and the log tail. Then the
  // stream continues after its last event.
  const resumed = useJob(kept?.job ? null : jobId);
  const onRetry = useCallback(
    (job: Job) => {
      setFailed(null);
      setKept({ id: job.id, job });
      navigate("/setup/progress", { replace: true, state: { job } });
    },
    [navigate],
  );
  const onStepChange = useCallback((running: JobStep | null) => setAction(running?.action ?? null), []);

  if (jobId && jobKindFromId(jobId) === "restore-dr") return <Navigate to="/setup/restore" replace />;
  if (!jobId) {
    if (setup.isPending) return <Skeleton className="h-96 rounded-stage" />;
    if (setup.data?.state === "ready") return <Navigate to="/setup/done" replace />;
    return <Navigate to="/setup" replace />;
  }
  const start = kept?.job ?? resumed.data ?? null;
  const waiting = !kept?.job && resumed.isPending;

  return (
    <WizardStage mode={loadMode()} page="install">
      <p className="min-h-12 max-w-prose type-body text-ink" data-testid="install-phase" aria-live="polite">
        {failed ? "" : phaseSentence(action)}
      </p>
      {failed && failed.id === jobId ? <SetupFailed job={failed} onRetry={onRetry} /> : null}
      {waiting ? (
        <StepList steps={[]} reserve={8} />
      ) : resumed.error && !kept?.job ? null : (
        <JobProgress
          jobId={jobId}
          initial={start}
          onDone={onDone}
          onStepChange={onStepChange}
          reserve={start?.steps?.length ? 0 : 8}
        />
      )}
      {resumed.error && !kept?.job ? (
        <ProblemAlert error={resumed.error} onRetry={() => void resumed.refetch()} />
      ) : null}
    </WizardStage>
  );
}
