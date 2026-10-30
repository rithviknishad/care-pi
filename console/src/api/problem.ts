import type { Problem } from "./types";

export const PROBLEM_BASE = "https://care-pi.rithviknishad.dev/problems/";

// The slugs of docs/api/README.md section 7. A later version can add slugs:
// the console maps an unknown slug to the general text.
export type ProblemSlug =
  | "malformed-request"
  | "invalid-request"
  | "invalid-config"
  | "unsupported-config"
  | "weak-password"
  | "host-check-failed"
  | "not-authenticated"
  | "bad-credentials"
  | "totp-required"
  | "csrf"
  | "reauth-required"
  | "not-found"
  | "job-not-found"
  | "unit-not-found"
  | "not-claimed"
  | "already-claimed"
  | "setup-incomplete"
  | "setup-finished"
  | "step-not-skippable"
  | "job-running"
  | "job-not-cancellable"
  | "care-user-exists"
  | "state-conflict"
  | "config-changed"
  | "payload-too-large"
  | "unsupported-media-type"
  | "precondition-required"
  | "rate-limited"
  | "locked-out"
  | "internal"
  | "not-implemented-in-this-version"
  | "care-not-running"
  | "unavailable"
  | "network"
  | "unknown";

export function slugOf(type: string | undefined | null): ProblemSlug {
  if (!type) return "unknown";
  return (type.startsWith(PROBLEM_BASE) ? type.slice(PROBLEM_BASE.length) : type) as ProblemSlug;
}

// An error of the API: an RFC 9457 problem, or a network error with the slug "network".
export class ProblemError extends Error {
  readonly problem: Problem;
  readonly slug: ProblemSlug;
  readonly status: number;
  readonly retryAfter: number | null;

  constructor(problem: Problem, retryAfter: number | null = null) {
    super(problem.detail || problem.title);
    this.name = "ProblemError";
    this.problem = problem;
    this.slug = slugOf(problem.type);
    this.status = problem.status;
    this.retryAfter = retryAfter ?? problem.retry_after_s ?? null;
  }

  is(...slugs: ProblemSlug[]): boolean {
    return slugs.includes(this.slug);
  }

  // The message of the first validation error at this pointer (or below it).
  fieldError(pointer: string): string | undefined {
    return this.problem.errors?.find((e) => e.pointer === pointer || e.pointer.endsWith(pointer))?.message;
  }
}

function isProblem(body: unknown): body is Problem {
  return (
    typeof body === "object" &&
    body !== null &&
    typeof (body as Problem).type === "string" &&
    typeof (body as Problem).status === "number"
  );
}

function retryAfterOf(response: Response | undefined): number | null {
  const v = response?.headers.get("Retry-After");
  if (!v) return null;
  const n = Number(v);
  if (Number.isFinite(n)) return Math.max(0, Math.round(n));
  const t = Date.parse(v);
  return Number.isNaN(t) ? null : Math.max(0, Math.round((t - Date.now()) / 1000));
}

// Makes a ProblemError from the body and the response of a failed request.
export function toProblemError(body: unknown, response?: Response): ProblemError {
  const retry = retryAfterOf(response);
  if (isProblem(body)) return new ProblemError(body, retry);
  const status = response?.status ?? 0;
  return new ProblemError(
    {
      type: PROBLEM_BASE + (status === 0 ? "network" : "unknown"),
      title: status === 0 ? "No connection" : `HTTP ${status}`,
      status: status || 599,
      detail: typeof body === "string" && body.length < 300 ? body : "",
      instance: response ? new URL(response.url || "/", "http://x").pathname : "",
    } as Problem,
    retry,
  );
}

export function networkError(cause: unknown): ProblemError {
  return new ProblemError(
    {
      type: PROBLEM_BASE + "network",
      title: "No connection",
      status: 599,
      detail: cause instanceof Error ? cause.message : String(cause),
      instance: "",
    } as Problem,
    null,
  );
}

export function asProblem(error: unknown): ProblemError | null {
  return error instanceof ProblemError ? error : null;
}
