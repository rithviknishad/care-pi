import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

// The dashboard grid: 1 column on a phone, 2 from 640 px, 4 from 1280 px. Cards have fixed heights
// (StatCard 8 rem), so a card that loads does not move the others. A child spans with sm:col-span-2.
export function DashboardGrid({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4", className)}>{children}</div>;
}
