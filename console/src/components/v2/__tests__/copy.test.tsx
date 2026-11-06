import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { buildCopyText, copyHeaderLines } from "../copy";
import { CopyButton } from "../copy-button";
import { CopyHeaderProvider } from "../copy-provider";
import { MonoBlock } from "../mono-block";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  return writeText;
}

afterEach(() => vi.clearAllMocks());

describe("copy (S68)", () => {
  it("puts the device, the agent version, the time, and the page in the header", () => {
    const now = new Date("2026-11-06T04:17:07Z");
    expect(copyHeaderLines({ device: "care.local", version: "0.1.1", page: "/logs" }, now)).toEqual([
      "Device: care.local",
      "Agent version: 0.1.1",
      "Time: 2026-11-06T04:17:07.000Z",
      "Page: /logs",
    ]);
    expect(buildCopyText("body", { device: "care.local" }, "Title", now)).toBe(
      "Device: care.local\nAgent version: —\nTime: 2026-11-06T04:17:07.000Z\n\nTitle\n\nbody",
    );
    expect(buildCopyText("only the value")).toBe("only the value");
  });

  it("copies a value without a header, then says Copied", async () => {
    const writeText = mockClipboard();
    render(<CopyButton text="21:69:AE" label="Copy the fingerprint" />);
    await userEvent.click(screen.getByRole("button", { name: "Copy the fingerprint" }));
    expect(writeText).toHaveBeenCalledWith("21:69:AE");
    expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith("Copied", undefined);
  });

  it("copies a report with the header of the shell", async () => {
    const writeText = mockClipboard();
    render(
      <CopyHeaderProvider value={{ device: "care.local", version: "0.1.1 (64caa26)" }}>
        <CopyButton text="log line" report title="Log of care-api" />
      </CopyHeaderProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    const text = writeText.mock.calls[0]?.[0] as string;
    expect(text).toMatch(/^Device: care\.local\nAgent version: 0\.1\.1 \(64caa26\)\nTime: .+\nPage: \//);
    expect(text).toMatch(/\n\nLog of care-api\n\nlog line$/);
    expect(toast.success).toHaveBeenCalledWith("Copied", { description: expect.stringContaining("support") });
  });

  it("shows data in a mono block with a copy button in the corner", async () => {
    const writeText = mockClipboard();
    render(<MonoBlock label="Fingerprint (SHA-256)" text="AB:CD" lines={2} />);
    const block = screen.getByLabelText("Fingerprint (SHA-256)");
    expect(block.tagName).toBe("PRE");
    expect(block).toHaveTextContent("AB:CD");
    expect(block).toHaveAttribute("tabindex", "0");
    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("AB:CD");
  });
});
