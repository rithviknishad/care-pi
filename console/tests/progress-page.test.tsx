import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

const api = vi.hoisted(() => ({ finish: vi.fn() }));
vi.mock("@/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/client")>();
  return {
    ...actual,
    api: { ...actual.api, POST: (path: string) => (path === "/setup/finish" ? api.finish() : null) },
    call: async <T,>(p: Promise<T>) => p,
  };
});

vi.mock("@/features/jobs/job-progress", () => ({
  JobProgress: ({
    jobId,
    onDone,
    children,
  }: {
    jobId: string;
    onDone: (job: Job) => void;
    children?: React.ReactNode;
  }) => {
    progress.onDone = onDone;
    return (
      <div>
        <p data-testid="job">{jobId}</p>
        {children}
      </div>
    );
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
          <Route path="/logs" element={<p data-testid="logs">logs</p>} />
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

describe("failed setup", () => {
  const failedJob = {
    id: "20261030T165115.744Z-setup",
    kind: "setup",
    state: "failed",
    started_at: "2026-10-30T16:51:15Z",
    finished_at: "2026-10-30T17:00:19Z",
    steps: [
      {
        index: 0,
        name: "validate: config.yaml",
        action: "validate",
        state: "succeeded",
        started_at: "x",
        finished_at: "x",
        detail: null,
      },
      {
        index: 9,
        name: "ensure-stanza",
        action: "ensure-stanza",
        state: "failed",
        started_at: "2026-10-30T16:55:00Z",
        finished_at: "2026-10-30T17:00:19Z",
        detail: "podman exec --user postgres care-db pg_isready --quiet: exit status 255",
      },
    ],
    error: {
      type: "https://care-pi.rithviknishad.dev/problems/internal",
      title: "Internal error",
      status: 500,
      detail: "apply failed at ensure-stanza: podman exec --user postgres care-db pg_isready --quiet: exit status 255",
      instance: "/api/v1/jobs/20261030T165115.744Z-setup",
    },
    result: null,
  } as unknown as Job;

  it("shows the failed step in plain words, the problem, a retry, a copy, and the log of the unit", async () => {
    setup.data = { state: "setup", job_id: "20261030T165115.744Z-setup" };
    renderPage();
    act(() => progress.onDone?.(failedJob));
    expect(screen.getByTestId("setup-failed-step")).toHaveTextContent(
      "This step failed: Prepare the database backups.",
    );
    expect(screen.getByTestId("setup-failed-detail")).toHaveTextContent("apply failed at ensure-stanza");
    expect(screen.getByTestId("setup-failed-log")).toHaveAttribute("href", "/logs?unit=db");
    expect(screen.getByTestId("setup-failed-log")).toHaveTextContent("Show the log of care-db");
    expect(screen.getByRole("button", { name: "Copy details" })).toBeInTheDocument();
    expect(screen.queryByText(/fix the problem/i)).toBeNull();
    // The raw error is in the collapsed technical details.
    expect(screen.queryByTestId("setup-failed-technical")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Technical details" }));
    expect(screen.getByTestId("setup-failed-technical")).toHaveTextContent("Step: ensure-stanza");
    expect(screen.getByTestId("setup-failed-technical")).toHaveTextContent("problems/internal 500 Internal error");

    // Retry runs POST /setup/finish again and follows the new job.
    api.finish.mockResolvedValueOnce({ id: "20261030T171000.000Z-setup", kind: "setup", state: "running" });
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(screen.getByTestId("job")).toHaveTextContent("20261030T171000.000Z-setup"));
    expect(api.finish).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("setup-failed")).toBeNull();
  });
});
