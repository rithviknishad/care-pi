import type { ReactNode } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-start gap-3">
      <div className="min-w-0 flex-1">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description ? <p className="mt-1 max-w-prose text-sm text-soft-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap gap-2">{actions}</div> : null}
    </div>
  );
}

// Label and value rows with a fixed height. A value that arrives later replaces a skeleton
// of the same height, so the rows do not move. Below 640 px (sm) the label is above the value and
// the value is not cut ("Configured na…" on a phone, wave 4c); from 640 px they are side by side.
export function DataList({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-1 text-sm sm:grid-cols-[minmax(0,10rem)_minmax(0,1fr)] sm:gap-x-4", className)}>
      {children}
    </dl>
  );
}

export function DataRow({
  label,
  value,
  loading = false,
  title,
  testId,
}: {
  label: ReactNode;
  value: ReactNode;
  loading?: boolean;
  title?: string;
  testId?: string;
}) {
  return (
    <>
      <dt className="pt-1.5 text-xs leading-5 text-soft-foreground sm:truncate sm:pt-0 sm:text-sm sm:leading-8">
        {label}
      </dt>
      <dd
        className="flex min-h-7 min-w-0 items-center pb-1.5 sm:h-8 sm:min-h-0 sm:pb-0"
        title={title}
        data-testid={testId}
      >
        {loading ? <Skeleton className="h-4 w-28" /> : <span className="min-w-0 break-words sm:truncate">{value}</span>}
      </dd>
    </>
  );
}
