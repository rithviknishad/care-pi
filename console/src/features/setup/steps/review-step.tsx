import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { storeSetup } from "@/api/queries";
import type { SetupState, SetupStepId } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { fmt, strings } from "@/lib/strings";
import { StepActions, StepIntro, type StepProps } from "../step-frame";
import { blockingSteps, needsEnvSkip, type WizardPage } from "../wizard";

function Row({
  label,
  value,
  step,
  setup,
  onEdit,
}: {
  label: string;
  value: React.ReactNode;
  step: SetupStepId;
  setup: SetupState;
  onEdit: (page: WizardPage) => void;
}) {
  const status = setup.steps.find((s) => s.id === step)?.status ?? "pending";
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-3 py-3 sm:grid-cols-[12rem_minmax(0,1fr)_auto_auto]">
      <span className="text-sm text-soft-foreground">{label}</span>
      <span className="col-span-2 row-start-2 min-w-0 truncate text-sm sm:col-span-1 sm:row-start-auto">{value}</span>
      <Badge variant={status === "done" ? "success" : status === "skipped" ? "neutral" : "warning"} size="sm">
        {strings.setup.statuses[status] ?? status}
      </Badge>
      {step !== "account" ? (
        <Button type="button" variant="link" size="sm" className="px-0" onClick={() => onEdit(step as WizardPage)}>
          {strings.common.change}
        </Button>
      ) : null}
    </li>
  );
}

export function ReviewStep({ setup, mode, onBack, onEdit }: StepProps & { onEdit: (page: WizardPage) => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const d = setup.draft;
  const r = strings.review.value;
  const finish = useMutation({
    mutationFn: async () => {
      if (needsEnvSkip(setup, mode)) {
        storeSetup(qc, await call(api.POST("/setup/steps/{step}/skip", { params: { path: { step: "env" } } })));
      }
      return call(api.POST("/setup/finish"));
    },
    onSuccess: (job) => {
      void qc.invalidateQueries({ queryKey: ["setup"] });
      navigate("/setup/progress", { state: { job } });
    },
    onError: (e) => {
      const p = asProblem(e);
      if (p?.is("job-running") && p.problem.job_id) navigate("/setup/progress");
    },
  });
  const blocking = blockingSteps(setup).filter((s) => !(s === "env" && mode === "easy"));

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        finish.mutate();
      }}
    >
      <StepIntro>{strings.review.intro}</StepIntro>
      <ul className="divide-y rounded-lg border" data-testid="review">
        <Row label={r.displayName} value={d.identity.display_name} step="identity" setup={setup} onEdit={onEdit} />
        <Row
          label={r.hostname}
          value={`https://${d.identity.hostname}.local/`}
          step="identity"
          setup={setup}
          onEdit={onEdit}
        />
        <Row label={r.tls} value={r.localCa} step="tls" setup={setup} onEdit={onEdit} />
        <Row label={r.preset} value={d.size.preset ?? "small-clinic"} step="size" setup={setup} onEdit={onEdit} />
        {mode === "advanced" ? (
          <>
            <Row
              label={r.env}
              value={fmt(r.envCount, {
                backend: Object.keys(d.env.backend ?? {}).length,
                frontend: Object.keys(d.env.frontend ?? {}).length,
              })}
              step="env"
              setup={setup}
              onEdit={onEdit}
            />
            <Row label={r.fe} value={d.env.fe.provider} step="env" setup={setup} onEdit={onEdit} />
          </>
        ) : null}
        <Row label={r.backups} value={strings.common.none} step="backups" setup={setup} onEdit={onEdit} />
        <Row
          label={r.careAdmin}
          value={d.care_admin.username ? `${d.care_admin.username} <${d.care_admin.email ?? ""}>` : "—"}
          step="care-admin"
          setup={setup}
          onEdit={onEdit}
        />
      </ul>
      {blocking.length > 0 ? (
        <Alert variant="warning">
          <AlertDescription>
            {strings.review.missing} {blocking.map((s) => strings.setup.steps[s] ?? s).join(", ")}
          </AlertDescription>
        </Alert>
      ) : (
        <p className="text-sm text-soft-foreground">{strings.review.duration}</p>
      )}
      {finish.error ? <ProblemAlert error={finish.error} /> : null}
      <StepActions
        onBack={onBack}
        busy={finish.isPending}
        disabled={blocking.length > 0}
        submitLabel={strings.review.finish}
      />
    </form>
  );
}
