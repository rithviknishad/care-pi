import type { MaintenanceState } from "@/api/types";
import { fmt, stepActionText, strings } from "@/lib/strings";

// The text of the maintenance banner: "Maintenance in progress: <operator text of the step>"
// (docs/console-ui.md section 9). "" when the maintenance page is off.
export function maintenanceText(m: Pick<MaintenanceState, "active" | "step"> | null | undefined) {
  if (!m?.active) return "";
  if (!m.step) return strings.maintenance.noStep;
  return fmt(strings.maintenance.banner, { step: stepActionText(m.step) });
}
