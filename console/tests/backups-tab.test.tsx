import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Config } from "@/api/types";
import { BackupsTab } from "@/features/settings/backups-tab";

const data = vi.hoisted(() => ({ config: null as unknown }));

vi.mock("@/api/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/queries")>();
  return { ...actual, useConfig: () => ({ data: { config: data.config, etag: '"1"' }, error: null }) };
});

function configWith(backups: Record<string, unknown>): Config {
  return {
    version: 1,
    backups: { wal_archive_timeout_min: 5, destinations: [{ id: "usb", type: "posix", path: "/mnt/usb" }], ...backups },
  } as unknown as Config;
}

function renderTab(config: Config) {
  data.config = config;
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <BackupsTab config={config} />
    </QueryClientProvider>,
  );
}

describe("Settings > Backups", () => {
  it("the RPO uses the frequency that the operator picks, also the default of the agent (review m1)", async () => {
    renderTab(
      configWith({
        wal_archive_timeout_min: 5,
        destinations: [{ id: "usb", type: "posix", path: "/mnt/usb", interval_min: 60 }],
      }),
    );
    expect(screen.getByTestId("settings-rpo")).toHaveTextContent("you lose at most 1 hour 5 minutes of work");
    await userEvent.click(screen.getByRole("button", { name: "Every 15 minutes" }));
    expect(screen.getByTestId("settings-rpo")).toHaveTextContent("you lose at most 20 minutes of work");
  });
});
