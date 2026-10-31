import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckIcon, CircleDotIcon, CircleIcon, MinusIcon } from "lucide-react";
import { useState } from "react";
import { Link, Navigate, useNavigate, useParams } from "react-router";
import { api, call } from "@/api/client";
import { storeSetup, useSetup } from "@/api/queries";
import type { SetupState } from "@/api/types";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
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

const TITLES: Record<WizardPage, string> = {
  "recovery-codes": strings.recoveryStep.title,
  hardware: strings.hardware.title,
  identity: strings.identity.title,
  tls: strings.tls.title,
  size: strings.size.title,
  env: strings.env.title,
  backups: strings.backups.title,
  "care-admin": strings.careAdmin.title,
  review: strings.review.title,
};

function pageStatus(setup: SetupState, page: WizardPage): string {
  if (page === "review") return setup.can_finish ? "current" : "pending";
  return setup.steps.find((s) => s.id === page)?.status ?? "pending";
}

function StatusIcon({ status }: { status: string }) {
  if (status === "done") return <CheckIcon className="size-4 text-green-600 dark:text-green-400" aria-hidden="true" />;
  if (status === "skipped") return <MinusIcon className="size-4 text-soft-foreground" aria-hidden="true" />;
  if (status === "current") return <CircleDotIcon className="size-4 text-primary" aria-hidden="true" />;
  return <CircleIcon className="size-4 text-soft-foreground" aria-hidden="true" />;
}

function Stepper({ setup, mode, page }: { setup: SetupState; mode: SetupMode; page: WizardPage }) {
  const pages = wizardPages(mode);
  const index = pages.indexOf(page);
  return (
    <>
      <div className="flex flex-col gap-2 lg:hidden">
        <p className="text-sm text-soft-foreground">
          {fmt(strings.setup.stepOf, { n: index + 1, total: pages.length })}
        </p>
        <Progress value={((index + 1) / pages.length) * 100} aria-label={strings.setup.title} />
      </div>
      <nav aria-label={strings.setup.title} className="hidden lg:block">
        <ol className="flex flex-col gap-0.5">
          {pages.map((p) => {
            const status = pageStatus(setup, p);
            return (
              <li key={p}>
                <Link
                  to={`/setup/${p}`}
                  aria-current={p === page ? "step" : undefined}
                  className={cn(
                    "flex h-10 items-center gap-3 rounded-md px-3 text-sm",
                    p === page
                      ? "bg-muted-background font-semibold"
                      : "text-muted-foreground hover:bg-muted-background",
                  )}
                >
                  <StatusIcon status={status} />
                  <span className="flex-1 truncate">{p === "review" ? TITLES.review : strings.setup.steps[p]}</span>
                  <span className="sr-only">{strings.setup.statuses[status] ?? status}</span>
                </Link>
              </li>
            );
          })}
        </ol>
      </nav>
    </>
  );
}

function ModeToggle({ mode, onMode }: { mode: SetupMode; onMode: (m: SetupMode) => void }) {
  return (
    <div className="flex flex-col items-end gap-1">
      <ToggleGroup
        variant="outline"
        size="sm"
        value={[mode]}
        onValueChange={(v) => {
          const next = v[0];
          if (next === "easy" || next === "advanced") onMode(next);
        }}
        aria-label={strings.setup.modeLabel}
      >
        <ToggleGroupItem value="easy">{strings.setup.easy}</ToggleGroupItem>
        <ToggleGroupItem value="advanced">{strings.setup.advanced}</ToggleGroupItem>
      </ToggleGroup>
    </div>
  );
}

function WizardSkeleton() {
  return (
    <div aria-busy="true">
      <Skeleton className="mb-6 h-8 w-40" />
      <div className="grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)]">
        <Skeleton className="hidden h-80 lg:block" />
        <Skeleton className="h-96" />
      </div>
    </div>
  );
}

const STEPS: Record<Exclude<WizardPage, "review">, (p: StepProps) => React.ReactNode> = {
  "recovery-codes": (p) => <RecoveryCodesStep {...p} />,
  hardware: (p) => <HardwareStep {...p} />,
  identity: (p) => <IdentityStep {...p} />,
  tls: (p) => <TlsStep {...p} />,
  size: (p) => <SizeStep {...p} />,
  env: (p) => <EnvStep {...p} />,
  backups: (p) => <BackupsStep {...p} />,
  "care-admin": (p) => <CareAdminStep {...p} />,
};

export default function SetupWizard() {
  const { page } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const setup = useSetup();
  const [mode, setModeState] = useState<SetupMode>(loadMode);
  const skipEnv = useMutation({
    mutationFn: () => call(api.POST("/setup/steps/{step}/skip", { params: { path: { step: "env" } } })),
  });

  if (setup.isPending) return <WizardSkeleton />;
  if (setup.isError) return <ProblemAlert error={setup.error} />;
  const s = setup.data;
  if (s.state === "applying") return <Navigate to="/setup/progress" replace />;
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
  const props: StepProps = { setup: s, mode, onDone: (u) => void onDone(u), onBack };

  return (
    <div>
      <PageHeader
        title={strings.setup.title}
        description={mode === "easy" ? strings.setup.easyHelp : strings.setup.advancedHelp}
        actions={<ModeToggle mode={mode} onMode={setMode} />}
      />
      <div className="grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)]">
        <Stepper setup={s} mode={mode} page={page} />
        {/* The hardware step keeps its buttons in view (sticky), so its card must not clip. */}
        <Card className={cn("min-w-0", page === "hardware" && "overflow-visible")}>
          <CardHeader>
            <CardTitle>
              <h2 className="text-lg" data-testid="step-title">
                {TITLES[page]}
              </h2>
            </CardTitle>
          </CardHeader>
          <CardContent>
            {page === "review" ? (
              <ReviewStep {...props} onEdit={(p) => navigate(`/setup/${p}`)} />
            ) : (
              // The key resets the form state when the operator opens another step.
              <div key={page}>{STEPS[page](props)}</div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
