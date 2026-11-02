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
