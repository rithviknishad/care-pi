import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ClockBanner } from "@/app/layout/clock-banner";

const status = vi.hoisted(() => ({
  data: undefined as
    | {
        clock: { policy: string; synchronized: boolean; source: string; trusted: boolean };
        maintenance: { active: boolean };
      }
    | undefined,
}));

vi.mock("@/api/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/queries")>();
  return { ...actual, useMaintenance: () => ({ data: status.data }) };
});

function renderBanner(compact = false) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <ClockBanner enabled compact={compact} />
    </QueryClientProvider>,
  );
}

const clock = (source: string, trusted: boolean) => ({
  clock: { policy: "ntp", synchronized: trusted, source, trusted },
  maintenance: { active: false },
});

// S52: the banner "Clock not trusted" shows until NTP synchronizes the clock.
describe("clock banner", () => {
  beforeEach(() => {
    status.data = undefined;
  });

  it("shows the clock that the browser set at the claim", () => {
    status.data = clock("browser", false);
    renderBanner();
    const banner = screen.getByTestId("clock-banner");
    expect(banner).toHaveAttribute("role", "status");
    expect(banner).toHaveAttribute("data-source", "browser");
    expect(banner).toHaveTextContent("Clock not trusted");
    expect(banner).toHaveTextContent("came from the browser at the claim");
  });

  it("shows a clock without NTP and without the browser in the compact form", () => {
    status.data = clock("none", false);
    renderBanner(true);
    expect(screen.getByTestId("clock-banner")).toHaveTextContent("The device has no time server (NTP).");
  });

  it("hides when the clock is trusted, while the status loads, and during maintenance", () => {
    status.data = clock("ntp", true);
    const { container, rerender } = renderBanner();
    expect(container).toBeEmptyDOMElement();
    status.data = undefined;
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <ClockBanner enabled />
      </QueryClientProvider>,
    );
    expect(container).toBeEmptyDOMElement();
    status.data = { ...clock("browser", false), maintenance: { active: true } };
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <ClockBanner enabled />
      </QueryClientProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
