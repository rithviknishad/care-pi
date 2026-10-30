import { expect, test } from "../fixtures";
import { checkHandoffAndDashboard, env, followSetupJob, note, saveState, shot } from "./real";

// A setup job failed, and the agent restarted (for example after an update of the agent). The operator logs in,
// enters the Care password again (the agent keeps it only in memory), and starts the setup again
// (docs/api/README.md section 5.2).
test("finish the setup again after a failed setup job", async ({ page }, info) => {
  test.skip(!env.consolePassword || !env.carePassword, "set CONSOLE_PASSWORD and CARE_PASSWORD");
  test.setTimeout(env.setupTimeoutMs + 3 * 60_000);

  await page.goto("/");
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel("Console password").fill(env.consolePassword);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(/\/setup\/care-admin$/);
  await expect(page.getByText("The agent restarted. Enter the Care password again.")).toBeVisible();
  await expect(page.getByLabel("User name")).toHaveValue(env.careUsername);
  await shot(page, info, "16-refinish-care-admin");
  await page.getByLabel("Care password").fill(env.carePassword);
  await page.getByLabel("Type the password again").fill(env.carePassword);
  await page.getByTestId("step-submit").click();

  await expect(page.getByTestId("step-title")).toHaveText("Review");
  await shot(page, info, "17-refinish-review");
  const finishAt = Date.now();
  await page.getByRole("button", { name: "Start the setup" }).click();
  note(info, `finish (again) clicked at ${new Date(finishAt).toISOString()}`);
  await followSetupJob(page, info, finishAt, "-2");
  await checkHandoffAndDashboard(page, info);
  await saveState(page, "desktop");
});
