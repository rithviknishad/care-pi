import type { ReactNode } from "react";
import { Card, CardAction, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";

// A card with a heading (h2), an optional action at the right of the heading, a body, and an optional footer.
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
    <Card size="sm" className={className} data-testid={testId}>
      <CardHeader>
        <CardTitle>
          <h2>{title}</h2>
        </CardTitle>
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-3">{children}</CardContent>
      {footer ? <CardFooter className="flex flex-wrap gap-2">{footer}</CardFooter> : null}
    </Card>
  );
}
