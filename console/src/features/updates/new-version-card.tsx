import { useMutation } from "@tanstack/react-query";
import { AlertTriangleIcon, DownloadIcon, ExternalLinkIcon, RefreshCwIcon, RocketIcon } from "lucide-react";
import { useState } from "react";
import { api, call } from "@/api/client";
import { asProblem, ProblemError, type ProblemError as PE } from "@/api/problem";
import type { Job, Status, UpdatesOverview } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { WhatNext } from "@/components/v2/panel";
import { useNow } from "@/components/v2/use-now";
import { SectionCard } from "@/components/app/section-card";
import { ShortId } from "@/components/app/short-id";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { formatDuration } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { updateStatus } from "./update-text";
import { formatZoned } from "./window";

const u = strings.updates;

function downtimeText(o: UpdatesOverview): string {
  const t = o.target;
  if (!t || !o.update_available) return "—";
  if (t.path === "none" || t.path === "plug-config") return u.downtimeNone;
  if (t.estimated_downtime_s) return fmt(u.downtimeAbout, { time: formatDuration(t.estimated_downtime_s) });
  return u.downtimeUnknown;
}

function Components({ o }: { o: UpdatesOverview }) {
  if (o.components.length === 0) return null;
  return (
    <Collapsible className="rounded-lg border">
      <CollapsibleTrigger asChild>
        <Button type="button" variant="ghost" size="sm" className="w-full justify-start">
          {u.whatChanges}
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ul className="flex flex-col divide-y border-t text-xs" data-testid="update-components">
          {o.components.map((c) => (
            <li key={c.id} className="grid gap-1 px-3 py-2 sm:grid-cols-[minmax(0,12rem)_minmax(0,1fr)]">
              <span className="truncate" title={c.id}>
                {c.label}
              </span>
              {c.changed ? (
                <span className="flex min-w-0 flex-wrap items-center gap-x-2">
                  <ShortId value={c.current} label={`${c.label} (${u.from})`} />
                  <span aria-hidden="true">→</span>
                  <ShortId value={c.target} label={`${c.label} (${u.to})`} />
                </span>
              ) : (
                <span className="text-soft-foreground">{u.unchanged}</span>
              )}
            </li>
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  );
}

// "Install now": a confirm dialog, then POST /updates/apply. Outside the window the agent answers
// 409 update-window-closed with next_window_at; the dialog then offers "Install anyway" (now: true).
function ApplyButton({
  o,
  tz,
  disabled,
  onJob,
  secondary = false,
}: {
  o: UpdatesOverview;
  tz: string;
  disabled: boolean;
  onJob: (j: Job) => void;
  secondary?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [closed, setClosed] = useState<PE | null>(null);
  const version = o.target?.version ?? o.latest?.version ?? "";
  const apply = useMutation({
    mutationFn: (now: boolean) => call(api.POST("/updates/apply", { body: { version: null, now, reason: null } })),
    onSuccess: (job) => {
      setOpen(false);
      setClosed(null);
      onJob(job);
    },
    onError: (e) => {
      const p = asProblem(e);
      if (p?.is("update-window-closed")) setClosed(p);
    },
  });
  const downtime = o.target?.estimated_downtime_s;
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Button
        type="button"
        variant={secondary ? "outline" : "default"}
        onClick={() => {
          apply.reset();
          setClosed(null);
          setOpen(true);
        }}
        disabled={disabled}
        data-testid="update-apply"
      >
        <RocketIcon data-icon="inline-start" />
        {u.apply}
      </Button>
      <AlertDialogContent data-testid="apply-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>{closed ? u.windowClosedTitle : fmt(u.applyConfirm, { version })}</AlertDialogTitle>
          <AlertDialogDescription>
            {closed ? fmt(u.windowClosed, { time: formatZoned(closed.problem.next_window_at, tz) }) : u.applyText}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {!closed && downtime ? (
          <p className="text-sm">{fmt(u.applyDowntime, { time: formatDuration(downtime) })}</p>
        ) : null}
        {apply.error && !closed ? <ProblemAlert error={apply.error} /> : null}
        <AlertDialogFooter>
          <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
          <Button
            type="button"
            variant={closed ? "destructive-solid" : "default"}
            onClick={() => apply.mutate(Boolean(closed))}
            disabled={apply.isPending}
            data-testid={closed ? "apply-anyway" : "apply-confirm"}
          >
            {apply.isPending ? <Spinner data-icon="inline-start" /> : null}
            {closed ? u.applyAnyway : u.apply}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// The newest version and what the operator can do: check, download (stage), install.
export function NewVersionCard({
  o,
  status,
  tz,
  busy,
  onJob,
}: {
  o: UpdatesOverview | undefined;
  status: Status | undefined;
  tz: string;
  busy: boolean;
  onJob: (job: Job) => void;
}) {
  const check = useMutation({ mutationFn: () => call(api.POST("/updates/check")), onSuccess: onJob });
  const stage = useMutation({
    mutationFn: () => call(api.POST("/updates/stage", { body: { version: null } })),
    onSuccess: onJob,
  });
  const loading = !o;
  const latest = o?.latest ?? null;
  const target = o?.update_available ? o.target : null;
  const shown = target ?? latest;
  const installed = Boolean(latest && o?.current?.version === latest.version);
  const st = o ? updateStatus(o) : null;
  const blocker = o?.blocked_by ?? null;
  const tooOld = blocker?.code === "agent-too-old";
  const canStage = Boolean(target && !target.staged && !blocker);
  const canApply = Boolean(target && !blocker);
  const error = check.error ?? stage.error;
  const now = useNow(true, 60_000);
  // "Install tonight" (S66): download now, install in the update window (the next one, when it is within a day).
  const nextOpen = o?.window.next_open_at ? Date.parse(o.window.next_open_at) : null;
  const windowStart = o?.next_apply_at ?? o?.window.next_open_at ?? null;
  const tonight = nextOpen !== null && nextOpen - now < 24 * 3600 * 1000;
  const windowAt = windowStart ? formatZoned(windowStart, tz) : null;
  return (
    <SectionCard
      title={u.newVersion}
      testId="new-version-card"
      footer={
        <>
          <Button
            type="button"
            variant={canApply ? "ghost" : "default"}
            className={canApply ? "no-underline" : undefined}
            onClick={() => check.mutate()}
            disabled={loading || busy || check.isPending}
            data-testid="update-check"
          >
            {check.isPending ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
            {u.check}
          </Button>
          {canStage ? (
            <Button
              type="button"
              onClick={() => stage.mutate()}
              disabled={busy || stage.isPending}
              data-testid="update-stage"
            >
              {stage.isPending ? <Spinner data-icon="inline-start" /> : <DownloadIcon data-icon="inline-start" />}
              {tonight ? u.installTonight : u.installInWindow}
            </Button>
          ) : null}
          {o && canApply ? <ApplyButton o={o} tz={tz} disabled={busy} onJob={onJob} secondary={canStage} /> : null}
          {o && canApply && windowAt ? (
            <WhatNext className="w-full" id="install-next">
              {fmt(target?.staged ? u.stagedNext : u.installLaterNext, { time: windowAt })}
            </WhatNext>
          ) : null}
        </>
      }
    >
      {tooOld && o ? (
        <Alert variant="warning" data-testid="agent-too-old">
          <AlertTriangleIcon />
          <AlertTitle>{u.agentTooOldTitle}</AlertTitle>
          <AlertDescription>
            {fmt(u.agentTooOld, {
              solution: o.solution.name,
              version: o.target?.version ?? o.latest?.version ?? "",
              min: blocker?.min_agent_version ?? o.target?.min_agent_version ?? "?",
              agent: status?.agent.version ?? "?",
            })}
          </AlertDescription>
        </Alert>
      ) : null}
      <DataList>
        <DataRow
          label={u.newest}
          loading={loading}
          value={
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate font-semibold" data-testid="newest-version">
                {shown?.version ?? "—"}
              </span>
              {installed ? (
                <Badge size="sm" variant="neutral">
                  {u.installedBadge}
                </Badge>
              ) : null}
            </span>
          }
        />
        <DataRow label={u.released} loading={loading} value={formatZoned(shown?.released_at, tz, "date")} />
        {/* Below 640 px the summary can use 3 lines; the row reserves them before the data arrives. */}
        <DataRow
          label={u.changes}
          title={shown?.summary ?? undefined}
          value={
            <span
              className="line-clamp-3 block min-h-15 sm:line-clamp-none sm:min-h-0 sm:truncate"
              data-testid="update-summary"
            >
              {o ? (shown?.summary ?? "—") : <Skeleton className="mt-1 h-4 w-28" />}
            </span>
          }
        />
        <DataRow
          label={u.state}
          loading={loading}
          testId="update-state"
          value={
            st ? (
              <Badge size="sm" variant={st.variant} className="max-w-full truncate">
                {st.text}
              </Badge>
            ) : null
          }
        />
        <DataRow label={u.downtime} loading={loading} value={o ? downtimeText(o) : ""} />
        <DataRow
          label={u.lastCheck}
          loading={loading}
          value={o?.last_check_at ? formatZoned(o.last_check_at, tz) : u.never}
        />
      </DataList>
      {/* The line of the release notes keeps its height without a link, so the card does not grow. */}
      <p className="flex min-h-5 items-center text-sm">
        {shown?.changelog_url ? (
          <a
            href={shown.changelog_url}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 underline-offset-4 hover:underline"
          >
            <ExternalLinkIcon className="size-4" aria-hidden="true" />
            {u.releaseNotes}
          </a>
        ) : null}
      </p>
      {o?.track === "pinned" && !o.update_available && o.available.length > 0 ? (
        <p className="text-sm text-soft-foreground">
          {fmt(u.pinnedNewer, { version: o.current?.version ?? o.pin ?? "" })}
        </p>
      ) : null}
      {o?.last_check_error ? <ProblemAlert error={new ProblemError(o.last_check_error)} title={u.checkFailed} /> : null}
      {o ? <Components o={o} /> : null}
      <p className="text-xs text-soft-foreground">
        {target?.staged ? u.stagedHelp : canStage ? u.stageHelp : u.checkHelp}
      </p>
      {error ? <ProblemAlert error={error} /> : null}
    </SectionCard>
  );
}
