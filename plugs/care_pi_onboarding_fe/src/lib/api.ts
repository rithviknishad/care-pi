import type { Status, StepName, StepResult } from "@/lib/wizard";
import { HttpError, HttpMethod, request } from "@/lib/request";

const BASE = "/api/care_pi_onboarding";

export type Preview = Record<string, any> & {
  step: StepName;
  state: string;
  result: Record<string, any>;
};

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

function detailOf(error: HttpError): string {
  const cause = error.cause as Record<string, any> | undefined;
  if (cause && typeof cause.detail === "string") return cause.detail;
  const first = Array.isArray(cause?.errors) ? cause.errors[0] : undefined;
  if (first && typeof first.msg === "string") return first.msg;
  return "";
}

/** Turns an HTTP failure into one sentence for the screen. The caller gets the code for its own text. */
function toApiError(error: unknown): ApiError {
  if (error instanceof HttpError) {
    return new ApiError(error.status, detailOf(error));
  }
  return new ApiError(0, "");
}

async function call<T>(
  path: string,
  method: HttpMethod,
  options: {
    body?: unknown;
    query?: Record<string, string | undefined>;
    signal?: AbortSignal;
  } = {},
): Promise<T> {
  try {
    return (await request(
      { path, method, TResponse: {} as T },
      {
        body: options.body,
        queryParams: options.query,
        signal: options.signal,
      },
    )) as T;
  } catch (error) {
    throw toApiError(error);
  }
}

/** apply and skip answer 400 with a step result when the step fails. That is a result, not an error. */
async function stepCall(
  path: string,
  body: Record<string, unknown>,
): Promise<StepResult> {
  try {
    return await request(
      { path, method: HttpMethod.POST, TResponse: {} as StepResult },
      { body },
    );
  } catch (error) {
    if (
      error instanceof HttpError &&
      error.status === 400 &&
      (error.cause as Record<string, unknown> | undefined)?.status === "failed"
    ) {
      return error.cause as unknown as StepResult;
    }
    throw toApiError(error);
  }
}

export const onboardingApi = {
  status: (facility?: string | null, signal?: AbortSignal) =>
    call<Status>(`${BASE}/status/`, HttpMethod.GET, {
      query: { facility: facility ?? undefined },
      signal,
    }),
  preview: (step: StepName, facility?: string | null, signal?: AbortSignal) =>
    call<Preview>(`${BASE}/steps/${step}/preview/`, HttpMethod.GET, {
      query: { facility: facility ?? undefined },
      signal,
    }),
  apply: (step: StepName, body: Record<string, unknown>) =>
    stepCall(`${BASE}/steps/${step}/apply/`, body),
  skip: (step: StepName, facility?: string | null) =>
    stepCall(`${BASE}/steps/${step}/skip/`, facility ? { facility } : {}),
  removeDemo: (facility: string) =>
    call<StepResult>(`${BASE}/demo_data/`, HttpMethod.DELETE, {
      query: { facility },
    }),
  closeWizard: (reason: "finished" | "later") =>
    call<{ wizard: Status["wizard"] }>(
      `${BASE}/wizard/close/`,
      HttpMethod.POST,
      { body: { reason } },
    ),
};
