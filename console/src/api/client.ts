import createClient, { type Middleware } from "openapi-fetch";
import { networkError, slugOf, toProblemError } from "./problem";
import type { paths } from "./types";

// The API is on the origin of the console (S48): https://<host>:8443/api/v1.
export const API_BASE = "/api/v1";

const UNSAFE = new Set(["POST", "PUT", "PATCH", "DELETE"]);

// The CSRF token lives in memory only (docs/api/README.md section 3).
// After a page reload, the console reads it again with GET /session.
let csrfToken: string | null = null;

export function setCsrfToken(token: string | null): void {
  csrfToken = token;
}

export function getCsrfToken(): string | null {
  return csrfToken;
}

type UnauthenticatedHandler = (request: Request) => void;
let unauthenticated: UnauthenticatedHandler = () => {};

// The app sets this handler. The API calls it for each 401 with the type not-authenticated.
// It does not call it for bad-credentials or totp-required (also 401).
export function onUnauthenticated(handler: UnauthenticatedHandler): void {
  unauthenticated = handler;
}

const middleware: Middleware = {
  onRequest({ request }) {
    if (!request.headers.has("Accept")) request.headers.set("Accept", "application/json");
    if (UNSAFE.has(request.method)) {
      if (csrfToken) request.headers.set("X-CSRF-Token", csrfToken);
      // The agent needs Content-Type on each unsafe request, also without a body (415 else).
      if (!request.headers.has("Content-Type")) request.headers.set("Content-Type", "application/json");
    }
    return request;
  },
  async onResponse({ request, response }) {
    if (response.status === 401) {
      try {
        const body = (await response.clone().json()) as { type?: string };
        if (slugOf(body.type) === "not-authenticated") {
          setCsrfToken(null);
          unauthenticated(request);
        }
      } catch {
        // The body is not a problem. The caller handles the status.
      }
    }
    return response;
  },
};

export interface ApiOptions {
  fetch?: (request: Request) => Promise<Response>;
  baseUrl?: string;
}

export function createApi(options: ApiOptions = {}) {
  const client = createClient<paths>({
    baseUrl: options.baseUrl ?? API_BASE,
    credentials: "include",
    ...(options.fetch ? { fetch: options.fetch } : {}),
  });
  client.use(middleware);
  return client;
}

// api: the operations below /api/v1. root: the public operations below /_care-pi.
export const api = createApi();
export const root = createApi({ baseUrl: "" });

type Result<T> = { data?: T; error?: unknown; response: Response };

// Returns the data, or throws a ProblemError.
export async function call<T>(request: Promise<Result<T>>): Promise<T> {
  return (await callFull(request)).data;
}

export async function callFull<T>(request: Promise<Result<T>>): Promise<{ data: T; response: Response }> {
  let result: Result<T>;
  try {
    result = await request;
  } catch (cause) {
    throw networkError(cause);
  }
  if (result.error !== undefined || !result.response.ok) throw toProblemError(result.error, result.response);
  return { data: result.data as T, response: result.response };
}

// The paths of the API for plain links and EventSource (both need a URL, not a fetch).
export function apiUrl(path: string, query?: Record<string, string | number | boolean | undefined>): string {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query ?? {})) if (v !== undefined) params.set(k, String(v));
  const qs = params.toString();
  return `${API_BASE}${path}${qs ? `?${qs}` : ""}`;
}
