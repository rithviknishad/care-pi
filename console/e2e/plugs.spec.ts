import type { Page } from "@playwright/test";
import { expect, isMobile, login, noSideScroll, shot, test } from "./fixtures";

// Phase 4: the Solution and plugs page (docs/api/README.md sections 14.6 and 14.7, docs/console-ui.md section 10).

async function open(page: Page) {
  await page.goto(`/login?next=${encodeURIComponent("/plugs")}`);
  await login(page);
  await expect(page).toHaveURL(/\/plugs$/);
  await expect(page.getByTestId("solution-name")).toHaveText("CARE Clinic (care-clinic)");
}

test("turn a frontend plug on: the plan preview, then the apply job", async ({ page, resetMock, requests }, info) => {
  await resetMock("ready");
  await open(page);
  const card = page.getByTestId("plug-care_fasterappointments_fe");
  const toggle = card.getByTestId("plug-switch-care_fasterappointments_fe");
  await expect(card).toHaveAttribute("data-enabled", "false");
  await expect(card.getByTestId("plug-bundle")).toContainText("Not downloaded");

  // Cancel: the switch goes back.
  await toggle.click();
  const plan = page.getByTestId("plug-plan");
  await expect(plan).toContainText("Turn on Faster appointments?");
  await expect(toggle).toHaveAttribute("aria-checked", "true");
  await plan.getByRole("button", { name: "Cancel" }).click();
  await expect(plan).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-checked", "false");

  await toggle.click();
  await expect(page.getByTestId("plug-plan-effect")).toHaveAttribute("data-path", "plug-config");
  await expect(plan).toContainText("This changes the frontend plug list. Care keeps running.");
  await expect(page.getByTestId("plug-plan-steps")).toContainText("Download the web app and the plug files");
  await expect(page.getByTestId("plug-plan-steps")).toContainText("Send the plug settings to Care");
  await shot(page, info, "plugs-plan", false);
  await page.getByTestId("plug-apply").click();
  await expect(plan).toBeHidden();
  await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(card).toHaveAttribute("data-on", "true");
  await expect(card.getByTestId("plug-bundle")).toContainText("On the device");
  await expect(card.getByTestId("plug-source")).toHaveText("Your choice");
  expect(requests.some((u) => u.includes("/api/v1/plugs/care_fasterappointments_fe?dry_run=true"))).toBeTruthy();
  expect(requests.some((u) => u.includes("/api/v1/plugs/care_fasterappointments_fe?apply=true"))).toBeTruthy();

  // The history of the Updates page has the plug change.
  await page.goto("/updates");
  const row = page.getByTestId("history-row").first();
  await expect(row).toHaveAttribute("data-kind", "plug-change");
  await expect(row.getByTestId("history-what")).toHaveText("Plug change: Plug care_fasterappointments_fe");
});

test("Filly needs its settings: the errors show at the fields, then the cutover", async ({ page, resetMock }, info) => {
  await resetMock("ready", { step_ms: 250 });
  await open(page);
  const card = page.getByTestId("plug-care_filly");
  const toggle = card.getByTestId("plug-switch-care_filly");
  await toggle.click();
  // 422 plug-settings-invalid: the switch goes back, the form opens with the errors of the agent.
  await expect(card.getByTestId("plug-needs-settings")).toBeVisible();
  await expect(toggle).toHaveAttribute("aria-checked", "false");
  await expect(card.getByTestId("setting-error-MEDISPEAK_API_KEY")).toHaveText(
    "A value is necessary when the plug is on.",
  );
  await expect(card.getByTestId("setting-error-MEDISPEAK_BASE_URL")).toHaveText(
    "A value is necessary when the plug is on.",
  );

  // The console checks the pattern and the URL before the request.
  await card.getByTestId("setting-MEDISPEAK_API_KEY").fill("sk_wrong");
  await card.getByTestId("setting-MEDISPEAK_BASE_URL").fill("medispeak.example.org");
  await card.getByTestId("plug-settings-preview-care_filly").click();
  await expect(card.getByTestId("setting-error-MEDISPEAK_API_KEY")).toHaveText(
    "The value does not have the correct format.",
  );
  await expect(card.getByTestId("setting-error-MEDISPEAK_BASE_URL")).toHaveText(
    "Enter an address that starts with http:// or https://.",
  );
  await card.getByTestId("plug-settings-care_filly").scrollIntoViewIfNeeded();
  await shot(page, info, "plugs-settings", false);

  await card.getByTestId("setting-MEDISPEAK_API_KEY").fill("msk_live_0123456789");
  await card.getByTestId("setting-MEDISPEAK_BASE_URL").fill("https://medispeak.example.org/api/v2");
  await expect(card.getByTestId("plug-settings-preview-care_filly")).toHaveText("Turn on with these settings");
  await card.getByTestId("plug-settings-preview-care_filly").click();
  const plan = page.getByTestId("plug-plan");
  await expect(plan).toContainText("Turn on Filly?");
  await expect(page.getByTestId("plug-plan-effect")).toHaveAttribute("data-path", "cutover");
  await expect(plan).toContainText("Care stops for about 2 min");
  await expect(plan).toContainText("Filly needs internet access at run time.");
  await page.getByTestId("plug-apply").click();
  // A backend plug that turns on runs the cutover: the maintenance page shows.
  await expect(page.getByTestId("maintenance-banner").filter({ visible: true })).toBeVisible({ timeout: 15_000 });
  await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 30_000 });
  await expect(page.getByTestId("maintenance-banner").filter({ visible: true })).toHaveCount(0);
  await expect(card).toHaveAttribute("data-on", "true");
  // The secret is never shown: the form tells that a value is set.
  await expect(card.getByTestId("setting-MEDISPEAK_API_KEY")).toContainText("A value is set");
  await expect(card.getByText("turn on the Filly screens in the plugin marketplace")).toBeVisible();
});

test("an external bundle, an automatic plug, and the advanced settings", async ({ page, resetMock }, info) => {
  await resetMock("ready");
  await open(page);
  const multihost = page.getByTestId("plug-care_multihost_files_be");
  await expect(multihost.getByTestId("plug-automatic")).toHaveText("Automatic");
  await expect(multihost.getByTestId("plug-auto-reason")).toContainText(
    "this plug turns on when Care has more than 1 address. This device has 1.",
  );
  await expect(multihost.getByRole("switch")).toHaveCount(0);

  const token = page.getByTestId("plug-care_token_display_fe");
  await expect(token.getByTestId("plug-bundle")).toContainText("External");
  await expect(token.getByTestId("plug-external")).toContainText("It is not pinned.");
  await token.getByTestId("plug-settings-toggle-care_token_display_fe").click();
  const form = token.getByTestId("plug-settings-care_token_display_fe");
  await expect(form.getByTestId("setting-config.sound")).toBeHidden();
  await form.getByRole("button", { name: "Advanced settings" }).click();
  await expect(form.getByTestId("setting-config.sound")).toBeVisible();
  await form.getByTestId("setting-config.refresh_seconds").fill("2");
  await form.getByTestId("plug-settings-preview-care_token_display_fe").click();
  await expect(form.getByTestId("setting-error-config.refresh_seconds")).toHaveText("Use 5 or more.");
  await form.getByTestId("setting-config.refresh_seconds").fill("60");
  await form.getByTestId("plug-settings-preview-care_token_display_fe").click();
  const plan = page.getByTestId("plug-plan");
  await expect(plan).toContainText("Change the settings of Token display?");
  await expect(page.getByTestId("plug-plan-effect")).toHaveAttribute("data-path", "plug-config");
  await page.getByTestId("plug-apply").click();
  await expect(token.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(form.getByTestId("setting-config.refresh_seconds")).toHaveValue("60");
  await expect(form).toContainText("Changed");
  await token.scrollIntoViewIfNeeded();
  await shot(page, info, "plugs-advanced", false);
  await noSideScroll(page);
  if (isMobile(page)) {
    const cut = await page
      .locator("main dl > dt")
      .evaluateAll((els) => els.filter((el) => el.scrollWidth > el.clientWidth + 1).map((el) => el.textContent));
    expect(cut, "cut labels").toEqual([]);
  }
});

test("a solution without plugs shows the empty state", async ({ page, resetMock }) => {
  await resetMock("ready", { no_plugs: true });
  await open(page);
  await expect(page.getByTestId("plugs-empty")).toContainText("This solution has no plugs.");
});
