import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { Job } from "@/api/types";
import ProgressPage from "@/features/setup/progress-page";

const setup = vi.hoisted(() => ({
  data: { state: "applying", job_id: "20261030T165115.744Z-setup" as string | null },
}));
const progress = vi.hoisted(() => ({ onDone: null as ((job: Job) => void) | null }));

vi.mock("@/api/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/queries")>();
  return { ...actual, useSetup: () => ({ isPending: false, data: setup.data }) };
});

vi.mock("@/features/jobs/job-progress", () => ({
  JobProgress: ({ jobId, onDone }: { jobId: string; onDone: (job: Job) => void }) => {
    progress.onDone = onDone;
    return <p data-testid="job">{jobId}</p>;
  },
}));

function Done() {
  const job = (useLocation().state as { job?: Job } | null)?.job;
  const result = job?.result as { care_username?: string } | undefined;
  return <p data-testid="done">{result?.care_username ?? "no job result"}</p>;
}

function renderPage() {
  const ui = () => (
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={["/setup/progress"]}>
        <Routes>
          <Route path="/setup/progress" element={<ProgressPage />} />
          <Route path="/setup/done" element={<Done />} />
          <Route path="/setup" element={<p>wizard</p>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
  const r = render(ui());
  return { rerender: () => r.rerender(ui()) };
}

describe("setup progress page", () => {
  it("keeps the job after GET /setup clears job_id, so the hand-off page gets the job result", () => {
    const { rerender } = renderPage();
    expect(screen.getByTestId("job")).toHaveTextContent("20261030T165115.744Z-setup");

    // The job succeeded: GET /setup now gives the state ready and no job_id. The done event comes after it.
    setup.data = { state: "ready", job_id: null };
    rerender();
    expect(screen.getByTestId("job")).toHaveTextContent("20261030T165115.744Z-setup");

    const job = {
      id: "20261030T165115.744Z-setup",
      kind: "setup",
      state: "succeeded",
      result: { kind: "setup", care_username: "w4admin" },
    } as unknown as Job;
    act(() => progress.onDone?.(job));
    expect(screen.getByTestId("done")).toHaveTextContent("w4admin");
  });
});
