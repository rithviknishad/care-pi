import { expect, login, noSideScroll, shot, test } from "./fixtures";

// Phase 7 (docs/api/README.md section 17.3): a new root CA. The certificate page asks first, runs the job, then shows
// the warning "install it again on every device" with the old and the new fingerprint.
test("make a new root certificate and compare the fingerprints", async ({ page, resetMock }, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fcertificate");
  await login(page);
  await expect(page).toHaveURL(/\/certificate$/);
  await expect(page.getByTestId("ca-changed")).toHaveCount(0);
  const card = page.getByTestId("ca-rotate-card");
  await card.getByTestId("ca-rotate").click();
  await expect(page.getByRole("alertdialog")).toContainText("Make a new root certificate?");
  await page.getByTestId("ca-rotate-confirm").click();
  await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  const warning = page.getByTestId("ca-changed");
  await expect(warning).toHaveAttribute("data-reason", "rotation");
  await expect(warning).toContainText("install it again on every device");
  const fps = warning.getByTestId("ca-fingerprints");
  await expect(fps).toContainText("7B:2E:4F:9A");
  await expect(fps).toContainText("3F:1D:6C:0E");
  await expect(page.getByTestId("ca-fingerprint")).toContainText("7B:2E:4F:9A");
  await noSideScroll(page);
  await shot(page, info, "ca-rotate-01-warning");
  await warning.getByTestId("ca-changed-dismiss").click();
  await expect(warning).toBeHidden();
});
