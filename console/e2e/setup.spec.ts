import { expect, test } from "./fixtures";

// Journey 1: claim -> recovery codes -> wizard (easy path) -> finish -> SSE progress -> hand-off.
test("claim the device and finish the setup on the easy path", async ({ page, resetMock, requests }) => {
  await resetMock("unclaimed");
  await page.goto("/");
  await expect(page).toHaveURL(/\/claim$/);

  // Claim with the code, set the console password.
  await page.getByRole("button", { name: "Use the claim code" }).click();
  await page.getByLabel("Claim code").fill("4f7k q2mx 9pda");
  await expect(page.getByLabel("Claim code")).toHaveValue("4F7K-Q2MX-9PDA");
  await page.getByLabel("New console password").fill("a long console password");
  await page.getByLabel("Type the password again").fill("a long console password");
  await page.getByRole("button", { name: "Claim the device" }).click();

  // The recovery codes show one time.
  const codes = page.getByTestId("recovery-codes").locator("li");
  await expect(codes).toHaveCount(8);
  const first = (await codes.first().locator("span").last().textContent())?.trim() ?? "";
  expect(first).toMatch(/^[0-9A-Z]{5}-[0-9A-Z]{5}$/);
  const continueButton = page.getByRole("button", { name: "Continue" });
  await expect(continueButton).toBeDisabled();
  await page.getByText("I saved these codes in a safe place.").click();
  await continueButton.click();

  // Step: type one code back.
  await expect(page).toHaveURL(/\/setup\/recovery-codes$/);
  await page.getByLabel("One of your recovery codes").fill(first.toLowerCase());
  await page.getByTestId("step-submit").click();

  // Step: hardware. A short summary; each check is in "Details".
  await expect(page.getByTestId("step-title")).toHaveText("Check the device");
  await expect(page.getByTestId("host-check-summary")).toContainText("All 43 checks passed");
  await expect(page.getByTestId("hardware-summary")).toContainText("Raspberry Pi 5");
  await page.getByRole("button", { name: "I read the notes" }).click();

  // Step: identity. The easy path shows the announced name, not the host name field.
  await expect(page.getByTestId("step-title")).toHaveText("Name your clinic");
  await expect(page.getByTestId("advertised-name")).toContainText("care.local");
  await page.getByLabel("Clinic name").fill("Sunrise Clinic");
  await page.getByTestId("step-submit").click();

  // Step: certificate. One sentence and "Keep Care working offline".
  await expect(page.getByTestId("step-title")).toHaveText("Keep the connection safe");
  await expect(page.getByRole("switch", { name: "Keep Care working offline" })).toBeChecked();
  await page.getByTestId("step-submit").click();

  // Step: size (S65). The 3 preset cards; busy-counter is dimmed on 2 GB with the reason.
  await expect(page.getByTestId("step-title")).toHaveText("How big is your clinic?");
  const presets = page.getByTestId("size-presets");
  await expect(presets.getByRole("radio")).toHaveCount(3);
  await expect(presets.getByRole("radio", { name: /Busy counter/ })).toBeDisabled();
  await expect(presets.locator("[data-value=busy-counter]")).toContainText("Busy counter needs 4 GB or more");
  await expect(presets.locator("[data-value=small-clinic]")).toContainText("Recommended");
  await expect(presets.getByRole("radio", { name: /Small clinic/ })).toBeChecked();
  await presets.getByText("Just me").click();
  await expect(presets.getByRole("radio", { name: /Just me/ })).toBeChecked();
  await page.getByTestId("step-submit").click();

  // The easy path skips env. Step: backups (a dev build can skip it).
  await expect(page.getByTestId("step-title")).toHaveText("Keep a copy of your data");
  await page.getByRole("button", { name: "Skip this step" }).click();

  // Step: Care administrator. The password must not be the console password.
  await expect(page.getByTestId("step-title")).toHaveText("Make the Care administrator");
  await page.getByLabel("User name").fill("admin");
  await page.getByLabel("Email").fill("admin@sunrise.example");
  await page.getByLabel("Care password").fill("a long console password");
  await page.getByLabel("Type the password again").fill("a long console password");
  await page.getByTestId("step-submit").click();
  await expect(page.getByText("same as the console password")).toBeVisible();
  await page.getByLabel("Care password").fill("another long care password");
  await page.getByLabel("Type the password again").fill("another long care password");
  await page.getByTestId("step-submit").click();

  // Review and finish.
  await expect(page.getByTestId("step-title")).toHaveText("Check your choices");
  await expect(page.getByTestId("review")).toContainText("Sunrise Clinic");
  await expect(page.getByTestId("review")).toContainText("Just me");
  await expect(page.getByTestId("review")).toContainText("solo");
  await page.getByRole("button", { name: "Install Care" }).click();

  // Progress over SSE: the steps append, then the hand-off page opens.
  await expect(page).toHaveURL(/\/setup\/progress$/);
  await expect(page.getByTestId("step-title")).toHaveText("Installing Care");
  await expect(page.getByTestId("install-phase")).not.toBeEmpty();
  await expect(page.getByTestId("job-step").first()).toBeVisible();
  await expect(page.getByTestId("job-step-title").first()).toHaveText("Check the settings");
  await expect(page).toHaveURL(/\/setup\/done$/, { timeout: 30_000 });
  await expect(page.getByTestId("care-url")).toHaveText("https://care.local/login");
  await expect(page.getByRole("img", { name: /QR code/ })).toBeVisible();
  await expect(page.getByRole("link", { name: "Install the certificate on this device" })).toBeVisible();
  await expect(page.getByTestId("setup-state")).toHaveText("Ready");

  // The dashboard opens after setup.
  await page.getByRole("link", { name: "Go to the dashboard" }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByTestId("status-state")).toHaveText("OK");
  expect(requests.length).toBeGreaterThan(10);
});
