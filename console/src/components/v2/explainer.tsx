import { useId, type CSSProperties } from "react";
import { fmt } from "@/lib/strings";
import { cn } from "@/lib/utils";
import {
  retentionSummary,
  retentionZones,
  type RetentionPolicy,
  type RetentionZone,
  type RetentionZoneKind,
} from "./retention";
import { v2Strings } from "./strings";

export interface ExplainerProps {
  policy: RetentionPolicy;
  /** A heading above the diagram. */
  title?: string;
  className?: string;
  testId?: string;
}

// The look of each zone: older zones have fewer, larger, and lighter dots (the copies "merge").
const ZONE: Record<RetentionZoneKind, { max: number; r: number; opacity: number }> = {
  moment: { max: 14, r: 2.5, opacity: 1 },
  day: { max: 10, r: 3.5, opacity: 0.9 },
  week: { max: 8, r: 4.5, opacity: 0.78 },
  month: { max: 6, r: 5.5, opacity: 0.66 },
};

// One drawing of the track in pixels (W x H). The phone and the desktop each get a drawing of their own size, so a dot
// keeps its size on each screen (a scaled SVG makes the dots too small on a phone).
function Track({
  zones,
  width: W,
  height: H,
  density,
  className,
}: {
  zones: RetentionZone[];
  width: number;
  height: number;
  density: number;
  className?: string;
}) {
  const clipId = useId();
  const maskId = useId();
  const PAD = 10;
  const AXIS = H / 2;
  const n = Math.max(1, zones.length);
  const zw = (W - 2 * PAD) / n;
  let order = 0;
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      width={W}
      height={H}
      className={cn("block h-auto w-full", className)}
      aria-hidden="true"
    >
      <line x1={PAD} y1={AXIS} x2={W - PAD} y2={AXIS} strokeWidth="2" className="stroke-line-strong" />
      {zones.map((z, i) => {
        const x1 = W - PAD - (i + 1) * zw;
        const x2 = W - PAD - i * zw;
        const look = ZONE[z.kind];
        const count = Math.max(1, Math.min(Math.round(look.max * density), z.count));
        const step = (zw - 20) / Math.max(1, count);
        const flow = z.kind === "moment";
        const cx = (j: number) => x2 - 12 - (flow ? j - 1 : j) * step;
        return (
          <g key={z.kind}>
            {i > 0 ? (
              <line
                x1={x2}
                x2={x2}
                y1={AXIS - 12}
                y2={AXIS + 12}
                strokeWidth="1.5"
                strokeDasharray="3 4"
                className="stroke-line-stronger"
              />
            ) : null}
            {flow ? (
              <defs>
                <clipPath id={clipId}>
                  <rect x={x1} y={0} width={zw} height={H} />
                </clipPath>
                <linearGradient id={`${maskId}-g`} x1="0" x2="1" y1="0" y2="0">
                  <stop offset="0" stopColor="white" stopOpacity="0" />
                  <stop offset="0.4" stopColor="white" stopOpacity="1" />
                </linearGradient>
                <mask id={maskId}>
                  <rect x={x1} y={0} width={zw} height={H} fill={`url(#${maskId}-g)`} />
                </mask>
              </defs>
            ) : null}
            <g clipPath={flow ? `url(#${clipId})` : undefined} mask={flow ? `url(#${maskId})` : undefined}>
              <g
                className={flow ? "motion-ok:animate-v2-flow" : undefined}
                style={flow ? ({ "--v2-flow-x": `${-step}px` } as CSSProperties) : undefined}
              >
                {Array.from({ length: count + (flow ? 1 : 0) }, (_, j) => (
                  <circle
                    key={j}
                    cx={cx(j)}
                    cy={AXIS}
                    r={look.r}
                    fillOpacity={Math.max(0.3, look.opacity - j * 0.025)}
                    className="fill-primary-solid motion-ok:animate-v2-pop"
                    style={{
                      animationDelay: `${order++ * 28}ms`,
                      transformOrigin: `${cx(j)}px ${AXIS}px`,
                      transformBox: "view-box",
                    }}
                  />
                ))}
              </g>
            </g>
          </g>
        );
      })}
      <circle
        cx={W - PAD}
        cy={AXIS}
        r="7"
        strokeWidth="2"
        className="fill-none stroke-primary-solid motion-ok:animate-v2-ping"
        style={{ transformOrigin: `${W - PAD}px ${AXIS}px`, transformBox: "view-box" }}
      />
      <circle cx={W - PAD} cy={AXIS} r="6" className="fill-primary-solid" />
    </svg>
  );
}

// The backups retention explainer (S66, item 2). A timeline from the oldest copy (left) to now (right), a legend, and
// one plain sentence. Motion (only when the operator allows motion): the dots appear from now to the past, the newest
// copies flow to the left and fade into the daily copies, and "now" pulses. Each part has a fixed height, so the
// figure reserves its space.
export function Explainer({ policy, title = v2Strings.explainer.title, className, testId }: ExplainerProps) {
  const zones = retentionZones(policy);
  const summary = retentionSummary(policy);
  const key = `${policy.interval_min}-${policy.keep_within_days}-${policy.keep_daily}-${policy.keep_weekly}-${policy.keep_monthly}`;
  const oldest = zones[zones.length - 1];
  return (
    <figure className={cn("flex min-w-0 flex-col gap-3", className)} data-testid={testId}>
      {title ? <h3 className="type-h3 text-ink">{title}</h3> : null}
      <div className="rounded-card border border-line-soft bg-surface-tint px-3 pt-2 pb-2" key={key}>
        <Track zones={zones} width={320} height={48} density={0.45} className="sm:hidden" />
        <Track zones={zones} width={720} height={56} density={1} className="hidden sm:block" />
        <div className="flex justify-between px-1 type-data text-ink-soft">
          <span>{oldest ? fmt(v2Strings.explainer.ago, { span: oldest.span }) : ""}</span>
          <span>{v2Strings.explainer.now}</span>
        </div>
      </div>
      <ul className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4">
        {[...zones].reverse().map((z) => (
          <li key={z.kind} className="flex min-w-0 items-center gap-2">
            <svg viewBox="0 0 14 14" className="size-3.5 shrink-0" aria-hidden="true">
              <circle
                cx="7"
                cy="7"
                r={ZONE[z.kind].r + 1}
                className="fill-primary-solid"
                fillOpacity={ZONE[z.kind].opacity}
              />
            </svg>
            <span className="min-w-0 type-small text-ink">
              <span className="font-medium">{v2Strings.explainer.zone[z.kind]}</span>
              <span className="text-ink-soft"> · </span>
              <span className="type-data text-ink-muted">{z.span}</span>
            </span>
          </li>
        ))}
      </ul>
      <figcaption className="max-w-prose type-body text-ink" data-testid={testId ? `${testId}-summary` : undefined}>
        {summary}
      </figcaption>
    </figure>
  );
}
