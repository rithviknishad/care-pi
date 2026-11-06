import type { ReactNode } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { CopyButton } from "./copy-button";

export interface KeyValueItem {
  id?: string;
  label: ReactNode;
  value?: ReactNode;
  /** Data (an id, a hash, an address, a time): Geist Mono 13/20. */
  mono?: boolean;
  /** A value to copy: a small copy button at the end of the row. */
  copy?: string;
  /** The value comes later: a skeleton of the row's size. */
  loading?: boolean;
  /** The full value as a tooltip, when the row cuts it (from 640 px). */
  title?: string;
  testId?: string;
}

export interface KeyValueListProps {
  items: KeyValueItem[];
  /** Each value comes later (the first load of a card). */
  loading?: boolean;
  /** The width of the label column from 640 px. */
  labelWidth?: "sm" | "md" | "lg";
  className?: string;
}

const LABEL = {
  sm: "sm:grid-cols-[8rem_minmax(0,1fr)]",
  md: "sm:grid-cols-[11rem_minmax(0,1fr)]",
  lg: "sm:grid-cols-[14rem_minmax(0,1fr)]",
};

// Labels and values. Below 640 px the label is above the value and the value wraps (nothing is cut on a phone).
// From 640 px they are side by side in one line of 48 px. Each row has a fixed minimum height, and its skeleton
// has the same height, so the list does not move when the values come.
export function KeyValueList({ items, loading = false, labelWidth = "md", className }: KeyValueListProps) {
  return (
    <dl className={cn("flex flex-col", className)}>
      {items.map((item, i) => {
        const busy = loading || item.loading;
        return (
          <div
            key={item.id ?? i}
            className={cn(
              "grid min-h-16 grid-cols-1 content-center gap-x-4 border-b border-line-soft py-2 last:border-b-0 sm:min-h-12 sm:items-center sm:py-0",
              LABEL[labelWidth],
            )}
          >
            <dt className="truncate type-small text-ink-soft">{item.label}</dt>
            <dd
              className={cn(
                "flex min-h-8 min-w-0 items-center gap-2",
                item.mono ? "type-data text-ink" : "type-small font-medium text-ink",
              )}
              title={item.title}
              data-testid={item.testId}
            >
              {busy ? (
                <Skeleton className="h-4 w-36" />
              ) : (
                <span className="min-w-0 flex-1 break-words sm:truncate">{item.value ?? "—"}</span>
              )}
              {item.copy !== undefined && !busy ? (
                <CopyButton text={item.copy} iconOnly size="xs" variant="ghost" className="-my-1 shrink-0" />
              ) : null}
            </dd>
          </div>
        );
      })}
    </dl>
  );
}
