import { useMutation } from "@tanstack/react-query";
import { PackageIcon, RotateCcwIcon } from "lucide-react";
import { useState } from "react";
import { api, call } from "@/api/client";
import { useOsUpdates } from "@/api/queries";
import type { Job, OsReboot, UpdatesOverview } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import { SectionCard } from "@/components/app/section-card";
import { Alert, AlertDescription } from "@/components/ui/alert";
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
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fmt, strings } from "@/lib/strings";
import { describeWindow, formatZoned } from "./window";

const u = strings.updates;

// OS updates (GET /updates/os; docs/solutions.md section 11): the security updates, the packages to install,
// the held container packages, the reboot. "Install now" and "Reboot now" need a reauthentication.
export function OsCard({
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
  const os = useOsUpdates();
  const d = os.data;
  const loading = !d;
  const [confirm, setConfirm] = useState<"install" | "reboot" | null>(null);
  const [reboot, setReboot] = useState<OsReboot | null>(null);
  const install = useMutation({
    mutationFn: () =>
      withReauth(() =>
        call(api.POST("/updates/os/apply", { body: { now: true, reboot_if_required: false, reason: null } })),
      ),
    onSuccess: onJob,
  });
  const restart = useMutation({
    mutationFn: (now: boolean) => withReauth(() => call(api.POST("/updates/os/reboot", { body: { now, delay_s: 5 } }))),
    onSuccess: setReboot,
  });
  const security = d?.upgradable.filter((p) => p.security).length ?? 0;
  const upgrade = d?.held.filter((h) => h.action === "upgrade").length ?? 0;
  const count = (d?.upgradable.length ?? 0) + upgrade;
  const windowText = o ? `${describeWindow(o.window)}, ${formatZoned(o.window.next_open_at, tz)}` : "";
  const error = [install.error, restart.error].find((e) => e && !isReauthCancelled(e));
  return (
    <SectionCard
      title={u.osTitle}
      testId="os-card"
      footer={
        <AlertDialog open={confirm !== null} onOpenChange={(open) => (open ? null : setConfirm(null))}>
          <Button
            type="button"
            variant="outline"
            onClick={() => setConfirm("install")}
            disabled={loading || busy || count === 0 || install.isPending}
            data-testid="os-install"
          >
            {install.isPending ? <Spinner data-icon="inline-start" /> : <PackageIcon data-icon="inline-start" />}
            {u.osInstall}
          </Button>
          <Button
            type="button"
            variant={d?.reboot_required ? "default" : "outline"}
            onClick={() => setConfirm("reboot")}
            disabled={loading || busy || restart.isPending}
            data-testid="os-reboot"
          >
            {restart.isPending ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}
            {u.osReboot}
          </Button>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{confirm === "install" ? u.osInstallConfirm : u.osRebootConfirm}</AlertDialogTitle>
              <AlertDialogDescription>
                {confirm === "install" ? fmt(u.osInstallText, { count }) : fmt(u.osRebootText, { window: windowText })}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
              {confirm === "reboot" ? (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    setConfirm(null);
                    restart.mutate(false);
                  }}
                  data-testid="os-reboot-window"
                >
                  {u.osRebootWindow}
                </Button>
              ) : null}
              <Button
                type="button"
                variant={confirm === "reboot" ? "destructive-solid" : "default"}
                onClick={() => {
                  const action = confirm;
                  setConfirm(null);
                  if (action === "install") install.mutate();
                  else restart.mutate(true);
                }}
                data-testid="os-confirm"
              >
                {confirm === "install" ? u.osInstall : u.osReboot}
              </Button>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      }
    >
      <DataList>
        <DataRow label={u.os} loading={loading} value={d?.distro.pretty_name} />
        <DataRow
          label={u.security}
          loading={loading}
          value={d ? (d.security_auto ? u.securityAuto : u.securityOff) : ""}
        />
        <DataRow
          label={u.pending}
          loading={loading}
          testId="os-pending"
          value={d ? (count > 0 ? fmt(u.pendingCount, { count, security }) : u.pendingNone) : ""}
        />
        <DataRow
          label={u.held}
          loading={loading}
          value={d ? fmt(u.heldCount, { count: d.held.length, upgrade }) : ""}
        />
        <DataRow
          label={u.rebootNeeded}
          loading={loading}
          testId="os-reboot-required"
          value={
            d
              ? d.reboot_required
                ? fmt(u.rebootYes, { packages: d.reboot_required_packages.join(", ") || "—" })
                : u.rebootNo
              : ""
          }
          title={d?.reboot_required_packages.join(", ")}
        />
        <DataRow
          label={u.rebootScheduled}
          loading={loading}
          value={formatZoned(reboot?.at ?? d?.reboot_scheduled_at, tz)}
        />
        <DataRow label={u.lastFull} loading={loading} value={formatZoned(d?.last_full_upgrade?.finished_at, tz)} />
      </DataList>
      {/* The trigger shows before the data (disabled), so the card keeps its height. */}
      {!d || d.held.length > 0 ? (
        <Collapsible className="rounded-lg border">
          <CollapsibleTrigger asChild>
            <Button type="button" variant="ghost" size="sm" className="w-full justify-start" disabled={!d}>
              {u.heldList}
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className="overflow-x-auto border-t">
              <Table className="text-xs">
                <TableHeader>
                  <TableRow>
                    <TableHead>{u.heldName}</TableHead>
                    <TableHead>{u.heldInstalled}</TableHead>
                    <TableHead>{u.heldTested}</TableHead>
                    <TableHead>{u.heldAction}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(d?.held ?? []).map((h) => (
                    <TableRow key={h.name}>
                      <TableCell>{h.name}</TableCell>
                      <TableCell className="font-mono">{h.installed}</TableCell>
                      <TableCell className="font-mono">{h.tested ?? "—"}</TableCell>
                      <TableCell>{u.heldActions[h.action] ?? h.action}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </CollapsibleContent>
        </Collapsible>
      ) : null}
      {reboot ? (
        <Alert variant="info" data-testid="os-reboot-accepted">
          <AlertDescription>
            {fmt(reboot.scheduled ? u.osRebootScheduledAt : u.osRebootAt, { time: formatZoned(reboot.at, tz) })}
          </AlertDescription>
        </Alert>
      ) : null}
      {os.error ? <ProblemAlert error={os.error} /> : null}
      {error ? <ProblemAlert error={error} /> : null}
    </SectionCard>
  );
}
