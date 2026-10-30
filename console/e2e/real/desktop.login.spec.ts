import { expect, test } from "../fixtures";
import { cardBoxes, env, note, saveState, shot } from "./real";

// A laptop on a ready device: login -> dashboard. The cards keep their place when the data arrives and refreshes.
test("log in on a laptop and read the dashboard", async ({ page }, info) => {
  test.skip(!env.consolePassword, "set CONSOLE_PASSWORD");
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/login\?next=%2Fdashboard$/);
  await page.getByLabel("Console password").fill(env.consolePassword);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(/\/dashboard$/);

  // Hold the next GET /status after a reload, so the page shows its skeletons; then compare the boxes.
  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/v1/status", async (route) => {
    await held;
    await route.continue();
  });
  await page.reload();
  await expect(page.getByTestId("units")).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const skeleton = await cardBoxes(page);
  const skeletonRows = await page.getByTestId("units").locator("tbody tr").count();
  await shot(page, info, "18-dashboard-skeleton");
  release();
  await expect(page.getByTestId("status-state")).toBeVisible();
  await expect
    .poll(() => page.getByTestId("units").locator("tbody tr").locator("td").first().textContent())
    .toMatch(/care/);
  const loaded = await cardBoxes(page);
  const rows = page.getByTestId("units").locator("tbody tr");
  const units = await rows.allTextContents();
  note(
    info,
    `desktop dashboard: ${loaded.length} cards; skeleton rows ${skeletonRows}, unit rows ${units.length}; ` +
      `boxes equal after the data: ${JSON.stringify(skeleton) === JSON.stringify(loaded)}`,
  );
  for (let i = 0; i < loaded.length; i++) {
    if (JSON.stringify(skeleton[i]) !== JSON.stringify(loaded[i])) {
      note(info, `card ${i}: skeleton ${JSON.stringify(skeleton[i])} -> data ${JSON.stringify(loaded[i])}`);
    }
  }
  note(info, `units: ${units.join(" | ")}`);
  for (const r of units) expect(r).toMatch(/active\//);
  for (const r of units) expect(r).not.toMatch(/inactive|failed/);
  await shot(page, info, "19-dashboard");
  await saveState(page, "desktop");
  expect(loaded.length).toBe(8);
});
