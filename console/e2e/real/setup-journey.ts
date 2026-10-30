import fs from "node:fs";
import path from "node:path";
import type { Page, TestInfo } from "@playwright/test";
import { expect } from "../fixtures";
import { checkHandoffAndDashboard, checkServedChain, env, followSetupJob, note, saveState, shot } from "./real";

// setupJourney is the first contact on a new device: claim -> recovery codes -> wizard -> finish -> job (SSE) ->
// hand-off -> dashboard. desktop.setup.spec.ts and phone.setup.spec.ts run it. It saves the storage state as
// <STATE_DIR>/<stateName>.json.
export async function setupJourney(page: Page, info: TestInfo, stateName: string): Promise<void> {
  if (env.caCert) {
    const chain = await checkServedChain();
    note(info, `served chain against CA_CERT: authorized=${chain.authorized} issuer="${chain.issuer}" ${chain.error}`);
    expect(chain.authorized, chain.error).toBe(true);
  }

  // Claim: the installer printed the code, so the claim code field shows first.
  await page.goto("/");
  await expect(page).toHaveURL(/\/claim$/);
  await expect(page.getByLabel("Claim code")).toBeVisible();
  await expect(page.getByLabel("Password of the device user")).toHaveCount(0);
  await shot(page, info, "01-claim");
  await page.getByLabel("Claim code").fill(env.claimCode.toLowerCase().replace(/-/g, " "));
  await expect(page.getByLabel("Claim code")).toHaveValue(env.claimCode.toUpperCase());
  await page.getByLabel("New console password").fill(env.consolePassword);
  await page.getByLabel("Type the password again").fill(env.consolePassword);
  await shot(page, info, "02-claim-filled");
  await page.getByRole("button", { name: "Claim the device" }).click();

  // The recovery codes show one time. Download the .txt file.
  const items = page.getByTestId("recovery-codes").locator("li");
  await expect(items).toHaveCount(8);
  const codes = (await items.locator("span:last-child").allTextContents()).map((c) => c.trim());
  for (const c of codes) expect(c).toMatch(/^[0-9A-Z]{5}-[0-9A-Z]{5}$/);
  await shot(page, info, "03-recovery-codes");
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Download (.txt)" }).click(),
  ]);
  expect(download.suggestedFilename()).toBe("care-pi-recovery-codes.txt");
  const file = path.join(env.stateDir, "recovery-codes.txt");
  fs.mkdirSync(env.stateDir, { recursive: true });
  await download.saveAs(file);
  const text = fs.readFileSync(file, "utf8");
  for (const c of codes) expect(text).toContain(c);
  expect(text).toContain(new URL(env.baseURL).host);
  note(info, `recovery codes: 8, file ${file} (${text.length} bytes)`);
  const continueButton = page.getByRole("button", { name: "Continue" });
  await expect(continueButton).toBeDisabled();
  await page.getByText("I saved these codes in a safe place.").click();
  await continueButton.click();

  // Step: type one code back.
  await expect(page).toHaveURL(/\/setup\/recovery-codes$/);
  await page.getByLabel("One of your recovery codes").fill(codes[2] ?? "");
  await shot(page, info, "04-confirm-code");
  await page.getByTestId("step-submit").click();

  // Step: hardware. The facts of the device.
  await expect(page.getByTestId("step-title")).toHaveText("Hardware check");
  await expect(page.getByTestId("host-checks").locator("li").first()).not.toHaveText("");
  const facts = page.locator("dl").first();
  await expect(facts).toContainText("Memory (RAM)");
  const fact = async (label: string) => {
    const dd = facts.locator("dt", { hasText: new RegExp(`^${label.replace(/[()]/g, "\\$&")}$`) });
    return ((await dd.locator("xpath=following-sibling::dd[1]").textContent()) ?? "").trim();
  };
  await expect.poll(() => fact("Memory (RAM)")).not.toBe("");
  const hw = {
    model: await fact("Model"),
    ram: await fact("Memory (RAM)"),
    pageSize: await fact("Kernel page size"),
    disk: await fact("Root disk"),
    rtc: await fact("Real-time clock"),
    ethernet: await fact("Ethernet"),
    clock: await fact("Clock"),
  };
  note(info, `hardware: ${JSON.stringify(hw)}`);
  if (env.ram) expect(hw.ram).toBe(env.ram);
  if (env.pageSizeKiB) expect(hw.pageSize).toBe(`${env.pageSizeKiB} KiB`);
  if (env.diskKind) expect(hw.disk).toContain(env.diskKind);
  if (env.rtc) expect(hw.rtc).toBe(env.rtc);
  expect(hw.ethernet).toMatch(/^Connected/);
  for (const ip of env.ips) expect(hw.ethernet).toContain(ip);
  const checks = page.getByTestId("host-checks").locator("li");
  note(info, `host checks: ${await checks.count()}, failed: ${await checks.locator('[aria-label="Failed"]').count()}`);
  await shot(page, info, "05-hardware");
  await page.getByRole("button", { name: "I read the warnings" }).click();

  // Step: identity. The easy path keeps the host name and shows the announced name and the addresses.
  await expect(page.getByTestId("step-title")).toHaveText("Name and address");
  await expect(page.getByTestId("advertised-name")).toContainText(".local");
  const advertised = (await page.getByTestId("advertised-name").textContent()) ?? "";
  const panel = page.getByTestId("advertised-name").locator("..");
  const ipsText = (await panel.locator("p").nth(1).textContent()) ?? "";
  note(info, `identity: "${advertised.trim()}" / "${ipsText.trim()}"`);
  if (env.hostname) expect(advertised).toContain(`${env.hostname}.local`);
  for (const ip of env.ips) expect(ipsText).toContain(ip);
  await expect(page.getByLabel("Device name")).toHaveCount(0);
  await page.getByLabel("Clinic name").fill(env.displayName);
  await shot(page, info, "06-identity");
  await page.getByTestId("step-submit").click();

  // Step: certificate (local CA).
  await expect(page.getByTestId("step-title")).toHaveText("Certificate");
  await shot(page, info, "07-tls");
  await page.getByRole("button", { name: "Use the local certificate" }).click();

  // Step: size, easy path. 2 to 4 people, 3 or 4 at the busiest time: small-clinic.
  await expect(page.getByTestId("step-title")).toHaveText("Size");
  await page.getByRole("radio", { name: "2 to 4 people" }).click();
  await page.getByRole("radio", { name: "3 or 4" }).click();
  await expect(page.getByTestId("size-result")).toContainText("Preset: small-clinic");
  await shot(page, info, "08-size");
  await page.getByTestId("step-submit").click();

  // The easy path skips env. Look at env on the advanced path: the defaults are read-only. Keep it skipped.
  await expect(page.getByTestId("step-title")).toHaveText("Backups");
  await page
    .getByRole("radio", { name: "Advanced" })
    .or(page.getByRole("button", { name: "Advanced" }))
    .click();
  await page.goto("/setup/env");
  await expect(page.getByTestId("step-title")).toHaveText("Environment");
  const defaults = page.locator("details", { hasText: "Defaults of this version (read-only)" });
  await expect(defaults).toHaveCount(2);
  await defaults.first().locator("summary").click();
  await defaults.last().locator("summary").click();
  await expect(defaults.first().locator("dt", { hasText: "DJANGO_SETTINGS_MODULE" })).toBeVisible();
  await expect(defaults.last().locator("dt", { hasText: "REACT_ALLOWED_LOCALES" })).toBeVisible();
  await expect(defaults.locator("input, textarea, select")).toHaveCount(0);
  await expect(page.getByText("No variables.")).toHaveCount(2);
  await shot(page, info, "09-env-advanced");
  await page.getByRole("button", { name: "Skip this step" }).click();
  await expect(page.getByTestId("step-title")).toHaveText("Backups");
  await page
    .getByRole("radio", { name: "Easy" })
    .or(page.getByRole("button", { name: "Easy" }))
    .click();

  // Step: backups. A dev build can skip it.
  await expect(page.getByText("This is a dev build. You can skip this step.")).toBeVisible();
  await shot(page, info, "10-backups");
  await page.getByTestId("step-submit").click();

  // Step: Care administrator.
  await expect(page.getByTestId("step-title")).toHaveText("Care administrator");
  await page.getByLabel("User name").fill(env.careUsername);
  await page.getByLabel("Email").fill(env.careEmail);
  await page.getByLabel("Care password").fill(env.carePassword);
  await page.getByLabel("Type the password again").fill(env.carePassword);
  await shot(page, info, "11-care-admin");
  await page.getByTestId("step-submit").click();

  // Review and finish.
  await expect(page.getByTestId("step-title")).toHaveText("Review");
  const review = page.getByTestId("review");
  await expect(review).toContainText(env.displayName);
  await expect(review).toContainText("small-clinic");
  await expect(review).toContainText(env.careUsername);
  await expect(page.getByText(/^Setup takes about 10 minutes\./)).toBeVisible();
  await shot(page, info, "12-review");
  const finishAt = Date.now();
  await page.getByRole("button", { name: "Start the setup" }).click();
  note(info, `finish clicked at ${new Date(finishAt).toISOString()}`);

  await followSetupJob(page, info, finishAt);
  await checkHandoffAndDashboard(page, info);
  await saveState(page, stateName);
}
