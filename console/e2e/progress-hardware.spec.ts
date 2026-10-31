import { expect, login, noSideScroll, shot, test } from "./fixtures";

// Wave 4d (Phase 2 exit test follow-ups 3 to 6): the progress page, a failed setup, and the hardware step.

async function startSetup(page: import("@playwright/test").Page) {
  await page.goto("/login?next=%2Fsetup%2Freview");
  await login(page);
  await expect(page.getByTestId("step-title")).toHaveText("Review");
  await page.getByRole("button", { name: "Start the setup" }).click();
  await expect(page).toHaveURL(/\/setup\/progress$/);
}

test("the progress page shows operator text and movement during the lock resolution", async ({
  page,
  resetMock,
}, info) => {
  await resetMock("review", { step_ms: 1500 });
  await startSetup(page);

  // The lock resolution: a step that runs, its expected time, its last output line, and an indeterminate bar.
  const resolve = page.locator('[data-testid="job-step"][data-action="resolve-lock"]');
  await expect(resolve).toHaveAttribute("data-state", "running");
  await expect(resolve.getByTestId("job-step-title")).toHaveText("Resolve the versions of the images (about 30 s)");
  await expect(resolve.getByTestId("job-step-indeterminate")).toBeVisible();
  await expect(resolve).toContainText(/resolve [^ ]+ … [0-9a-f]{12}/);
  await shot(page, info, "progress-resolve");

  // Later steps: each title is an operator sentence, never the raw key.
  const cycle = page.locator('[data-testid="job-step"][data-action="full-cycle"]');
  await expect(cycle).toHaveAttribute("data-state", "running", { timeout: 30_000 });
  await expect(resolve.getByTestId("job-step-title")).toHaveText("Resolve the versions of the images");
  const titles = await page.getByTestId("job-step-title").allTextContents();
  expect(titles.slice(0, 9)).toEqual([
    "Check the settings",
    "Check the device",
    "Resolve the versions of the images",
    "Make the directories",
    "Write the configuration files",
    "Load the new service files",
    "Start Care at each boot",
    "Download the container images",
    "Stop and start all the services of Care",
  ]);
  for (const t of titles) expect(t).not.toMatch(/^[a-z]+(-[a-z]+)+(:|$)/);
  await expect(page.getByTestId("job-step-raw")).toHaveCount(0);
  const pull = page.locator('[data-testid="job-step"][data-action="pull-images"]');
  await expect(pull).toContainText("caddy:2, care@sha256:77fe743e51a6, redis@sha256:21090af93f83");
  await expect(pull).toHaveAttribute("title", /^pull-images: localhost\/care-pi\/caddy:2 /);
  await noSideScroll(page);
  await shot(page, info, "progress");
});

test("a failed setup shows the step, the problem, a retry, the details, and the log of the unit", async ({
  page,
  resetMock,
}, info) => {
  await resetMock("review", { fail_step: "ensure-stanza" });
  await startSetup(page);

  const failed = page.getByTestId("setup-failed");
  await expect(failed).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("setup-failed-step")).toHaveText("This step failed: Prepare the database backups.");
  await expect(page.getByTestId("setup-failed-detail")).toContainText(
    "apply failed at ensure-stanza: podman exec --user postgres care-db",
  );
  await expect(failed).not.toContainText("fix the problem");
  await expect(page.getByTestId("setup-failed-log")).toHaveAttribute("href", "/logs?unit=db");
  await expect(page.getByTestId("setup-failed-log")).toHaveText("Show the log of care-db");
  await expect(failed.getByRole("button", { name: "Copy details" })).toBeVisible();
  // The raw error is in the collapsed technical details.
  await expect(page.getByTestId("setup-failed-technical")).toHaveCount(0);
  await noSideScroll(page);
  await shot(page, info, "setup-failed");
  await failed.getByRole("button", { name: "Technical details" }).click();
  await expect(page.getByTestId("setup-failed-technical")).toContainText("Step: ensure-stanza");
  await expect(page.getByTestId("setup-failed-technical")).toContainText("problems/internal 500 Internal error");
  await shot(page, info, "setup-failed-details");

  // Retry: POST /setup/finish again. The mock fails one time, so the second job reaches the hand-off.
  await failed.getByRole("button", { name: "Retry" }).click();
  await expect(page.getByTestId("setup-failed")).toHaveCount(0);
  await expect(page).toHaveURL(/\/setup\/done$/, { timeout: 30_000 });
  await expect(page.getByTestId("setup-state")).toHaveText("Ready");
});

test("the hardware step shows the failures first and one row for the passed checks", async ({
  page,
  resetMock,
}, info) => {
  await resetMock("setup");
  await page.goto("/login?next=%2Fsetup%2Fhardware");
  await login(page);
  await expect(page.getByTestId("step-title")).toHaveText("Hardware check");
  const summary = page.getByTestId("host-check-summary");
  await expect(summary).toContainText("All 43 checks passed");
  await expect(page.getByTestId("host-check-problems")).toHaveCount(0);
  await expect(page.getByTestId("host-check-passed")).toHaveCount(0);
  // The same size format as the check: binary units, 1 decimal (256 GB).
  await expect(page.getByText("USB drive, 238.5 GiB (/dev/sda)")).toBeVisible();
  // The button is in the window without a scroll (a fixed bar on a phone).
  await expect(page.getByRole("button", { name: "I read the warnings" })).toBeInViewport();
  await noSideScroll(page);
  await shot(page, info, "hardware", false);

  await summary.click();
  const passed = page.getByTestId("host-check-passed");
  await expect(passed.locator("li")).toHaveCount(20);
  await expect(passed.locator('[data-check="directories"]')).toContainText("Directories: 24 OK");
  await expect(passed.locator('[data-check^="dir "]')).toHaveCount(0);
  await expect(passed.locator('[data-check="root disk"]')).toContainText("/dev/sda (usb, 238.5 GiB)");
  await expect(page.getByRole("button", { name: "I read the warnings" })).toBeInViewport({ ratio: 1 });
  await passed.locator('[data-check="directories"]').scrollIntoViewIfNeeded();
  await shot(page, info, "hardware-open", false);
});

test("a failed host check comes before the summary", async ({ page, resetMock }) => {
  await resetMock("setup", { disk: "sd" });
  await page.goto("/login?next=%2Fsetup%2Fhardware");
  await login(page);
  const problems = page.getByTestId("host-check-problems");
  await expect(problems.locator("li")).toHaveCount(1);
  await expect(problems).toContainText("root disk");
  await expect(problems).toContainText("Fix: use a USB SSD or an NVMe drive of 64 GB or more");
  await expect(page.getByTestId("host-check-summary")).toContainText("42 of 43 checks passed");
});
