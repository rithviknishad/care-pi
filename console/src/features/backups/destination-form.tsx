import { useMutation } from "@tanstack/react-query";
import { PlugZapIcon, ShieldCheckIcon } from "lucide-react";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { useSshKey } from "@/api/queries";
import type { Destination, DestinationCredentials, DestinationTestResult, HostKey } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { FormField, PasswordInput } from "@/components/app/form-field";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { fmt, strings } from "@/lib/strings";
import { DESTINATION_ID, destinationId } from "./backups-format";
import type { Choice } from "./destination-meta";
import { DestinationSchedule, type ScheduleValue } from "./destination-parts";
import { TestResultView } from "./test-result";
import { UsbDiskPicker } from "./usb-disks";

const t = strings.dest;
type Fields = Omit<Destination, "id" | "type" | "interval_min" | "retention">;
type Ready = { fields: Fields; credentials: DestinationCredentials | null } | null;

function useTest() {
  return useMutation({
    mutationFn: (body: {
      destination: Destination;
      credentials: DestinationCredentials | null;
      accept_fingerprint?: string | null;
      create_bucket?: boolean;
    }) =>
      call(api.POST("/backups/destinations/test", { body: { ...body, create_bucket: body.create_bucket ?? false } })),
  });
}

function TestButton({
  onClick,
  busy,
  disabled,
  again,
}: {
  onClick: () => void;
  busy: boolean;
  disabled?: boolean;
  again: boolean;
}) {
  return (
    <Button type="button" variant="outline" onClick={onClick} disabled={busy || disabled} data-testid="dest-test">
      {busy ? <Spinner data-icon="inline-start" /> : <PlugZapIcon data-icon="inline-start" />}
      {again ? t.testAgain : t.test}
    </Button>
  );
}

function Testing({ busy }: { busy: boolean }) {
  return (
    <p className="min-h-5 text-xs text-soft-foreground" aria-live="polite">
      {busy ? t.testing : ""}
    </p>
  );
}

// USB disk or folder (type posix).
function PosixFields({ id, takenLabels, onReady }: { id: string; takenLabels: string[]; onReady: (r: Ready) => void }) {
  const fid = useId();
  const test = useTest();
  const [mode, setMode] = useState<"disk" | "folder">("disk");
  const [path, setPath] = useState("");
  const [result, setResult] = useState<DestinationTestResult | null>(null);
  const run = (fields: Fields) => {
    onReady(null);
    setResult(null);
    test.mutate(
      { destination: { id, type: "posix", ...fields }, credentials: null },
      {
        onSuccess: (r) => {
          setResult(r);
          onReady(r.ok ? { fields, credentials: null } : null);
        },
      },
    );
  };
  const pathOk = /^\/\S*$/.test(path) && path.length > 1;
  return (
    <div className="flex flex-col gap-3">
      <Tabs
        value={mode}
        onValueChange={(v) => {
          setMode(v === "folder" ? "folder" : "disk");
          setResult(null);
          onReady(null);
        }}
      >
        <TabsList>
          <TabsTrigger value="disk">{strings.backups.choices.usb}</TabsTrigger>
          <TabsTrigger value="folder">{t.usb.folder}</TabsTrigger>
        </TabsList>
        <TabsContent value="disk" className="pt-3">
          <UsbDiskPicker takenLabels={takenLabels} onSelect={(label) => run({ disk_label: label })} />
        </TabsContent>
        <TabsContent value="folder" className="pt-3">
          <div className="flex flex-col gap-2">
            <FormField id={`${fid}-path`} label={t.usb.folderPath} help={t.usb.folderHelp}>
              <Input
                id={`${fid}-path`}
                value={path}
                onChange={(e) => {
                  setPath(e.target.value.trim());
                  setResult(null);
                  onReady(null);
                }}
                placeholder="/mnt/backup"
                autoCapitalize="none"
                spellCheck={false}
                className="font-mono"
              />
            </FormField>
            <div>
              <TestButton
                onClick={() => run({ path })}
                busy={test.isPending}
                disabled={!pathOk}
                again={result !== null}
              />
            </div>
          </div>
        </TabsContent>
      </Tabs>
      <Testing busy={test.isPending} />
      {test.error ? <ProblemAlert error={test.error} /> : null}
      {result ? <TestResultView result={result} /> : null}
    </div>
  );
}

export function HostKeyPanel({
  hostKey,
  changed,
  onTrust,
  busy,
}: {
  hostKey: HostKey;
  changed: boolean;
  onTrust: () => void;
  busy: boolean;
}) {
  return (
    <Alert variant={changed ? "destructive" : "warning"} data-testid="host-key">
      <ShieldCheckIcon aria-hidden="true" />
      <AlertTitle>{t.sftp.hostKeyTitle}</AlertTitle>
      <AlertDescription className="flex flex-col gap-3">
        <p>{changed ? t.sftp.hostKeyChanged : t.sftp.hostKeyIntro}</p>
        <dl className="grid gap-1 text-foreground sm:grid-cols-[10rem_minmax(0,1fr)]">
          <dt className="text-soft-foreground">{t.sftp.keyType}</dt>
          <dd className="font-mono" data-testid="host-key-type">
            {hostKey.type}
          </dd>
          <dt className="text-soft-foreground">{t.sftp.fingerprint}</dt>
          <dd className="font-mono break-all" data-testid="host-key-fingerprint">
            {hostKey.fingerprint_sha256}
          </dd>
        </dl>
        <div>
          <Button type="button" onClick={onTrust} disabled={busy} data-testid="host-key-trust">
            {busy ? <Spinner data-icon="inline-start" /> : null}
            {t.sftp.trust}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  );
}

function InstallKeyPanel({ user, onTestAgain, busy }: { user: string; onTestAgain: () => void; busy: boolean }) {
  const key = useSshKey("active");
  const line = key.data?.authorized_keys_line ?? "";
  const command = `mkdir -p ~/.ssh && echo '${line}' >> ~/.ssh/authorized_keys`;
  return (
    <section className="flex flex-col gap-3 rounded-lg border p-4" data-testid="install-key">
      <h4 className="font-semibold">{t.sftp.installTitle}</h4>
      <p className="text-sm">{fmt(t.sftp.installIntro, { user })}</p>
      <div className="flex items-start gap-2">
        {key.data ? (
          <code
            className="min-w-0 flex-1 rounded-md border bg-soft-background p-2 text-xs break-all"
            data-testid="ssh-key-line"
          >
            {line}
          </code>
        ) : (
          <Skeleton className="h-14 flex-1" />
        )}
        <CopyButton value={line} label={t.sftp.publicKey} />
      </div>
      {key.error ? <ProblemAlert error={key.error} /> : null}
      <p className="text-xs text-soft-foreground">{t.sftp.installNas}</p>
      <p className="text-xs text-soft-foreground">{fmt(t.sftp.installVps, { user })}</p>
      <div className="flex items-start gap-2">
        <code className="min-w-0 flex-1 rounded-md border bg-soft-background p-2 text-xs break-all">{command}</code>
        <CopyButton value={command} />
      </div>
      <div>
        <Button type="button" onClick={onTestAgain} disabled={busy} data-testid="dest-test-again">
          {busy ? <Spinner data-icon="inline-start" /> : null}
          {t.testAgain}
        </Button>
      </div>
    </section>
  );
}

// A network folder (type sftp) with the host key check (TOFU, docs/api/README.md section 13.6):
// test -> type and fingerprint -> "Trust this server" -> test with accept_fingerprint -> the public key of the device
// for authorized_keys -> test again -> host_key of the destination.
function SftpFields({ id, onReady }: { id: string; onReady: (r: Ready) => void }) {
  const fid = useId();
  const test = useTest();
  const [host, setHost] = useState("");
  const [port, setPort] = useState("22");
  const [user, setUser] = useState("");
  const [path, setPath] = useState("");
  const [accepted, setAccepted] = useState<string | null>(null);
  const [presented, setPresented] = useState<{ key: HostKey; changed: boolean } | null>(null);
  const [result, setResult] = useState<DestinationTestResult | null>(null);
  const portN = Number(port);
  const fields = (): Fields => ({ host, user, path, ...(portN && portN !== 22 ? { port: portN } : {}) });
  const valid =
    /^[^\s/]+$/.test(host) && /^[^\s@]+$/.test(user) && /^\/\S*$/.test(path) && portN >= 1 && portN <= 65535;
  const reset = () => {
    setAccepted(null);
    setPresented(null);
    setResult(null);
    test.reset();
    onReady(null);
  };
  const run = (accept: string | null) => {
    onReady(null);
    const f = fields();
    test.mutate(
      { destination: { id, type: "sftp", ...f }, credentials: null, accept_fingerprint: accept },
      {
        onSuccess: (r) => {
          setResult(r);
          if (r.next_action === "accept-host-key" && r.host_key) {
            setPresented({ key: r.host_key, changed: accept !== null });
            setAccepted(null);
          } else setPresented(null);
          if (r.ok && r.host_key) onReady({ fields: { ...f, host_key: r.host_key.public_key }, credentials: null });
        },
        onError: (e) => {
          const p = asProblem(e);
          if (p?.is("destination-untrusted-hostkey") && p.problem.host_key) {
            setResult(null);
            setAccepted(null);
            setPresented({ key: p.problem.host_key, changed: true });
          }
        },
      },
    );
  };
  const untrusted = asProblem(test.error)?.is("destination-untrusted-hostkey");
  const change =
    <T,>(set: (v: T) => void) =>
    (v: T) => {
      set(v);
      reset();
    };
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-soft-foreground">{t.sftp.intro}</p>
      <div className="grid gap-x-4 sm:grid-cols-[minmax(0,1fr)_7rem]">
        <FormField id={`${fid}-host`} label={t.sftp.host} help={t.sftp.hostHelp}>
          <Input
            id={`${fid}-host`}
            value={host}
            onChange={(e) => change(setHost)(e.target.value.trim())}
            autoCapitalize="none"
            spellCheck={false}
            placeholder="nas.local"
          />
        </FormField>
        <FormField id={`${fid}-port`} label={t.sftp.port}>
          <Input
            id={`${fid}-port`}
            value={port}
            onChange={(e) => change(setPort)(e.target.value.replace(/\D/g, ""))}
            inputMode="numeric"
          />
        </FormField>
        <FormField id={`${fid}-user`} label={t.sftp.user}>
          <Input
            id={`${fid}-user`}
            value={user}
            onChange={(e) => change(setUser)(e.target.value.trim())}
            autoCapitalize="none"
            spellCheck={false}
            autoComplete="off"
          />
        </FormField>
        <FormField id={`${fid}-path`} label={t.sftp.path} help={t.sftp.pathHelp} className="sm:col-span-2">
          <Input
            id={`${fid}-path`}
            value={path}
            onChange={(e) => change(setPath)(e.target.value.trim())}
            autoCapitalize="none"
            spellCheck={false}
            placeholder="/volume1/care-backup"
            className="font-mono"
          />
        </FormField>
      </div>
      <div>
        <TestButton
          onClick={() => run(accepted)}
          busy={test.isPending}
          disabled={!valid}
          again={result !== null || presented !== null}
        />
      </div>
      <Testing busy={test.isPending} />
      {test.error && !untrusted ? <ProblemAlert error={test.error} /> : null}
      {presented ? (
        <HostKeyPanel
          hostKey={presented.key}
          changed={presented.changed}
          busy={test.isPending}
          onTrust={() => {
            setAccepted(presented.key.fingerprint_sha256);
            run(presented.key.fingerprint_sha256);
          }}
        />
      ) : null}
      {result?.next_action === "install-ssh-key" ? (
        <InstallKeyPanel user={user} busy={test.isPending} onTestAgain={() => run(accepted)} />
      ) : null}
      {result && result.next_action !== "accept-host-key" && result.next_action !== "install-ssh-key" ? (
        <TestResultView result={result} />
      ) : null}
      {result?.ok && result.host_key ? (
        <p className="text-xs break-all text-soft-foreground" data-testid="host-key-trusted">
          {fmt(t.sftp.trusted, { type: result.host_key.type, fingerprint: result.host_key.fingerprint_sha256 })}
        </p>
      ) : null}
    </div>
  );
}

// A cloud bucket (type s3). The keys go to PUT /backups/destinations/{id}/secrets (or the step backups), never to
// config.yaml. The test makes the bucket only when the operator asks for it.
function S3Fields({ id, onReady }: { id: string; onReady: (r: Ready) => void }) {
  const fid = useId();
  const test = useTest();
  const [v, setV] = useState({ endpoint: "", region: "", bucket: "", prefix: "", access: "", secret: "" });
  const [result, setResult] = useState<DestinationTestResult | null>(null);
  const set = (k: keyof typeof v) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setV({ ...v, [k]: k === "secret" ? e.target.value : e.target.value.trim() });
    setResult(null);
    test.reset();
    onReady(null);
  };
  const fields = (): Fields => ({
    endpoint: v.endpoint,
    bucket: v.bucket,
    ...(v.region ? { region: v.region } : {}),
    ...(v.prefix ? { prefix: v.prefix.replace(/^\/+|\/+$/g, "") } : {}),
  });
  const creds = { access_key_id: v.access, secret_access_key: v.secret };
  const valid =
    /^https?:\/\/\S+$/.test(v.endpoint) &&
    /^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(v.bucket) &&
    v.access !== "" &&
    v.secret !== "" &&
    (v.region === "" || /^[a-z0-9-]{1,32}$/.test(v.region));
  const run = (createBucket: boolean) => {
    onReady(null);
    const f = fields();
    test.mutate(
      { destination: { id, type: "s3", ...f }, credentials: creds, create_bucket: createBucket },
      {
        onSuccess: (r) => {
          setResult(r);
          onReady(r.ok ? { fields: f, credentials: creds } : null);
        },
      },
    );
  };
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-soft-foreground">{t.s3.intro}</p>
      <div className="grid gap-x-4 sm:grid-cols-2">
        <FormField id={`${fid}-endpoint`} label={t.s3.endpoint} help={t.s3.endpointHelp} className="sm:col-span-2">
          <Input
            id={`${fid}-endpoint`}
            value={v.endpoint}
            onChange={set("endpoint")}
            placeholder="https://"
            autoCapitalize="none"
            spellCheck={false}
          />
        </FormField>
        <FormField id={`${fid}-region`} label={t.s3.region} help={t.s3.regionHelp}>
          <Input
            id={`${fid}-region`}
            value={v.region}
            onChange={set("region")}
            autoCapitalize="none"
            spellCheck={false}
          />
        </FormField>
        <FormField id={`${fid}-bucket`} label={t.s3.bucket}>
          <Input
            id={`${fid}-bucket`}
            value={v.bucket}
            onChange={set("bucket")}
            autoCapitalize="none"
            spellCheck={false}
          />
        </FormField>
        <FormField id={`${fid}-prefix`} label={t.s3.prefix} help={t.s3.prefixHelp} className="sm:col-span-2">
          <Input
            id={`${fid}-prefix`}
            value={v.prefix}
            onChange={set("prefix")}
            autoCapitalize="none"
            spellCheck={false}
          />
        </FormField>
        <FormField id={`${fid}-access`} label={t.s3.accessKey}>
          <Input
            id={`${fid}-access`}
            value={v.access}
            onChange={set("access")}
            autoCapitalize="none"
            spellCheck={false}
            autoComplete="off"
          />
        </FormField>
        <FormField id={`${fid}-secret`} label={t.s3.secretKey} help={t.s3.keysHelp}>
          <PasswordInput id={`${fid}-secret`} value={v.secret} onChange={set("secret")} autoComplete="off" />
        </FormField>
      </div>
      <div>
        <TestButton onClick={() => run(false)} busy={test.isPending} disabled={!valid} again={result !== null} />
      </div>
      <Testing busy={test.isPending} />
      {test.error ? <ProblemAlert error={test.error} /> : null}
      {result?.next_action === "create-bucket" ? (
        <Alert variant="warning" data-testid="create-bucket">
          <AlertDescription className="flex flex-col items-start gap-2">
            <p>{fmt(t.s3.noBucket, { bucket: v.bucket })}</p>
            <Button
              type="button"
              onClick={() => run(true)}
              disabled={test.isPending}
              data-testid="create-bucket-button"
            >
              {t.s3.createBucket}
            </Button>
          </AlertDescription>
        </Alert>
      ) : result ? (
        <TestResultView result={result} />
      ) : null}
    </div>
  );
}

const DEFAULT_NAMES: Record<Choice, string> = { usb: "usb", sftp: "nas", s3: "cloud" };

// The form of one new destination: the fields of the type, the test, the interval and the retention, and the name.
// "Add this destination" needs a passed test. The caller writes the destination (the step backups, or PATCH /config).
export function DestinationForm({
  choice,
  existingIds,
  takenLabels,
  walTimeoutMin,
  onAdd,
  onCancel,
  busy = false,
  addLabel = t.add,
}: {
  choice: Choice;
  existingIds: string[];
  takenLabels: string[];
  walTimeoutMin: number | undefined;
  onAdd: (destination: Destination, credentials: DestinationCredentials | null) => void;
  onCancel: (() => void) | null;
  busy?: boolean;
  addLabel?: string;
}) {
  const fid = useId();
  const [id, setId] = useState(() => destinationId(DEFAULT_NAMES[choice], existingIds));
  const [schedule, setSchedule] = useState<ScheduleValue>({});
  const [ready, setReady] = useState<Ready>(null);
  const idError = !DESTINATION_ID.test(id) ? t.nameInvalid : existingIds.includes(id) ? t.nameUsed : null;
  const type = choice === "usb" ? "posix" : choice;
  const add = () => {
    if (!ready || idError) return;
    const destination = {
      id,
      type,
      ...ready.fields,
      ...(schedule.interval_min ? { interval_min: schedule.interval_min } : {}),
      ...(schedule.retention ? { retention: schedule.retention } : {}),
    } as Destination;
    onAdd(destination, ready.credentials);
  };
  return (
    <section className="flex flex-col gap-4 rounded-lg border p-4" data-testid="dest-form" data-choice={choice}>
      <FormField id={`${fid}-id`} label={t.name} help={t.nameHelp} error={idError}>
        <Input
          id={`${fid}-id`}
          value={id}
          onChange={(e) => setId(e.target.value.toLowerCase().trim())}
          autoCapitalize="none"
          spellCheck={false}
          className="max-w-xs font-mono"
          aria-invalid={idError ? true : undefined}
        />
      </FormField>
      {choice === "usb" ? <PosixFields id={id} takenLabels={takenLabels} onReady={setReady} /> : null}
      {choice === "sftp" ? <SftpFields id={id} onReady={setReady} /> : null}
      {choice === "s3" ? <S3Fields id={id} onReady={setReady} /> : null}
      <DestinationSchedule value={schedule} onChange={setSchedule} walTimeoutMin={walTimeoutMin} />
      <div className="flex flex-wrap gap-2 border-t pt-4">
        {onCancel ? (
          <Button type="button" variant="outline" onClick={onCancel}>
            {t.cancel}
          </Button>
        ) : null}
        <Button type="button" onClick={add} disabled={!ready || Boolean(idError) || busy} data-testid="dest-add">
          {busy ? <Spinner data-icon="inline-start" /> : null}
          {addLabel}
        </Button>
      </div>
    </section>
  );
}
