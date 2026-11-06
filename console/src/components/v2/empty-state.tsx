import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Illustration } from "./illustration";
import type { IllustrationName } from "./illustrations";

export interface EmptyStateProps {
  illustration: IllustrationName;
  title: ReactNode;
  /** One sentence: why it is empty, and what the action does. */
  description?: ReactNode;
  /** One action. */
  action?: ReactNode;
  className?: string;
}

// Nothing to show yet. The illustration, one sentence, and one action. It reserves the height of a card.
export function EmptyState({ illustration, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex min-h-72 flex-col items-center justify-center gap-3 rounded-card border border-dashed border-line-strong bg-surface-tint px-6 py-8 text-center",
        className,
      )}
    >
      <Illustration name={illustration} size="md" />
      <h3 className="type-h3 text-ink">{title}</h3>
      {description ? <p className="max-w-sm type-small text-ink-muted">{description}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}
