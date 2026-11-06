import { cn } from "@/lib/utils";

// A skeleton for inline places (a heading, a paragraph, a span): the careui Skeleton is a div, which a <p> cannot hold.
export function InlineSkeleton({ className }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      data-slot="skeleton"
      className={cn("inline-block animate-pulse rounded-md bg-muted", className)}
    />
  );
}
