import type { Page } from "@playwright/test";
import { expect, isMobile, login, noSideScroll, READY_PASSWORD, shot, test } from "./fixtures";

// Phase 4: the Updates page (docs/api/README.md section 14, docs/console-ui.md section 11).

async function open(page: Page, path: string) {
  await page.goto(`/login?next=${encodeURIComponent(path)}`);
  await login(page);
  await expect(page).toHaveURL((u) => u.pathname === path.split("?")[0]);
}

// The banner has one slot for phones and one for the desktop; only one is visible.
const banner = (page: Page) => page.getByTestId("maintenance-banner").filter({ visible: true });

test("check, download, and install an update; the maintenance banner shows and clears", async ({
  page,
  resetMock,
  requests,
}, info) => {
  await resetMock("ready", { step_ms: 250 });
  await open(page, "/updates");
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.0");
  await expect(page.getByTestId("track-badge")).toHaveText("Stable");
  await expect(page.getByTestId("update-state")).toHaveText("Up to date");
  await expect(page.getByTestId("history-row")).toHaveCount(1);
  await expect(banner(page)).toHaveCount(0);
  await shot(page, info, "updates-idle");

  // Check now: the index has 2026.11.1.
  await page.getByTestId("update-check").click();
  const job = page.getByTestId("update-job");
  await expect(job).toHaveAttribute("data-kind", "update-check");
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded");
  await expect(job.getByTestId("job-step-title").first()).toHaveText("Download the list of versions");
  await expect(page.getByTestId("newest-version")).toHaveText("2026.11.1");
  await expect(page.getByTestId("update-state")).toHaveText("Available. Not downloaded.");
  await expect(page.getByText("The downloads happen while Care keeps running.")).toBeVisible();
  await page.getByTestId("job-close").click();
  await shot(page, info, "updates-available");

  // Download (stage): Care keeps running. The banner names the job while it runs (S67), then clears.
  await page.getByTestId("update-stage").click();
  await expect(job).toHaveAttribute("data-kind", "update-stage");
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded");
  await expect(page.getByTestId("update-state")).toHaveText("Downloaded. Ready to install.");
  await expect(banner(page)).toHaveCount(0);

  // Install now: the confirmation, then 409 update-window-closed, then "Install anyway" (now: true).
  await page.getByTestId("update-apply").click();
  const dialog = page.getByTestId("apply-dialog");
  await expect(dialog).toContainText("Install 2026.11.1 now?");
  await expect(dialog).toContainText("Care stops for a few minutes. Staff see a maintenance page.");
  await page.getByTestId("apply-confirm").click();
  await expect(dialog).toContainText("The update window is closed");
  await expect(dialog).toContainText(/The window opens at \w{3} \d{4}-\d{2}-\d{2} 02:00/);
  await page.getByTestId("apply-anyway").click();
  await expect(dialog).toBeHidden();
  await expect(job).toHaveAttribute("data-kind", "update-apply");

  // The maintenance banner of the top bar shows the step that runs, then clears.
  await expect(banner(page)).toBeVisible({ timeout: 15_000 });
  await expect(banner(page)).toContainText("Maintenance");
  // The banner follows the step: the migrations run behind the maintenance page.
  await expect(banner(page)).toHaveAttribute("data-step", "migrate", { timeout: 15_000 });
  await expect(banner(page)).toContainText("Update the database");
  await shot(page, info, "updates-applying", false);
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 30_000 });
  await expect(banner(page)).toHaveCount(0);
  for (const text of ["Show the maintenance page", "Make a backup point of the database", "Update the database"]) {
    await expect(job.getByTestId("job-step-title").filter({ hasText: text })).toHaveCount(1);
  }
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.1");
  await expect(page.getByTestId("rollback-to")).toHaveText("2026.11.0");

  // The history: a new row at the top.
  const row = page.getByTestId("history-row").first();
  await expect(row).toHaveAttribute("data-kind", "update");
  await expect(row.getByTestId("history-what")).toHaveText("Update: 2026.11.0 → 2026.11.1");
  await expect(row.getByTestId("history-outcome")).toHaveText("Succeeded");
  await expect(page.getByTestId("history-row")).toHaveCount(2);
  await page.getByTestId("job-close").click();
  await shot(page, info, "updates-history");
  await noSideScroll(page);

  for (const path of ["/api/v1/updates/check", "/api/v1/updates/stage", "/api/v1/updates/apply"]) {
    expect(requests.some((u) => u.endsWith(path))).toBeTruthy();
  }
});

test("roll back with the password, then the offer to restore the database", async ({ page, resetMock }, info) => {
  await resetMock("ready", { updates: "updated", stale_auth: true });
  await open(page, "/updates");
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.1");
  await expect(page.getByTestId("rollback-to")).toHaveText("2026.11.0");
  await expect(page.getByTestId("rollback-pitr-note")).toContainText("A rollback does not undo these changes.");
  await expect(page.getByTestId("pitr-offer")).toHaveCount(0);

  await page.getByTestId("update-rollback").click();
  await page.getByTestId("rollback-confirm").click();
  // POST /updates/rollback needs a recent authentication (x-reauth).
  const reauth = page.getByRole("dialog", { name: "Enter your password again" });
  await reauth.getByLabel("Console password").fill(READY_PASSWORD);
  await reauth.getByRole("button", { name: "Confirm" }).click();

  const job = page.getByTestId("update-job");
  await expect(job).toHaveAttribute("data-kind", "update-rollback");
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.0");
  await expect(page.getByTestId("history-row").first()).toHaveAttribute("data-kind", "rollback");

  // The PITR hand-off: the time of the checkpoint in the zone of the device (02:05:40Z = 07:35 in Asia/Kolkata).
  const offer = page.getByTestId("pitr-offer");
  await expect(offer).toContainText("Restore the database to 07:35 (before the update)?");
  await expect(offer).toContainText("2026.11.0 runs again, but the database keeps the changes that the update made.");
  await expect(offer).toHaveAttribute("data-target-time", "2026-11-22T02:05:40Z");
  await offer.scrollIntoViewIfNeeded();
  await shot(page, info, "updates-pitr-offer", false);

  await offer.getByTestId("pitr-open").click();
  await expect(page).toHaveURL(/\/restore\?time=2026-11-22T02%3A05%3A40Z$/);
  // The Restore page (Phase 3) opens "Undo a mistake" with the time of the offer filled in.
  const offered = page.getByTestId("restore-offered");
  await expect(offered).toHaveAttribute("data-time", "2026-11-22T02:05:40Z");
  await expect(offered).toContainText("An update offered a restore of the database to");
  await expect(offered).toContainText("(in UTC: 2026-11-22 02:05:40Z)");
  await expect(page.getByTestId("restore-panel")).toHaveAttribute("data-kind", "pitr");
  const local = await page.evaluate(() => {
    const d = new Date("2026-11-22T02:05:40Z");
    const p = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  });
  await expect(page.getByTestId("pitr-time")).toHaveValue(local);

  // "Not now" hides the offer for the session.
  await page.getByRole("link", { name: "Back to the Updates page" }).click();
  await page.getByTestId("pitr-dismiss").click();
  await expect(page.getByTestId("pitr-offer")).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.0");
  await expect(page.getByTestId("pitr-offer")).toHaveCount(0);
});

test("a failed update rolls back by itself and offers the restore", async ({ page, resetMock }) => {
  await resetMock("ready", { updates: "staged", update_fail: "migrate", window_open: true });
  await open(page, "/updates");
  await expect(page.getByTestId("update-state")).toHaveText("Downloaded. Ready to install.");
  await page.getByTestId("update-apply").click();
  await page.getByTestId("apply-confirm").click();
  const job = page.getByTestId("update-job");
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "failed", { timeout: 20_000 });
  await expect(job.locator('[data-problem="migration-failed"]')).toContainText(
    "The database update failed. The previous version runs again.",
  );
  await expect(job.locator('[data-action="rollback"]')).toHaveAttribute("data-state", "succeeded");
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.0");
  await expect(page.getByTestId("pitr-offer")).toBeVisible();
  const row = page.getByTestId("history-row").first();
  await expect(row).toHaveAttribute("data-outcome", "rolled-back");
  await expect(row).toContainText("Restore offered");
  await expect(banner(page)).toHaveCount(0);
});

test("an update that needs a newer agent tells the operator to update the agent first", async ({ page, resetMock }) => {
  await resetMock("ready", { updates: "agent-too-old" });
  await open(page, "/updates");
  const alert = page.getByTestId("agent-too-old");
  await expect(alert).toContainText("Update the agent first");
  await expect(alert).toContainText("care-clinic 2026.11.1 needs agent v0.5.0 or newer. This device has agent v0.4.1.");
  await expect(alert).toContainText("run the installer again");
  await expect(page.getByTestId("update-state")).toContainText("Blocked: this version needs agent v0.5.0 or newer");
  await expect(page.getByTestId("update-apply")).toHaveCount(0);
  await expect(page.getByTestId("update-stage")).toHaveCount(0);
  await page.goto("/dashboard");
  await expect(page.getByTestId("updates-line")).toHaveText("2026.11.1 is blocked: the agent is too old");
});

test("the dashboard shows the update and the maintenance state", async ({ page, resetMock }) => {
  await resetMock("ready", { updates: "staged", window_open: true, step_ms: 500 });
  await open(page, "/dashboard");
  await expect(page.getByTestId("updates-line")).toHaveText("2026.11.1 is ready to install");
  await page.getByTestId("updates-line").getByRole("link").click();
  await expect(page).toHaveURL(/\/updates$/);
  await page.getByTestId("update-apply").click();
  await page.getByTestId("apply-confirm").click();
  await expect(banner(page)).toBeVisible({ timeout: 15_000 });
  // S67: the banner links to the page of the job, on each page; the dashboard shows the same step in its problems.
  await page.goto("/dashboard");
  await expect(page.getByTestId("status-maintenance")).toContainText("Maintenance in progress:");
  await expect(page.getByTestId("status-state")).toHaveText("Maintenance");
  await expect(banner(page)).toBeVisible();
  await expect(banner(page)).toHaveAttribute("href", /^\/jobs\/\d{8}T\d{6}\.\d{3}Z-update-apply$/);
  await expect(banner(page)).toContainText(/step \d+ of \d+/);
  // The total is the planned step count of the job (the staged update: 10 cutover steps and prune-images), not the
  // count of the steps that started, so the banner never says "step N of N" before the end.
  const [, n, total] = /step (\d+) of (\d+)/.exec((await banner(page).textContent()) ?? "") ?? [];
  expect(Number(total)).toBeGreaterThan(1);
  expect(Number(n)).toBeLessThanOrEqual(Number(total));
  expect(Number(total)).toBe(11);
});

test("change the window: the plan preview and the apply job", async ({ page, resetMock }) => {
  await resetMock("ready");
  await open(page, "/updates");
  await expect(page.getByTestId("window-summary")).toHaveText("Sun, 02:00 to 05:00 (3 h)");
  await expect(page.getByTestId("window-zone")).toHaveText("Asia/Kolkata (UTC+05:30)");
  await expect(page.getByTestId("dst-note")).toHaveText("Asia/Kolkata has no daylight saving time.");
  await page.getByTestId("window-edit").click();
  const editor = page.getByTestId("window-editor");
  await editor.getByRole("button", { name: "Saturday" }).click();
  await editor.getByTestId("window-start").fill("23:00");
  await editor.getByTestId("window-end").fill("02:00");
  await expect(editor.getByTestId("window-preview")).toHaveText(
    "New window: Sat, Sun, 23:00 to 02:00 the next day (3 h)",
  );
  await editor.getByTestId("window-auto").click();
  await editor.getByTestId("config-preview").click();
  await expect(editor.getByTestId("plan-preview")).toContainText("daemon-reload");
  await editor.getByTestId("config-apply").click();
  await expect(editor.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(page.getByTestId("window-summary")).toHaveText("Sat, Sun, 23:00 to 02:00 the next day (3 h)");
  await expect(page.getByTestId("window-auto-state")).toHaveText("On");
});

test("pin a version: PATCH /config with If-Match, then a check", async ({ page, resetMock, requests }) => {
  await resetMock("ready", { updates: "available" });
  await open(page, "/updates");
  await page.getByTestId("track-options").getByText("Pinned", { exact: true }).click();
  await expect(page.getByTestId("pin-version")).toBeVisible();
  await page.getByTestId("pin-version").selectOption("2026.11.0");
  await page.getByTestId("track-save").click();
  await expect(page.getByTestId("update-job")).toHaveAttribute("data-kind", "update-check");
  await expect(page.getByTestId("track-badge")).toHaveText("Pinned");
  await expect(page.getByTestId("update-state")).toHaveText("Pinned. Newer versions exist.");
  expect(requests.some((u) => u.includes("/api/v1/config"))).toBeTruthy();
  // Custom: the refs are in Settings.
  await page.getByTestId("track-options").getByText("Custom (Not supported)").click();
  await page.getByTestId("custom-refs-link").click();
  await expect(page).toHaveURL(/\/settings\?tab=solution$/);
  await expect(page.getByLabel("Care image (tag or digest)")).toBeVisible();
});

// The page does not move when the data arrives (docs/console-ui.md section 5): hold each read of the page,
// record the box of each card (the skeletons), release, and compare.
test("the Updates page keeps its layout when the data arrives", async ({ page, resetMock, browserName }) => {
  await resetMock("ready");
  await open(page, "/updates");
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.0");

  if (browserName === "chromium") {
    await page.addInitScript(() => {
      (window as unknown as { __cls: number }).__cls = 0;
      new PerformanceObserver((list) => {
        for (const e of list.getEntries() as unknown as { value: number; hadRecentInput: boolean }[]) {
          if (!e.hadRecentInput) (window as unknown as { __cls: number }).__cls += e.value;
        }
      }).observe({ type: "layout-shift", buffered: true });
    });
  }
  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route(/\/api\/v1\/(updates|status|solutions)/, async (route) => {
    await held;
    await route.continue();
  });
  await page.reload();
  await expect(page.getByTestId("history-card")).toBeVisible();
  await expect(page.getByTestId("current-version")).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready);
  const cards = page.locator("main [data-slot=card]");
  const boxes = () =>
    cards.evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        return [r.x, r.y + window.scrollY, r.width, r.height].map((v) => Math.round(v));
      }),
    );
  const before = await boxes();
  expect(before.length).toBe(7);

  release();
  await expect(page.getByTestId("current-version")).toHaveText("2026.11.0");
  await expect(page.getByTestId("agent-version")).toHaveText("v0.4.1");
  await expect(page.getByTestId("os-pending")).toContainText("packages");
  await expect(page.getByTestId("window-zone")).toContainText("Asia/Kolkata");
  await expect(page.getByTestId("history-row")).toHaveCount(1);
  const after = await boxes();
  console.log(
    `updates cards (${page.viewportSize()?.width} px): ${before.length} boxes, equal: ${JSON.stringify(before) === JSON.stringify(after)}`,
  );
  expect(after).toEqual(before);
  if (browserName === "chromium") {
    const cls = await page.evaluate(() => (window as unknown as { __cls: number }).__cls);
    console.log(`updates CLS (${page.viewportSize()?.width} px): ${cls.toFixed(4)}`);
    expect(cls).toBeLessThan(0.01);
  }
  await noSideScroll(page);
  if (isMobile(page)) {
    const cut = await page
      .locator("main dl > dt")
      .evaluateAll((els) => els.filter((el) => el.scrollWidth > el.clientWidth + 1).map((el) => el.textContent));
    expect(cut, "cut labels").toEqual([]);
  }
});

test("a device without Phase 4 shows the not-available page", async ({ page, resetMock }) => {
  await resetMock("ready", { phase4: false });
  await open(page, "/updates");
  await expect(page.getByTestId("not-available")).toContainText("This feature comes in Phase 4.");
  await page.goto("/plugs");
  await expect(page.getByTestId("not-available")).toContainText("This feature comes in Phase 4.");
  await page.goto("/dashboard");
  await expect(page.getByTestId("updates-line")).toHaveText("Not available in this version");
});

test("a withdrawn running version shows a warning", async ({ page, resetMock }) => {
  await resetMock("ready", { updates: "withdrawn" });
  await open(page, "/updates");
  await expect(page.getByTestId("release-state")).toHaveText("Withdrawn");
  await expect(page.getByTestId("withdrawn-warning")).toContainText("The maintainers withdrew this version");
  await expect(page.getByTestId("withdrawn-warning")).toContainText(
    "Reason: The appointment list of care develop of 2026-11-13 is slow.",
  );
  await expect(page.getByTestId("update-state")).toHaveText("Available. Not downloaded.");
});

// S69: a power loss during the cutover. The agent rolled back at the next start; the history says so in plain words,
// and the restore offer of the database stays.
test("an update that a power loss interrupted shows a plain sentence and the restore offer", async ({
  page,
  resetMock,
}, info) => {
  await resetMock("ready", { updates: "recovered" });
  await open(page, "/updates");
  const rows = page.getByTestId("history-row");
  await expect(rows.first()).toHaveAttribute("data-kind", "rollback");
  await expect(rows.first()).toHaveAttribute("data-outcome", "rolled-back");
  const sentence = "The update was interrupted. Care returned to the previous version.";
  await expect(rows.first().getByTestId("history-sentence")).toHaveText(sentence);
  await expect(rows.first()).toContainText("After the restart of the device");
  await expect(rows.nth(1)).toHaveAttribute("data-kind", "update");
  await expect(rows.nth(1).getByTestId("history-sentence")).toHaveText(sentence);
  await expect(page.getByTestId("pitr-offer")).toBeVisible();
  await expect(page.getByTestId("pitr-open")).toHaveAttribute("href", /^\/restore\?time=/);
  await shot(page, info, "updates-recovered");
});
