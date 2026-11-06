import { WrenchIcon } from "lucide-react";
import { Link } from "react-router";
import { useMaintenance } from "@/api/queries";
import { Spinner } from "@/components/ui/spinner";
import { stepActionText, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { maintenanceText } from "./maintenance";

// The maintenance banner of the top bar. The top bar reserves its slot: from 640 px the space between the state
// and the buttons, below 640 px the place of the device name. So the banner moves nothing when it shows or hides.
// It reads GET /_care-pi/status (useMaintenance), the same state as the maintenance page of Caddy.
export function MaintenanceBanner({ enabled, compact = false }: { enabled: boolean; compact?: boolean }) {
  const status = useMaintenance(enabled);
  const m = status.data?.maintenance;
  if (!m?.active) return null;
  const step = m.step ? stepActionText(m.step) : "";
  return (
    <Link
      to="/updates"
      role="status"
      aria-live="polite"
      title={`${maintenanceText(m)}. ${strings.maintenance.open}`}
      data-testid="maintenance-banner"
      data-step={m.step ?? undefined}
      className={cn(
        "flex h-banner min-w-0 items-center gap-2 rounded-full border border-warn-border bg-warn-bg px-3 text-warn-fg outline-none hover:border-warn-solid focus-visible:ring-3 focus-visible:ring-ring/50",
        compact ? "w-full" : "max-w-full type-small",
      )}
    >
      {compact ? (
        <>
          <WrenchIcon className="size-4 shrink-0" aria-hidden="true" />
          {/* Below 640 px: 2 lines, so the step text has the width of the bar. */}
          <span className="flex min-w-0 flex-col text-xs leading-4">
            <span className="truncate font-semibold">{strings.maintenance.noStep}</span>
            <span className="truncate">{step}</span>
          </span>
        </>
      ) : (
        <>
          <Spinner className="size-4 shrink-0" />
          <span className="min-w-0 truncate">{maintenanceText(m)}</span>
        </>
      )}
    </Link>
  );
}
