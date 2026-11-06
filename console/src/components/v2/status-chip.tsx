import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { toneSolid, toneSurface, type Tone } from "./tone";

export interface StatusChipProps {
  tone?: Tone;
  children: ReactNode;
  /** A running state: the dot breathes (not with reduced motion). */
  live?: boolean;
  /** A fixed width in ch, so a change of the label moves nothing (for example the state chip of the top bar). */
  width?: number;
  size?: "sm" | "md";
  className?: string;
  testId?: string;
}

// A state in a few words: a dot and a label on the tone surface. Colour is never the only signal: the label says it.
export function StatusChip({
  tone = "neutral",
  children,
  live = false,
  width,
  size = "md",
  className,
  testId,
}: StatusChipProps) {
  return (
    <span
      data-tone={tone}
      data-testid={testId}
      style={width ? { minWidth: `calc(${width}ch + 1.75rem)` } : undefined}
      className={cn(
        "inline-flex max-w-full shrink-0 items-center gap-1.5 rounded-full border font-medium whitespace-nowrap",
        size === "sm" ? "h-6 px-2 text-xs" : "h-7 px-2.5 type-small",
        toneSurface[tone],
        className,
      )}
    >
      <span
        aria-hidden="true"
        className={cn("size-2 shrink-0 rounded-full", toneSolid[tone], live && "motion-ok:animate-v2-breathe")}
      />
      <span className="min-w-0 truncate">{children}</span>
    </span>
  );
}
