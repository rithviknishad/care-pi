import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { StepList, StepProgress, type StepItem } from "../step-list";

const t0 = 1_700_000_000_000;
const steps: StepItem[] = [
  { id: "a", title: "Check the settings", state: "done", startedAt: t0, endedAt: t0 + 3000 },
  {
    id: "b",
    title: "Start the database",
    state: "running",
    startedAt: Date.now() - 5000,
    expectedSeconds: 220,
    log: "db ready",
  },
  { id: "c", title: "Start Care", state: "pending" },
];

describe("StepList", () => {
  it("names the state of each step for screen readers and shows the time in mono", () => {
    render(<StepList steps={steps} testId="steps" />);
    const rows = screen.getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("Done: Check the settings");
    expect(rows[0]).toHaveTextContent("00:03");
    expect(rows[1]).toHaveTextContent("Running: Start the database");
    expect(rows[1]).toHaveTextContent("Usually about 4 min");
    expect(rows[2]).toHaveTextContent("Waiting: Start Care");
    expect(screen.getByTestId("steps")).toHaveAttribute("aria-busy", "true");
  });

  it("reserves rows before the steps come (append only)", () => {
    const { container } = render(<StepList steps={steps.slice(0, 1)} reserve={4} />);
    expect(container.querySelectorAll("li")).toHaveLength(4);
    expect(container.querySelectorAll("li[aria-hidden=true]")).toHaveLength(3);
  });

  it("opens the log of a step with its Copy", async () => {
    render(<StepList steps={steps} />);
    const toggle = screen.getByRole("button", { name: "Show the log: Start the database" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(toggle);
    expect(screen.getByRole("button", { name: "Hide the log: Start the database" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getByText("db ready")).toBeInTheDocument();
  });

  it("pops the check mark only when a step ends while the list is open", () => {
    const { container, rerender } = render(<StepList steps={steps} />);
    expect(container.querySelector(".motion-ok\\:animate-v2-pop")).toBeNull();
    rerender(<StepList steps={[steps[0], { ...steps[1], state: "done", endedAt: Date.now() }, steps[2]]} />);
    expect(container.querySelectorAll(".motion-ok\\:animate-v2-pop")).toHaveLength(1);
  });
});

describe("StepProgress", () => {
  const bar = () => screen.getByRole("progressbar");

  it("shows the step of the planned count and a bar of the steps that ended", () => {
    render(<StepProgress steps={steps} total={12} succeeded={null} />);
    expect(screen.getByText("Step 2 of 12")).toBeInTheDocument();
    expect(bar()).toHaveAttribute("data-state", "determinate");
    expect(bar()).toHaveAttribute("aria-valuenow", "8");
  });

  it("without a planned count shows Step N and an indeterminate bar", () => {
    render(<StepProgress steps={steps} total={null} succeeded={null} />);
    expect(screen.getByText("Step 2")).toBeInTheDocument();
    expect(screen.queryByText(/of/)).toBeNull();
    expect(bar()).toHaveAttribute("data-state", "indeterminate");
    expect(bar()).not.toHaveAttribute("aria-valuenow");
  });

  it("is never full before the job is done, and never shows a step after the total", () => {
    const ended = steps.map((s) => ({ ...s, state: "done" as const }));
    const { rerender } = render(<StepProgress steps={ended} total={3} succeeded={null} />);
    expect(bar()).toHaveAttribute("aria-valuenow", "99");
    rerender(<StepProgress steps={ended} total={3} succeeded />);
    expect(bar()).toHaveAttribute("aria-valuenow", "100");
    // A job that runs more steps than it planned (a rollback): the total grows with them.
    rerender(
      <StepProgress steps={[...ended, { id: "d", title: "Roll back", state: "running" }]} total={3} succeeded={null} />,
    );
    expect(screen.getByText("Step 4 of 4")).toBeInTheDocument();
  });
});
