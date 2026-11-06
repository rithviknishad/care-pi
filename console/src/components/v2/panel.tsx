import { ArrowRightIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface PanelProps {
  title?: ReactNode;
  description?: ReactNode;
  /** A control at the right of the title (a chip, a small button). */
  action?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  /** tint: a section that groups related controls (2–6 % primary). */
  variant?: "card" | "tint";
  /** The heading level of the title. */
  as?: "h2" | "h3";
  className?: string;
  testId?: string;
}

// The v2 card: radius 12, a soft shadow, and a hint of the primary colour. Use it for each group of a page.
export function Panel({
  title,
  description,
  action,
  children,
  footer,
  variant = "card",
  as: H = "h2",
  className,
  testId,
}: PanelProps) {
  return (
    <section
      data-testid={testId}
      className={cn(
        "flex min-w-0 flex-col gap-4 rounded-card border p-4 sm:p-6",
        variant === "card" ? "border-line bg-surface-card shadow-soft" : "border-line-soft bg-surface-tint",
        className,
      )}
    >
      {title || action ? (
        <header className="flex min-h-8 items-start gap-3">
          <div className="min-w-0 flex-1">
            {title ? <H className="type-h3 text-ink">{title}</H> : null}
            {description ? <p className="mt-1 max-w-prose type-small text-ink-muted">{description}</p> : null}
          </div>
          {action ? <div className="flex shrink-0 items-center gap-2">{action}</div> : null}
        </header>
      ) : null}
      {children}
      {footer ? (
        <footer className="flex flex-wrap items-center gap-3 border-t border-line-soft pt-4">{footer}</footer>
      ) : null}
    </section>
  );
}

// "What happens next": one plain sentence under each primary button (console/design.md, "Copy").
export function WhatNext({ children, className, id }: { children: ReactNode; className?: string; id?: string }) {
  return (
    <p id={id} className={cn("flex items-start gap-1.5 type-small text-ink-muted", className)}>
      <ArrowRightIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-primary-solid" />
      <span>{children}</span>
    </p>
  );
}
