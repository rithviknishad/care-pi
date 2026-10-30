import { describe, expect, it, vi } from "vitest";
import { call, createApi, getCsrfToken, onUnauthenticated, setCsrfToken } from "@/api/client";
import { ProblemError, toProblemError } from "@/api/problem";

const BASE = "https://care.local:8443/api/v1";
const PROBLEM = "https://care-pi.rithviknishad.dev/problems/";

function problem(
  slug: string,
  status: number,
  extra: Record<string, unknown> = {},
  headers: Record<string, string> = {},
) {
  return new Response(
    JSON.stringify({
      type: PROBLEM + slug,
      title: slug,
      status,
      detail: `detail of ${slug}`,
      instance: "/api/v1/x",
      ...extra,
    }),
    { status, headers: { "Content-Type": "application/problem+json", ...headers } },
  );
}

function setup(response: () => Response) {
  const requests: Request[] = [];
  const fetch = vi.fn(async (req: Request) => {
    requests.push(req);
    return response();
  });
  return { api: createApi({ fetch, baseUrl: BASE }), requests };
}

describe("api client", () => {
  it("sends credentials and no CSRF header on a GET", async () => {
    setCsrfToken("t".repeat(43));
    const { api, requests } = setup(() => Response.json({ status: "ok", time: "2026-10-30T10:00:00Z" }));
    await call(api.GET("/health"));
    expect(requests[0]?.credentials).toBe("include");
    expect(requests[0]?.headers.get("X-CSRF-Token")).toBeNull();
    expect(requests[0]?.headers.get("Accept")).toBe("application/json");
  });

  it("sends the CSRF token and Content-Type on each unsafe request", async () => {
    setCsrfToken("c".repeat(43));
    const { api, requests } = setup(
      () => new Response(null, { status: 202, headers: { "Content-Type": "application/json" } }),
    );
    await api.POST("/setup/finish");
    await api.DELETE("/security/sessions/{session_ref}", { params: { path: { session_ref: "3a9f0c2e7b1d4e68" } } });
    for (const r of requests) {
      expect(r.headers.get("X-CSRF-Token")).toBe("c".repeat(43));
      expect(r.headers.get("Content-Type")).toBe("application/json");
    }
  });

  it("keeps the Content-Type of a merge patch", async () => {
    setCsrfToken("p".repeat(43));
    const { api, requests } = setup(() => Response.json({}));
    // openapi-fetch types only application/json bodies. The body of this operation is
    // application/merge-patch+json, so the test gives the body without its type.
    const patch = { identity: { display_name: "X" } } as never;
    await api.PATCH("/config", {
      params: { header: { "If-Match": '"9f2c1a7e0b3d4c55"' } },
      body: patch,
      headers: { "Content-Type": "application/merge-patch+json" },
    });
    expect(requests[0]?.headers.get("Content-Type")).toBe("application/merge-patch+json");
    expect(requests[0]?.headers.get("If-Match")).toBe('"9f2c1a7e0b3d4c55"');
  });

  it("calls the handler and drops the token on 401 not-authenticated", async () => {
    const handler = vi.fn();
    onUnauthenticated(handler);
    setCsrfToken("x".repeat(43));
    const { api } = setup(() => problem("not-authenticated", 401));
    await expect(call(api.GET("/status"))).rejects.toBeInstanceOf(ProblemError);
    expect(handler).toHaveBeenCalledTimes(1);
    expect(getCsrfToken()).toBeNull();
  });

  it("does not call the handler on 401 bad-credentials or totp-required", async () => {
    const handler = vi.fn();
    onUnauthenticated(handler);
    for (const slug of ["bad-credentials", "totp-required"]) {
      const { api } = setup(() => problem(slug, 401));
      const err = await call(api.POST("/session/login", { body: { password: "x" } })).catch((e: unknown) => e);
      expect(err).toBeInstanceOf(ProblemError);
      expect((err as ProblemError).slug).toBe(slug);
    }
    expect(handler).not.toHaveBeenCalled();
  });

  it("parses a problem with Retry-After and field errors", async () => {
    const { api } = setup(() =>
      problem(
        "locked-out",
        429,
        { retry_after_s: 900, errors: [{ pointer: "/new_password", message: "too short", code: "minLength" }] },
        { "Retry-After": "899" },
      ),
    );
    const err = (await call(
      api.POST("/session/claim", { body: { method: "claim-code", secret: "x", new_password: "y" } }),
    ).catch((e: unknown) => e)) as ProblemError;
    expect(err.slug).toBe("locked-out");
    expect(err.status).toBe(429);
    expect(err.retryAfter).toBe(899);
    expect(err.is("locked-out", "rate-limited")).toBe(true);
    expect(err.fieldError("/new_password")).toBe("too short");
  });

  it("maps a body that is not a problem and a network error", async () => {
    const plain = toProblemError("Bad Gateway", new Response("Bad Gateway", { status: 502 }));
    expect(plain.slug).toBe("unknown");
    expect(plain.status).toBe(502);
    const fetch = vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    });
    const api = createApi({ fetch, baseUrl: BASE });
    const err = (await call(api.GET("/status")).catch((e: unknown) => e)) as ProblemError;
    expect(err.slug).toBe("network");
  });
});
