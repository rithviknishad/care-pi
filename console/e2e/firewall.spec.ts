import { expect, login, noSideScroll, shot, test } from "./fixtures";

// Phase 7 (docs/console-server.md section 13): the policy drop with the rollback timer. The operator switches the
// policy, the apply loads it, and the console asks to keep it within the time; else it goes back to accept.
test("switch the firewall to drop and keep it", async ({ page, resetMock }, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fsecurity");
  await login(page);
  const card = page.getByTestId("firewall-card");
  await expect(card).toBeVisible();
  await expect(card.getByTestId("firewall-live")).toHaveText("Not loaded yet");
  await card.getByRole("radio", { name: /^Drop/ }).click();
  await card.getByTestId("config-preview").click();
  await expect(card.getByTestId("plan-preview")).toBeVisible();
  await card.getByTestId("config-apply").click();
  await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  const pending = card.getByTestId("firewall-pending");
  await expect(pending).toBeVisible({ timeout: 10_000 });
  await expect(pending).toContainText(/goes back to accept in \d+ s/);
  await noSideScroll(page);
  await shot(page, info, "firewall-01-pending", false);
  await card.getByTestId("firewall-keep").click();
  await expect(pending).toHaveCount(0);
  await expect(card.getByTestId("firewall-live")).toHaveText("Drop");
  await shot(page, info, "firewall-02-kept", false);
});

test("without a confirmation the policy goes back to accept", async ({ page, resetMock }) => {
  await resetMock("ready", { firewall_rollback_s: 3 });
  await page.goto("/login?next=%2Fsecurity");
  await login(page);
  const card = page.getByTestId("firewall-card");
  await card.getByRole("radio", { name: /^Drop/ }).click();
  await card.getByTestId("config-preview").click();
  await card.getByTestId("config-apply").click();
  await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(card.getByTestId("firewall-pending")).toBeVisible();
  await expect(card.getByTestId("firewall-pending")).toHaveCount(0, { timeout: 15_000 });
  await expect(card.getByTestId("firewall-live")).toHaveText("Accept");
  await expect(card).toContainText("nobody confirmed in time");
});

// S64: the extra ports of the firewall (security.firewall.allow_ports). The operator adds two ports for the exporters
// of the device and switches to drop in one change; the plan reloads the firewall. Then one port goes again.
test("open extra ports for the exporters", async ({ page, resetMock }, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fsecurity");
  await login(page);
  const card = page.getByTestId("firewall-card");
  const ports = card.getByTestId("allow-ports");
  await expect(ports.getByTestId("allow-ports-none")).toBeVisible();
  // A port of care-pi gets the reason at once.
  await ports.getByTestId("allow-port-port").fill("443");
  await ports.getByTestId("allow-port-add").click();
  await expect(ports).toContainText("The firewall already accepts 443/tcp (Care and the console).");
  await ports.getByTestId("allow-port-port").fill("9100");
  await ports.getByTestId("allow-port-iface").fill("tailscale0");
  await ports.getByTestId("allow-port-comment").fill("node exporter");
  await ports.getByTestId("allow-port-add").click();
  await ports.getByTestId("allow-port-port").fill("9256");
  await ports.getByTestId("allow-port-iface").fill("tailscale0");
  await ports.getByTestId("allow-port-add").click();
  const list = ports.getByTestId("allow-ports-list");
  await expect(list.getByRole("listitem")).toHaveText([
    /9100\/tcp on tailscale0\s*node exporter/,
    /9256\/tcp on tailscale0/,
  ]);
  await expect(ports.getByTestId("allow-port-port")).toHaveValue("");
  await card.getByRole("radio", { name: /^Drop/ }).click();
  await noSideScroll(page);
  await shot(page, info, "firewall-03-allow-ports", true);
  await card.getByTestId("config-preview").click();
  const plan = card.getByTestId("plan-preview");
  await expect(plan).toContainText("reload-firewall");
  await card.getByTestId("config-apply").click();
  await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(card.getByTestId("firewall-pending")).toBeVisible({ timeout: 10_000 });
  await card.getByTestId("firewall-keep").click();
  await expect(card.getByTestId("firewall-live")).toHaveText("Drop");
  const state = await page.request.get("/api/v1/security/firewall");
  expect((await state.json()).allow_ports).toEqual([
    { port: 9100, proto: "tcp", iface: "tailscale0", comment: "node exporter" },
    { port: 9256, proto: "tcp", iface: "tailscale0", comment: null },
  ]);
  await card.getByRole("button", { name: "Close" }).click();

  // Remove 9256/tcp: the list reads the new config after the close.
  await expect(list.getByRole("listitem")).toHaveCount(2);
  await list.getByRole("button", { name: "Remove 9256/tcp on tailscale0" }).click();
  await expect(list.getByRole("listitem")).toHaveCount(1);
  await card.getByTestId("config-preview").click();
  await expect(card.getByTestId("plan-preview")).toContainText("reload-firewall");
  await card.getByTestId("config-apply").click();
  await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  const after = await page.request.get("/api/v1/security/firewall");
  expect((await after.json()).allow_ports).toEqual([
    { port: 9100, proto: "tcp", iface: "tailscale0", comment: "node exporter" },
  ]);
});
