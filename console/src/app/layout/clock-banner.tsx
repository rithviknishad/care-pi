import { ClockAlertIcon } from "lucide-react";
import { useMaintenance } from "@/api/queries";
import { strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

// The banner "Clock not trusted" (S52, docs/api/README.md section 16). A device without internet sets its clock from
// the browser at the claim; the clock is not trusted until NTP synchronizes it (clock.trusted of GET /_care-pi/status).
// It has the slot of the maintenance banner in the top bar (--banner-height), so it moves nothing when it shows or hides. The
// maintenance banner wins the slot while it shows. The operator cannot close it: it goes when the clock is trusted.
export function ClockBanner({ enabled, compact = false }: { enabled: boolean; compact?: boolean }) {
  const status = useMaintenance(enabled);
  const clock = status.data?.clock;
  if (!clock || clock.trusted || status.data?.maintenance?.active) return null;
  const detail = strings.clock.detail[clock.source] ?? strings.clock.detail.none ?? "";
  return (
    <div
      role="status"
      aria-live="polite"
      title={detail}
      data-testid="clock-banner"
      data-source={clock.source}
      className={cn(
        "flex h-banner min-w-0 items-center gap-2 rounded-full border border-warn-border bg-warn-bg px-3 text-warn-fg",
        compact ? "w-full" : "max-w-full type-small",
      )}
    >
      <ClockAlertIcon className="size-4 shrink-0" aria-hidden="true" />
      {/* Below 640 px: 2 lines, so the text has the width of the bar. */}
      <span className={cn("flex min-w-0", compact ? "flex-col text-xs leading-4" : "items-baseline gap-1.5")}>
        <span className="shrink-0 truncate font-semibold">{strings.clock.untrusted}</span>
        <span className="min-w-0 truncate">{detail}</span>
      </span>
    </div>
  );
}
