import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { toneSolid, type Tone } from "./tone";

export interface TimelineItem {
  id: string;
  /** The time, already formatted (Geist Mono). */
  time: string;
  /** The full time (ISO) for the datetime attribute. */
  datetime?: string;
  title: ReactNode;
  description?: ReactNode;
  tone?: Tone;
  icon?: LucideIcon;
}

// Events in time order (the update history, the backups of a day). Append only: a new event goes at the end
// (or the start, if the page shows the newest first) and the others keep their place.
// Below 640 px the time is above the title; from 640 px it is a column at the left.
export function Timeline({ items, className, testId }: { items: TimelineItem[]; className?: string; testId?: string }) {
  return (
    <ol className={cn("flex flex-col", className)} data-testid={testId}>
      {items.map((item, i) => {
        const Icon = item.icon;
        const tone = item.tone ?? "primary";
        return (
          <li
            key={item.id}
            className="relative grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-3 pb-5 last:pb-0 sm:grid-cols-[8.5rem_1.5rem_minmax(0,1fr)]"
          >
            {i < items.length - 1 ? (
              <span
                aria-hidden="true"
                className="absolute top-7 bottom-1 left-[0.6875rem] w-0.5 rounded-full bg-line sm:left-[calc(8.5rem+0.75rem+0.6875rem)]"
              />
            ) : null}
            <time
              dateTime={item.datetime}
              className="col-start-2 row-start-1 type-data text-ink-soft sm:col-start-1 sm:pt-0.5 sm:text-right"
            >
              {item.time}
            </time>
            <span className="col-start-1 row-span-2 row-start-1 grid size-6 place-items-center sm:col-start-2">
              {Icon ? (
                <span
                  className={cn(
                    "grid size-6 place-items-center rounded-full text-white dark:text-black/80",
                    toneSolid[tone],
                  )}
                >
                  <Icon className="size-3.5" strokeWidth={2.5} aria-hidden="true" />
                </span>
              ) : (
                <span className={cn("size-3 rounded-full ring-4 ring-surface-card", toneSolid[tone])} />
              )}
            </span>
            <div className="col-start-2 min-w-0 sm:col-start-3 sm:row-start-1">
              <p className="type-body font-medium text-ink">{item.title}</p>
              {item.description ? <p className="mt-0.5 type-small text-ink-muted">{item.description}</p> : null}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
