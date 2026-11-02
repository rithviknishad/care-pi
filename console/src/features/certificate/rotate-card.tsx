import { useMutation, useQueryClient } from "@tanstack/react-query";
import { RefreshCwIcon } from "lucide-react";
import { useState } from "react";
import { api, call } from "@/api/client";
import { qk } from "@/api/queries";
import type { Job, TlsState } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { JobProgress } from "@/features/jobs/job-progress";
import { fmt, strings } from "@/lib/strings";

const r = strings.certificate.rotate;

// Phase 7: POST /tls/local-ca/rotate. A new root CA with a new key, for the names and addresses of now. Each device
// of the clinic must install it again, so the button asks first.
export function RotateCard({ tls }: { tls: TlsState }) {
  const qc = useQueryClient();
  const { withReauth } = useReauth();
  const [open, setOpen] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const rotate = useMutation({
    mutationFn: () => withReauth(() => call(api.POST("/tls/local-ca/rotate"))),
    onSuccess: setJob,
  });
  const uncovered = tls.local_ca.uncovered;
  return (
    <Card size="sm" data-testid="ca-rotate-card">
      <CardHeader>
        <CardTitle>
          <h2 className="flex items-center gap-2">
            <RefreshCwIcon className="size-4" aria-hidden="true" />
            {r.title}
          </h2>
        </CardTitle>
        <CardDescription>
          {uncovered.length > 0 ? fmt(r.introUncovered, { names: uncovered.join(", ") }) : r.intro}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {job ? (
          <JobProgress
            jobId={job.id}
            initial={job}
            title={r.running}
            onDone={() => {
              void qc.invalidateQueries({ queryKey: qk.tls });
              void qc.invalidateQueries({ queryKey: ["tls-instructions"] });
            }}
          >
            <Button type="button" variant="outline" className="self-start" onClick={() => setJob(null)}>
              {strings.common.close}
            </Button>
          </JobProgress>
        ) : (
          <AlertDialog open={open} onOpenChange={setOpen}>
            <Button
              type="button"
              variant="outline"
              className="self-start"
              onClick={() => setOpen(true)}
              disabled={rotate.isPending || !tls.local_ca.root}
              data-testid="ca-rotate"
            >
              {rotate.isPending ? <Spinner data-icon="inline-start" /> : null}
              {r.button}
            </Button>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{r.confirmTitle}</AlertDialogTitle>
                <AlertDialogDescription>{r.confirm}</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive-solid"
                  onClick={() => {
                    setOpen(false);
                    rotate.mutate();
                  }}
                  data-testid="ca-rotate-confirm"
                >
                  {r.button}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        )}
        {rotate.error && !isReauthCancelled(rotate.error) ? <ProblemAlert error={rotate.error} /> : null}
      </CardContent>
    </Card>
  );
}
