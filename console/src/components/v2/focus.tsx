import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Illustration } from "./illustration";
import type { IllustrationName } from "./illustrations";

// The page behind a focus task (claim, login, recover, certificate): the page colour with a gradient band at the top.
// The band is decoration; text on it sits on a scrim pill (FocusBar). band="bar": the gradient only behind the bar
// (a page whose own text starts at the top, without a FocusCard).
export function FocusBackdrop({
  top,
  children,
  band = "tall",
  className,
}: {
  top?: ReactNode;
  children: ReactNode;
  band?: "tall" | "bar";
  className?: string;
}) {
  return (
    <div className={cn("relative isolate flex min-h-dvh flex-col bg-surface-page", className)}>
      {band === "tall" ? (
        <div
          aria-hidden="true"
          className="absolute inset-x-0 top-0 -z-10 h-72 overflow-hidden bg-gradient-hero mask-b-from-50% sm:h-96"
        >
          <Illustration
            name="network"
            size="xl"
            className="absolute top-14 right-[6%] hidden w-96 on-gradient opacity-20 sm:inline-block"
          />
        </div>
      ) : (
        <div aria-hidden="true" className="absolute inset-x-0 top-0 -z-10 h-topbar bg-gradient-hero" />
      )}
      {top}
      {children}
    </div>
  );
}

export interface FocusCardProps {
  title: ReactNode;
  description?: ReactNode;
  illustration?: IllustrationName;
  children?: ReactNode;
  footer?: ReactNode;
  /** md: one form (claim, login). lg: a guide with steps (the certificate). */
  width?: "md" | "lg";
  className?: string;
}

// The one card of a focus page. The text is left-aligned; only the card is in the centre.
export function FocusCard({
  title,
  description,
  illustration,
  children,
  footer,
  width = "md",
  className,
}: FocusCardProps) {
  return (
    <main id="main" className="flex w-full flex-1 justify-center px-gutter pt-4 pb-12 sm:pt-10">
      <div
        className={cn(
          "flex w-full flex-col gap-6 self-start rounded-stage border border-line bg-surface-card p-6 shadow-lift sm:p-8",
          width === "md" ? "max-w-[30rem]" : "max-w-3xl",
          className,
        )}
      >
        <header className="flex flex-col gap-3">
          {illustration ? <Illustration name={illustration} size="sm" className="w-24" /> : null}
          <h1 className="type-h1 text-ink">{title}</h1>
          {description ? <p className="type-body text-ink-muted">{description}</p> : null}
        </header>
        {children}
        {footer ? <footer className="border-t border-line-soft pt-4 type-small text-ink-muted">{footer}</footer> : null}
      </div>
    </main>
  );
}

// The bar on the band: the brand at the left, controls at the right. Each text has a scrim pill.
export function FocusBar({ start, end }: { start?: ReactNode; end?: ReactNode }) {
  return (
    <div className="flex h-topbar items-center gap-3 px-gutter on-gradient">
      <div className="flex min-w-0 flex-1 items-center gap-2">{start}</div>
      <div className="flex shrink-0 items-center gap-1">{end}</div>
    </div>
  );
}

export function Focus({
  top,
  ...card
}: FocusCardProps & {
  top?: ReactNode;
}) {
  return (
    <FocusBackdrop top={top}>
      <FocusCard {...card} />
    </FocusBackdrop>
  );
}
