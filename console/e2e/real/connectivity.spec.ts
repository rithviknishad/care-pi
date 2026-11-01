import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures";
import { env, note, shot, visibleWithin } from "./real";

// Phase 6 on a real device (plans/08-spike-log.md, "Phase 6 exit test — plan"): the public name, Tailscale, and
// Wi-Fi. Each part changes the device, so each part is opt-in (README.md in this directory):
//
//   CONNECTIVITY_TEST=1                run the journey (else each test is skipped)
//   CONNECTIVITY_PROJECT=desktop|phone the project that runs it (default desktop; the other project skips)
//   PUBLIC_HINT=<hint>                 turn the public name on with this hint (empty: skip)
//   PUBLIC_STAGING=1                   use the staging directory of Let's Encrypt (the first run)
//   PUBLIC_OFF=1                       turn the public name off at the end (the name does not come back)
//   TAILSCALE_AUTH_KEY=tskey-…         turn Tailscale on with this auth key
//   TAILSCALE_LOGIN=1                  turn Tailscale on with the login link: the test prints the link and waits
//   TAILSCALE_OFF=1                    turn Tailscale off at the end
//   WIFI_SSID, WIFI_PSK, WIFI_COUNTRY  add this Wi-Fi network (failover on); WIFI_REMOVE=1 removes it at the end
//   CONNECTIVITY_TIMEOUT_MIN           the time limit of one job (default 20)
const e = process.env;
const flow = {
  enabled: e.CONNECTIVITY_TEST === "1",
  project: e.CONNECTIVITY_PROJECT ?? "desktop",
  hint: e.PUBLIC_HINT ?? "",
  staging: e.PUBLIC_STAGING === "1",
  publicOff: e.PUBLIC_OFF === "1",
  authKey: e.TAILSCALE_AUTH_KEY ?? "",
  login: e.TAILSCALE_LOGIN === "1",
  tailscaleOff: e.TAILSCALE_OFF === "1",
  ssid: e.WIFI_SSID ?? "",
  psk: e.WIFI_PSK ?? "",
  country: e.WIFI_COUNTRY ?? "",
  wifiRemove: e.WIFI_REMOVE === "1",
  jobTimeoutMs: Number(e.CONNECTIVITY_TIMEOUT_MIN ?? 20) * 60_000,
};

test.describe.configure({ mode: "serial" });

test.beforeEach(({ page: _page }, info) => {
  test.skip(!flow.enabled, "set CONNECTIVITY_TEST=1: this journey changes the device");
  test.skip(info.project.name !== flow.project, `the project ${flow.project} runs it (CONNECTIVITY_PROJECT)`);
  test.skip(!env.consolePassword, "set CONSOLE_PASSWORD");
});

async function openConnectivity(page: Page) {
  await page.goto("/login?next=%2Fsettings%3Ftab%3Dconnectivity");
  await page.getByLabel("Console password").fill(env.consolePassword);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(/\/settings\?tab=connectivity$/);
  await expect(page.getByTestId("connectivity")).toBeVisible();
}

async function jobEnd(page: Page): Promise<string> {
  const job = page.getByTestId("job-progress");
  await expect(job).toHaveAttribute("data-job-state", /succeeded|failed|cancelled/, { timeout: flow.jobTimeoutMs });
  return (await job.getAttribute("data-job-state")) ?? "";
}

// PUT /tls/tailscale and the removals need a recent authentication (x-reauth): the console asks for the password
// again only when the last check is old.
async function password(page: Page) {
  const reauth = page.getByRole("dialog", { name: "Enter your password again" });
  if (await visibleWithin(reauth, 3000)) {
    await reauth.getByLabel("Console password").fill(env.consolePassword);
    await reauth.getByRole("button", { name: "Confirm" }).click();
  }
}

test("public name: enrol, certificate, bookmark advice", async ({ page }, info) => {
  test.skip(!flow.hint, "set PUBLIC_HINT");
  test.setTimeout(flow.jobTimeoutMs + 5 * 60_000);
  await openConnectivity(page);
  const card = page.getByTestId("public-card");
  if ((await card.getByTestId("mode-state").textContent()) === "Off") {
    await card.getByLabel("Start of the name").fill(flow.hint);
    if (flow.staging) await card.getByTestId("public-staging").click();
    await card.getByTestId("public-on").click();
    const state = await jobEnd(page);
    note(info, `public enrol: ${state}`);
    expect(state).toBe("succeeded");
  }
  const name = (await card.getByTestId("public-name").textContent()) ?? "";
  expect(name).toMatch(new RegExp(`^${flow.hint}-[a-z2-7]{4}\\.care-pi\\.rithviknishad\\.dev$`));
  note(info, `public name: ${name}; certificate: ${await card.getByTestId("public-cert").textContent()}`);
  await shot(page, info, "connectivity-public");
});

test("Tailscale: enrol with a key or the login link", async ({ page }, info) => {
  test.skip(!flow.authKey && !flow.login, "set TAILSCALE_AUTH_KEY or TAILSCALE_LOGIN=1");
  test.setTimeout(flow.jobTimeoutMs + 20 * 60_000);
  await openConnectivity(page);
  const card = page.getByTestId("tailscale-card");
  if ((await card.getByTestId("mode-state").textContent()) === "Off") {
    if (flow.authKey) await card.getByLabel("Auth key (optional)").fill(flow.authKey);
    await card.getByTestId("tailscale-on").click();
    await password(page);
    if (!flow.authKey) {
      const link = page.getByTestId("tailscale-login-link");
      await expect(link).toBeVisible({ timeout: 5 * 60_000 });
      note(info, `open this login link and log in: ${await link.getAttribute("href")}`);
      await shot(page, info, "connectivity-tailscale-login");
    }
    const state = await jobEnd(page);
    note(info, `tailscale enrol: ${state}`);
    expect(state).toBe("succeeded");
  }
  note(info, `tailscale name: ${await card.getByTestId("tailscale-name").textContent()}`);
  await shot(page, info, "connectivity-tailscale");
});

test("Wi-Fi: scan, add with the failover", async ({ page }, info) => {
  test.skip(!flow.ssid || !flow.psk, "set WIFI_SSID and WIFI_PSK");
  test.setTimeout(flow.jobTimeoutMs + 5 * 60_000);
  await openConnectivity(page);
  const card = page.getByTestId("wifi-card");
  await card.getByTestId("wifi-scan").click();
  const nets = card.getByTestId("wifi-networks");
  await expect(nets).toBeVisible({ timeout: 30_000 });
  const listed = nets.getByLabel(flow.ssid, { exact: true });
  if (await listed.count()) await listed.check();
  else {
    await nets.getByLabel("Other network (type the name)").check();
    await card.getByLabel("Network name").fill(flow.ssid);
  }
  await card.getByLabel("Passphrase").fill(flow.psk);
  if (flow.country) await card.getByLabel("Country").fill(flow.country);
  await card.getByTestId("wifi-save").click();
  const state = await jobEnd(page);
  note(
    info,
    `wi-fi: ${state}; ${await card
      .getByTestId("wifi-route")
      .textContent()
      .catch(() => "")}`,
  );
  expect(state).toBe("succeeded");
  await expect(card.getByTestId("wifi-ssid")).toContainText(flow.ssid);
  await shot(page, info, "connectivity-wifi");
});

test("clean up: the opt-in removals", async ({ page }) => {
  test.skip(!flow.publicOff && !flow.tailscaleOff && !flow.wifiRemove, "set PUBLIC_OFF, TAILSCALE_OFF, or WIFI_REMOVE");
  test.setTimeout(3 * flow.jobTimeoutMs);
  await openConnectivity(page);
  const off = async (cardId: string, buttonId: string, label: string) => {
    await page.getByTestId(cardId).getByTestId(buttonId).click();
    await page.getByRole("alertdialog").getByRole("button", { name: label }).click();
    await password(page);
    expect(await jobEnd(page)).toBe("succeeded");
    await page.getByRole("button", { name: "Close" }).click();
  };
  if (flow.wifiRemove) await off("wifi-card", "wifi-remove", "Remove the Wi-Fi");
  if (flow.tailscaleOff) await off("tailscale-card", "tailscale-off", "Turn off");
  if (flow.publicOff) await off("public-card", "public-off", "Turn off");
});
