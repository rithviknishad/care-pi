import { expect, test } from "../fixtures";
import { addDestination, backupEnv } from "./backups-journey";
import { cardBoxes, env, note, pageWidth, shot, visibleWithin } from "./real";

// The Backups and Restore pages on a real device that is ready (Phase 3): the read-only checks run on the desktop
// and on the phone; the jobs run only on the desktop and only with their variable (e2e/real/README.md):
//   BACKUP_JOBS=1      Back up now (a checkpoint), Verify, and the test restore (drill).
//   BACKUP_PITR=1      A restore to 2 minutes ago, then its undo. Care stops 2 times for about 1 minute.
//   BACKUP_ADD_DEST=1  Add the destination of BACKUP_DEST from the Backups page, then Back up now to it.
// The specs log in with CONSOLE_PASSWORD.
test.describe.configure({ mode: "serial" });

// The first page of a test logs in through /login?next=…: a direct goto of a page without a session shows the page
// URL first, and the SPA goes to /login only after its session check (a check of the URL at once misses it).
const loggedIn = new WeakSet<import("@playwright/test").Page>();

async function open(page: import("@playwright/test").Page, path: string, project: string) {
  if (loggedIn.has(page)) {
    await page.goto(path);
  } else {
    await page.goto(`/login?next=${encodeURIComponent(path)}`);
    await page.getByLabel("Console password").fill(env.consolePassword);
    await page.getByRole("button", { name: "Log in" }).click();
    loggedIn.add(page);
  }
  await expect(page).toHaveURL(new RegExp(`${path.replace(/[?]/g, "\\?")}$`));
  note(test.info(), `${project}: ${path}`);
}

async function password(page: import("@playwright/test").Page) {
  const dialog = page.getByRole("dialog", { name: "Enter your password again" });
  if (await visibleWithin(dialog, 3000)) {
    await dialog.getByLabel("Console password").fill(env.consolePassword);
    await page.getByRole("button", { name: "Confirm", exact: true }).click();
  }
}

async function jobSucceeds(scope: import("@playwright/test").Locator, timeout = 10 * 60_000) {
  await expect(scope.getByTestId("job-progress")).toHaveAttribute("data-job-state", /succeeded|failed/, { timeout });
  const state = await scope.getByTestId("job-progress").getAttribute("data-job-state");
  const steps = await scope
    .getByTestId("job-step")
    .evaluateAll((els) => els.map((el) => `${el.getAttribute("data-action")}=${el.getAttribute("data-state")}`));
  note(test.info(), `job ${state}: ${steps.join(", ")}`);
  expect(state).toBe("succeeded");
}

test("the Backups page: state, health, and no move when the data arrives", async ({ page }, info) => {
  test.skip(!env.consolePassword, "set CONSOLE_PASSWORD");
  await open(page, "/backups", info.project.name);
  const health = page.getByTestId("backup-health");
  await expect(health).toBeVisible();
  note(
    info,
    `health: ${await health.getAttribute("data-state")} ${await health.getAttribute("data-code")} "${await health.textContent()}"`,
  );
  await expect(page.getByTestId("local-state")).toBeVisible();
  await expect(page.getByTestId("key-state")).toBeVisible();
  note(
    info,
    `local: ${await page.getByTestId("local-state").textContent()}, window ${await page.getByTestId("local-window").textContent()}, key ${await page.getByTestId("key-state").textContent()}, destinations ${await page.getByTestId("destination-card").count()}`,
  );
  for (const card of await page.getByTestId("destination-card").all()) {
    note(
      info,
      `destination ${await card.getAttribute("data-id")}: ${await card.getAttribute("data-state")}; ${await card.getByTestId("destination-last-db").textContent()}`,
    );
  }
  await shot(page, info, "30-backups");

  // A reload: the boxes of the cards are the same before and after the data (the skeletons of the last visit).
  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route(/\/api\/v1\/backups(\/health)?$/, async (route) => {
    await held;
    await route.continue();
  });
  await page.reload();
  await expect(page.getByTestId("backup-health-skeleton")).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const before = await cardBoxes(page);
  release();
  await expect(page.getByTestId("local-state")).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const after = await cardBoxes(page);
  note(info, `backups cards: ${before.length} before, ${after.length} after`);
  expect(after).toEqual(before);
  await page.unroute(/\/api\/v1\/backups(\/health)?$/);
  const width = await pageWidth(page);
  expect(width.scroll).toBeLessThanOrEqual(width.client);

  // The dashboard has the same health row; the restore page has the window.
  await open(page, "/dashboard", info.project.name);
  await expect(page.getByTestId("backup-health")).toBeVisible();
  await open(page, "/restore?kind=pitr", info.project.name);
  await expect(page.getByTestId("pitr-panel").or(page.getByText("No backup exists yet."))).toBeVisible();
  if (await page.getByTestId("pitr-panel").isVisible()) {
    note(
      info,
      `restore window from ${await page.getByTestId("pitr-from").textContent()}; ${await page.getByTestId("pitr-files").textContent()}`,
    );
  }
  await shot(page, info, "31-restore-pitr");
});

test("Back up now, verify, and the test restore", async ({ page }, info) => {
  test.skip(info.project.name !== "desktop" || !backupEnv.jobs || !env.consolePassword, "set BACKUP_JOBS=1 (desktop)");
  test.setTimeout(30 * 60_000);
  await open(page, "/backups", info.project.name);
  const dialog = page.getByTestId("job-dialog");
  await page.getByTestId("local-backup").click();
  await jobSucceeds(dialog);
  await shot(page, info, "32-backup-now");
  await dialog.getByTestId("job-dialog-close").click();
  await page.getByRole("button", { name: "Verify now" }).click();
  await jobSucceeds(dialog);
  await dialog.getByTestId("job-dialog-close").click();
  await page.getByTestId("drill").click();
  await jobSucceeds(dialog);
  await dialog.getByTestId("job-dialog-close").click();
  await expect(page.getByTestId("drill-last")).toContainText("Passed");
});

test("add a destination from the Backups page", async ({ page }, info) => {
  test.skip(
    info.project.name !== "desktop" || !backupEnv.addDest || !backupEnv.dest,
    "set BACKUP_ADD_DEST=1 and BACKUP_DEST",
  );
  test.setTimeout(20 * 60_000);
  await open(page, "/backups?add=1", info.project.name);
  const dialog = page.getByTestId("add-dialog");
  await addDestination(page, info, dialog, "33-add");
  await expect(dialog.getByTestId("plan-preview")).toBeVisible({ timeout: 60_000 });
  await dialog.getByTestId("dest-apply").click();
  await jobSucceeds(page.getByTestId("job-dialog"));
  await page.getByTestId("job-dialog-close").click();
  const card = page.getByTestId("destination-card").last();
  await card.getByTestId("destination-run").click();
  await jobSucceeds(page.getByTestId("job-dialog"));
  await shot(page, info, "34-added");
});

test("restore to 2 minutes ago, then undo the restore", async ({ page }, info) => {
  test.skip(info.project.name !== "desktop" || !backupEnv.pitr, "set BACKUP_PITR=1 (desktop): Care stops 2 times");
  test.setTimeout(30 * 60_000);
  await open(page, "/restore?kind=pitr", info.project.name);
  const panel = page.getByTestId("pitr-panel");
  const target = await page.evaluate(() => {
    const d = new Date(Date.now() - 120_000);
    const p = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
  });
  await panel.getByTestId("pitr-time").fill(target);
  note(info, `pitr: ${await panel.getByTestId("pitr-warning").textContent()}`);
  await panel.getByTestId("pitr-start").click();
  await page.getByTestId("restore-confirm").click();
  await password(page);
  const started = Date.now();
  await jobSucceeds(page.getByTestId("restore-job"));
  note(
    info,
    `pitr job: ${Math.round((Date.now() - started) / 1000)} s; ${await page.getByTestId("restore-result").textContent()}`,
  );
  await shot(page, info, "35-pitr-done");
  await page.getByTestId("undo-start").click();
  await page.getByTestId("restore-confirm").click();
  await password(page);
  await jobSucceeds(page.getByTestId("restore-job").first());
  await shot(page, info, "36-undo-done");
});
