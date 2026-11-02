import { expect, login, noSideScroll, shot, test } from "./fixtures";

// Phase 7: the Support page runs care-pi doctor (GET /api/v1/doctor) and shows each check with its fix.
test("the support page shows the health checks, the failed ones first", async ({ page, resetMock }, info) => {
  await resetMock("ready", { doctor: "fail" });
  await page.goto("/login?next=%2Fsupport");
  await login(page);
  const card = page.getByTestId("doctor-card");
  await expect(card).toHaveAttribute("data-status", "fail");
  const first = card.getByTestId("doctor-checks").locator("li").first();
  await expect(first).toHaveAttribute("data-check", "ports");
  await expect(first).toContainText("tailscaled on 100.101.102.103:443");
  await expect(first).toContainText("tailscale serve reset");
  await expect(card.locator('[data-check="backup-usb"]')).toHaveAttribute("data-status", "warn");
  await noSideScroll(page);
  await shot(page, info, "doctor-01-failed");
  const reads = page.waitForRequest((r) => r.url().endsWith("/api/v1/doctor"));
  await card.getByTestId("doctor-run").click();
  await reads;
  await expect(card.getByTestId("doctor-status")).toHaveText("Failed");
});
