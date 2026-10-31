import { useQueryClient } from "@tanstack/react-query";
import { CloudIcon, SettingsIcon } from "lucide-react";
import { useCallback, useState } from "react";
import { asProblem } from "@/api/problem";
import { qk } from "@/api/queries";
import type { Plug } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { ShortId } from "@/components/app/short-id";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { JobProgress } from "@/features/jobs/job-progress";
import { formatBytes } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { PlanDialog } from "./plan-dialog";
import { PlugSettingsForm } from "./plug-settings-form";
import { bundleBadge, isAutomatic, kindBadge, plugConfigBadge, sourceBadge } from "./plug-text";
import { usePlugChange } from "./use-plug-change";

const p = strings.plugs;

function RefText({ refName, tracked }: { refName: string | null; tracked: boolean }) {
  if (!refName) return <>—</>;
  return (
    <span className="flex min-w-0 items-center gap-2">
      <code className="truncate font-mono text-xs">{refName}</code>
      <Badge size="xs" variant="neutral">
        {tracked ? p.tracked : p.pinned}
      </Badge>
    </span>
  );
}

function Parts({ plug }: { plug: Plug }) {
  const be = plug.backend;
  const fe = plug.frontend;
  return (
    <div className="grid gap-x-6 gap-y-2 md:grid-cols-2">
      {be ? (
        <section aria-label={p.backend} data-testid="plug-backend">
          <h3 className="mb-1 text-xs font-semibold text-soft-foreground uppercase">{p.backend}</h3>
          <DataList>
            <DataRow label={p.package} value={be.package} title={be.package} />
            <DataRow label={p.ref} value={<RefText refName={be.ref} tracked={be.tracked} />} />
            <DataRow label={p.commit} value={<ShortId value={be.sha} label={`${plug.label} ${p.commit}`} />} />
            <DataRow label={p.inImage} value={be.installed ? p.inImageYes : p.inImageNo} />
          </DataList>
        </section>
      ) : null}
      {fe ? (
        <section aria-label={p.frontend} data-testid="plug-frontend">
          <h3 className="mb-1 text-xs font-semibold text-soft-foreground uppercase">{p.frontend}</h3>
          <DataList>
            <DataRow label={p.ref} value={<RefText refName={fe.ref} tracked={fe.tracked} />} />
            <DataRow label={p.commit} value={<ShortId value={fe.sha} label={`${plug.label} ${p.commit}`} />} />
            <DataRow
              label={p.bundle}
              testId="plug-bundle"
              value={
                <span className="flex min-w-0 items-center gap-2">
                  <Badge size="sm" variant={bundleBadge(fe.bundle.state).variant} data-state={fe.bundle.state}>
                    {bundleBadge(fe.bundle.state).text}
                  </Badge>
                  {fe.bundle.size_bytes ? (
                    <span className="text-xs text-soft-foreground">{formatBytes(fe.bundle.size_bytes)}</span>
                  ) : null}
                </span>
              }
            />
            <DataRow
              label={p.plugConfig}
              value={
                <Badge size="sm" variant={plugConfigBadge(fe.plug_config.state).variant}>
                  {plugConfigBadge(fe.plug_config.state).text}
                </Badge>
              }
            />
          </DataList>
          {fe.source === "external" && fe.meta_url ? (
            <p className="mt-1 text-xs break-all text-amber-700 dark:text-amber-300" data-testid="plug-external">
              {fmt(p.external, { url: fe.meta_url })}
            </p>
          ) : null}
        </section>
      ) : null}
    </div>
  );
}

// One plug: the switch (with the plan preview before the change), the parts with their refs and bundle,
// the settings form, and the job of the change.
export function PlugCard({ plug, hostnameCount }: { plug: Plug; hostnameCount: number }) {
  const qc = useQueryClient();
  const flow = usePlugChange(plug);
  const [settingsOpen, setSettingsOpen] = useState(!plug.settings_complete && plug.enabled);
  const [jobDone, setJobDone] = useState(false);
  const auto = isAutomatic(plug);
  const kind = kindBadge(plug.kind);
  const source = sourceBadge(plug.source);
  // Optimistic: the switch shows the requested state while the plan loads and while the dialog is open.
  const checked = flow.request?.enabled ?? plug.enabled;
  const dryProblem = asProblem(flow.dry.error);
  // A plug that turns on without its required settings: the agent answers 422 plug-settings-invalid.
  // The form then shows the errors and "Turn on with these settings".
  const enableIntent = Boolean(dryProblem?.is("plug-settings-invalid") && flow.dry.variables?.enabled === true);
  const busy = flow.dry.isPending || flow.apply.isPending || Boolean(flow.job && !jobDone);
  const onStep = useCallback(() => void qc.invalidateQueries({ queryKey: qk.publicStatus }), [qc]);

  const toggle = (on: boolean) => {
    flow.dry.mutate({ enabled: on });
    setJobDone(false);
  };
  // Keep the form open from the first 422 on (the state of an earlier render; react.dev "storing information").
  if (enableIntent && !settingsOpen) setSettingsOpen(true);

  return (
    <Card size="sm" data-testid={`plug-${plug.id}`} data-enabled={plug.enabled} data-on={plug.on}>
      <CardHeader>
        <CardTitle>
          <h2 className="flex flex-wrap items-center gap-x-2">
            {plug.label}
            <code className="text-xs font-normal text-soft-foreground">{plug.id}</code>
          </h2>
        </CardTitle>
        {plug.description ? <CardDescription>{plug.description}</CardDescription> : null}
        <CardAction>
          {auto ? (
            <Badge variant="neutral" data-testid="plug-automatic">
              {p.sources.auto}
            </Badge>
          ) : (
            <Label className="flex items-center gap-2 font-normal">
              <span className="w-[3ch] text-right text-xs text-soft-foreground">{checked ? p.on : p.off}</span>
              <Switch
                checked={checked}
                onCheckedChange={toggle}
                disabled={busy || flow.preview !== null}
                aria-label={fmt(p.turnOn, { label: plug.label })}
                data-testid={`plug-switch-${plug.id}`}
              />
            </Label>
          )}
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap gap-1.5">
          <Badge size="sm" variant={kind.variant}>
            {kind.text}
          </Badge>
          <Badge size="sm" variant={source.variant} data-testid="plug-source">
            {source.text}
          </Badge>
          {plug.pending ? (
            <Badge size="sm" variant="warning" data-testid="plug-pending">
              {p.pending}
            </Badge>
          ) : null}
          {plug.requires_internet ? (
            <Badge size="sm" variant="neutral">
              <CloudIcon data-icon="inline-start" />
              {p.internet}
            </Badge>
          ) : null}
        </div>
        {auto ? (
          <Alert variant="info" data-testid="plug-auto-reason">
            <AlertDescription>
              {fmt(p.auto, { count: hostnameCount })} {p.autoReadonly}
            </AlertDescription>
          </Alert>
        ) : null}
        <Parts plug={plug} />
        {plug.enabled && plug.setup_steps.length > 0 ? (
          <div className="text-sm">
            <p className="font-semibold">{p.setupSteps}</p>
            <ul className="list-disc pl-5 text-soft-foreground">
              {plug.setup_steps.map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ul>
          </div>
        ) : null}
        {plug.settings.length > 0 ? (
          <Collapsible open={settingsOpen} onOpenChange={setSettingsOpen} className="rounded-lg border">
            <CollapsibleTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="w-full justify-start"
                data-testid={`plug-settings-toggle-${plug.id}`}
              >
                <SettingsIcon data-icon="inline-start" />
                {fmt(p.settingsCount, { count: plug.settings.length })}
              </Button>
            </CollapsibleTrigger>
            <CollapsibleContent className="border-t px-3 pt-3 pb-3">
              {enableIntent ? (
                <Alert variant="warning" className="mb-3" data-testid="plug-needs-settings">
                  <AlertDescription>{p.enableNeedsSettings}</AlertDescription>
                </Alert>
              ) : null}
              <PlugSettingsForm
                key={JSON.stringify(plug.settings.map((s) => [s.key, s.value, s.secret_set]))}
                plug={plug}
                plugOn={enableIntent || plug.enabled}
                enable={enableIntent}
                problem={dryProblem}
                pending={flow.dry.isPending}
                disabled={busy || flow.preview !== null}
                onPreview={(settings) =>
                  flow.dry.mutate({ ...(settings ? { settings } : {}), ...(enableIntent ? { enabled: true } : {}) })
                }
              />
            </CollapsibleContent>
          </Collapsible>
        ) : null}
        {flow.dry.error && !enableIntent ? <ProblemAlert error={flow.dry.error} /> : null}
        {flow.job ? (
          <div className="border-t pt-3" data-testid="plug-job">
            <JobProgress
              jobId={flow.job.id}
              initial={flow.job}
              title={fmt(p.jobTitle, { label: plug.label })}
              onStepChange={onStep}
              onDone={() => {
                setJobDone(true);
                void qc.invalidateQueries({ queryKey: qk.plugs });
                void qc.invalidateQueries({ queryKey: qk.updates });
                void qc.invalidateQueries({ queryKey: qk.status });
                void qc.invalidateQueries({ queryKey: qk.publicStatus });
              }}
            >
              {jobDone ? (
                <Button type="button" variant="outline" className="self-start" onClick={() => flow.setJob(null)}>
                  {strings.common.close}
                </Button>
              ) : null}
            </JobProgress>
          </div>
        ) : null}
      </CardContent>
      <PlanDialog flow={flow} label={plug.label} />
    </Card>
  );
}
