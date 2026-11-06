import { render, screen, within } from "@testing-library/react";
import { HomeIcon, LifeBuoyIcon, LockIcon, ScrollTextIcon, SettingsIcon, UploadIcon } from "lucide-react";
import { MemoryRouter } from "react-router";
import { beforeAll, describe, expect, it, vi } from "vitest";
import { AppShell, type ShellNavItem } from "../app-shell";
import { Focus } from "../focus";
import { Stage } from "../stage";
import { ThemeToggle } from "../theme-toggle";
import { Toaster } from "../toast";

const nav: ShellNavItem[] = [
  { to: "/dashboard", label: "Home", icon: HomeIcon, primary: true },
  { to: "/backups", label: "Backups", icon: UploadIcon, primary: true },
  { to: "/settings", label: "Settings", icon: SettingsIcon, primary: true },
  { to: "/logs", label: "Logs", icon: ScrollTextIcon },
  { to: "/security", label: "Security", icon: LockIcon },
  { to: "/support", label: "Support", icon: LifeBuoyIcon },
];

// jsdom has no matchMedia (careui's use-mobile and sonner read it).
beforeAll(() => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
});

describe("v2 layouts", () => {
  it("AppShell has the rail and a bottom bar with the primary places and More", () => {
    render(
      <MemoryRouter initialEntries={["/backups"]}>
        <AppShell
          nav={nav}
          deviceName="care.local"
          status={<span>Ready</span>}
          banner={<span>Banner</span>}
          actions={<ThemeToggle />}
        >
          <p>Page</p>
        </AppShell>
      </MemoryRouter>,
    );
    const [rail, bar] = screen.getAllByRole("navigation", { name: "Main" });
    expect(within(rail).getAllByRole("link")).toHaveLength(6);
    expect(within(rail).getByRole("link", { name: "Backups" })).toHaveAttribute("aria-current", "page");
    expect(
      within(bar)
        .getAllByRole("link")
        .map((a) => a.textContent),
    ).toEqual(["Home", "Backups", "Settings"]);
    expect(within(bar).getByRole("button", { name: "More" })).toBeInTheDocument();
    expect(screen.getByTestId("device-name")).toHaveTextContent("care.local");
    expect(screen.getByText("Banner")).toBeInTheDocument();
    expect(screen.getByRole("main")).toHaveAttribute("id", "main");
    expect(screen.getByRole("button", { name: "Theme: System" })).toBeInTheDocument();
  });

  it("AppShell minimal has no rail and no bottom bar, only the Menu button; the device name has a skeleton", () => {
    const { container } = render(
      <MemoryRouter>
        <AppShell nav={nav} navMode="minimal">
          <p>Page</p>
        </AppShell>
      </MemoryRouter>,
    );
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.getByRole("button", { name: "Open the menu" })).toBeInTheDocument();
    expect(
      within(screen.getByTestId("device-name")).getByText("", { selector: "[data-slot=skeleton]" }),
    ).toBeInTheDocument();
    expect(container.querySelector("aside")).toBeNull();
  });

  it("Stage shows the step of the total, the steps, the task, and what happens next", () => {
    render(
      <MemoryRouter>
        <Stage
          steps={[
            { id: "a", label: "Clinic name", href: "/setup/identity" },
            { id: "b", label: "Size" },
            { id: "c", label: "Backups" },
          ]}
          current={1}
          railTitle="Set up your Care box"
          title="How big is your clinic?"
          primary={<button>Continue</button>}
          back={<button>Back</button>}
          next="Next, you select where the backups go."
        >
          <p>Task</p>
        </Stage>
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "How big is your clinic?" })).toBeInTheDocument();
    expect(screen.getAllByText("Step 2 of 3").length).toBeGreaterThan(0);
    expect(screen.getByRole("progressbar", { name: "Step 2 of 3" })).toHaveAttribute("aria-valuenow", "33");
    const steps = screen.getByRole("navigation", { name: "Setup steps" });
    expect(within(steps).getByRole("link", { name: /Clinic name/ })).toHaveAttribute("href", "/setup/identity");
    expect(within(steps).getByText("Size").closest("li")).toHaveAttribute("aria-current", "step");
    expect(screen.getByText("Next, you select where the backups go.")).toBeInTheDocument();
  });

  it("Focus has one card with the title in the main region", () => {
    render(
      <Focus title="Claim this device" description="Prove that you own the device." illustration="pi-box">
        <button>Claim</button>
      </Focus>,
    );
    const main = screen.getByRole("main");
    expect(within(main).getByRole("heading", { level: 1, name: "Claim this device" })).toBeInTheDocument();
    expect(within(main).getByRole("button", { name: "Claim" })).toBeInTheDocument();
  });

  it("Toaster renders the sonner region", () => {
    const { container } = render(<Toaster />);
    expect(container.querySelector("section")).toHaveAttribute("aria-label", expect.stringContaining("Notifications"));
  });
});
