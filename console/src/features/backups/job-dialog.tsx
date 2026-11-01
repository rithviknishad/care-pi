import { useState, type ReactNode } from "react";
import { ProblemError } from "@/api/problem";
import type { Job } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { JobProgress } from "@/features/jobs/job-progress";
import { jobKindText } from "@/features/jobs/step-text";
import { strings } from "@/lib/strings";
import { useInvalidateBackups } from "./use-backups";

// The progress of a backup or restore job in a dialog, so the page does not move. The operator can close the dialog:
// the job continues, and the page shows it in the row of the running job.
export function JobDialog({
  job,
  title,
  intro,
  onClose,
  onDone,
  children,
}: {
  job: Job | null;
  title?: string;
  intro?: string;
  onClose: () => void;
  onDone?: (job: Job) => void;
  // The result of the job, after it ends (the final job of the event done).
  children?: (done: Job) => ReactNode;
}) {
  return (
    <Dialog open={job !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-2xl" innerClassName="max-h-[85dvh] overflow-y-auto" data-testid="job-dialog">
        {job ? (
          <JobDialogBody key={job.id} job={job} title={title} intro={intro} onClose={onClose} onDone={onDone}>
            {children}
          </JobDialogBody>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function JobDialogBody({
  job,
  title,
  intro,
  onClose,
  onDone,
  children,
}: {
  job: Job;
  title?: string | undefined;
  intro?: string | undefined;
  onClose: () => void;
  onDone?: ((job: Job) => void) | undefined;
  children?: ((done: Job) => ReactNode) | undefined;
}) {
  const invalidate = useInvalidateBackups();
  const [done, setDone] = useState<Job | null>(null);
  return (
    <>
      <DialogHeader>
        <DialogTitle>{title ?? jobKindText(job.kind)}</DialogTitle>
        {intro ? <DialogDescription>{intro}</DialogDescription> : null}
      </DialogHeader>
      <JobProgress
        jobId={job.id}
        initial={job}
        onDone={(final) => {
          setDone(final);
          invalidate();
          onDone?.(final);
        }}
      >
        {done?.state === "failed" && done.error ? <ProblemAlert error={new ProblemError(done.error)} /> : null}
        {done && children ? children(done) : null}
        <Button type="button" variant="outline" className="self-start" onClick={onClose} data-testid="job-dialog-close">
          {strings.backupsPage.close}
        </Button>
      </JobProgress>
    </>
  );
}
