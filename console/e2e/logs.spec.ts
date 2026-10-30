import { expect, login, test } from "./fixtures";

// Journey 3: the log viewer appends new lines while it follows, and stops while it pauses.
test("follow the logs of a unit", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Flogs%3Funit%3Dapi");
  await login(page);
  await expect(page).toHaveURL(/\/logs\?unit=api$/);
  const lines = page.getByTestId("log-line");
  await expect(lines).toHaveCount(200);

  await page.getByTestId("log-follow").click();
  await expect(lines.first()).toBeVisible();
  const start = await lines.count();
  await expect.poll(() => lines.count(), { timeout: 10_000 }).toBeGreaterThan(start + 3);

  // The newest line stays in view (sticky bottom): the box is scrolled to its end.
  const box = page.getByTestId("log-lines");
  await expect.poll(() => box.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeLessThan(30);

  await page.getByTestId("log-pause").click();
  const paused = await lines.count();
  await expect(page.getByText(/new lines$/)).toBeVisible();
  await page.waitForTimeout(800);
  expect(await lines.count()).toBe(paused);

  await page.getByTestId("log-resume").click();
  await expect.poll(() => lines.count()).toBeGreaterThan(paused);
});
