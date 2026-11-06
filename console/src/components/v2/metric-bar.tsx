import { Skeleton } from "@/components/ui/skeleton";
import { formatBytes } from "@/lib/format";
import { fmt } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { v2Strings } from "./strings";
import { meterTone } from "./tone";

export interface MetricBarProps {
  label: string;
  used: number | null | undefined;
  /** The limit. null: no limit (the bar shows the track only). */
  cap: number | null | undefined;
  /** The format of used and cap. Default: bytes (formatBytes, binary units). */
  format?: (n: number) => string;
  /** The fraction from which the bar is amber, and red. */
  warnAt?: number;
  dangerAt?: number;
  loading?: boolean;
  className?: string;
  testId?: string;
}

const FILL = { ok: "bg-primary-solid", warn: "bg-warn-solid", danger: "bg-danger-solid" };

// Used against a limit (memory against the cap of a unit). The fill is a transform (scaleX), so it changes
// no layout. Fixed height: a label line and an 8 px bar.
export function MetricBar({
  label,
  used,
  cap,
  format = formatBytes,
  warnAt = 0.8,
  dangerAt = 0.95,
  loading = false,
  className,
  testId,
}: MetricBarProps) {
  const known = used !== null && used !== undefined;
  const fraction = known && cap ? Math.min(1, Math.max(0, used / cap)) : 0;
  const tone = meterTone(fraction, warnAt, dangerAt);
  const text = !known
    ? "—"
    : cap
      ? fmt(v2Strings.metric.usedOf, { used: format(used), cap: format(cap) })
      : fmt(v2Strings.metric.noCap, { used: format(used) });
  return (
    <div className={cn("flex min-w-0 flex-col gap-2", className)} data-testid={testId} data-tone={tone}>
      <div className="flex h-5 min-w-0 items-baseline justify-between gap-3">
        <span className="min-w-0 truncate type-small text-ink-muted">{label}</span>
        {loading ? <Skeleton className="h-4 w-28" /> : <span className="shrink-0 type-data text-ink">{text}</span>}
      </div>
      {/* A meter needs a value and a limit; without them the text of the label line says it all. */}
      <div
        {...(known && cap
          ? {
              role: "meter",
              "aria-label": label,
              "aria-valuemin": 0,
              "aria-valuemax": cap,
              "aria-valuenow": used,
              "aria-valuetext": text,
            }
          : { "aria-hidden": true })}
        className="relative h-2 overflow-hidden rounded-full bg-primary-track"
      >
        <div
          className={cn(
            "absolute inset-0 origin-left rounded-full motion-ok:transition-transform motion-ok:duration-(--duration-slow) motion-ok:ease-out",
            FILL[tone],
          )}
          style={{ transform: `scaleX(${loading ? 0 : fraction})` }}
        />
      </div>
    </div>
  );
}
