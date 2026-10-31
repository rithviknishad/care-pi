import type { Job, JobStep } from "@/api/types";
import { formatDateTime } from "@/lib/format";
import { strings } from "@/lib/strings";

const p = strings.progress;

// failedStep is the step that failed, else the last step that started.
export function failedStep(job: Job): JobStep | null {
  const steps = job.steps ?? [];
  return steps.find((s) => s.state === "failed") ?? [...steps].reverse().find((s) => s.started_at) ?? null;
}

// failureDetails is the text of "Copy details" and of the technical details block.
export function failureDetails(job: Job): string {
  const step = failedStep(job);
  const e = job.error;
  const lines = [
    `${p.technicalJob}: ${job.id} (${job.kind}, ${job.state}) ${formatDateTime(job.started_at)} - ${formatDateTime(job.finished_at)}`,
    step ? `${p.technicalStep}: ${step.name}` : null,
    step?.detail ? `${p.technicalStepDetail}: ${step.detail}` : null,
    e ? `${p.technicalProblem}: ${e.type} ${e.status ?? ""} ${e.title ?? ""}`.trimEnd() : null,
    e?.detail ? `detail: ${e.detail}` : null,
    e?.instance ? `instance: ${e.instance}` : null,
  ];
  return lines.filter((l): l is string => l !== null).join("\n");
}
