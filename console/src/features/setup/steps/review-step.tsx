import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PencilIcon } from "lucide-react";
import type { ReactNode } from "react";
import { useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { storeSetup } from "@/api/queries";
import type { SetupState, SetupStepId } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { ProblemCard } from "@/components/v2/problem-card";
import { StatusChip } from "@/components/v2/status-chip";
import { retentionSummary } from "@/components/v2/retention";
import { effectiveRetention } from "@/features/backups/backups-format";
import { fmt, strings } from "@/lib/strings";
import { StepActions, type StepProps } from "../step-frame";
import { blockingSteps, loadOnline, needsEnvSkip, type WizardPage } from "../wizard";

const r = strings.review.value;

function Row({
  label,
  value,
  mono = false,
  step,
  setup,
  onEdit,
}: {
  label: string;
  value: ReactNode;
  mono?: boolean;
  step: SetupStepId;
  setup: SetupState;
  onEdit: (page: WizardPage) => void;
}) {
  const status = setup.steps.find((s) => s.id === step)?.status ?? "pending";
  const done = status === "done" || status === "skipped";
  return (
    <li className="grid min-h-16 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-0.5 border-b border-line-soft py-3 last:border-b-0 sm:grid-cols-[11rem_minmax(0,1fr)_auto]">
      <span className="type-small text-ink-soft">{label}</span>
      <span
        className={`col-span-2 row-start-2 min-w-0 break-words sm:col-span-1 sm:row-start-auto ${mono ? "type-data text-ink" : "type-body font-medium text-ink"}`}
      >
        {value}
      </span>
      <span className="col-start-2 row-start-1 flex items-center gap-1 sm:col-start-3">
        {done ? null : (
          <StatusChip tone="warn" size="sm">
            {strings.setup.statuses[status] ?? status}
          </StatusChip>
        )}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="no-underline"
          onClick={() => onEdit(step as WizardPage)}
          aria-label={`${strings.common.change}: ${label}`}
        >
          <PencilIcon data-icon="inline-start" />
          {strings.common.change}
        </Button>
      </span>
    </li>
  );
}

// The review (S66): a calm summary of each choice, with "Change" on each row. Nothing changes on the device before
// "Install Care".
export function ReviewStep({ setup, mode, onBack, onEdit, next }: StepProps & { onEdit: (page: WizardPage) => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const d = setup.draft;
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
  const preset = d.size.preset ?? "small-clinic";
  const destinations = d.backups?.destinations ?? [];
  const first = destinations[0];

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        finish.mutate();
      }}
    >
      <ul className="flex flex-col rounded-card border border-line-soft bg-surface-tint px-4" data-testid="review">
        <Row label={r.displayName} value={d.identity.display_name} step="identity" setup={setup} onEdit={onEdit} />
        <Row
          label={r.hostname}
          value={`https://${d.identity.hostname}.local/`}
          mono
          step="identity"
          setup={setup}
          onEdit={onEdit}
        />
        <Row
          label={r.tls}
          value={loadOnline() ? r.localCaOnline : r.localCa}
          step="tls"
          setup={setup}
          onEdit={onEdit}
        />
        <Row
          label={r.preset}
          value={
            <>
              {strings.presets.titles[preset] ?? preset}{" "}
              <span className="type-data font-normal text-ink-soft">({preset})</span>
            </>
          }
          step="size"
          setup={setup}
          onEdit={onEdit}
        />
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
            <Row label={r.fe} value={d.env.fe.provider} mono step="env" setup={setup} onEdit={onEdit} />
          </>
        ) : null}
        <Row
          label={r.backups}
          value={
            first
              ? `${fmt(r.backupsValue, {
                  count: destinations.length,
                  key: (strings.backupsPage.keyStates[d.backups?.recovery_key.state ?? "none"] ?? "").toLowerCase(),
                })} ${retentionSummary({ interval_min: first.interval_min ?? 15, ...effectiveRetention(first.retention) })}`
              : strings.common.none
          }
          step="backups"
          setup={setup}
          onEdit={onEdit}
        />
        <Row
          label={r.careAdmin}
          value={d.care_admin.username ? `${d.care_admin.username} <${d.care_admin.email ?? ""}>` : "—"}
          step="care-admin"
          setup={setup}
          onEdit={onEdit}
        />
      </ul>
      {blocking.length > 0 ? (
        <ProblemCard
          tone="warn"
          title={strings.review.missing}
          steps={blocking.map((s) => strings.setup.steps[s] ?? s)}
        />
      ) : (
        <p className="max-w-prose type-body text-ink-muted">{strings.review.duration}</p>
      )}
      {finish.error ? <ProblemAlert error={finish.error} /> : null}
      <StepActions
        onBack={onBack}
        busy={finish.isPending}
        disabled={blocking.length > 0}
        submitLabel={strings.review.finish}
        next={next}
      />
    </form>
  );
}
