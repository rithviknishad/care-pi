import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { ChoiceCards } from "../choice-cards";
import { Explainer } from "../explainer";
import { ProblemCard } from "../problem-card";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function Presets() {
  const [value, setValue] = useState<string | null>("small-clinic");
  return (
    <ChoiceCards
      name="preset"
      legend="How many people use Care at the same time?"
      value={value}
      onChange={setValue}
      choices={[
        { value: "solo", title: "Just me", fit: "1 or 2 people." },
        { value: "small-clinic", title: "Small clinic", fit: "3 or 4 staff.", recommended: true },
        { value: "busy-counter", title: "Busy counter", fit: "5 to 10 staff.", disabled: true },
      ]}
    />
  );
}

describe("v2 interactive components", () => {
  it("ChoiceCards are native radios in a group with a legend and a Recommended badge", async () => {
    render(<Presets />);
    const group = screen.getByRole("group", { name: "How many people use Care at the same time?" });
    expect(group.tagName).toBe("FIELDSET");
    expect(screen.getByRole("radio", { name: /Small clinic/ })).toBeChecked();
    expect(screen.getByText("Recommended")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: /Just me/ }));
    expect(screen.getByRole("radio", { name: /Just me/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /Busy counter/ })).toBeDisabled();
  });

  it("ProblemCard has the plain title, the steps, Retry, Copy, and the technical details in a disclosure", async () => {
    const onRetry = vi.fn();
    render(
      <ProblemCard
        title="We could not reach the backup disk"
        description="The disk did not answer."
        steps={["Connect the disk.", "Press Try again."]}
        technical="status: 502"
        onRetry={onRetry}
        announce
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("We could not reach the backup disk");
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(onRetry).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
    const details = screen.getByText("Technical details").closest("details");
    expect(details).not.toHaveAttribute("open");
    await userEvent.click(screen.getByText("Technical details"));
    expect(details).toHaveAttribute("open");
    expect(screen.getByText("status: 502")).toBeInTheDocument();
  });

  it("Explainer draws the zones for a phone and a desktop and says the policy in one sentence", () => {
    const { container } = render(
      <Explainer
        policy={{ interval_min: 15, keep_within_days: 2, keep_daily: 14, keep_weekly: 8, keep_monthly: 6 }}
        testId="ex"
      />,
    );
    expect(container.querySelectorAll("figure svg[aria-hidden=true]").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByTestId("ex-summary")).toHaveTextContent(/^Every 15 minutes we save a copy\./);
    expect(screen.getByText("6 months ago")).toBeInTheDocument();
    expect(screen.getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "1 each month · 6 months",
      "1 each week · 2 months",
      "1 each day · 2 weeks",
      "Each copy · 2 days",
    ]);
  });

  it.each([
    [
      "12 months",
      { keep_within_days: 2, keep_daily: 14, keep_weekly: 8, keep_monthly: 12 },
      "1 year",
      "any month in the last year",
    ],
    [
      "1 month",
      { keep_within_days: 2, keep_daily: 14, keep_weekly: 8, keep_monthly: 1 },
      "1 month",
      "any month in the last month",
    ],
    [
      "1 week",
      { keep_within_days: 2, keep_daily: 7, keep_weekly: 1, keep_monthly: 0 },
      "1 week",
      "any week in the last week",
    ],
    [
      "1 day",
      { keep_within_days: 0, keep_daily: 1, keep_weekly: 0, keep_monthly: 0 },
      "1 day",
      "any day in the last day",
    ],
  ])(
    "Explainer names one unit with its number on the axis (%s), and without it in the sentence",
    (_, keep, length, clause) => {
      render(<Explainer policy={{ interval_min: 15, ...keep }} testId="ex" />);
      expect(screen.getByText(`${length} ago`)).toBeInTheDocument();
      expect(screen.getAllByRole("listitem")[0]).toHaveTextContent(new RegExp(` · ${length}$`));
      expect(screen.getByTestId("ex-summary")).toHaveTextContent(clause);
    },
  );
});
