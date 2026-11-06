import { MOCK_URL, expect, login, noSideScroll, shot, test } from "./fixtures";

// Phase 6 (docs/api/README.md section 15): Settings > Connectivity. Each journey runs on the desktop, the
// iPhone, and the narrow phone of playwright.config.ts.

test("turn on the public name: register, certificate, and the names of the dashboard", async ({
  page,
  resetMock,
}, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fsettings%3Ftab%3Dconnectivity");
  await login(page);
  await expect(page).toHaveURL(/\/settings\?tab=connectivity$/);
  const card = page.getByTestId("public-card");
  await expect(card.getByTestId("mode-state")).toHaveText("Off");
  const hint = card.getByLabel("Start of the name");
  await expect(hint).toHaveValue("care");
  await hint.fill("sunrise");
  await shot(page, info, "connectivity-01-public-off");
  await card.getByTestId("public-on").click();
  const job = page.getByTestId("job-progress");
  await expect(job).toBeVisible();
  await expect(job).toContainText("Register the device with the DNS service");
  await expect(job).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(card.getByTestId("mode-state")).toHaveText("On");
  await expect(card.getByTestId("public-name")).toHaveText("sunrise-k3x9.care-pi.rithviknishad.dev");
  await expect(card.getByTestId("public-cert")).toContainText("Issued");
  await expect(card.getByTestId("public-bookmark")).toContainText("https://sunrise-k3x9.care-pi.rithviknishad.dev/");
  await noSideScroll(page);
  await shot(page, info, "connectivity-02-public-on");

  // The dashboard shows each name with its mode and its certificate.
  await page.goto("/");
  const names = page.getByTestId("served-names");
  await expect(names.locator("li")).toHaveCount(2);
  await expect(names.locator("li").first()).toContainText("Public");
  await expect(names.locator("li").first()).toContainText("sunrise-k3x9.care-pi.rithviknishad.dev");
  await expect(names.locator("li").first()).toContainText("Let's Encrypt certificate");
  await expect(names.locator("li").nth(1)).toContainText("care.local");
  await shot(page, info, "connectivity-03-dashboard");

  // Off: a confirmation, then the job deletes the name.
  await page.goto("/settings?tab=connectivity");
  await card.getByTestId("public-off").click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Turn off" }).click();
  await expect(page.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(card.getByTestId("mode-state")).toHaveText("Off");
});

test("the public name fails when the DNS service does not answer", async ({ page, resetMock }) => {
  await resetMock("ready", { public_fail: "dns" });
  await page.goto("/login?next=%2Fsettings%3Ftab%3Dconnectivity");
  await login(page);
  await page.getByTestId("public-card").getByTestId("public-on").click();
  const job = page.getByTestId("job-progress");
  await expect(job).toHaveAttribute("data-job-state", "failed", { timeout: 20_000 });
  await expect(job).toContainText(/the DNS service does not answer/i);
  await expect(page.getByTestId("public-card").getByTestId("mode-state")).toHaveText("Off");
});

test("turn on Tailscale with the login link: QR code, wait, MagicDNS name", async ({
  page,
  resetMock,
  request,
}, info) => {
  await resetMock("ready", { tailscale_login: "manual" });
  await page.goto("/login?next=%2Fsettings%3Ftab%3Dconnectivity");
  await login(page);
  const card = page.getByTestId("tailscale-card");
  await card.getByLabel("Auth key (optional)").fill("not-a-key");
  await expect(card.getByText("An auth key starts with tskey-.")).toBeVisible();
  await card.getByLabel("Auth key (optional)").fill("");
  await card.getByTestId("tailscale-on").click();
  // The login box comes when tailscaled waits for the login.
  const box = page.getByTestId("tailscale-login");
  await expect(box).toBeVisible({ timeout: 20_000 });
  await expect(box.getByRole("img", { name: "Log in to Tailscale" })).toBeVisible();
  await expect(box.getByTestId("tailscale-login-link")).toHaveAttribute(
    "href",
    /^https:\/\/login\.tailscale\.com\/a\//,
  );
  await expect(page.getByTestId("job-progress")).toContainText("Wait for the login to Tailscale");
  await noSideScroll(page);
  await shot(page, info, "connectivity-04-tailscale-login");
  // The operator logs in on another device.
  expect((await request.post(`${MOCK_URL}/__mock/tailscale-login`)).ok()).toBeTruthy();
  await expect(page.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(box).toBeHidden();
  await expect(card.getByTestId("tailscale-name")).toHaveText("care.tail1234.ts.net");
  await expect(card.getByTestId("mode-state")).toHaveText("On");
  await shot(page, info, "connectivity-05-tailscale-on");
});

test("add a Wi-Fi network with the failover", async ({ page, resetMock }, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fsettings%3Ftab%3Dconnectivity");
  await login(page);
  const card = page.getByTestId("wifi-card");
  await expect(card.getByTestId("wifi-presets")).toContainText("Clinic-Setup");
  await card.getByTestId("wifi-scan").click();
  const nets = card.getByTestId("wifi-networks");
  await expect(nets.getByRole("radio")).toHaveCount(5);
  await nets.getByLabel("Sunrise-Staff").check();
  await card.getByLabel("Passphrase").fill("short");
  await card.getByLabel("Country").fill("in");
  await expect(card.getByLabel("Country")).toHaveValue("IN");
  await card.getByTestId("wifi-save").click();
  await expect(card.getByText("Use 8 to 63 characters.")).toBeVisible();
  await card.getByLabel("Passphrase").fill("a wifi passphrase");
  await expect(card.getByTestId("wifi-failover")).toBeChecked();
  await noSideScroll(page);
  await shot(page, info, "connectivity-06-wifi-form");
  await card.getByTestId("wifi-save").click();
  await expect(page.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 20_000 });
  await expect(card.getByTestId("wifi-ssid")).toContainText("Sunrise-Staff");
  await expect(card.getByTestId("wifi-ssid")).toContainText("Connected");
  await expect(card.getByTestId("wifi-route")).toHaveText("Ethernet carries the traffic now.");
  await expect(card.getByTestId("dhcp-hints")).toContainText("wlan0");
  await shot(page, info, "connectivity-07-wifi-on");
  const state = await (await page.request.get(`${MOCK_URL}/__mock/state`)).json();
  expect(JSON.stringify(state.file)).not.toContain("a wifi passphrase");
});

test("the certificate page shows the name limits and the one-time warning of a new root", async ({
  page,
  resetMock,
}, info) => {
  await resetMock("ready", { connectivity: "migrated" });
  await page.goto("/login?next=%2Fcertificate");
  await login(page);
  await expect(page).toHaveURL(/\/certificate$/);
  const warning = page.getByTestId("ca-changed");
  await expect(warning).toContainText("The certificate of this device changed");
  await expect(warning).toContainText("3F:1D:6C:0E");
  const limits = page.getByTestId("ca-constraints");
  await expect(limits.getByTestId("ca-constraints-dns")).toHaveText(".local.care-pi.rithviknishad.dev");
  await expect(limits).toContainText("192.168.1.0/24");
  // The QR code of 07 §4.4: the certificate page with the DER root in the fragment.
  await expect(page.getByTestId("qr-text")).toHaveAttribute(
    "data-qr",
    /^https:\/\/care-pi\.rithviknishad\.dev\/cert#[A-Za-z0-9_-]+$/,
  );
  await page.getByTestId("qr-mode").getByRole("button", { name: "No internet" }).click();
  await expect(page.getByTestId("qr-text")).toHaveAttribute("data-qr", /^SHA-256 3F:1D:6C:0E/);
  await shot(page, info, "connectivity-08-certificate");
  await warning.getByTestId("ca-changed-dismiss").click();
  await expect(warning).toBeHidden();
  await page.reload();
  // The name limits are in "Check the fingerprint (for experts)".
  await page.getByText("Check the fingerprint (for experts)").click();
  await expect(page.getByTestId("ca-constraints")).toBeVisible();
  await expect(page.getByTestId("ca-changed")).toBeHidden();
});
