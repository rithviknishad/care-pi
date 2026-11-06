import type { ReactNode } from "react";
import type { IllustrationName } from "@/components/v2/illustrations";
import { Stage, type StageStep } from "@/components/v2/stage";
import { strings } from "@/lib/strings";
import { wizardPages, type SetupMode, type WizardPage } from "./wizard";

const w = strings.wizard;

const ILLUSTRATION: Record<WizardPage | "install", IllustrationName> = {
  "recovery-codes": "shield-certificate",
  hardware: "pi-box",
  identity: "clinic",
  tls: "shield-certificate",
  size: "clinic",
  env: "pi-box",
  backups: "backup-vault",
  "care-admin": "clinic",
  review: "pi-box",
  install: "pi-box",
};

// The steps of the rail: the pages of the path, then "Install" (the progress page). A done step links back.
function stageSteps(mode: SetupMode, current: number): StageStep[] {
  const pages = wizardPages(mode);
  return [
    ...pages.map((p, i) => ({
      id: p,
      label: w.stepLabels[p] ?? p,
      href: i < current ? `/setup/${p}` : undefined,
    })),
    { id: "install", label: w.stepLabels.install ?? w.install },
  ];
}

// The wizard layout (console/design.md section 8.2) around one task: the rail with the progress, the title, one
// sentence, and the task. The task brings its own footer (StepActions: Back, the primary button, what happens next).
export function WizardStage({
  mode,
  page,
  title,
  description,
  children,
  toolbar,
}: {
  mode: SetupMode;
  page: WizardPage | "install";
  title?: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  toolbar?: ReactNode;
}) {
  const pages = wizardPages(mode);
  const current = page === "install" ? pages.length : pages.indexOf(page);
  return (
    <div className="flex flex-col gap-4">
      {toolbar}
      <Stage
        steps={stageSteps(mode, current)}
        current={current}
        railTitle={w.railTitle}
        railHint={w.railHint}
        illustration={ILLUSTRATION[page]}
        title={<span data-testid="step-title">{title ?? w.titles[page]}</span>}
        description={description ?? w.descriptions[page]}
      >
        {children}
      </Stage>
    </div>
  );
}
