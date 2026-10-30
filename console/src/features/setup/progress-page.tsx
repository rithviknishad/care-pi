import { useQueryClient } from "@tanstack/react-query";
import { XCircleIcon } from "lucide-react";
import { useCallback, useState } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router";
import { qk, useSetup } from "@/api/queries";
import type { Job } from "@/api/types";
import { PageHeader } from "@/components/app/page";
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { JobProgress } from "@/features/jobs/job-progress";
import { problemText, strings } from "@/lib/strings";
import { slugOf } from "@/api/problem";

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

  const jobId = initial?.id ?? setup.data?.job_id ?? null;
  if (!jobId) {
    if (setup.isPending) return <Skeleton className="h-96" />;
    if (setup.data?.state === "ready") return <Navigate to="/setup/done" replace />;
    return <Navigate to="/setup" replace />;
  }

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title={strings.progress.title} description={strings.progress.intro} />
      <Card>
        <CardContent>
          <JobProgress jobId={jobId} initial={initial} onDone={onDone}>
            {failed ? (
              <Alert variant="destructive" data-testid="setup-failed">
                <XCircleIcon />
                <AlertTitle>{strings.progress.failed}</AlertTitle>
                <AlertDescription>
                  <p>{strings.progress.failedIntro}</p>
                  {failed.error ? <p>{failed.error.detail || problemText(slugOf(failed.error.type))}</p> : null}
                </AlertDescription>
                <AlertAction>
                  <Button asChild variant="outline" size="sm">
                    <Link to="/setup/review">{strings.progress.backToSetup}</Link>
                  </Button>
                </AlertAction>
              </Alert>
            ) : null}
          </JobProgress>
        </CardContent>
      </Card>
    </div>
  );
}
