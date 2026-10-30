import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { api } from "@/api/client";
import { useJobStream } from "@/features/jobs/use-job-stream";

vi.mock("@/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/client")>();
  return { ...actual, api: { GET: vi.fn() } };
});

const PROBLEM = "https://care-pi.rithviknishad.dev/problems/";
const get = api.GET as unknown as Mock;

function problem(slug: string, status: number) {
  const body = { type: PROBLEM + slug, title: slug, status, detail: `detail of ${slug}`, instance: "/api/v1/jobs/j" };
  const headers: Record<string, string> = { "Content-Type": "application/problem+json" };
  if (status === 429 || status === 503) headers["Retry-After"] = "2";
  return { error: body, response: new Response(JSON.stringify(body), { status, headers }) };
}

async function tick(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("useJobStream poll fallback", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    // jsdom has no EventSource; the hook then reads the JSON form of the events.
    vi.stubGlobal("EventSource", undefined);
    get.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("stops on a permanent 4xx problem and gives the problem to the UI", async () => {
    get.mockResolvedValue(problem("job-not-found", 404));
    const { result } = renderHook(() => useJobStream("20261030T101500.123Z-apply"));
    await tick(10);
    expect(result.current.state).toBe("error");
    expect(result.current.problem?.slug).toBe("job-not-found");
    await tick(10_000);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["rate-limited", 429],
    ["unavailable", 503],
  ])("tries again after %s (%i)", async (slug, status) => {
    get.mockResolvedValueOnce(problem(slug, status)).mockResolvedValue({
      data: { events: [], done: true },
      response: Response.json({ events: [], done: true }),
    });
    const { result } = renderHook(() => useJobStream("20261030T101500.123Z-apply"));
    await tick(10);
    expect(result.current.state).toBe("poll");
    expect(result.current.problem).toBeNull();
    await tick(2_000);
    expect(get).toHaveBeenCalledTimes(2);
    await tick(10_000);
    expect(get).toHaveBeenCalledTimes(2);
  });
});
