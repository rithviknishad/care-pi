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
// of the same height, so the rows do not move.
export function DataList({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <dl
      className={cn(
        "grid grid-cols-[minmax(0,7.5rem)_minmax(0,1fr)] gap-x-3 text-sm sm:grid-cols-[minmax(0,10rem)_minmax(0,1fr)] sm:gap-x-4",
        className,
      )}
    >
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
      <dt className="truncate leading-8 text-soft-foreground">{label}</dt>
      <dd className="flex h-8 min-w-0 items-center" title={title} data-testid={testId}>
        {loading ? <Skeleton className="h-4 w-28" /> : <span className="truncate">{value}</span>}
      </dd>
    </>
  );
}
