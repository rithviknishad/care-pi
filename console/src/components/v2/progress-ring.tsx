import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface ProgressRingProps {
  /** 0 to 1. null: the work runs without a known end (a turning arc). */
  value: number | null;
  size?: number;
  stroke?: number;
  /** The text in the centre, for example "40 %". Mono figures, so the width does not change. */
  children?: ReactNode;
  label: string;
  className?: string;
  /** On a gradient: white arc on a translucent track. */
  onGradient?: boolean;
}

export function ProgressRing({
  value,
  size = 64,
  stroke = 6,
  children,
  label,
  className,
  onGradient = false,
}: ProgressRingProps) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const v = value === null ? null : Math.min(1, Math.max(0, value));
  return (
    <span
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={v === null ? undefined : Math.round(v * 100)}
      className={cn("relative inline-grid shrink-0 place-items-center", className)}
      style={{ width: size, height: size }}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        aria-hidden="true"
        className={cn("-rotate-90", v === null && "motion-ok:animate-v2-spin")}
      >
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          strokeWidth={stroke}
          className={onGradient ? "stroke-white/25" : "stroke-primary-track"}
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          strokeWidth={stroke}
          strokeLinecap="round"
          strokeDasharray={c}
          strokeDashoffset={v === null ? c * 0.7 : c * (1 - v)}
          className={cn(
            onGradient ? "stroke-white" : "stroke-primary-solid",
            "motion-ok:transition-[stroke-dashoffset] motion-ok:duration-(--duration-slow) motion-ok:ease-out",
          )}
        />
      </svg>
      {children ? (
        <span className="absolute inset-0 grid place-items-center type-data font-medium">{children}</span>
      ) : null}
    </span>
  );
}
