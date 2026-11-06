import { useMutation } from "@tanstack/react-query";
import { EyeIcon, KeyRoundIcon, RefreshCwIcon } from "lucide-react";
import { useState } from "react";
import { api, call } from "@/api/client";
import { useSshKey } from "@/api/queries";
import type { BackupsOverview, Job, RecoveryKey, RecoveryKeyStatus } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { ProblemAlert } from "@/components/app/problem-alert";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { KeyValueList } from "@/components/v2/key-value-list";
import { Panel } from "@/components/v2/panel";
import { StatusChip } from "@/components/v2/status-chip";
import type { Tone } from "@/components/v2/tone";
import { formatDateTime } from "@/lib/format";
import { strings } from "@/lib/strings";
import { KeySheet, RecoveryKeyCeremony } from "./recovery-key-ceremony";
import { useInvalidateBackups, useShowKey } from "./use-backups";

const p = strings.backupsPage;

const KEY_TONE: Partial<Record<string, Tone>> = { confirmed: "ok", unconfirmed: "warn" };

function BigDialog({
  open,
  onClose,
  title,
  description,
  children,
  testId,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children: React.ReactNode;
  testId?: string;
}) {
  return (
    <Dialog open={open} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent className="sm:max-w-2xl" innerClassName="max-h-[85dvh] overflow-y-auto" data-testid={testId}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        {open ? children : null}
      </DialogContent>
    </Dialog>
  );
}

// The rotation (docs/api/README.md section 13.4): the pending key and its ceremony, the new SSH key for each SFTP
// server, then the job recovery-key-rotate (recent authentication).
function RotateBody({
  status,
  hasSftp,
  clinic,
  onStatus,
  onJob,
}: {
  status: RecoveryKeyStatus;
  hasSftp: boolean;
  clinic: string;
  onStatus: (s: RecoveryKeyStatus) => void;
  onJob: (job: Job) => void;
}) {
  const { withReauth } = useReauth();
  const [removeOld, setRemoveOld] = useState(true);
  const ready = Boolean(status.pending?.confirmed_at);
  const sshKey = useSshKey("pending", ready && hasSftp);
  const rotate = useMutation({
    mutationFn: () =>
      withReauth(() =>
        call(api.POST("/backups/recovery-key/rotate", { body: { new_key: true, remove_old_repositories: removeOld } })),
      ),
    onSuccess: onJob,
  });
  return (
    <div className="flex flex-col gap-4">
      <RecoveryKeyCeremony status={status} which="pending" clinic={clinic} onChange={onStatus} />
      {ready ? (
        <>
          {hasSftp ? (
            <section className="flex flex-col gap-2" data-testid="rotate-ssh">
              <p className="text-sm">{p.rotateSsh}</p>
              <div className="flex items-start gap-2">
                {sshKey.data ? (
                  <code className="min-w-0 flex-1 rounded-md border bg-soft-background p-2 text-xs break-all">
                    {sshKey.data.authorized_keys_line}
                  </code>
                ) : (
                  <Skeleton className="h-14 flex-1" />
                )}
                <CopyButton value={sshKey.data?.authorized_keys_line ?? ""} label={strings.dest.sftp.publicKey} />
              </div>
            </section>
          ) : null}
          <label className="flex items-start gap-3 text-sm">
            <Checkbox checked={removeOld} onCheckedChange={(v) => setRemoveOld(v === true)} />
            <span>{p.rotateRemove}</span>
          </label>
          {rotate.error && !isReauthCancelled(rotate.error) ? <ProblemAlert error={rotate.error} /> : null}
          <div>
            <Button
              type="button"
              onClick={() => rotate.mutate()}
              disabled={rotate.isPending}
              data-testid="rotate-start"
            >
              {rotate.isPending ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
              {p.rotateStart}
            </Button>
          </div>
        </>
      ) : null}
    </div>
  );
}

// The recovery key (plans/02 section 2 and 7): its state, "Show the recovery key" (recent authentication),
// the ceremony when the key is new or not confirmed, the rotation, and the re-key of a Phase 2 repository.
export function RecoveryKeyCard({
  overview,
  clinic,
  onJob,
}: {
  overview: BackupsOverview | undefined;
  clinic: string;
  onJob: (job: Job) => void;
}) {
  const invalidate = useInvalidateBackups();
  const { withReauth } = useReauth();
  const [override, setOverride] = useState<RecoveryKeyStatus | null>(null);
  const status = override ?? overview?.recovery_key;
  const [shownKey, setShownKey] = useState<RecoveryKey | null>(null);
  const [dialog, setDialog] = useState<"ceremony" | "rotate" | "rekey" | null>(null);
  const show = useShowKey("active", setShownKey);
  const rekey = useMutation({
    mutationFn: () =>
      withReauth(() =>
        call(api.POST("/backups/recovery-key/rotate", { body: { new_key: false, remove_old_repositories: true } })),
      ),
    onSuccess: (job) => {
      setDialog(null);
      onJob(job);
    },
  });
  const onStatus = (next: RecoveryKeyStatus) => {
    setOverride(next);
    invalidate();
  };
  const close = () => {
    setDialog(null);
    setOverride(null);
  };
  const state = status?.state;
  const hasSftp = Boolean(overview?.destinations.some((d) => d.type === "sftp"));

  return (
    <Panel
      id="recovery-key"
      testId="key-card"
      title={p.keyTitle}
      description={strings.keyCeremony.intro}
      className="scroll-mt-24"
      action={
        <span className="flex w-32 justify-end">
          {status ? (
            <StatusChip tone={KEY_TONE[status.state] ?? "danger"} testId="key-state">
              {p.keyStates[status.state] ?? status.state}
            </StatusChip>
          ) : (
            <Skeleton className="h-7 w-24 rounded-full" />
          )}
        </span>
      }
      footer={
        !status ? (
          // The buttons of a confirmed key, invisible: the same size before the data (docs/console-ui.md 5).
          <div className="invisible contents" aria-hidden="true">
            <Button type="button" tabIndex={-1}>
              <EyeIcon data-icon="inline-start" />
              {p.show}
            </Button>
            <Button type="button" variant="outline" tabIndex={-1}>
              <RefreshCwIcon data-icon="inline-start" />
              {p.rotate}
            </Button>
          </div>
        ) : (
          <>
            {state === "confirmed" ? (
              <>
                <Button type="button" onClick={() => show.mutate()} disabled={show.isPending} data-testid="key-show">
                  {show.isPending ? <Spinner data-icon="inline-start" /> : <EyeIcon data-icon="inline-start" />}
                  {p.show}
                </Button>
                <Button type="button" variant="outline" onClick={() => setDialog("rotate")} data-testid="key-rotate">
                  <RefreshCwIcon data-icon="inline-start" />
                  {p.rotate}
                </Button>
              </>
            ) : (
              <Button type="button" onClick={() => setDialog("ceremony")} data-testid="key-open-ceremony">
                <KeyRoundIcon data-icon="inline-start" />
                {state === "unconfirmed" ? strings.banners.actions["recovery-key-unconfirmed"] : p.make}
              </Button>
            )}
            {status.local_repository === "legacy" && state === "confirmed" ? (
              <Button type="button" variant="outline" onClick={() => setDialog("rekey")} data-testid="key-rekey">
                {p.rekey}
              </Button>
            ) : null}
          </>
        )
      }
    >
      <KeyValueList
        loading={!status}
        items={[
          {
            id: "key-id",
            label: p.keyId,
            mono: true,
            value: status?.key_id ?? p.none,
            copy: status?.key_id ?? undefined,
          },
          { id: "made", label: p.keyMade, value: formatDateTime(status?.created_at) },
          {
            id: "confirmed",
            label: strings.backupsPage.keyStates.confirmed,
            value: formatDateTime(status?.confirmed_at),
          },
        ]}
      />
      {status?.state === "unconfirmed" ? (
        <Alert variant="warning">
          <AlertDescription>{strings.keyCeremony.reminder}</AlertDescription>
        </Alert>
      ) : null}
      {status?.local_repository === "legacy" ? (
        <Alert variant="info">
          <AlertDescription>{p.rekeyIntro}</AlertDescription>
        </Alert>
      ) : null}
      {show.error && !isReauthCancelled(show.error) ? <ProblemAlert error={show.error} /> : null}

      <BigDialog open={shownKey !== null} onClose={() => setShownKey(null)} title={p.keyTitle} testId="key-dialog">
        {shownKey ? <KeySheet recoveryKey={shownKey} clinic={clinic} /> : null}
      </BigDialog>
      <BigDialog open={dialog === "ceremony"} onClose={close} title={p.keyTitle} testId="ceremony-dialog">
        {status ? <RecoveryKeyCeremony status={status} clinic={clinic} onChange={onStatus} /> : null}
      </BigDialog>
      <BigDialog
        open={dialog === "rotate"}
        onClose={close}
        title={p.rotateTitle}
        description={p.rotateIntro}
        testId="rotate-dialog"
      >
        {status ? (
          <RotateBody
            status={status}
            hasSftp={hasSftp}
            clinic={clinic}
            onStatus={onStatus}
            onJob={(job) => {
              close();
              onJob(job);
            }}
          />
        ) : null}
      </BigDialog>
      <BigDialog
        open={dialog === "rekey"}
        onClose={close}
        title={p.rekey}
        description={p.rekeyIntro}
        testId="rekey-dialog"
      >
        <div className="flex flex-col gap-3">
          {rekey.error && !isReauthCancelled(rekey.error) ? <ProblemAlert error={rekey.error} /> : null}
          <div>
            <Button type="button" onClick={() => rekey.mutate()} disabled={rekey.isPending} data-testid="rekey-start">
              {rekey.isPending ? <Spinner data-icon="inline-start" /> : null}
              {p.rotateStart}
            </Button>
          </div>
        </div>
      </BigDialog>
    </Panel>
  );
}
