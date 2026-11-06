import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { StepList, type StepItem } from "../step-list";

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
