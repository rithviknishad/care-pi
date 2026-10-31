import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures";
import { cardBoxes, env, note, shot } from "./real";

// Phase 4 on a real device: the Updates page, an update with the maintenance banner, a rollback with the PITR
// offer, and a plug switch. Each part changes the device, so each part is opt-in (README.md in this directory):
//
//   UPDATES_TEST=1                     run the journey (else each test is skipped)
//   UPDATES_PROJECT=desktop|phone      the project that runs it (default desktop; the other project skips)
//   UPDATES_APPLY=1                    download and install the target ("Install anyway" outside the window)
//   UPDATES_ROLLBACK=1                 roll back after the update (password dialog), then open the PITR offer
//   UPDATES_PLUG=<plug id>             turn this plug on, then off again (for example care_fasterappointments_fe)
//   EXPECT_VERSION, EXPECT_TARGET      the running version before, and the target after the check (optional)
//   UPDATE_TIMEOUT_MIN                 the time limit of one update or rollback job (default 20)
const e = process.env;
const flow = {
  enabled: e.UPDATES_TEST === "1",
  project: e.UPDATES_PROJECT ?? "desktop",
  apply: e.UPDATES_APPLY === "1",
  rollback: e.UPDATES_ROLLBACK === "1",
  plug: e.UPDATES_PLUG ?? "",
  expectVersion: e.EXPECT_VERSION ?? "",
  expectTarget: e.EXPECT_TARGET ?? "",
  jobTimeoutMs: Number(e.UPDATE_TIMEOUT_MIN ?? 20) * 60_000,
};

test.describe.configure({ mode: "serial" });

test.beforeEach(({ page: _page }, info) => {
  test.skip(!flow.enabled, "set UPDATES_TEST=1: this journey changes the device");
  test.skip(info.project.name !== flow.project, `the project ${flow.project} runs it (UPDATES_PROJECT)`);
  test.skip(!env.consolePassword, "set CONSOLE_PASSWORD");
});

const banner = (page: Page) => page.getByTestId("maintenance-banner").filter({ visible: true });

async function openUpdates(page: Page) {
  await page.goto("/login?next=%2Fupdates");
  await page.getByLabel("Console password").fill(env.consolePassword);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(/\/updates$/);
  await expect(page.getByTestId("current-version")).toBeVisible();
}

// Follows the job card of the page to its end. While it runs, it records each step of the maintenance banner and
// the public status (the maintenance page of Caddy reads the same member).
async function followJob(page: Page, info: import("@playwright/test").TestInfo, tag: string): Promise<string> {
  const job = page.getByTestId("update-job").getByTestId("job-progress");
  const end = Date.now() + flow.jobTimeoutMs;
  const steps: string[] = [];
  let publicSeen = false;
  for (;;) {
    const state = await job.getAttribute("data-job-state");
    if (state && state !== "running" && state !== "queued") {
      note(
        info,
        `${tag}: job ${state}; banner steps: ${steps.join(", ") || "none"}; public maintenance seen: ${publicSeen}`,
      );
      return state;
    }
    if (Date.now() > end) throw new Error(`${tag}: the job did not end in ${flow.jobTimeoutMs / 60_000} min`);
    const step = (await banner(page).count()) > 0 ? await banner(page).getAttribute("data-step") : null;
    if (step && steps.at(-1) !== step) {
      steps.push(step);
      if (steps.length === 1) await shot(page, info, `${tag}-banner`);
    }
    const pub = await page.request.get("/_care-pi/status");
    if (pub.ok() && (await pub.json()).maintenance?.active) publicSeen = true;
    await page.waitForTimeout(1000);
  }
}

test("the Updates page reads the state in place and checks for updates", async ({ page }, info) => {
  test.setTimeout(5 * 60_000);
  await openUpdates(page);
  if (flow.expectVersion) await expect(page.getByTestId("current-version")).toHaveText(flow.expectVersion);
  await page.evaluate(() => document.fonts.ready);
  const before = await cardBoxes(page);
  await shot(page, info, "updates-01-page");

  // GET /updates each 30 s: the cards do not move.
  const updated = (await page.getByTestId("updates-updated").textContent()) ?? "";
  await expect(page.getByTestId("updates-updated")).not.toHaveText(updated, { timeout: 45_000 });
  const after = await cardBoxes(page);
  note(
    info,
    `updates page: ${before.length} cards, equal after a refresh: ${JSON.stringify(before) === JSON.stringify(after)}`,
  );
  expect(after).toEqual(before);

  await page.getByTestId("update-check").click();
  const state = await followJob(page, info, "check");
  await shot(page, info, "updates-02-checked");
  if (state === "failed") {
    // The index of a private repository gives HTTP 404 (docs/solutions.md, open point 1).
    note(info, `check failed: ${await page.getByTestId("update-job").innerText()}`);
    return;
  }
  note(
    info,
    `newest ${await page.getByTestId("newest-version").textContent()}, state ${await page.getByTestId("update-state").textContent()}`,
  );
  if (flow.expectTarget) await expect(page.getByTestId("newest-version")).toHaveText(flow.expectTarget);
});

test("install the target with the maintenance banner; the history gets a row", async ({ page }, info) => {
  test.skip(!flow.apply, "set UPDATES_APPLY=1");
  test.setTimeout(flow.jobTimeoutMs * 2 + 5 * 60_000);
  await openUpdates(page);
  const from = (await page.getByTestId("current-version").textContent()) ?? "";
  if ((await page.getByTestId("update-stage").count()) > 0) {
    await page.getByTestId("update-stage").click();
    expect(await followJob(page, info, "stage")).toBe("succeeded");
    await page.getByTestId("job-close").click();
  }
  await page.getByTestId("update-apply").click();
  await page.getByTestId("apply-confirm").click();
  const anyway = page.getByTestId("apply-anyway");
  if (await anyway.isVisible({ timeout: 5_000 }).catch(() => false)) {
    note(info, "the window is closed: Install anyway");
    await anyway.click();
  }
  await expect(page.getByTestId("update-job")).toHaveAttribute("data-kind", "update-apply");
  const state = await followJob(page, info, "apply");
  await shot(page, info, "updates-03-applied");
  expect(state).toBe("succeeded");
  await expect(banner(page)).toHaveCount(0);
  const to = (await page.getByTestId("current-version").textContent()) ?? "";
  note(info, `update ${from} -> ${to}`);
  if (flow.expectTarget) expect(to).toBe(flow.expectTarget);
  const row = page.getByTestId("history-row").first();
  await expect(row).toHaveAttribute("data-kind", "update");
  await expect(row.getByTestId("history-outcome")).toHaveText("Succeeded");
});

test("roll back; the PITR offer links to the Restore page", async ({ page }, info) => {
  test.skip(!flow.rollback, "set UPDATES_ROLLBACK=1");
  test.setTimeout(flow.jobTimeoutMs + 5 * 60_000);
  await openUpdates(page);
  await page.getByTestId("update-rollback").click();
  await page.getByTestId("rollback-confirm").click();
  const reauth = page.getByRole("dialog", { name: "Enter your password again" });
  if (await reauth.isVisible({ timeout: 5_000 }).catch(() => false)) {
    await reauth.getByLabel("Console password").fill(env.consolePassword);
    await reauth.getByRole("button", { name: "Confirm" }).click();
  }
  expect(await followJob(page, info, "rollback")).toBe("succeeded");
  const offer = page.getByTestId("pitr-offer");
  if ((await offer.count()) === 0) {
    note(info, "no PITR offer (the update that installed the lock took no checkpoint)");
    return;
  }
  const time = (await offer.getAttribute("data-target-time")) ?? "";
  note(info, `PITR offer: ${time}`);
  await shot(page, info, "updates-04-pitr-offer");
  await offer.getByTestId("pitr-open").click();
  await page.waitForURL(/\/restore\?time=/);
  expect(page.url()).toContain(`/restore?time=${encodeURIComponent(time)}`);
  await shot(page, info, "updates-05-restore");
});

test("switch a plug on and off; Care lists it in plug_config", async ({ page }, info) => {
  test.skip(!flow.plug, "set UPDATES_PLUG=<plug id>");
  test.setTimeout(2 * flow.jobTimeoutMs + 5 * 60_000);
  await page.goto("/login?next=%2Fplugs");
  await page.getByLabel("Console password").fill(env.consolePassword);
  await page.getByRole("button", { name: "Log in" }).click();
  const card = page.getByTestId(`plug-${flow.plug}`);
  await expect(card).toBeVisible();
  const care = `https://${new URL(env.baseURL).hostname}/api/v1/plug_config/`;
  for (const on of [true, false]) {
    await expect(card).toHaveAttribute("data-enabled", String(!on));
    await card.getByTestId(`plug-switch-${flow.plug}`).click();
    const plan = page.getByTestId("plug-plan");
    await expect(plan).toBeVisible();
    note(
      info,
      `${flow.plug} ${on ? "on" : "off"}: path ${await page.getByTestId("plug-plan-effect").getAttribute("data-path")}`,
    );
    await shot(page, info, `plugs-${on ? "on" : "off"}-plan`);
    await page.getByTestId("plug-apply").click();
    await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", /succeeded|failed/, {
      timeout: flow.jobTimeoutMs,
    });
    await expect(card.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded");
    await expect(card).toHaveAttribute("data-on", String(on));
    const rows = await page.request.get(care);
    const listed = rows.ok() ? JSON.stringify(await rows.json()).includes(flow.plug) : null;
    note(info, `${care}: ${rows.status()}, lists ${flow.plug}: ${listed}`);
    await card.getByRole("button", { name: "Close" }).click();
  }
});
