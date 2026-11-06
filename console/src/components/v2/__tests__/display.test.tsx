import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DashboardGrid } from "../dashboard-grid";
import { EmptyState } from "../empty-state";
import { Hero } from "../hero";
import { Illustration } from "../illustration";
import { illustrationNames } from "../illustrations";
import { KeyValueList } from "../key-value-list";
import { MetricBar } from "../metric-bar";
import { Panel, WhatNext } from "../panel";
import { ProgressRing } from "../progress-ring";
import { StatCard } from "../stat-card";
import { StatusChip } from "../status-chip";
import { Timeline } from "../timeline";
import { meterTone } from "../tone";

const MIB = 1024 * 1024;

describe("v2 display components", () => {
  it("StatusChip says the state in words, with the tone and a fixed width", () => {
    render(
      <StatusChip tone="ok" width={10} testId="chip">
        Ready
      </StatusChip>,
    );
    const chip = screen.getByTestId("chip");
    expect(chip).toHaveTextContent("Ready");
    expect(chip).toHaveAttribute("data-tone", "ok");
    expect(chip.style.minWidth).toBe("calc(10ch + 1.75rem)");
  });

  it("ProgressRing is a progressbar with a value, or without one while the end is not known", () => {
    const { rerender } = render(<ProgressRing value={0.25} label="Step 3 of 8" />);
    expect(screen.getByRole("progressbar", { name: "Step 3 of 8" })).toHaveAttribute("aria-valuenow", "25");
    rerender(<ProgressRing value={null} label="Running" />);
    expect(screen.getByRole("progressbar", { name: "Running" })).not.toHaveAttribute("aria-valuenow");
  });

  it("KeyValueList shows a skeleton of each row, then the values and a copy button", () => {
    const items = [
      { label: "Model", value: "Raspberry Pi 5" },
      { label: "Address", value: "192.168.1.10", mono: true, copy: "192.168.1.10" },
    ];
    const { container, rerender } = render(<KeyValueList items={items} loading />);
    expect(container.querySelectorAll("[data-slot=skeleton]")).toHaveLength(2);
    expect(screen.queryByText("Raspberry Pi 5")).toBeNull();
    rerender(<KeyValueList items={items} />);
    expect(screen.getByText("Model").tagName).toBe("DT");
    expect(screen.getByText("192.168.1.10").closest("dd")).toHaveClass("type-data");
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
  });

  it("StatCard keeps its rows: skeletons first, then the value, the unit, and the hint", () => {
    const { rerender } = render(
      <StatCard label="Free disk" value="103" unit="GiB" hint="Enough" loading testId="card" />,
    );
    expect(screen.getByTestId("card")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByTestId("card")).toHaveClass("h-32");
    rerender(<StatCard label="Free disk" value="103" unit="GiB" hint="Enough" testId="card" />);
    expect(screen.getByTestId("card")).toHaveTextContent("Free disk103GiBEnough");
  });

  it("MetricBar is a meter with a plain text value and a tone from the thresholds", () => {
    render(<MetricBar label="Database" used={313 * MIB} cap={384 * MIB} testId="bar" />);
    const meter = screen.getByRole("meter", { name: "Database" });
    expect(meter).toHaveAttribute("aria-valuetext", "313 MiB of 384 MiB");
    expect(screen.getByTestId("bar")).toHaveAttribute("data-tone", "warn");
    expect(meterTone(0.5)).toBe("ok");
    expect(meterTone(0.96)).toBe("danger");
  });

  it("DashboardGrid, Panel, and WhatNext lay out their children", () => {
    render(
      <DashboardGrid>
        <Panel
          title="Updates"
          description="A new version is ready."
          footer={<WhatNext>Care stops for 4 min.</WhatNext>}
        >
          <p>Body</p>
        </Panel>
      </DashboardGrid>,
    );
    expect(screen.getByRole("heading", { level: 2, name: "Updates" })).toBeInTheDocument();
    expect(screen.getByText("Care stops for 4 min.")).toBeInTheDocument();
  });

  it("Timeline lists the events with a datetime", () => {
    render(<Timeline items={[{ id: "1", time: "09:30", datetime: "2026-11-06T09:30:00Z", title: "Setup started" }]} />);
    const item = screen.getByRole("listitem");
    expect(within(item).getByText("09:30").tagName).toBe("TIME");
    expect(within(item).getByText("Setup started")).toBeInTheDocument();
  });

  it("Illustration inlines each of the 8 drawings, as decoration or as an image with a label", () => {
    expect(illustrationNames).toHaveLength(8);
    const { container } = render(
      <>
        {illustrationNames.map((n) => (
          <Illustration key={n} name={n} />
        ))}
        <Illustration name="clinic" label="A clinic" />
      </>,
    );
    expect(container.querySelectorAll("svg")).toHaveLength(9);
    expect(container.querySelector("[data-illustration=pi-box]")).toHaveAttribute("aria-hidden", "true");
    expect(screen.getByRole("img", { name: "A clinic" })).toBeInTheDocument();
  });

  it("Hero has the title, one sentence, and skeletons while loading", () => {
    const { container, rerender } = render(<Hero title="All good" description="Care runs." loading size="compact" />);
    expect(screen.queryByText("All good")).toBeNull();
    expect(container.querySelectorAll("[data-slot=skeleton]").length).toBeGreaterThan(0);
    rerender(<Hero title="All good" description="Care runs." illustration="pi-box" />);
    expect(screen.getByRole("heading", { level: 1, name: "All good" })).toBeInTheDocument();
    expect(screen.getByText("Care runs.")).toBeInTheDocument();
  });

  it("EmptyState has the illustration, the sentence, and one action", () => {
    render(
      <EmptyState
        illustration="backup-vault"
        title="No backups yet"
        description="Add a place."
        action={<button>Add</button>}
      />,
    );
    expect(screen.getByRole("heading", { name: "No backups yet" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
  });
});
