import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/api/client";
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

// The body of the PUT /config of the preview (the save of the form).
function capturePut() {
  const bodies: Config[] = [];
  vi.spyOn(api, "PUT").mockImplementation((async (_path: string, init: { body: Config }) => {
    bodies.push(init.body);
    return { data: { written: false, etag: '"1"', plan: { changed: false }, job: null }, response: new Response(null) };
  }) as never);
  return bodies;
}

describe("Settings > Backups", () => {
  afterEach(() => vi.restoreAllMocks());

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

  // review m3: the agent takes the retention of a destination without its own numbers from backups.retention.
  it("a destination without retention shows files_keep_daily of the config, and the save writes daily copies only", async () => {
    const bodies = capturePut();
    renderTab(
      configWith({
        retention: { db_full: 2, files_keep_daily: 30, files_keep_weekly: 8 },
        destinations: [{ id: "usb", type: "posix", path: "/mnt/usb" }],
      }),
    );
    const days = screen.getByLabelText("Keep copies for (days)");
    expect(days).toHaveValue(30);
    await userEvent.clear(days);
    await userEvent.type(days, "60");
    await userEvent.click(screen.getByTestId("config-preview"));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]?.backups?.destinations?.[0]).toMatchObject({
      id: "usb",
      retention: { keep_within_days: 0, keep_daily: 60, keep_weekly: 0, keep_monthly: 0 },
    });
  });

  it("writes the numbers of the operator also when they are the constants of the agent", async () => {
    const bodies = capturePut();
    renderTab(
      configWith({
        retention: { db_full: 2, files_keep_daily: 30, files_keep_weekly: 8 },
        destinations: [{ id: "usb", type: "posix", path: "/mnt/usb" }],
      }),
    );
    const days = screen.getByLabelText("Keep copies for (days)");
    await userEvent.clear(days);
    await userEvent.type(days, "14");
    await userEvent.click(screen.getByTestId("config-preview"));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]?.backups?.destinations?.[0]?.retention).toEqual({
      keep_within_days: 0,
      keep_daily: 14,
      keep_weekly: 0,
      keep_monthly: 0,
    });
  });
});
