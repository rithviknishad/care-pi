import type { PublicStatus } from "@/api/types";

// The job that a device without a session follows: the finish job of the setup, or the job of the maintenance page.
export function publicJobId(pub: PublicStatus | undefined): string | null {
  if (!pub) return null;
  if (pub.setup_state === "applying" && pub.setup_job_id) return pub.setup_job_id;
  if (pub.maintenance?.active) return pub.maintenance.job_id ?? pub.job?.id ?? null;
  return null;
}
