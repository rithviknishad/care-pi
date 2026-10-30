import { useMutation, useQueryClient } from "@tanstack/react-query";
import { HammerIcon, PowerIcon, RotateCcwIcon } from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { api, call } from "@/api/client";
import { qk, useConfig, useConfigSchema, useFe, usePresets } from "@/api/queries";
import type { Config, Job, PowerAction, SizeStep } from "@/api/types";
import { DataList, DataRow, PageHeader } from "@/components/app/page";
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
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { JobProgress } from "@/features/jobs/job-progress";
import { RESERVED_BACKEND, RESERVED_FRONTEND } from "@/features/setup/env-defaults";
import { EnvForm } from "@/features/setup/env-form";
import { modelFromStep, stepFromModel, validateEnv, type EnvModel } from "@/features/setup/env-logic";
import { IdentityForm } from "@/features/setup/identity-form";
import { validateIdentity, type IdentityValue } from "@/features/setup/identity-logic";
import { SizeForm } from "@/features/setup/size-form";
import { validateSize } from "@/features/setup/size-logic";
import { formatBytes, formatDateTime, formatTime } from "@/lib/format";
import { reservedNames } from "@/lib/schema";
import { fmt, strings } from "@/lib/strings";
import { ConfigChange } from "./config-change";

const st = strings.settings;
const TABS = ["identity", "size", "env", "fe", "power"] as const;
type Tab = (typeof TABS)[number];

function WithEffective({ render }: { render: (config: Config, etag: string) => React.ReactNode }) {
  const effective = useConfig("effective");
  if (effective.isError) return <ProblemAlert error={effective.error} />;
  if (!effective.data) return <Skeleton className="h-64" />;
  return <>{render(effective.data.config, effective.data.etag)}</>;
}

function IdentityTab({ config }: { config: Config }) {
  const [value, setValue] = useState<IdentityValue>({
    display_name: config.identity?.display_name ?? "",
    hostname: config.identity?.hostname ?? "care",
  });
  const errors = validateIdentity(value);
  const hostChanged = value.hostname !== (config.identity?.hostname ?? "care");
  return (
    <ConfigChange
      edit={(file) => ({ ...file, identity: { ...file.identity, ...value } })}
      valid={Object.keys(errors).length === 0}
    >
      <IdentityForm value={value} onChange={setValue} editableHostname errors={errors} />
      {hostChanged ? (
        <Alert variant="warning" className="mt-4">
          <AlertDescription>{st.hostnameChange}</AlertDescription>
        </Alert>
      ) : null}
    </ConfigChange>
  );
}

function SizeTab({ config }: { config: Config }) {
  const presets = usePresets();
  const schema = useConfigSchema();
  const [value, setValue] = useState<SizeStep>(config.size ?? { preset: "small-clinic" });
  const errors = validateSize(value, schema.data);
  return (
    <ConfigChange edit={(file) => ({ ...file, size: value })} valid={Object.keys(errors).length === 0}>
      <SizeForm
        mode="advanced"
        step={value}
        onChange={setValue}
        presets={presets.data}
        schema={schema.data}
        errors={errors}
      />
    </ConfigChange>
  );
}

function EnvTab({ config }: { config: Config }) {
  const schema = useConfigSchema();
  const [model, setModel] = useState<EnvModel>(() =>
    modelFromStep({
      backend: config.env?.backend ?? {},
      frontend: config.env?.frontend ?? {},
      fe: config.fe ?? { provider: "local" },
    }),
  );
  const reserved = {
    backend: reservedNames(schema.data, "backend", RESERVED_BACKEND),
    frontend: reservedNames(schema.data, "frontend", RESERVED_FRONTEND),
  };
  const errors = validateEnv(model, reserved);
  return (
    <ConfigChange
      edit={(file) => {
        const step = stepFromModel(model);
        return { ...file, env: { backend: step.backend, frontend: step.frontend }, fe: step.fe };
      }}
      valid={Object.keys(errors).length === 0}
    >
      <EnvForm model={model} onChange={setModel} errors={errors} />
    </ConfigChange>
  );
}

function FeTab() {
  const qc = useQueryClient();
  const fe = useFe();
  const [job, setJob] = useState<Job | null>(null);
  const build = useMutation({
    mutationFn: () => call(api.POST("/fe/build", { body: { force: true } })),
    onSuccess: setJob,
  });
  const f = fe.data;
  return (
    <div className="flex flex-col gap-4">
      {fe.isError ? <ProblemAlert error={fe.error} /> : null}
      <DataList>
        <DataRow label={st.provider} loading={!f} value={f?.provider} />
        <DataRow label={st.current} loading={!f} value={f?.current ?? "—"} />
        <DataRow label={strings.dashboard.careFeSha} loading={!f} value={f?.care_fe_sha?.slice(0, 12) ?? "—"} />
        <DataRow
          label={st.lastBuild}
          loading={!f}
          value={
            f?.last_build
              ? `${f.last_build.provider} ${formatDateTime(f.last_build.built_at)} (${f.last_build.duration ?? "—"})`
              : "—"
          }
        />
        <DataRow
          label={st.builds}
          loading={!f}
          value={
            f ? f.builds.map((b) => `${b.id}${b.current ? " *" : ""} ${formatBytes(b.bytes)}`).join(", ") || "—" : ""
          }
        />
      </DataList>
      <p className="text-xs text-soft-foreground">{st.rebuildHelp}</p>
      {build.error ? <ProblemAlert error={build.error} /> : null}
      {job ? (
        <JobProgress jobId={job.id} initial={job} onDone={() => void qc.invalidateQueries({ queryKey: qk.fe })}>
          <Button type="button" variant="outline" className="self-start" onClick={() => setJob(null)}>
            {strings.common.close}
          </Button>
        </JobProgress>
      ) : (
        <Button
          type="button"
          className="self-start"
          onClick={() => build.mutate()}
          disabled={build.isPending || f?.building}
        >
          {build.isPending ? <Spinner data-icon="inline-start" /> : <HammerIcon data-icon="inline-start" />}
          {st.rebuild}
        </Button>
      )}
    </div>
  );
}

function PowerButton({
  action,
  label,
  help,
  confirm,
  onAccepted,
}: {
  action: "reboot" | "shutdown";
  label: string;
  help: string;
  confirm: string;
  onAccepted: (a: PowerAction) => void;
}) {
  const { withReauth } = useReauth();
  const [open, setOpen] = useState(false);
  const run = useMutation({
    mutationFn: () =>
      action === "reboot"
        ? call(api.POST("/power/reboot", { body: { delay_s: 5 } }))
        : withReauth(() => call(api.POST("/power/shutdown", { body: { delay_s: 5 } }))),
    onSuccess: onAccepted,
  });
  return (
    <div className="flex flex-col gap-2 rounded-lg border p-4">
      <p className="text-sm">{help}</p>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <Button
          type="button"
          variant={action === "shutdown" ? "destructive" : "outline"}
          className="self-start"
          onClick={() => setOpen(true)}
        >
          {action === "shutdown" ? <PowerIcon data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}
          {label}
        </Button>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm}</AlertDialogTitle>
            <AlertDialogDescription>{help}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
            <AlertDialogAction
              variant={action === "shutdown" ? "destructive-solid" : "default"}
              onClick={() => {
                setOpen(false);
                run.mutate();
              }}
            >
              {label}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {run.error && !isReauthCancelled(run.error) ? <ProblemAlert error={run.error} /> : null}
    </div>
  );
}

function PowerTab() {
  const [accepted, setAccepted] = useState<PowerAction | null>(null);
  return (
    <div className="flex flex-col gap-4">
      {accepted ? (
        <Alert variant="info">
          <AlertDescription>
            {fmt(st.powerAccepted, { action: accepted.action, time: formatTime(accepted.at) })}
          </AlertDescription>
        </Alert>
      ) : null}
      <PowerButton
        action="reboot"
        label={st.reboot}
        help={st.rebootHelp}
        confirm={st.rebootConfirm}
        onAccepted={setAccepted}
      />
      <PowerButton
        action="shutdown"
        label={st.shutdown}
        help={st.shutdownHelp}
        confirm={st.shutdownConfirm}
        onAccepted={setAccepted}
      />
    </div>
  );
}

const TAB_LABELS: Record<Tab, string> = {
  identity: st.identity,
  size: st.size,
  env: st.env,
  fe: st.fe,
  power: st.power,
};

export default function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const raw = params.get("tab");
  const tab: Tab = (TABS as readonly string[]).includes(raw ?? "") ? (raw as Tab) : "identity";
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={st.title} />
      <Tabs value={tab} onValueChange={(v) => setParams({ tab: String(v) }, { replace: true })}>
        <div className="-mx-1 overflow-x-auto px-1 pb-1">
          <TabsList aria-label={st.title}>
            {TABS.map((t) => (
              <TabsTrigger key={t} value={t} className="px-3">
                {TAB_LABELS[t]}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        <Card>
          <CardContent>
            <TabsContent value="identity">
              {tab === "identity" ? (
                <WithEffective render={(c, etag) => <IdentityTab key={etag} config={c} />} />
              ) : null}
            </TabsContent>
            <TabsContent value="size">
              {tab === "size" ? <WithEffective render={(c, etag) => <SizeTab key={etag} config={c} />} /> : null}
            </TabsContent>
            <TabsContent value="env">
              {tab === "env" ? <WithEffective render={(c, etag) => <EnvTab key={etag} config={c} />} /> : null}
            </TabsContent>
            <TabsContent value="fe">{tab === "fe" ? <FeTab /> : null}</TabsContent>
            <TabsContent value="power">{tab === "power" ? <PowerTab /> : null}</TabsContent>
          </CardContent>
        </Card>
      </Tabs>
    </div>
  );
}
