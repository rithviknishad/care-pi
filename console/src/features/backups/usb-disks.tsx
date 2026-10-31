import { useMutation, useQueryClient } from "@tanstack/react-query";
import { HardDriveIcon, RefreshCwIcon, UsbIcon } from "lucide-react";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { qk, useBackupDisks } from "@/api/queries";
import type { BackupDisk, DiskPrepareResult, Job } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { ProblemAlert } from "@/components/app/problem-alert";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { JobProgress } from "@/features/jobs/job-progress";
import { formatBytes } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { nextDiskLabel } from "./destination-meta";

const u = strings.dest.usb;

function diskName(d: BackupDisk): string {
  return d.model ?? d.device;
}

function PrepareDialog({
  disk,
  label,
  open,
  onOpenChange,
  onConfirm,
}: {
  disk: BackupDisk;
  label: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  const id = useId();
  const [typed, setTyped] = useState("");
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setTyped("");
        onOpenChange(next);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{fmt(u.wipeTitle, { disk: diskName(disk) })}</AlertDialogTitle>
          <AlertDialogDescription>
            {fmt(u.wipeText, { disk: `${diskName(disk)} ${disk.device}`, size: formatBytes(disk.size_bytes), label })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <FormField id={`${id}-label`} label={fmt(u.wipeType, { label })}>
          <Input
            id={`${id}-label`}
            value={typed}
            onChange={(e) => setTyped(e.target.value.trim())}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            className="font-mono"
            data-testid="wipe-confirm-input"
          />
        </FormField>
        <AlertDialogFooter>
          <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
          <Button
            type="button"
            variant="destructive-solid"
            disabled={typed !== label}
            onClick={() => {
              setTyped("");
              onConfirm();
            }}
            data-testid="wipe-confirm"
          >
            {u.wipeConfirm}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function DiskRow({ disk, note, children }: { disk: BackupDisk; note?: string | null; children?: React.ReactNode }) {
  const Icon = disk.transport === "usb" ? UsbIcon : HardDriveIcon;
  return (
    <li
      className="flex flex-col gap-3 rounded-lg border p-3 sm:flex-row sm:items-center"
      data-testid="disk"
      data-usage={disk.usage}
      data-disk={disk.id}
    >
      <div className="flex min-w-0 flex-1 items-start gap-3">
        <Icon className="mt-0.5 size-5 shrink-0 text-soft-foreground" aria-hidden="true" />
        <div className="min-w-0">
          <p className="truncate font-semibold" title={disk.id}>
            {diskName(disk)}
          </p>
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-soft-foreground">
            <span>{disk.device}</span>
            <span>{formatBytes(disk.size_bytes)}</span>
            <span>{disk.transport}</span>
            <Badge
              size="sm"
              variant={disk.usage === "system" ? "neutral" : disk.usage === "care-backup" ? "success" : "info"}
            >
              {u.usages[disk.usage] ?? disk.usage}
            </Badge>
            {disk.mounted ? (
              <Badge size="sm" variant="neutral">
                {u.mounted}
              </Badge>
            ) : null}
          </p>
          {note ? <p className="mt-1 text-xs text-soft-foreground">{note}</p> : null}
        </div>
      </div>
      {children ? <div className="shrink-0">{children}</div> : null}
    </li>
  );
}

// The disks of GET /backups/disks for the choice "USB disk" (plans/02 section 7). The system disk is shown, but
// the console refuses it. A disk with care-pi backups can be used without a change. Another disk needs a prepare:
// the job erases it (a recent authentication and the label typed again).
export function UsbDiskPicker({
  takenLabels,
  onSelect,
  restore = false,
}: {
  takenLabels: readonly string[];
  onSelect: (label: string) => void;
  // The restore of a new device: only the disks with care-pi backups, and no prepare.
  restore?: boolean;
}) {
  const qc = useQueryClient();
  const { withReauth } = useReauth();
  const disks = useBackupDisks();
  const [confirm, setConfirm] = useState<BackupDisk | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const items = (disks.data?.items ?? []).filter((d) => !restore || d.usage === "care-backup");
  const label = nextDiskLabel([...takenLabels, ...(disks.data?.items ?? []).map((d) => d.care_label)]);
  const prepare = useMutation({
    mutationFn: (disk: BackupDisk) =>
      withReauth(() =>
        call(
          api.POST("/backups/disks/{disk_id}/prepare", {
            params: { path: { disk_id: disk.id } },
            body: { wipe: true, label },
          }),
        ),
      ),
    onSuccess: setJob,
  });

  return (
    <div className="flex flex-col gap-3" data-testid="usb-disks">
      <div className="flex flex-wrap items-center gap-2">
        <p className="flex-1 text-sm text-soft-foreground">{u.intro}</p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => void disks.refetch()}
          disabled={disks.isFetching}
        >
          {disks.isFetching ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
          {u.refresh}
        </Button>
      </div>
      {disks.isError ? <ProblemAlert error={disks.error} /> : null}
      {disks.isPending ? (
        <Skeleton className="h-36" />
      ) : items.length === 0 ? (
        <p className="text-sm">{restore ? strings.dr.diskNone : u.none}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {items.map((d) => {
            const usable = d.usage === "care-backup" && d.care_label && (restore || !d.destination_id);
            const note =
              d.usage === "system"
                ? u.system
                : d.usage === "care-backup" && d.care_label
                  ? d.destination_id && !restore
                    ? fmt(u.used, { id: d.destination_id })
                    : fmt(u.ready, { label: d.care_label })
                  : d.mounted
                    ? u.inUse
                    : null;
            return (
              <DiskRow key={d.id} disk={d} note={note}>
                {usable ? (
                  <Button
                    type="button"
                    size="sm"
                    variant={selected === d.care_label ? "outline" : "default"}
                    onClick={() => {
                      setSelected(d.care_label);
                      onSelect(d.care_label ?? "");
                    }}
                    aria-pressed={selected === d.care_label}
                    data-testid="disk-use"
                  >
                    {u.use}
                  </Button>
                ) : d.usage !== "system" && d.usage !== "care-backup" && !d.mounted ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setConfirm(d)}
                    disabled={prepare.isPending || job !== null}
                    data-testid="disk-prepare"
                  >
                    {u.prepare}
                  </Button>
                ) : null}
              </DiskRow>
            );
          })}
        </ul>
      )}
      {confirm ? (
        <PrepareDialog
          disk={confirm}
          label={label}
          open
          onOpenChange={(open) => (open ? undefined : setConfirm(null))}
          onConfirm={() => {
            const disk = confirm;
            setConfirm(null);
            prepare.mutate(disk);
          }}
        />
      ) : null}
      {prepare.error && !isReauthCancelled(prepare.error) ? <ProblemAlert error={prepare.error} /> : null}
      {job ? (
        <JobProgress
          jobId={job.id}
          initial={job}
          title={u.preparing}
          onDone={(done) => {
            void qc.invalidateQueries({ queryKey: qk.disks });
            const result = done.result as DiskPrepareResult | null;
            if (done.state === "succeeded" && result?.kind === "disk-prepare") onSelect(result.label);
          }}
        />
      ) : null}
    </div>
  );
}
