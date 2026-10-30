import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProblemAlert } from "@/components/app/problem-alert";
import { RecoveryCodesPanel } from "@/components/app/recovery-codes";
import { ProblemError } from "@/api/problem";
import type { Problem } from "@/api/types";

const CODES = [
  "7KQ2M-X9PDA",
  "1B3C5-D7E9F",
  "GH2JK-4MN6P",
  "QR8ST-0VW2X",
  "YZ4A6-B8C0D",
  "E2F4G-6H8J0",
  "K1M3N-5P7Q9",
  "R0S2T-4V6W8",
];

describe("RecoveryCodesPanel", () => {
  it("shows 8 codes and continues only after the check", async () => {
    const onContinue = vi.fn();
    render(<RecoveryCodesPanel codes={CODES} generatedAt="2026-10-30T10:00:00Z" onContinue={onContinue} />);
    expect(screen.getAllByRole("listitem")).toHaveLength(8);
    const button = screen.getByRole("button", { name: "Continue" });
    expect(button).toBeDisabled();
    await userEvent.click(screen.getByText("I saved these codes in a safe place."));
    expect(button).toBeEnabled();
    await userEvent.click(button);
    expect(onContinue).toHaveBeenCalledOnce();
  });
});

describe("ProblemAlert", () => {
  it("shows the console text of the type, the detail, and the missing steps", () => {
    const problem = {
      type: "https://care-pi.rithviknishad.dev/problems/setup-incomplete",
      title: "Setup is not complete",
      status: 409,
      detail: "Complete the steps in missing_steps first.",
      instance: "/api/v1/setup/finish",
      missing_steps: ["hardware", "care-admin"],
    } as Problem;
    render(<ProblemAlert error={new ProblemError(problem)} />);
    expect(screen.getByText("Setup is not complete.")).toBeInTheDocument();
    expect(screen.getByText("Hardware check, Care administrator")).toBeInTheDocument();
  });
});
