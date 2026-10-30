import { expect, login, test } from "./fixtures";

// The advanced path of a change after setup: edit -> plan preview (If-Match) -> apply -> job over SSE.
test("change the size with a plan preview and an apply job", async ({ page, resetMock, requests }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fsettings%3Ftab%3Dsize");
  await login(page);
  await expect(page).toHaveURL(/\/settings\?tab=size$/);
  await page.getByRole("radio", { name: /^solo/ }).click();
  await page.getByTestId("config-preview").click();
  await expect(page.getByTestId("plan-preview")).toContainText("Some units restart");
  await expect(page.getByTestId("plan-preview")).toContainText("care-api.service");
  await page.getByTestId("config-apply").click();
  await expect(page.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  expect(requests.some((u) => u.includes("/api/v1/config?dry_run=true"))).toBeTruthy();
  expect(requests.some((u) => u.includes("/api/v1/config?apply=true"))).toBeTruthy();
});

test("a phase 3 page shows the not-available state", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fbackups");
  await login(page);
  await expect(page.getByTestId("not-available")).toContainText("This feature comes in Phase 3.");
});
