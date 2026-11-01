import { ClockIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { fmt, strings } from "@/lib/strings";

// "Not available in this version" for the 501 not-implemented-in-this-version of a later phase.
export function NotAvailable({ phase, children }: { phase: number | string | null | undefined; children?: ReactNode }) {
  return (
    <Empty className="min-h-56 rounded-lg border border-dashed" data-testid="not-available">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <ClockIcon />
        </EmptyMedia>
        <EmptyTitle>{strings.unavailable.title}</EmptyTitle>
        <EmptyDescription>{fmt(strings.unavailable.phase, { phase: phase ?? "?" })}</EmptyDescription>
      </EmptyHeader>
      {children ? <EmptyContent>{children}</EmptyContent> : null}
    </Empty>
  );
}
