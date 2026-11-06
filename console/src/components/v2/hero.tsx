import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Illustration } from "./illustration";
import { InlineSkeleton } from "./inline-skeleton";
import type { IllustrationName } from "./illustrations";

export interface HeroProps {
  title: ReactNode;
  /** One sentence. It says the state or the purpose in plain words. */
  description?: ReactNode;
  /** A short line above the title (for example the device name, in mono). */
  eyebrow?: ReactNode;
  illustration?: IllustrationName;
  actions?: ReactNode;
  /** compact: a status band for the top of a page (dashboard). lg: landing, done page. */
  size?: "compact" | "lg";
  /** The text comes later: skeleton lines of the same height. */
  loading?: boolean;
  /** The heading level. A page has one h1. */
  as?: "h1" | "h2";
  className?: string;
  testId?: string;
}

// The gradient header. The text always sits on a scrim (contrast 4.5:1 or more on each point of the
// gradient). The band has a minimum height for its size, and the description has 2 lines at most, so
// the band does not grow when the text comes.
export function Hero({
  title,
  description,
  eyebrow,
  illustration,
  actions,
  size = "lg",
  loading = false,
  as: H = "h1",
  className,
  testId,
}: HeroProps) {
  const lg = size === "lg";
  return (
    <section
      data-testid={testId}
      className={cn(
        "relative isolate overflow-hidden rounded-stage bg-gradient-hero on-gradient shadow-soft",
        lg ? "min-h-52 sm:min-h-56" : "min-h-32",
        className,
      )}
    >
      <div aria-hidden="true" className="absolute inset-0 -z-10 bg-scrim-full sm:bg-transparent sm:bg-scrim" />
      <div
        className={cn(
          "flex h-full items-center gap-6",
          lg ? "min-h-52 p-6 sm:min-h-56 sm:p-8" : "min-h-32 p-5 sm:px-6",
        )}
      >
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          {eyebrow ? <div className="truncate type-data text-on-gradient-muted">{eyebrow}</div> : null}
          {/* The title and the description have the same boxes in the loading state: the skeleton is inside them. */}
          <H className={cn(lg ? "type-h1 text-balance sm:type-display" : "truncate type-h2")}>
            {loading ? (
              <InlineSkeleton className={cn("h-[0.8em] bg-white/25 align-middle", lg ? "w-3/4" : "w-1/2")} />
            ) : (
              title
            )}
          </H>
          {description !== undefined || loading ? (
            <p
              className={cn(
                "max-w-prose text-on-gradient-muted",
                lg ? "type-body" : "line-clamp-2 min-h-10 type-small sm:min-h-12 sm:type-body",
              )}
            >
              {loading ? <InlineSkeleton className="mt-1 h-[0.9em] w-full max-w-md bg-white/20" /> : description}
            </p>
          ) : null}
          {actions ? <div className="mt-4 flex flex-wrap gap-3">{actions}</div> : null}
        </div>
        {illustration ? (
          <Illustration
            name={illustration}
            size={lg ? "lg" : "md"}
            className={cn("hidden sm:inline-block", lg ? "w-40 md:w-52" : "w-28")}
          />
        ) : null}
      </div>
    </section>
  );
}
