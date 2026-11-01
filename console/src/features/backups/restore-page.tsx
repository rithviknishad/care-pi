import { useMutation } from "@tanstack/react-query";
import { AlertTriangleIcon, FileStackIcon, HistoryIcon, MonitorSmartphoneIcon, Undo2Icon } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { api, call } from "@/api/client";
import { ProblemError } from "@/api/problem";
import { useRestorePoints } from "@/api/queries";
import type { Job, RestoreFilesResult, RestorePitrResult, RestorePoints } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { FormField } from "@/components/app/form-field";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
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
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { Slider } from "@/components/ui/slider";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { JobProgress } from "@/features/jobs/job-progress";
import { formatBytes, formatDateTime, formatDuration } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import {
  clamp,
  formatMinute,
  fromLocalInput,
  isoSeconds,
  nearestSnapshot,
  restoreOffer,
  toLocalInput,
  undoCheckpoint,
} from "./backups-format";
import { useInvalidateBackups } from "./use-backups";

const r = strings.restore;
type Kind = "pitr" | "files" | "device";
const KINDS: { id: Kind; icon: typeof HistoryIcon }[] = [
  { id: "pitr", icon: HistoryIcon },
  { id: "files", icon: FileStackIcon },
  { id: "device", icon: MonitorSmartphoneIcon },
];

function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs]);
  return now;
}

function ConfirmDialog({
  open,
  title,
  text,
  onOpenChange,
  onConfirm,
}: {
  open: boolean;
  title: string;
  text: string;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{text}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
          <Button
            type="button"
            variant="destructive-solid"
            onClick={() => {
              onOpenChange(false);
              onConfirm();
            }}
            data-testid="restore-confirm"
          >
            {r.confirm}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// A restore job in the page: the steps with their operator text and the expected times of SP6, then the result.
function RestoreJob({ job, result }: { job: Job; result: (done: Job) => React.ReactNode }) {
  const invalidate = useInvalidateBackups();
  const [done, setDone] = useState<Job | null>(null);
  return (
    <section className="flex flex-col gap-3" data-testid="restore-job">
      <h3 className="font-semibold">{r.progressTitle}</h3>
      <JobProgress
        jobId={job.id}
        initial={job}
        onDone={(final) => {
          setDone(final);
          invalidate();
        }}
      >
        {done?.state === "failed" && done.error ? <ProblemAlert error={new ProblemError(done.error)} /> : null}
        {done ? result(done) : null}
      </JobProgress>
    </section>
  );
}

// The undo card: a restore of the checkpoint of the last restore job, for 24 hours (docs/api/README.md section 13.9.1).
function UndoCard({ points, onJob }: { points: RestorePoints; onJob: (job: Job) => void }) {
  const { withReauth } = useReauth();
  const [confirm, setConfirm] = useState(false);
  const undo = undoCheckpoint(points.checkpoints);
  const start = useMutation({
    mutationFn: (label: string) =>
      withReauth(() =>
        call(
          api.POST("/restore", { body: { kind: "pitr", time: null, checkpoint: label, reason: "undo the restore" } }),
        ),
      ),
    onSuccess: onJob,
  });
  if (!undo) return null;
  const at = formatMinute(undo.checkpoint.undo_point);
  return (
    <Alert variant="info" data-testid="undo-card">
      <Undo2Icon aria-hidden="true" />
      <AlertTitle>{r.undoTitle}</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-2">
        <p>{fmt(r.undoText, { at, undo: at, until: formatMinute(new Date(undo.until).toISOString()) })}</p>
        <Button type="button" variant="outline" size="sm" onClick={() => setConfirm(true)} data-testid="undo-start">
          {start.isPending ? <Spinner data-icon="inline-start" /> : <Undo2Icon data-icon="inline-start" />}
          {r.undo}
        </Button>
        {start.error && !isReauthCancelled(start.error) ? <ProblemAlert error={start.error} /> : null}
      </AlertDescription>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={r.undoConfirmTitle}
        text={fmt(r.undoConfirmText, { undo: at })}
        onConfirm={() => start.mutate(undo.checkpoint.label)}
      />
    </Alert>
  );
}

// "Undo a mistake": a point in time inside the restore window (docs/api/README.md section 13.9.1).
// offered is the time of the PITR offer of an update (/restore?time=…): the panel starts with it.
function PitrPanel({ points, offered }: { points: RestorePoints; offered: number | null }) {
  const id = useId();
  const { withReauth } = useReauth();
  const now = useNow();
  const pitr = points.pitr;
  const from = pitr ? Date.parse(pitr.from) : 0;
  // A restore job archives the current WAL segment first: a time up to the start of the job is possible.
  const max = Math.max(now, pitr ? Date.parse(pitr.to) : 0);
  const initial = offered ?? (pitr ? Date.parse(pitr.to) : 0);
  const [time, setTime] = useState(initial);
  const [text, setText] = useState(() => (pitr ? toLocalInput(initial) : ""));
  const [confirm, setConfirm] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const start = useMutation({
    mutationFn: (iso: string) =>
      withReauth(() =>
        call(api.POST("/restore", { body: { kind: "pitr", time: iso, checkpoint: null, reason: null } })),
      ),
    onSuccess: setJob,
  });
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const nearest = useMemo(() => nearestSnapshot(points.file_snapshots, time, "files"), [points.file_snapshots, time]);
  if (!pitr) {
    return (
      <Empty className="min-h-40 rounded-lg border border-dashed">
        <EmptyHeader>
          <EmptyDescription>{r.noBackup}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  if (job) {
    return (
      <RestoreJob
        job={job}
        result={(done) => {
          const result = done.result as RestorePitrResult | null;
          if (done.state !== "succeeded" || result?.kind !== "restore-pitr") return null;
          return (
            <Alert variant="success" data-testid="restore-result">
              <AlertTitle>{fmt(r.succeeded, { time: formatDateTime(result.restored_to) })}</AlertTitle>
              <AlertDescription>{fmt(r.downtimeWas, { time: formatDuration(result.downtime_s) })}</AlertDescription>
            </Alert>
          );
        }}
      />
    );
  }
  const inside = time >= from && time <= max;
  const timeText = formatMinute(new Date(time).toISOString());
  const setFromMs = (ms: number) => {
    const t = clamp(ms, from, max);
    setTime(t);
    setText(toLocalInput(t));
  };
  return (
    <div className="flex flex-col gap-4" data-testid="pitr-panel">
      <div className="grid gap-1 text-sm sm:grid-cols-2">
        <p>
          <span className="text-soft-foreground">{r.earliest}: </span>
          <span data-testid="pitr-from">{formatDateTime(pitr.from)}</span>
        </p>
        <p className="sm:text-right">
          <span className="text-soft-foreground">{r.latest}: </span>
          <span>{r.now}</span>
        </p>
      </div>
      <Slider
        value={[Math.round(time / 1000)]}
        min={Math.floor(from / 1000)}
        max={Math.ceil(max / 1000)}
        step={60}
        onValueChange={(v) => setFromMs((Array.isArray(v) ? (v[0] ?? 0) : v) * 1000)}
        aria-labelledby={`${id}-time-label`}
        data-testid="pitr-slider"
      />
      <FormField
        id={`${id}-time`}
        label={<span id={`${id}-time-label`}>{r.time}</span>}
        help={fmt(r.timeHelp, { zone })}
        error={inside ? null : r.outside}
      >
        <div className="flex flex-wrap items-center gap-2">
          <Input
            id={`${id}-time`}
            type="datetime-local"
            step={1}
            min={toLocalInput(from)}
            max={toLocalInput(max)}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              const t = fromLocalInput(e.target.value);
              if (t !== null) setTime(t);
            }}
            className="max-w-64 font-mono"
            aria-invalid={inside ? undefined : true}
            data-testid="pitr-time"
          />
          <Button type="button" variant="outline" size="sm" onClick={() => setFromMs(Date.now())}>
            {r.latest}
          </Button>
        </div>
      </FormField>
      <p className="text-sm" data-testid="pitr-files">
        {nearest
          ? fmt(r.nearestFiles, {
              time: formatMinute(nearest.snapshot.time),
              diff: formatDuration(Math.max(60, nearest.diffS)),
            })
          : r.noFiles}{" "}
        {r.filesStay}
      </p>
      <Alert variant="destructive" data-testid="pitr-warning">
        <AlertTriangleIcon aria-hidden="true" />
        <AlertTitle>{fmt(r.loss, { time: timeText })}</AlertTitle>
        <AlertDescription className="flex flex-col gap-1">
          <p>{r.downtime}</p>
          <p>{fmt(r.expected, { restore: "8 s", recovery: "5 s", start: "44 s" })}</p>
          <p>{r.undoNote}</p>
        </AlertDescription>
      </Alert>
      {start.error && !isReauthCancelled(start.error) ? <ProblemAlert error={start.error} /> : null}
      <div>
        <Button
          type="button"
          variant="destructive-solid"
          disabled={!inside || start.isPending}
          onClick={() => setConfirm(true)}
          data-testid="pitr-start"
        >
          {start.isPending ? <Spinner data-icon="inline-start" /> : <HistoryIcon data-icon="inline-start" />}
          {fmt(r.start, { time: timeText })}
        </Button>
      </div>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={fmt(r.confirmTitle, { time: timeText })}
        text={fmt(r.confirmText, { time: timeText })}
        onConfirm={() => start.mutate(isoSeconds(time))}
      />
    </div>
  );
}

const PATH = /^[A-Za-z0-9_-][A-Za-z0-9._-]*(\/[A-Za-z0-9_-][A-Za-z0-9._-]*)*\/?$/;

// "Files only": one files snapshot, into the storage of Care or into a side directory (section 13.9.2).
function FilesPanel({ points }: { points: RestorePoints }) {
  const id = useId();
  const { withReauth } = useReauth();
  const snapshots = points.file_snapshots.filter((s) => s.contents.includes("files"));
  const [key, setKey] = useState(() => (snapshots[0] ? `${snapshots[0].destination}/${snapshots[0].snapshot_id}` : ""));
  const [target, setTarget] = useState<"in-place" | "side-dir">("side-dir");
  const [pathsText, setPathsText] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const snap = snapshots.find((s) => `${s.destination}/${s.snapshot_id}` === key);
  const paths = pathsText
    .split("\n")
    .map((p) => p.trim())
    .filter(Boolean);
  const badPath = paths.some((p) => !PATH.test(p));
  const start = useMutation({
    mutationFn: () =>
      withReauth(() =>
        call(
          api.POST("/restore", {
            body: {
              kind: "files",
              destination: snap?.destination ?? "",
              snapshot_id: snap?.snapshot_id ?? "",
              paths: paths.length ? paths : null,
              target,
              reason: null,
            },
          }),
        ),
      ),
    onSuccess: setJob,
  });
  if (snapshots.length === 0) {
    return (
      <Empty className="min-h-40 rounded-lg border border-dashed">
        <EmptyHeader>
          <EmptyDescription>{r.files.noSnapshot}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  if (job) {
    return (
      <RestoreJob
        job={job}
        result={(done) => {
          const result = done.result as RestoreFilesResult | null;
          if (done.state !== "succeeded" || result?.kind !== "restore-files") return null;
          return (
            <Alert variant="success" data-testid="restore-result">
              <AlertTitle>{fmt(r.files.result, { files: result.files, bytes: formatBytes(result.bytes) })}</AlertTitle>
              <AlertDescription className="flex flex-col gap-2">
                {result.target === "side-dir" ? (
                  <div className="flex items-center gap-2">
                    <code className="min-w-0 flex-1 text-xs break-all" data-testid="restore-path">
                      {fmt(r.files.resultPath, { path: result.path })}
                    </code>
                    <CopyButton value={result.path} />
                  </div>
                ) : (
                  <p>{r.files.resultInPlace}</p>
                )}
                {result.verified ? <p>{r.files.verified}</p> : null}
              </AlertDescription>
            </Alert>
          );
        }}
      />
    );
  }
  const snapTime = snap ? formatMinute(snap.time) : "";
  return (
    <div className="flex flex-col gap-4" data-testid="files-panel">
      <FormField id={`${id}-snap`} label={r.files.snapshot} help={r.files.snapshotHelp}>
        <NativeSelect
          id={`${id}-snap`}
          value={key}
          onChange={(e) => setKey(e.target.value)}
          data-testid="files-snapshot"
        >
          {snapshots.map((s) => (
            <NativeSelectOption key={`${s.destination}/${s.snapshot_id}`} value={`${s.destination}/${s.snapshot_id}`}>
              {fmt(r.files.option, {
                time: formatMinute(s.time),
                destination: s.destination ?? "",
                size: formatBytes(s.size_bytes),
              })}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </FormField>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium">{r.files.target}</legend>
        <RadioGroup value={target} onValueChange={(v) => setTarget(v === "in-place" ? "in-place" : "side-dir")}>
          {(["side-dir", "in-place"] as const).map((t) => (
            <Label key={t} htmlFor={`${id}-${t}`} className="flex items-start gap-3 font-normal">
              <RadioGroupItem id={`${id}-${t}`} value={t} className="mt-0.5" />
              <span className="flex flex-col gap-0.5">
                <span className="font-semibold">{t === "side-dir" ? r.files.sideDir : r.files.inPlace}</span>
                <span className="text-xs text-soft-foreground">
                  {t === "side-dir" ? r.files.sideDirHelp : r.files.inPlaceHelp}
                </span>
              </span>
            </Label>
          ))}
        </RadioGroup>
      </fieldset>
      <FormField
        id={`${id}-paths`}
        label={r.files.paths}
        help={r.files.pathsHelp}
        error={badPath ? r.files.pathInvalid : null}
      >
        <Textarea
          id={`${id}-paths`}
          value={pathsText}
          onChange={(e) => setPathsText(e.target.value)}
          rows={3}
          className="font-mono"
          spellCheck={false}
        />
      </FormField>
      {start.error && !isReauthCancelled(start.error) ? <ProblemAlert error={start.error} /> : null}
      <div>
        <Button
          type="button"
          variant={target === "in-place" ? "destructive-solid" : "default"}
          disabled={!snap || badPath || start.isPending}
          onClick={() => setConfirm(true)}
          data-testid="files-start"
        >
          {start.isPending ? <Spinner data-icon="inline-start" /> : null}
          {r.files.start}
        </Button>
      </div>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={fmt(r.files.confirmTitle, { time: snapTime })}
        text={target === "side-dir" ? r.files.sideDirHelp : r.files.inPlaceHelp}
        onConfirm={() => start.mutate()}
      />
    </div>
  );
}

// The restore page (plans/02 section 6): 3 entry cards, then the panel of the selected kind.
export default function RestorePage() {
  const points = useRestorePoints();
  const [params, setParams] = useSearchParams();
  const raw = params.get("kind");
  // /restore?time=<RFC 3339 time>: the PITR offer of an update (docs/console-ui.md section 11.4) opens "Undo a mistake".
  const offerParam = params.get("time");
  const offered = restoreOffer(offerParam);
  const kind: Kind | null =
    raw === "pitr" || raw === "files" || raw === "device" ? raw : offered !== null ? "pitr" : null;
  const [undoJob, setUndoJob] = useState<Job | null>(null);
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={r.title} description={r.intro} />
      {offered !== null && offerParam ? (
        <Alert variant="info" data-testid="restore-offered" data-time={offerParam}>
          <HistoryIcon aria-hidden="true" />
          <AlertDescription className="flex flex-col items-start gap-2">
            <p>{fmt(r.offered, { time: formatDateTime(offerParam), utc: isoSeconds(offered).replace("T", " ") })}</p>
            <Button asChild variant="outline" size="sm">
              <Link to="/updates">{r.back}</Link>
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}
      <div role="radiogroup" aria-label={r.title} className="grid gap-3 md:grid-cols-3">
        {KINDS.map(({ id, icon: Icon }) => (
          <button
            key={id}
            type="button"
            role="radio"
            aria-checked={kind === id}
            onClick={() => setParams(offerParam ? { kind: id, time: offerParam } : { kind: id }, { replace: true })}
            className={cn(
              "flex min-h-32 flex-col items-start gap-2 rounded-xl border bg-card p-4 text-left outline-none hover:bg-muted-background focus-visible:outline-2 focus-visible:outline-indigo-500",
              kind === id && "border-primary ring-1 ring-primary",
            )}
            data-testid={`restore-kind-${id}`}
          >
            <Icon className="size-6 text-primary" aria-hidden="true" />
            <span className="font-semibold">{r.kinds[id]}</span>
            <span className="text-xs text-soft-foreground">{r.kindHelp[id]}</span>
          </button>
        ))}
      </div>
      {points.isError ? <ProblemAlert error={points.error} /> : null}
      {!points.data ? (
        <Skeleton className="h-72" />
      ) : (
        <>
          {undoJob ? (
            <Card>
              <CardContent>
                <RestoreJob job={undoJob} result={() => null} />
              </CardContent>
            </Card>
          ) : (
            <UndoCard points={points.data} onJob={setUndoJob} />
          )}
          {kind ? (
            <Card data-testid="restore-panel" data-kind={kind}>
              <CardHeader>
                <CardTitle>
                  <h2>{r.kinds[kind]}</h2>
                </CardTitle>
                <CardDescription>{r.kindHelp[kind]}</CardDescription>
              </CardHeader>
              <CardContent>
                {kind === "pitr" ? <PitrPanel key={offered ?? "now"} points={points.data} offered={offered} /> : null}
                {kind === "files" ? <FilesPanel points={points.data} /> : null}
                {kind === "device" ? (
                  <Alert variant="info" data-testid="restore-device">
                    <AlertDescription>{r.deviceReady}</AlertDescription>
                  </Alert>
                ) : null}
              </CardContent>
            </Card>
          ) : null}
        </>
      )}
    </div>
  );
}
