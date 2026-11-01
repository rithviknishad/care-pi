import { useMutation } from "@tanstack/react-query";
import { RotateCcwIcon } from "lucide-react";
import { useState } from "react";
import { api, call } from "@/api/client";
import type { Job, UpdatesOverview } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import { SectionCard } from "@/components/app/section-card";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { fmt, strings } from "@/lib/strings";
import { previousText } from "./update-text";
import { formatZoned } from "./window";

const u = strings.updates;

// The previous version and "Roll back" (POST /updates/rollback, reauthentication; docs/solutions.md section 9).
export function RollbackCard({
  o,
  tz,
  busy,
  onJob,
}: {
  o: UpdatesOverview | undefined;
  tz: string;
  busy: boolean;
  onJob: (job: Job) => void;
}) {
  const { withReauth } = useReauth();
  const [open, setOpen] = useState(false);
  const rollback = useMutation({
    mutationFn: () => withReauth(() => call(api.POST("/updates/rollback", { body: { to_lock: null, reason: null } }))),
    onSuccess: onJob,
  });
  const loading = !o;
  const r = o?.rollback;
  const previous = o ? previousText(o, tz) : "";
  const offer = r?.pitr_offer ?? null;
  return (
    <SectionCard
      title={u.rollbackTitle}
      testId="rollback-card"
      footer={
        <AlertDialog open={open} onOpenChange={setOpen}>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              rollback.reset();
              setOpen(true);
            }}
            disabled={loading || busy || !r?.available || rollback.isPending}
            data-testid="update-rollback"
          >
            {rollback.isPending ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}
            {u.rollback}
          </Button>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{fmt(u.rollbackConfirm, { version: previous })}</AlertDialogTitle>
              <AlertDialogDescription>{u.rollbackText}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
              <Button
                type="button"
                variant="destructive-solid"
                onClick={() => {
                  setOpen(false);
                  rollback.mutate();
                }}
                data-testid="rollback-confirm"
              >
                {u.rollback}
              </Button>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      }
    >
      <DataList>
        <DataRow label={u.previous} loading={loading} value={previous} testId="rollback-to" />
        <DataRow
          label={u.imagesPresent}
          loading={loading}
          value={r?.available ? (r.images_present ? u.imagesYes : u.imagesNo) : (r?.reason ?? "—")}
          title={r?.available ? undefined : (r?.reason ?? undefined)}
        />
      </DataList>
      <p className="min-h-10 text-xs text-soft-foreground" data-testid="rollback-pitr-note">
        {offer && o
          ? fmt(u.rollbackPitr, { version: o.current?.version ?? "", time: formatZoned(offer.target_time, tz) })
          : ""}
      </p>
      {rollback.error && !isReauthCancelled(rollback.error) ? <ProblemAlert error={rollback.error} /> : null}
    </SectionCard>
  );
}
