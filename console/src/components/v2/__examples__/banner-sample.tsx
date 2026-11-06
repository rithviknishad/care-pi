import { WrenchIcon } from "lucide-react";
import { cn } from "@/lib/utils";

// A sample of the banner slot: the maintenance banner in the v2 look (fixed height: --banner-height).
export function BannerSample({ compact = false }: { compact?: boolean }) {
  return (
    <div
      role="status"
      className={cn(
        "flex h-banner min-w-0 items-center gap-2 rounded-full border border-warn-border bg-warn-bg px-3 text-warn-fg",
        compact ? "w-full text-xs leading-4" : "max-w-full type-small",
      )}
    >
      <WrenchIcon className="size-4 shrink-0" aria-hidden="true" />
      <span className="min-w-0 truncate">
        <span className="font-semibold">Maintenance</span> · Install the update · about 2 min left
      </span>
    </div>
  );
}
