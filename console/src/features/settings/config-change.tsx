import { useMutation, useQueryClient } from "@tanstack/react-query";
import { RotateCwIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { qk, useConfig } from "@/api/queries";
import type { Config, ConfigChangeResult, Job, PlanPreview } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { JobProgress } from "@/features/jobs/job-progress";
import { fmt, strings } from "@/lib/strings";

const st = strings.settings;

function Plan({ plan }: { plan: PlanPreview }) {
  if (!plan.changed) return <p className="text-sm">{st.noChange}</p>;
  return (
    <section className="flex flex-col gap-3 rounded-lg border p-4 text-sm" data-testid="plan-preview">
      <h3 className="font-semibold">{st.planTitle}</h3>
      <p className="text-xs text-soft-foreground">
        {plan.summary.solution} ({plan.summary.track}), {plan.summary.preset}, {plan.summary.hostnames.join(", ")}
      </p>
      <p className="flex flex-wrap items-center gap-2">
        <span className="text-soft-foreground">{st.impact}:</span>
        <Badge variant={plan.impact === "none" || plan.impact === "reload" ? "info" : "warning"} size="sm">
          {st.impacts[plan.impact] ?? plan.impact}
        </Badge>
      </p>
      {plan.restarts.length > 0 ? (
        <p>
          <span className="text-soft-foreground">{st.restarts}: </span>
          {plan.restarts.join(", ")}
        </p>
      ) : null}
      {plan.fe_build ? (
        <p>{fmt(st.feBuild, { id: plan.fe_build.build_id, provider: plan.fe_build.provider })}</p>
      ) : null}
      {plan.actions.length > 0 ? (
        <ul className="list-disc pl-4 text-xs text-soft-foreground">
          {plan.actions.map((a, i) => (
            <li key={`${a.kind}-${i}`}>{a.text}</li>
          ))}
        </ul>
      ) : null}
      {plan.changes.length > 0 ? (
        <details className="text-xs">
          <summary className="cursor-pointer">{st.files}</summary>
          <ul className="mt-1 pl-4">
            {plan.changes.map((c) => (
              <li key={c.path}>
                {c.kind} {c.path}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {plan.warnings.length > 0 ? (
        <div className="text-amber-700 dark:text-amber-300">
          <p className="font-semibold">{st.warnings}</p>
          <ul className="list-disc pl-4">
            {plan.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}

// The flow of a config change (docs/api/README.md section 8): edit -> PUT /config?dry_run=true with
// If-Match (plan preview) -> PUT /config?apply=true with the same If-Match -> the apply job (SSE).
// The body is the file document (view=file) with the changes of the form.
export function ConfigChange({
  edit,
  valid,
  children,
}: {
  edit: (file: Config) => Config;
  valid: boolean;
  children: ReactNode;
}) {
  const qc = useQueryClient();
  const file = useConfig("file");
  const [plan, setPlan] = useState<ConfigChangeResult | null>(null);
  const [job, setJob] = useState<Job | null>(null);

  const etag = file.data?.etag ?? "";
  const put = (query: { dry_run?: boolean; apply?: boolean }) => {
    if (!file.data) throw new Error("config not loaded");
    return call(
      api.PUT("/config", {
        params: { header: { "If-Match": etag }, query },
        body: edit(file.data.config),
      }),
    );
  };
  const preview = useMutation({ mutationFn: () => put({ dry_run: true }), onSuccess: setPlan });
  const apply = useMutation({
    mutationFn: () => put({ apply: true }),
    // The settings page remounts the form when the ETag changes. Keep the old config in the cache
    // while the job shows, so the progress stays. "Close" reads the new config.
    onSuccess: (res) => {
      setPlan(null);
      if (res.job) setJob(res.job);
      else void qc.invalidateQueries({ queryKey: ["config"] });
    },
  });
  const changed = asProblem(preview.error ?? apply.error)?.is("config-changed", "precondition-required");

  const reload = () => {
    preview.reset();
    apply.reset();
    setPlan(null);
    void qc.invalidateQueries({ queryKey: ["config"] });
  };

  if (!file.data) {
    return (
      <div className="flex flex-col gap-4" aria-busy="true">
        {file.error ? <ProblemAlert error={file.error} /> : null}
        <Skeleton className="h-16" />
        <Skeleton className="h-16" />
        <Skeleton className="h-24" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <fieldset disabled={Boolean(plan || job)} className="disabled:opacity-60">
        {children}
      </fieldset>
      {changed ? (
        <div className="flex flex-col items-start gap-2">
          <ProblemAlert error={preview.error ?? apply.error} />
          <Button type="button" variant="outline" size="sm" onClick={reload}>
            <RotateCwIcon data-icon="inline-start" />
            {st.reload}
          </Button>
        </div>
      ) : preview.error || apply.error ? (
        <ProblemAlert error={preview.error ?? apply.error} />
      ) : null}
      {plan ? <Plan plan={plan.plan} /> : null}
      {job ? (
        <JobProgress
          jobId={job.id}
          initial={job}
          title={st.applying}
          onDone={() => {
            void qc.invalidateQueries({ queryKey: qk.status });
            void qc.invalidateQueries({ queryKey: qk.network });
            void qc.invalidateQueries({ queryKey: qk.fe });
          }}
        >
          <Button
            type="button"
            variant="outline"
            className="self-start"
            onClick={() => {
              setJob(null);
              void qc.invalidateQueries({ queryKey: ["config"] });
            }}
          >
            {strings.common.close}
          </Button>
        </JobProgress>
      ) : (
        <div className="flex flex-wrap gap-2 border-t pt-4">
          {plan ? (
            <>
              <Button type="button" variant="outline" onClick={() => setPlan(null)}>
                {strings.common.back}
              </Button>
              <Button
                type="button"
                onClick={() => apply.mutate()}
                disabled={apply.isPending || !plan.plan.changed}
                data-testid="config-apply"
              >
                {apply.isPending ? <Spinner data-icon="inline-start" /> : null}
                {st.apply}
              </Button>
            </>
          ) : (
            <Button
              type="button"
              onClick={() => preview.mutate()}
              disabled={!valid || preview.isPending}
              data-testid="config-preview"
            >
              {preview.isPending ? <Spinner data-icon="inline-start" /> : null}
              {st.preview}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
