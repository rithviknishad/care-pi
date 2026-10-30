import { asProblem } from "@/api/problem";
import { useCountdown } from "./use-countdown";

// The wait time of a 429 response (Retry-After), for a disabled submit button.
export function useRetryWait(error: unknown): number {
  const problem = asProblem(error);
  return useCountdown(problem?.is("rate-limited", "locked-out") ? problem.retryAfter : null, error);
}
