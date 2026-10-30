import { expect, isMobile, login, test } from "./fixtures";

// Journey 4: a staff device without a session opens the certificate page and gets the root CA.
test("the certificate page is public and gives the root CA for each OS", async ({ page, resetMock }) => {
  await resetMock("unclaimed");
  await page.goto("/certificate");
  await expect(page).toHaveURL(/\/certificate$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Install the certificate");
  await expect(page.getByTestId("ca-fingerprint")).toContainText("3F:1D:6C:0E:5A:2B:9E:4D");

  // The page selects the OS of this device from the user agent.
  const userAgent = await page.evaluate(() => navigator.userAgent);
  const expected = /iPhone/.test(userAgent) ? "iPhone and iPad" : /Android/.test(userAgent) ? "Android" : "Windows";
  const selected = page.getByRole("tab", { selected: true });
  await expect(selected).toContainText("Detected");
  await expect(selected).toContainText(expected);
  await expect(page.getByTestId("ca-steps").locator("li").first()).toBeVisible();
  await expect(page.getByRole("img", { name: /QR code/ })).toBeVisible();

  // Linux: the PEM file. No test browser is Linux, so each tab click changes the OS.
  await page.getByRole("tab", { name: "Linux" }).click();
  await expect(page.getByTestId("ca-download")).toHaveAttribute("href", "/api/v1/tls/root-ca?format=pem");
  const pem = await page.request.get("/api/v1/tls/root-ca?format=pem");
  expect(await pem.text()).toContain("-----BEGIN CERTIFICATE-----");

  // Android: 4 steps and the DER file.
  await page.getByRole("tab", { name: "Android" }).click();
  await expect(page).toHaveURL(/\/certificate\?os=android$/);
  await expect(page.getByTestId("ca-steps").locator("li")).toHaveCount(4);
  const der = page.getByTestId("ca-download");
  await expect(der).toHaveAttribute("href", "/api/v1/tls/root-ca?format=der");
  const derResponse = await page.request.get((await der.getAttribute("href")) ?? "");
  expect(derResponse.status()).toBe(200);
  expect(derResponse.headers()["content-type"]).toBe("application/x-x509-ca-cert");
  expect(derResponse.headers()["x-care-pi-fingerprint-sha256"]).toMatch(/^3f1d6c0e/);

  // iOS: the Apple profile.
  await page.getByRole("tab", { name: "iPhone and iPad" }).click();
  await expect(page.getByTestId("ca-download")).toHaveAttribute("href", "/api/v1/tls/root-ca/mobileconfig");
  const profile = await page.request.get("/api/v1/tls/root-ca/mobileconfig");
  expect(profile.headers()["content-type"]).toBe("application/x-apple-aspen-config");

  // Without a session the page has no navigation.
  await expect(page.getByRole("navigation", { name: "Main" })).toHaveCount(0);
});

test("with a session the certificate page has the navigation", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fcertificate");
  await login(page);
  await expect(page).toHaveURL(/\/certificate$/);
  await expect(page.getByTestId("ca-fingerprint")).toContainText("3F:1D:6C:0E");
  if (!isMobile(page)) {
    await expect(
      page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Certificate" }),
    ).toHaveAttribute("aria-current", "page");
  }
});
