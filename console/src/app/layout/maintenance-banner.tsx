import { ChevronRightIcon, WrenchIcon } from "lucide-react";
import { Link } from "react-router";
import { useMaintenance } from "@/api/queries";
import { Spinner } from "@/components/ui/spinner";
import { strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { bannerJob } from "./maintenance";

// The banner of the top bar: "Maintenance in progress · step 3 of 8 · Update the database · Open". It shows on each
// page and each device while a job runs (S67), and links to the page of the job. The top bar reserves its slot:
// from 640 px the space between the state and the buttons, below 640 px the place of the device name. So the banner
// moves nothing when it shows or hides.
export function MaintenanceBanner({ enabled, compact = false }: { enabled: boolean; compact?: boolean }) {
  const status = useMaintenance(enabled);
  const job = bannerJob(status.data);
  if (!job) return null;
  const label = [job.title, job.stepOf, job.step].filter(Boolean).join(" · ");
  return (
    <span role="status" aria-live="polite" className={cn("flex min-w-0", compact && "w-full")}>
      <Link
        to={job.id ? `/jobs/${encodeURIComponent(job.id)}` : "/jobs"}
        title={label}
        data-testid="maintenance-banner"
        data-step={job.action ?? undefined}
        data-job={job.id ?? undefined}
        className={cn(
          "flex h-banner min-w-0 items-center gap-2 rounded-full border border-warn-border bg-warn-bg px-3 text-warn-fg outline-none hover:border-warn-solid focus-visible:ring-3 focus-visible:ring-ring/50",
          compact ? "w-full" : "max-w-full type-small",
        )}
      >
        {compact ? (
          <>
            <WrenchIcon className="size-4 shrink-0" aria-hidden="true" />
            {/* Below 640 px: 2 lines, so the step text has the width of the bar. */}
            <span className="flex min-w-0 flex-1 flex-col text-xs leading-4">
              <span className="truncate font-semibold">
                {job.title}
                {job.stepOf ? <span className="font-normal"> · {job.stepOf}</span> : null}
              </span>
              <span className="truncate">{job.step}</span>
            </span>
            <ChevronRightIcon className="size-4 shrink-0" aria-hidden="true" />
          </>
        ) : (
          <>
            <Spinner className="size-4 shrink-0" />
            <span className="min-w-0 truncate">
              <span className="font-semibold">{job.title}</span>
              {job.stepOf ? <span className="type-data"> · {job.stepOf}</span> : null}
              {job.step ? <span> · {job.step}</span> : null}
            </span>
            <span className="flex shrink-0 items-center gap-0.5 font-semibold underline-offset-2">
              {strings.maintenance.openJob}
              <ChevronRightIcon className="size-4" aria-hidden="true" />
            </span>
          </>
        )}
      </Link>
    </span>
  );
}
