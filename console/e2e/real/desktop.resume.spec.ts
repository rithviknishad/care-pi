import { expect, test } from "../fixtures";
import { existingState, note, shot } from "./real";

// After a reboot (or later): the session of desktop.setup.spec.ts is still valid (24 h absolute), so the dashboard opens
// without a login.
const state = existingState("desktop");
test.use({ storageState: state });

test("the desktop session stays valid", async ({ page }, info) => {
  test.skip(!state, "run desktop.setup.spec.ts first (STATE_DIR/desktop.json)");
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByTestId("status-state")).toBeVisible();
  const rows = page.getByTestId("units").locator("tbody tr");
  await expect.poll(() => rows.count()).toBeGreaterThan(5);
  const units = await rows.allTextContents();
  note(info, `resume: state ${await page.getByTestId("status-state").textContent()}; units: ${units.join(" | ")}`);
  await shot(page, info, "20-resume-dashboard");
});
