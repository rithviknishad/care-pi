import type { MaintenanceState, PublicStatus } from "@/api/types";
import { fmt, jobKindText, stepActionText, strings } from "@/lib/strings";

// The text of the maintenance banner: "Maintenance in progress: <operator text of the step>"
// (docs/console-ui.md section 9). "" when the maintenance page is off.
export function maintenanceText(m: Pick<MaintenanceState, "active" | "step"> | null | undefined) {
  if (!m?.active) return "";
  if (!m.step) return strings.maintenance.noStep;
  return fmt(strings.maintenance.banner, { step: stepActionText(m.step) });
}

export interface BannerJob {
  id: string | null;
  /** "Maintenance in progress", or the title of the job ("Apply the settings"). */
  title: string;
  /** "step 3 of 8", or "" before the first step. */
  stepOf: string;
  /** The operator text of the step that runs now. */
  step: string;
  /** The action key of that step (data-step, for the tests). */
  action: string | null;
}

// The job of the banner (S67): the job that holds the apply lock (GET /_care-pi/status, job) or the job of the
// maintenance page. null when neither runs.
export function bannerJob(status: PublicStatus | undefined): BannerJob | null {
  const m = status?.maintenance;
  const job = status?.job ?? null;
  if (!job && !m?.active) return null;
  const action = m?.active && m.step ? m.step : (job?.step?.split(":")[0]?.trim() ?? null);
  const index = job?.step_index;
  const count = job?.step_count;
  return {
    id: job?.id ?? m?.job_id ?? null,
    title: m?.active ? strings.maintenance.noStep : job?.title || jobKindText(job?.kind),
    stepOf:
      index !== null && index !== undefined && count
        ? fmt(strings.maintenance.stepOf, { n: index + 1, total: Math.max(count, index + 1) })
        : "",
    step: action ? stepActionText(action) : "",
    action,
  };
}
