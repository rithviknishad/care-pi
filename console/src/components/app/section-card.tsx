import type { ReactNode } from "react";
import { Card, CardAction, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// A card with a heading (h2), an optional action at the right of the heading, a body, and an optional footer.
// It has the v2 card look (console/design.md): a border, the soft shadow, and the h3 type of a card title.
export function SectionCard({
  title,
  action,
  children,
  footer,
  className,
  testId,
}: {
  title: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
  testId?: string;
}) {
  return (
    <Card size="sm" className={cn("border border-line shadow-soft ring-0", className)} data-testid={testId}>
      <CardHeader>
        <CardTitle>
          <h2 className="type-h3 text-ink">{title}</h2>
        </CardTitle>
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-3">{children}</CardContent>
      {footer ? <CardFooter className="flex flex-wrap gap-2">{footer}</CardFooter> : null}
    </Card>
  );
}
