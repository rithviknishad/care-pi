import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangleIcon, ArchiveRestoreIcon, InfoIcon, SearchIcon, XCircleIcon } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Link, Navigate, useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { qk, storeSession, useSetup } from "@/api/queries";
import type { DrDestination, DrInspection, HostKey, Job, RestoreDrResult } from "@/api/types";
import { FormField, PasswordInput } from "@/components/app/form-field";
import { DataList, DataRow, PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
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
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { formatMinute } from "@/features/backups/backups-format";
import { HostKeyPanel } from "@/features/backups/destination-form";
import type { Choice } from "@/features/backups/destination-meta";
import { DestinationChoices } from "@/features/backups/destination-parts";
import { UsbDiskPicker } from "@/features/backups/usb-disks";
import { JobProgress } from "@/features/jobs/job-progress";
import { formatBytes } from "@/lib/format";
import { KEY_WORDS, checkKeyText, checksumOk } from "@/lib/recovery-key";
import { fmt, strings } from "@/lib/strings";
import { readDrHandoff, saveDrHandoff } from "./dr-handoff";
import { LINK_TONE } from "@/components/app/link-tone";
import { cn } from "@/lib/utils";

const d = strings.dr;
const t = strings.dest;

// The recovery key field: 24 words, typed or pasted (the .txt file or the QR text). The console checks the count,
// the word list, and the checksum (when the browser has Web Crypto). The agent checks them again.
function KeyField({ text, onText }: { text: string; onText: (v: string) => void }) {
  const id = useId();
  const check = checkKeyText(text);
  const [checksum, setChecksum] = useState<boolean | null>(null);
  const words = check.complete ? (check.words as string[]).join(" ") : "";
  useEffect(() => {
    let live = true;
    if (!words) return;
    void checksumOk(words.split(" ")).then((ok) => {
      if (live) setChecksum(ok);
    });
    return () => {
      live = false;
    };
  }, [words]);
  const error =
    check.unknown.length > 0
      ? fmt(d.unknown, { words: check.unknown.slice(0, 5).join(", ") })
      : check.count > KEY_WORDS
        ? fmt(d.tooMany, { count: check.count - KEY_WORDS })
        : words && checksum === false
          ? d.checksum
          : null;
  return (
    <FormField id={`${id}-key`} label={d.keyLabel} help={d.keyHelp} error={error}>
      <Textarea
        id={`${id}-key`}
        value={text}
        onChange={(e) => onText(e.target.value)}
        rows={5}
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        className="font-mono"
        aria-invalid={error ? true : undefined}
        data-testid="dr-key"
      />
      <p className="text-xs text-soft-foreground tabular-nums" data-testid="dr-key-count">
        {fmt(d.count, { count: Math.min(check.count, 99) })}
      </p>
    </FormField>
  );
}

// The destination of the old device: a care-pi backup disk or a folder, an SFTP folder, or an S3 bucket.
// It has no id and carries its secret (DrDestination).
function DrDestinationFields({ choice, onChange }: { choice: Choice; onChange: (dest: DrDestination | null) => void }) {
  const id = useId();
  const [v, setV] = useState({
    label: "",
    path: "",
    host: "",
    port: "22",
    user: "",
    sftpPath: "",
    endpoint: "",
    region: "",
    bucket: "",
    prefix: "",
    access: "",
    secret: "",
  });
  const [usbMode, setUsbMode] = useState<"disk" | "folder">("disk");
  const update = (next: typeof v, mode = usbMode) => {
    setV(next);
    if (choice === "usb") {
      if (mode === "disk") onChange(next.label ? { type: "posix", disk_label: next.label, path: null } : null);
      else onChange(/^\/\S+$/.test(next.path) ? { type: "posix", path: next.path, disk_label: null } : null);
    } else if (choice === "sftp") {
      const port = Number(next.port);
      const ok = next.host && next.user && /^\/\S*$/.test(next.sftpPath) && port >= 1 && port <= 65535;
      onChange(
        ok
          ? {
              type: "sftp",
              host: next.host,
              user: next.user,
              path: next.sftpPath,
              ...(port !== 22 ? { port } : {}),
              host_key: null,
            }
          : null,
      );
    } else {
      const ok = /^https?:\/\/\S+$/.test(next.endpoint) && next.bucket && next.access && next.secret;
      onChange(
        ok
          ? {
              type: "s3",
              endpoint: next.endpoint,
              bucket: next.bucket,
              region: next.region || null,
              prefix: next.prefix || null,
              access_key_id: next.access,
              secret_access_key: next.secret,
            }
          : null,
      );
    }
  };
  const field = (k: keyof typeof v, trim = true) => ({
    value: v[k],
    onChange: (e: React.ChangeEvent<HTMLInputElement>) =>
      update({ ...v, [k]: trim ? e.target.value.trim() : e.target.value }),
    autoCapitalize: "none",
    spellCheck: false,
  });
  if (choice === "usb") {
    return (
      <Tabs
        value={usbMode}
        onValueChange={(m) => {
          const mode = m === "folder" ? "folder" : "disk";
          setUsbMode(mode);
          update(v, mode);
        }}
      >
        <TabsList>
          <TabsTrigger value="disk">{d.disk}</TabsTrigger>
          <TabsTrigger value="folder">{d.folder}</TabsTrigger>
        </TabsList>
        <TabsContent value="disk" className="pt-3">
          <UsbDiskPicker restore takenLabels={[]} onSelect={(label) => update({ ...v, label }, "disk")} />
          {v.label ? (
            <p className="mt-2 text-sm" data-testid="dr-disk">
              {fmt(d.diskSelected, { label: v.label })}
            </p>
          ) : null}
        </TabsContent>
        <TabsContent value="folder" className="pt-3">
          <FormField id={`${id}-path`} label={t.usb.folderPath}>
            <Input id={`${id}-path`} {...field("path")} placeholder="/mnt/old-backup" className="font-mono" />
          </FormField>
        </TabsContent>
      </Tabs>
    );
  }
  if (choice === "sftp") {
    return (
      <div className="grid gap-x-4 sm:grid-cols-[minmax(0,1fr)_7rem]">
        <FormField id={`${id}-host`} label={t.sftp.host} help={t.sftp.hostHelp}>
          <Input id={`${id}-host`} {...field("host")} />
        </FormField>
        <FormField id={`${id}-port`} label={t.sftp.port}>
          <Input id={`${id}-port`} {...field("port")} inputMode="numeric" />
        </FormField>
        <FormField id={`${id}-user`} label={t.sftp.user}>
          <Input id={`${id}-user`} {...field("user")} autoComplete="off" />
        </FormField>
        <FormField id={`${id}-spath`} label={t.sftp.path} className="sm:col-span-2">
          <Input id={`${id}-spath`} {...field("sftpPath")} className="font-mono" />
        </FormField>
      </div>
    );
  }
  return (
    <div className="grid gap-x-4 sm:grid-cols-2">
      <FormField id={`${id}-endpoint`} label={t.s3.endpoint} help={t.s3.endpointHelp} className="sm:col-span-2">
        <Input id={`${id}-endpoint`} {...field("endpoint")} placeholder="https://" />
      </FormField>
      <FormField id={`${id}-region`} label={t.s3.region}>
        <Input id={`${id}-region`} {...field("region")} />
      </FormField>
      <FormField id={`${id}-bucket`} label={t.s3.bucket}>
        <Input id={`${id}-bucket`} {...field("bucket")} />
      </FormField>
      <FormField id={`${id}-prefix`} label={t.s3.prefix} className="sm:col-span-2">
        <Input id={`${id}-prefix`} {...field("prefix")} />
      </FormField>
      <FormField id={`${id}-access`} label={t.s3.accessKey}>
        <Input id={`${id}-access`} {...field("access")} autoComplete="off" />
      </FormField>
      <FormField id={`${id}-secret`} label={t.s3.secretKey}>
        <PasswordInput
          id={`${id}-secret`}
          value={v.secret}
          onChange={(e) => update({ ...v, secret: e.target.value })}
          autoComplete="off"
        />
      </FormField>
    </div>
  );
}

function Inspection({ insp }: { insp: DrInspection }) {
  return (
    <section className="flex flex-col gap-3 rounded-lg border p-4" data-testid="dr-inspection">
      <h3 className="font-semibold">{d.found}</h3>
      <DataList>
        <DataRow label={d.clinic} value={insp.source.display_name} testId="dr-clinic" />
        <DataRow label={d.hostname} value={`${insp.source.hostname}.local`} />
        <DataRow label={d.version} value={insp.source.agent_version ?? "—"} />
        <DataRow label={d.lastDb} value={formatMinute(insp.newest_db_snapshot?.time)} testId="dr-last-db" />
        <DataRow label={d.lastFiles} value={formatMinute(insp.newest_files_snapshot?.time)} />
        <DataRow
          label={d.window}
          value={insp.pitr ? `${formatMinute(insp.pitr.from)} – ${formatMinute(insp.pitr.to)}` : "—"}
        />
        <DataRow
          label={d.sizes}
          value={fmt(d.sizesValue, {
            db: formatBytes(insp.sizes.database_bytes),
            files: formatBytes(insp.sizes.files_bytes),
          })}
        />
        <DataRow
          label={d.space}
          value={fmt(d.spaceValue, {
            free: formatBytes(insp.space.free_bytes),
            required: formatBytes(insp.space.required_bytes),
          })}
        />
      </DataList>
      {!insp.space.ok ? (
        <Alert variant="destructive">
          <XCircleIcon aria-hidden="true" />
          <AlertDescription>{d.spaceLow}</AlertDescription>
        </Alert>
      ) : null}
      {insp.warnings.map((w) => (
        <Alert key={w.code} variant="warning">
          <AlertTriangleIcon aria-hidden="true" />
          <AlertDescription>
            {d.warnings[w.code] ? fmt(d.warnings[w.code] ?? "", { message: w.message }) : w.message}
          </AlertDescription>
        </Alert>
      ))}
    </section>
  );
}

function DrJob({
  jobId,
  initial,
  name,
  onFailed,
}: {
  jobId: string;
  initial: Job | null;
  name: string;
  onFailed: (job: Job) => void;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return (
    <JobProgress
      jobId={jobId}
      initial={initial}
      title={d.progressTitle}
      intro={d.progressIntro}
      onDone={(job) => {
        if (job.state === "succeeded") {
          const result = job.result as RestoreDrResult | null;
          saveDrHandoff({
            name: name || readDrHandoff()?.name || "",
            consoleUrl: result?.kind === "restore-dr" ? result.console_url : null,
          });
          // The job ended each session: the console account of the old device is back.
          qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
          storeSession(qc, null);
          navigate("/login", { replace: true });
        } else {
          void qc.invalidateQueries({ queryKey: qk.setup });
          onFailed(job);
        }
      }}
    />
  );
}

// The branch "Restore from backup" of the setup wizard (docs/api/README.md section 13.9.3): the recovery key and
// one destination -> POST /restore/dr/inspect -> what the backup has -> POST /restore/dr -> the job -> the login page.
export default function RestoreDrPage() {
  const setup = useSetup();
  const [keyText, setKeyText] = useState("");
  const [choice, setChoice] = useState<Choice | null>(null);
  const [dest, setDest] = useState<DrDestination | null>(null);
  const [hostKey, setHostKey] = useState<HostKey | null>(null);
  const [pause, setPause] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [failed, setFailed] = useState<Job | null>(null);
  const check = checkKeyText(keyText);
  const words = check.complete ? (check.words as string[]) : null;
  const withKey = (x: DrDestination): DrDestination =>
    x.type === "sftp" && hostKey ? { ...x, host_key: hostKey.public_key } : x;

  const inspect = useMutation({
    mutationFn: (accepted: HostKey | null) => {
      if (!words || !dest) throw new Error("incomplete");
      const destination = dest.type === "sftp" && accepted ? { ...dest, host_key: accepted.public_key } : withKey(dest);
      return call(api.POST("/restore/dr/inspect", { body: { recovery_key: { words }, destination } }));
    },
  });
  const start = useMutation({
    mutationFn: () => {
      if (!words || !dest) throw new Error("incomplete");
      return call(
        api.POST("/restore/dr", {
          body: {
            recovery_key: { words },
            destination: withKey(dest),
            time: null,
            snapshot_id: null,
            files: true,
            pause_backups: pause,
          },
        }),
      );
    },
    onSuccess: (j) => {
      setFailed(null);
      setJob(j);
    },
  });
  const lastFailedId = setup.data?.restore.job_state === "failed" ? setup.data.restore.job_id : null;
  const lastFailed = useQuery({
    queryKey: ["job", lastFailedId ?? ""],
    queryFn: () => call(api.GET("/jobs/{job_id}", { params: { path: { job_id: lastFailedId ?? "" } } })),
    enabled: Boolean(lastFailedId) && !failed && !job,
  });

  if (setup.isPending) return <Skeleton className="h-96" />;
  if (setup.isError) return <ProblemAlert error={setup.error} />;
  const s = setup.data;
  const running = s.state === "applying" && s.restore.job_id && s.job_id === s.restore.job_id ? s.restore.job_id : null;
  if (s.state === "applying" && !running && !job) return <Navigate to="/setup/progress" replace />;
  if (s.state === "ready" && !job) return <Navigate to="/dashboard" replace />;

  const insp = inspect.data ?? null;
  const untrusted = asProblem(inspect.error);
  const presented = untrusted?.is("destination-untrusted-hostkey") ? (untrusted.problem.host_key ?? null) : null;
  const name = insp?.source.display_name ?? "";
  const reset = () => {
    inspect.reset();
    setHostKey(null);
  };

  if (job || running) {
    return (
      <div className="mx-auto max-w-3xl">
        <PageHeader title={d.title} />
        <Card>
          <CardContent className="flex flex-col gap-4">
            <DrJob
              jobId={job?.id ?? running ?? ""}
              initial={job}
              name={name}
              onFailed={(j) => {
                setJob(null);
                setFailed(j);
              }}
            />
          </CardContent>
        </Card>
      </div>
    );
  }

  const failure = failed ?? lastFailed.data ?? null;
  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title={d.title} description={d.intro} />
      <Card>
        <CardContent className="flex flex-col gap-5">
          {failure ? (
            <Alert variant="destructive" data-testid="dr-failed">
              <XCircleIcon aria-hidden="true" />
              <AlertTitle>{d.failed}</AlertTitle>
              <AlertDescription className="flex flex-col gap-1">
                <p>{d.failedHelp}</p>
                {failure.error ? <p className="break-words">{failure.error.detail}</p> : null}
              </AlertDescription>
            </Alert>
          ) : null}
          <Alert variant="warning">
            <AlertTriangleIcon aria-hidden="true" />
            <AlertDescription>{d.oldOff}</AlertDescription>
          </Alert>
          <KeyField
            text={keyText}
            onText={(v) => {
              setKeyText(v);
              reset();
            }}
          />
          <section className="flex flex-col gap-3">
            <h3 className="font-semibold">{d.where}</h3>
            <DestinationChoices
              value={choice}
              onChange={(c) => {
                setChoice(c);
                setDest(null);
                reset();
              }}
            />
            {choice ? (
              <DrDestinationFields
                key={choice}
                choice={choice}
                onChange={(x) => {
                  setDest(x);
                  reset();
                }}
              />
            ) : null}
          </section>
          {presented ? (
            <HostKeyPanel
              hostKey={presented}
              changed={hostKey !== null}
              busy={inspect.isPending}
              onTrust={() => {
                setHostKey(presented);
                inspect.mutate(presented);
              }}
            />
          ) : null}
          {inspect.error && !presented ? <ProblemAlert error={inspect.error} /> : null}
          <p className="min-h-5 text-xs text-soft-foreground" aria-live="polite">
            {inspect.isPending ? d.inspecting : ""}
          </p>
          {!insp ? (
            <div>
              <Button
                type="button"
                onClick={() => inspect.mutate(hostKey)}
                disabled={!words || !dest || inspect.isPending}
                data-testid="dr-inspect"
              >
                {inspect.isPending ? <Spinner data-icon="inline-start" /> : <SearchIcon data-icon="inline-start" />}
                {d.inspect}
              </Button>
            </div>
          ) : (
            <>
              <Inspection insp={insp} />
              <Alert variant="info">
                <InfoIcon aria-hidden="true" />
                <AlertDescription data-testid="dr-account">{d.account}</AlertDescription>
              </Alert>
              <label className="flex items-start gap-3 text-sm">
                <Checkbox checked={pause} onCheckedChange={(v) => setPause(v === true)} />
                <span>{d.pause}</span>
              </label>
              {start.error ? <ProblemAlert error={start.error} /> : null}
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  onClick={() => setConfirm(true)}
                  disabled={start.isPending || !insp.space.ok}
                  data-testid="dr-start"
                >
                  {start.isPending ? (
                    <Spinner data-icon="inline-start" />
                  ) : (
                    <ArchiveRestoreIcon data-icon="inline-start" />
                  )}
                  {d.start}
                </Button>
              </div>
            </>
          )}
          <div className="border-t pt-4">
            <Button asChild variant="link" className={cn("px-0", LINK_TONE)}>
              <Link to="/setup">{d.backToSetup}</Link>
            </Button>
          </div>
        </CardContent>
      </Card>
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{d.confirmTitle}</AlertDialogTitle>
            <AlertDialogDescription>{fmt(d.confirmText, { name })}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
            <Button
              type="button"
              onClick={() => {
                setConfirm(false);
                saveDrHandoff({ name, consoleUrl: null });
                start.mutate();
              }}
              data-testid="dr-confirm"
            >
              {d.start}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
