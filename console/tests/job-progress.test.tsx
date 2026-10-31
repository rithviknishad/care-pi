import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { JobStep } from "@/api/types";
import { JobProgress } from "@/features/jobs/job-progress";
import { emptyJobView, type JobView } from "@/features/jobs/job-state";

const stream = vi.hoisted(() => ({ view: null as JobView | null }));
vi.mock("@/features/jobs/use-job-stream", () => ({
  useJobStream: () => ({ view: stream.view, state: "open", problem: null }),
}));

const step = (index: number, action: string | null, name: string, state: JobStep["state"]): JobStep => ({
  index,
  name,
  action,
  state,
  started_at: "2026-10-30T17:24:31Z",
  finished_at: state === "running" ? null : "2026-10-30T17:24:32Z",
  detail: null,
});

describe("job progress", () => {
  it("shows the operator text of each step, and movement while the lock resolves", () => {
    stream.view = {
      ...emptyJobView(),
      steps: [
        step(0, "host-check", "host-check: required checks", "succeeded"),
        step(
          1,
          "pull-images",
          "pull-images: localhost/care-pi/caddy:2 ghcr.io/ohcnetwork/care@sha256:77fe743e51a6eb322e89e18183c6012fa6ab8efdd447e8ff59dfb387e6da6ec3",
          "succeeded",
        ),
        step(2, "scale-down", "scale-down: solo", "succeeded"),
        step(3, "resolve-lock", "resolve-lock: image digests and git refs", "running"),
      ],
      logs: [
        {
          id: 9,
          time: "2026-10-30T17:24:40Z",
          step_index: 3,
          stream: "agent",
          line: "resolve ghcr.io/ohcnetwork/care:latest … 77fe743e51a6",
        },
      ],
    };
    render(<JobProgress jobId="20261030T172431.405Z-setup" />);
    const rows = screen.getAllByTestId("job-step");
    expect(within(rows[0]!).getByTestId("job-step-title")).toHaveTextContent("Check the device");
    expect(rows[0]).toHaveAttribute("title", "host-check: required checks");
    expect(within(rows[1]!).getByTestId("job-step-title")).toHaveTextContent("Download the container images");
    expect(rows[1]).toHaveTextContent("caddy:2, care@sha256:77fe743e51a6");
    // An unknown key: the raw name in a code element.
    expect(within(rows[2]!).getByTestId("job-step-raw")).toHaveTextContent("scale-down: solo");
    expect(within(rows[2]!).getByTestId("job-step-raw").tagName).toBe("CODE");
    // The running resolution: the expected time, the last resolve line, and an indeterminate bar.
    expect(within(rows[3]!).getByTestId("job-step-title")).toHaveTextContent(
      "Resolve the versions of the images (about 30 s)",
    );
    expect(rows[3]).toHaveTextContent("resolve ghcr.io/ohcnetwork/care:latest … 77fe743e51a6");
    const bar = within(rows[3]!).getByTestId("job-step-indeterminate");
    expect(bar).toHaveAttribute("data-indeterminate");
    expect(screen.queryByText(/^ensure-dirs|^pull-images:|^host-check:/)).toBeNull();
  });
});
