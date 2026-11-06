import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArchiveRestoreIcon } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { Link, Navigate, useNavigate, useParams } from "react-router";
import { api, call } from "@/api/client";
import { qk, storeSetup, useMaintenance, useSetup } from "@/api/queries";
import type { SetupState } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Panel } from "@/components/v2/panel";
import { strings } from "@/lib/strings";
import { BackupsStep } from "./steps/backups-step";
import { CareAdminStep } from "./steps/care-admin-step";
import { EnvStep } from "./steps/env-step";
import { HardwareStep } from "./steps/hardware-step";
import { IdentityStep } from "./steps/identity-step";
import { RecoveryCodesStep } from "./steps/recovery-codes-step";
import { ReviewStep } from "./steps/review-step";
import { SizeStep } from "./steps/size-step";
import { TlsStep } from "./steps/tls-step";
import type { StepProps } from "./step-frame";
import { WizardStage } from "./wizard-stage";
import {
  isWizardPage,
  loadMode,
  needsEnvSkip,
  nextPage,
  previousPage,
  resumePage,
  saveMode,
  wizardPages,
  type SetupMode,
  type WizardPage,
} from "./wizard";

const w = strings.wizard;

function ModeToggle({ mode, onMode }: { mode: SetupMode; onMode: (m: SetupMode) => void }) {
  return (
    <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
      <span className="hidden type-small text-ink-soft sm:inline">{w.modeHelp}</span>
      <ToggleGroup
        variant="outline"
        size="sm"
        value={[mode]}
        onValueChange={(v) => {
          const next = v[0];
          if (next === "easy" || next === "advanced") onMode(next);
        }}
        aria-label={w.mode}
      >
        <ToggleGroupItem value="easy">{strings.setup.easy}</ToggleGroupItem>
        <ToggleGroupItem value="advanced">{strings.setup.advanced}</ToggleGroupItem>
      </ToggleGroup>
    </div>
  );
}

function WizardSkeleton() {
  return (
    <div aria-busy="true" className="flex flex-col gap-4">
      <Skeleton className="ml-auto h-8 w-44" />
      <div className="grid gap-6 lg:grid-cols-[var(--stage-rail-width)_minmax(0,1fr)] lg:gap-8">
        <Skeleton className="h-32 rounded-stage lg:h-[34rem]" />
        <Skeleton className="h-[28rem] rounded-stage" />
      </div>
    </div>
  );
}

const STEPS: Record<Exclude<WizardPage, "review">, (p: StepProps) => ReactNode> = {
  "recovery-codes": (p) => <RecoveryCodesStep {...p} />,
  hardware: (p) => <HardwareStep {...p} />,
  identity: (p) => <IdentityStep {...p} />,
  tls: (p) => <TlsStep {...p} />,
  size: (p) => <SizeStep {...p} />,
  env: (p) => <EnvStep {...p} />,
  backups: (p) => <BackupsStep {...p} />,
  "care-admin": (p) => <CareAdminStep {...p} />,
};

function nextText(page: WizardPage, mode: SetupMode): string {
  if (page === "size" && mode === "advanced") return w.next.sizeAdvanced ?? "";
  return w.next[page] ?? "";
}

export default function SetupWizard() {
  const { page } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const setup = useSetup();
  // S67: another device can start the finish. The public status names it; then this page shows the progress.
  const pub = useMaintenance(true);
  const applying = pub.data?.setup_state === "applying";
  const [mode, setModeState] = useState<SetupMode>(loadMode);
  const skipEnv = useMutation({
    mutationFn: () => call(api.POST("/setup/steps/{step}/skip", { params: { path: { step: "env" } } })),
  });
  useEffect(() => {
    if (applying) void qc.invalidateQueries({ queryKey: qk.setup });
  }, [applying, qc]);

  if (setup.isPending) return <WizardSkeleton />;
  if (setup.isError) return <ProblemAlert error={setup.error} onRetry={() => void setup.refetch()} />;
  const s = setup.data;
  if (s.state === "applying" || applying) {
    const dr = s.restore.job_id !== null && s.restore.job_id === s.job_id;
    return <Navigate to={dr ? "/setup/restore" : "/setup/progress"} replace />;
  }
  if (s.state === "ready") return <Navigate to="/setup/done" replace />;
  if (!isWizardPage(page) || (mode === "easy" && page === "env")) {
    return <Navigate to={`/setup/${resumePage(s, mode)}`} replace />;
  }

  const setMode = (m: SetupMode) => {
    saveMode(m);
    setModeState(m);
  };

  const onDone = async (updated: SetupState) => {
    storeSetup(qc, updated);
    let target = nextPage(mode, page);
    // The easy path skips env when the operator passes the step before it (README section 5).
    if (page === "size" && needsEnvSkip(updated, mode)) {
      try {
        storeSetup(qc, await skipEnv.mutateAsync());
      } catch {
        target = "review";
      }
    }
    navigate(`/setup/${target}`);
  };
  const prev = previousPage(mode, page);
  const onBack = prev ? () => navigate(`/setup/${prev}`) : null;
  const props: StepProps = { setup: s, mode, onDone: (u) => void onDone(u), onBack, next: nextText(page, mode) };

  return (
    <WizardStage mode={mode} page={page} toolbar={<ModeToggle mode={mode} onMode={setMode} />}>
      {page === "review" ? (
        <ReviewStep {...props} onEdit={(p) => navigate(`/setup/${p}`)} />
      ) : (
        // The key resets the form state when the operator opens another step.
        <div key={page} className="flex flex-col gap-6">
          {STEPS[page](props)}
        </div>
      )}
      {page === wizardPages(mode)[0] && s.restore.available ? <RestoreEntry /> : null}
    </WizardStage>
  );
}

// The entry of the branch "Restore from backup" (docs/api/README.md section 13.9.3), on the first page of the wizard.
function RestoreEntry() {
  return (
    <Panel
      variant="tint"
      as="h3"
      title={strings.dr.entryTitle}
      description={strings.dr.entryText}
      testId="restore-entry"
    >
      <div>
        <Button asChild variant="outline">
          <Link to="/setup/restore" data-testid="restore-entry-link">
            <ArchiveRestoreIcon data-icon="inline-start" />
            {strings.dr.entry}
          </Link>
        </Button>
      </div>
    </Panel>
  );
}
