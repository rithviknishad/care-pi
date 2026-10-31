import { expect, test } from "../fixtures";
import { caDer, caFingerprint, cardBoxes, env, nextTotpStep, note, pageWidth, saveState, shot, totp } from "./real";

// A phone on a ready device: login -> dashboard -> certificate -> logs -> TOTP on -> logout -> login with TOTP -> TOTP off.
test.describe.configure({ mode: "serial" });

async function loginWithPassword(page: import("@playwright/test").Page) {
  await page.getByLabel("Console password").fill(env.consolePassword);
  await page.getByRole("button", { name: "Log in" }).click();
}

test("log in on a phone and use the console", async ({ page }, info) => {
  test.skip(!env.consolePassword, "set CONSOLE_PASSWORD");
  test.setTimeout(6 * 60_000);

  await page.goto("/");
  await expect(page).toHaveURL(/\/login$/);
  await shot(page, info, "01-login");
  await loginWithPassword(page);
  await expect(page).toHaveURL(/\/dashboard$/);

  // Dashboard: the cards fit the width, the units are a list of cards (wave 4d), nothing scrolls sideways,
  // no label or value is cut, and the cards do not move across 2 refreshes.
  await expect(page.getByTestId("status-state")).toBeVisible();
  const unitCards = page.getByTestId("units-cards").locator("[data-unit-card]");
  await expect.poll(() => unitCards.count()).toBeGreaterThan(5);
  await expect(page.getByTestId("units")).toBeHidden();
  await page.evaluate(() => document.fonts.ready);
  const cards = await cardBoxes(page);
  const width = await pageWidth(page);
  const unitsCard = await page
    .getByTestId("units-card")
    .evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
  const cut = await page
    .locator("main dl > dt, main dl > dd > span")
    .evaluateAll((els) => els.filter((el) => el.scrollWidth > el.clientWidth + 1).map((el) => el.textContent));
  note(
    info,
    `phone dashboard: ${cards.length} cards, page ${width.scroll}/${width.client} px, units card ${unitsCard.scroll}/${unitsCard.client} px, ` +
      `${await unitCards.count()} unit cards, cut texts: ${cut.length}`,
  );
  note(info, `units: ${(await unitCards.allTextContents()).join(" | ")}`);
  await shot(page, info, "02-dashboard");
  expect(cards.length).toBe(8);
  expect(width.scroll).toBeLessThanOrEqual(width.client);
  expect(unitsCard.scroll).toBeLessThanOrEqual(unitsCard.client);
  expect(cut, "cut labels or values").toEqual([]);
  for (let i = 1; i <= 2; i++) {
    const before = (await page.getByTestId("status-updated").textContent()) ?? "";
    await expect(page.getByTestId("status-updated")).not.toHaveText(before, { timeout: 20_000 });
    const after = await cardBoxes(page);
    note(info, `refresh ${i}: card boxes equal: ${JSON.stringify(after) === JSON.stringify(cards)}`);
    expect(after).toEqual(cards);
  }

  // Certificate: the fingerprint of the root CA, the Apple profile, and the QR code.
  await page.goto("/certificate");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Install the certificate");
  await expect(page.getByTestId("ca-fingerprint")).toContainText(/[0-9A-F]{2}:[0-9A-F]{2}/);
  const fingerprint = ((await page.getByTestId("ca-fingerprint").textContent()) ?? "").replace(/\s+/g, "");
  note(info, `certificate page fingerprint: ${fingerprint}`);
  // The page shows the fingerprint in groups of 8 bytes: compare the hex digits.
  const hex = (v: string) => v.toUpperCase().replace(/[^0-9A-F]/g, "");
  if (env.caCert) expect(hex(fingerprint)).toBe(hex(caFingerprint()));
  const selected = page.getByRole("tab", { selected: true });
  await expect(selected).toContainText("iPhone and iPad");
  await expect(selected).toContainText("Detected");
  await expect(page.getByTestId("ca-download")).toHaveAttribute("href", /mobileconfig$/);
  const href = (await page.getByTestId("ca-download").getAttribute("href")) ?? "";
  const profile = await page.request.get(href);
  expect(profile.status()).toBe(200);
  expect(profile.headers()["content-type"]).toBe("application/x-apple-aspen-config");
  const xml = await profile.text();
  const data = /<key>PayloadContent<\/key>\s*<data>([^<]+)<\/data>/.exec(xml)?.[1] ?? "";
  const der = Buffer.from(data.replace(/\s+/g, ""), "base64");
  note(info, `mobileconfig: ${xml.length} bytes, DER ${der.length} bytes`);
  if (env.caCert) expect(der.equals(caDer()), "the DER in the profile is the root CA").toBe(true);
  await expect(page.getByRole("img", { name: /QR code/ })).toBeVisible();
  await shot(page, info, "03-certificate");

  // Logs: follow the agent log. The requests of this page add lines.
  await page.goto("/logs?unit=agent");
  const lines = page.getByTestId("log-line");
  await expect.poll(() => lines.count(), { timeout: 20_000 }).toBeGreaterThan(0);
  const before = await lines.count();
  await page.getByTestId("log-follow").click();
  // Let the stream start, then make one request: the agent writes a line for each request.
  await page.waitForTimeout(2_000);
  const start = await lines.count();
  const t0 = Date.now();
  await page.request.get("/api/v1/health");
  await expect.poll(() => lines.count(), { timeout: 20_000 }).toBeGreaterThan(start);
  note(info, `logs: ${before} lines, ${start} after the follow started, a new line after ${Date.now() - t0} ms`);
  await shot(page, info, "04-logs-follow");
  await page.getByTestId("log-pause").click();

  // Security: TOTP on. A login a few minutes ago counts as the reauthentication; else the dialog asks.
  await page.goto("/security");
  await page.getByRole("button", { name: "Turn on" }).click();
  const reauth = page.getByRole("dialog");
  if (await reauth.isVisible().catch(() => false)) {
    await reauth.getByLabel("Console password").fill(env.consolePassword);
    await reauth.getByRole("button", { name: "Confirm" }).click();
  }
  const key = page.locator("code").filter({ hasText: /^[A-Z2-7 ]{16,}$/ });
  await expect(key).toBeVisible();
  const secret = ((await key.textContent()) ?? "").replace(/\s+/g, "");
  await expect(page.getByRole("img", { name: /authenticator app/ })).toBeVisible();
  await shot(page, info, "05-totp-enrol");
  await page.locator('input[autocomplete="one-time-code"]').fill(totp(secret));
  await page.getByRole("button", { name: "Verify and turn on" }).click();
  await expect(page.getByText(/^On since/)).toBeVisible();
  note(info, `TOTP on (secret ${secret.length} characters)`);
  await shot(page, info, "06-totp-on");

  // Log out, then log in with the password and the TOTP code of the next 30 s step (no replay).
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Log out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await loginWithPassword(page);
  await expect(page.getByLabel("Code from the authenticator app")).toBeVisible();
  await nextTotpStep(page);
  await page.locator('input[autocomplete="one-time-code"]').fill(totp(secret));
  await shot(page, info, "07-login-totp");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  note(info, "login with TOTP: ok");

  // TOTP off.
  await page.goto("/security");
  await page.getByRole("button", { name: "Turn off" }).click();
  if (await reauth.isVisible().catch(() => false)) {
    await reauth.getByLabel("Console password").fill(env.consolePassword);
    await reauth.getByRole("button", { name: "Confirm" }).click();
  }
  await expect(page.getByText("Off.", { exact: true })).toBeVisible();
  await shot(page, info, "08-totp-off");
  note(info, "TOTP off: ok");
  await saveState(page, "phone");
});
