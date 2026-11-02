import { boxes, expect, isMobile, login, MOCK_URL, mockState, noSideScroll, shot, test } from "./fixtures";

// The Backups page (plans/02 section 7, docs/api/README.md section 13): the health row, the local backups, the
// destinations, the recovery key, the schedule, the jobs, and the layout stability.

test("the Backups page shows the state, and Back up now runs a job", async ({ page, resetMock, requests }, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fbackups");
  await login(page);
  await expect(page).toHaveURL(/\/backups$/);

  await expect(page.getByTestId("backup-health")).toHaveAttribute("data-state", "ok");
  await expect(page.getByTestId("backup-health")).toContainText("The backups are OK.");
  await expect(page.getByTestId("local-state")).toHaveText("OK");
  await expect(page.getByTestId("local-window")).toContainText(" to ");
  const cards = page.getByTestId("destination-card");
  await expect(cards).toHaveCount(2);
  await expect(cards.nth(0)).toHaveAttribute("data-id", "usb");
  await expect(cards.nth(1)).toHaveAttribute("data-id", "b2");
  await expect(page.getByTestId("destination-state").first()).toHaveText("OK");
  await expect(page.getByTestId("key-state")).toHaveText("Confirmed");
  await expect(page.getByTestId("rpo")).toContainText("On this device: up to 5 min.");
  await expect(page.getByTestId("rpo")).toContainText("If this device breaks: up to 20 min.");
  await expect(page.getByTestId("schedule-card")).toContainText("Each Sunday at 01:00");
  await expect(page.getByTestId("drill-last")).toContainText("Passed");
  await expect(page.getByTestId("ssh-card")).toContainText("restrict ssh-ed25519");
  if (isMobile(page)) await noSideScroll(page);
  await shot(page, info, "backups-page");

  // Back up now: the local backup and a copy to each destination is "Back up now" of a card.
  await page.getByTestId("destination-run").first().click();
  const dialog = page.getByTestId("job-dialog");
  await expect(dialog.getByTestId("job-step-title").first()).toHaveText("Copy the backup to the destination");
  await expect(dialog.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await dialog.getByTestId("job-dialog-close").click();
  await expect(dialog).toHaveCount(0);

  // The test restore (drill) runs a job with its own steps.
  await page.getByTestId("drill").click();
  await expect(dialog.getByTestId("job-step-title").first()).toHaveText("Check the free disk space");
  await expect(dialog.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await dialog.getByTestId("job-dialog-close").click();
  expect(requests.some((u) => u.endsWith("/api/v1/backups/health"))).toBeTruthy();
});

test("a destination that does not answer shows a banner here and on the dashboard", async ({
  page,
  resetMock,
}, info) => {
  await resetMock("ready", { backups: "degraded" });
  await page.goto("/login?next=%2Fdashboard");
  await login(page);
  const row = page.getByTestId("backup-health");
  await expect(row).toHaveAttribute("data-code", "destination-unreachable");
  await expect(page.getByTestId("backup-health-text")).toContainText("nas: no successful backup for 1 d 2 h.");
  await shot(page, info, "dashboard-banner", false);
  await row.getByRole("link", { name: "Open Backups" }).click();
  await expect(page).toHaveURL(/\/backups$/);

  const nas = page.locator('[data-testid=destination-card][data-id="nas"]');
  await expect(nas.getByTestId("destination-state")).toHaveText("Unreachable");
  await expect(nas.getByTestId("destination-error")).toContainText("No route to host");
  await expect(
    page.locator('[data-testid=destination-card][data-id="b2"]').getByTestId("destination-state"),
  ).toHaveText("Degraded");
  await shot(page, info, "backups-degraded");

  // Back up now on all destinations: the copy to nas fails, the job continues with the others and ends failed.
  await nas.getByTestId("destination-run").click();
  const dialog = page.getByTestId("job-dialog");
  await expect(dialog.getByTestId("job-progress")).toHaveAttribute("data-job-state", "failed", { timeout: 20_000 });
  await expect(dialog.locator("[data-problem=destination-unreachable]")).toBeVisible();
});

test("without a destination: add an S3 bucket, make it, and apply", async ({ page, resetMock, requests }, info) => {
  await resetMock("ready", { backups: "no-destination" });
  await page.goto("/login?next=%2Fdashboard");
  await login(page);
  await expect(page.getByTestId("backup-health")).toHaveAttribute("data-code", "no-destination");
  await page.goto("/backups");
  await expect(page.getByTestId("no-destinations")).toBeVisible();
  await page.getByTestId("backup-health").getByRole("link", { name: "Add a destination" }).click();
  await expect(page).toHaveURL(/\/backups\?add=1$/);

  const dialog = page.getByTestId("add-dialog");
  await dialog.getByTestId("dest-choice-s3").click();
  await dialog.getByLabel("Endpoint").fill("https://s3.eu-central-003.backblazeb2.com");
  await dialog.getByLabel("Region").fill("eu-central-003");
  await dialog.getByLabel("Bucket").fill("new-clinic-bucket");
  await dialog.getByLabel("Access key id").fill("AKIAEXAMPLE0001");
  await dialog.getByLabel("Secret access key").fill("a secret access key");
  await dialog.getByTestId("dest-test").click();
  await expect(dialog.getByTestId("create-bucket")).toContainText("The bucket new-clinic-bucket does not exist.");
  await dialog.getByTestId("create-bucket-button").click();
  await expect(dialog.getByTestId("dest-test-result")).toHaveAttribute("data-next", "none");
  await expect(dialog.getByTestId("dest-rpo")).toContainText("Data loss at most 20 min");
  await shot(page, info, "backups-add-s3");
  await dialog.getByTestId("dest-add").click();
  await expect(dialog.getByTestId("plan-preview")).toContainText("restic init");
  await dialog.getByTestId("dest-apply").click();

  // The keys go to PUT /backups/destinations/{id}/secrets, then POST /apply: never into config.yaml.
  const job = page.getByTestId("job-dialog");
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await job.getByTestId("job-dialog-close").click();
  await expect(page.getByTestId("destination-card")).toHaveCount(1);
  await expect(page.getByTestId("destination-card")).toHaveAttribute("data-id", "cloud");
  expect(requests.some((u) => u.endsWith("/api/v1/backups/destinations/cloud/secrets"))).toBeTruthy();
  expect(requests.some((u) => u.endsWith("/api/v1/apply"))).toBeTruthy();
  const file = (await (await page.request.get(`${MOCK_URL}/__mock/state`)).json()) as {
    file: { backups: { destinations: Record<string, unknown>[] } };
  };
  expect(JSON.stringify(file.file.backups)).not.toContain("a secret access key");
});

test("an unconfirmed key: show the words again with a password check, then confirm", async ({ page, resetMock }) => {
  await resetMock("ready", { backups: "unconfirmed", force_reauth: true });
  await page.goto("/login?next=%2Fbackups");
  await login(page);
  await expect(page.getByTestId("backup-health")).toHaveAttribute("data-code", "recovery-key-unconfirmed");
  await expect(page.getByTestId("key-state")).toHaveText("Not confirmed");
  await page.getByTestId("key-open-ceremony").click();
  const dialog = page.getByTestId("ceremony-dialog");
  await expect(dialog.getByTestId("key-ceremony")).toHaveAttribute("data-phase", "confirm");
  await dialog.getByRole("button", { name: "Show the words again" }).click();

  // GET /backups/recovery-key needs a recent authentication: the password dialog opens.
  await page
    .getByRole("dialog", { name: "Enter your password again" })
    .getByLabel("Console password")
    .fill("correct horse battery staple");
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(dialog.getByTestId("key-words").locator("li")).toHaveCount(24);
  await dialog.getByText("I saved the recovery key").click();
  await dialog.getByTestId("key-saved-continue").click();

  const { key_words: words } = await mockState(page);
  const inputs = dialog.getByTestId("key-confirm").locator("input[data-position]");
  await expect(inputs).toHaveCount(3);
  for (let i = 0; i < 3; i++) {
    const position = Number(await inputs.nth(i).getAttribute("data-position"));
    await inputs.nth(i).fill((words?.[position - 1] ?? "").slice(0, 4));
  }
  await dialog.getByTestId("key-confirm-submit").click();
  await expect(dialog.getByTestId("key-ceremony")).toHaveAttribute("data-phase", "confirmed");
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("key-state")).toHaveText("Confirmed");
  await expect(page.getByTestId("backup-health")).toHaveAttribute("data-state", "ok");
});

// The cards keep their size and position when the data arrives (final-size skeletons, docs/console-ui.md section 5).
test("the Backups page does not move when the data arrives", async ({ page, resetMock }, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fbackups");
  await login(page);
  await expect(page.getByTestId("destination-card")).toHaveCount(2);

  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route(/\/api\/v1\/backups(\/health)?$/, async (route) => {
    await held;
    await route.continue();
  });
  await page.reload();
  await expect(page.getByTestId("destination-skeleton")).toHaveCount(2);
  await expect(page.getByTestId("backup-health-skeleton")).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const cards = page.locator("main [data-slot=card]");
  const before = await boxes(cards);
  const row = await boxes(page.getByTestId("backup-health-skeleton"));
  await shot(page, info, "backups-skeleton");
  expect(before.length).toBeGreaterThanOrEqual(7);

  release();
  await expect(page.getByTestId("destination-card")).toHaveCount(2);
  await expect(page.getByTestId("local-state")).toHaveText("OK");
  await page.evaluate(() => document.fonts.ready);
  const after = await boxes(cards);
  await shot(page, info, "backups-loaded");
  expect(after).toEqual(before);
  expect(await boxes(page.getByTestId("backup-health"))).toEqual(row);
  if (isMobile(page)) await noSideScroll(page);
});

test("Settings > Backups changes the schedule with a plan preview", async ({ page, resetMock, requests }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fbackups");
  await login(page);
  await page.getByTestId("schedule-edit").click();
  await expect(page).toHaveURL(/\/settings\?tab=backups$/);
  const form = page.getByTestId("backups-settings");
  await expect(page.getByTestId("settings-rpo")).toContainText("5 min on this device, 20 min if this device breaks");
  await form.getByLabel("Archive the database changes each (min)", { exact: true }).fill("2");
  await expect(page.getByTestId("settings-rpo")).toContainText("2 min on this device, 17 min if this device breaks");
  await form.getByLabel("Full backup", { exact: true }).fill("Sat 23:00");
  await page.getByTestId("config-preview").click();
  await expect(page.getByTestId("plan-preview")).toContainText("backup timer");
  await page.getByTestId("config-apply").click();
  await expect(page.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  expect(requests.some((u) => u.includes("/api/v1/config?dry_run=true"))).toBeTruthy();
  await page.goto("/backups");
  await expect(page.getByTestId("schedule-card")).toContainText("Each Saturday at 23:00");
  await expect(page.getByTestId("rpo")).toContainText("On this device: up to 2 min.");
});

// The rotation (docs/api/README.md section 13.4): the new key (pending) and its ceremony, then the job.
test("make a new recovery key and change to it", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fbackups");
  await login(page);
  const oldId = await page.getByTestId("key-card").locator("dd").first().textContent();
  await page.getByTestId("key-rotate").click();
  const dialog = page.getByTestId("rotate-dialog");
  await dialog.getByTestId("key-make").click();
  await expect(dialog.getByText("New key (not active)")).toBeVisible();
  const words = (await dialog.getByTestId("key-words").locator("li span:last-child").allTextContents()).map((w) =>
    w.trim(),
  );
  expect(words).toHaveLength(24);
  await dialog.getByText("I saved the recovery key").click();
  await dialog.getByTestId("key-saved-continue").click();
  const inputs = dialog.getByTestId("key-confirm").locator("input[data-position]");
  for (let i = 0; i < 3; i++) {
    const n = Number(await inputs.nth(i).getAttribute("data-position"));
    await inputs.nth(i).fill(words[n - 1] ?? "");
  }
  await dialog.getByTestId("key-confirm-submit").click();
  await dialog.getByTestId("rotate-start").click();
  const job = page.getByTestId("job-dialog");
  await expect(job.locator("[data-action=activate-key]")).toBeVisible();
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 30_000 });
  await job.getByTestId("job-dialog-close").click();
  await expect(page.getByTestId("key-card").locator("dd").first()).not.toHaveText(oldId ?? "");
});

// A host of Phase 2 after the upgrade (docs/api/README.md section 13.12): make the key, then re-key the local backups.
test("a Phase 2 host: make the key, then change the key of the local backups", async ({ page, resetMock }) => {
  await resetMock("ready", { backups: "legacy" });
  await page.goto("/login?next=%2Fbackups");
  await login(page);
  await expect(page.getByTestId("backup-health")).toHaveAttribute("data-code", "no-destination");
  await expect(page.getByTestId("backup-health-more")).toContainText("2 more");
  await page.getByTestId("backup-health-more").click();
  await expect(page.getByTestId("backup-banners").locator("li")).toHaveCount(3);
  await page.getByTestId("key-open-ceremony").click();
  const dialog = page.getByTestId("ceremony-dialog");
  await dialog.getByTestId("key-make").click();
  await expect(dialog.getByTestId("key-words").locator("li")).toHaveCount(24);
  const words = (await dialog.getByTestId("key-words").locator("li span:last-child").allTextContents()).map((w) =>
    w.trim(),
  );
  await dialog.getByText("I saved the recovery key").click();
  await dialog.getByTestId("key-saved-continue").click();
  const inputs = dialog.getByTestId("key-confirm").locator("input[data-position]");
  for (let i = 0; i < 3; i++) {
    const n = Number(await inputs.nth(i).getAttribute("data-position"));
    await inputs.nth(i).fill(words[n - 1] ?? "");
  }
  await dialog.getByTestId("key-confirm-submit").click();
  await expect(dialog.getByTestId("key-ceremony")).toHaveAttribute("data-phase", "confirmed");
  await page.keyboard.press("Escape");
  await page.getByTestId("key-rekey").click();
  await page.getByTestId("rekey-start").click();
  const job = page.getByTestId("job-dialog");
  await expect(job.locator("[data-action=rekey-local]")).toBeVisible();
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 30_000 });
  await job.getByTestId("job-dialog-close").click();
  await expect(page.getByTestId("key-rekey")).toHaveCount(0);
});
