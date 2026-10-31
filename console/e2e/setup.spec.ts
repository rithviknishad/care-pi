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

  // Step: hardware.
  await expect(page.getByTestId("step-title")).toHaveText("Hardware check");
  await expect(page.getByTestId("host-check-summary")).toContainText("All 43 checks passed");
  await page.getByRole("button", { name: "I read the warnings" }).click();

  // Step: identity. The easy path shows the announced name, not the host name field.
  await expect(page.getByTestId("step-title")).toHaveText("Name and address");
  await expect(page.getByTestId("advertised-name")).toContainText("care.local");
  await page.getByLabel("Clinic name").fill("Sunrise Clinic");
  await page.getByTestId("step-submit").click();

  // Step: certificate.
  await expect(page.getByTestId("step-title")).toHaveText("Certificate");
  await page.getByRole("button", { name: "Use the local certificate" }).click();

  // Step: size. Two plain questions give the preset.
  await expect(page.getByTestId("step-title")).toHaveText("Size");
  await page.getByRole("radio", { name: "1 person" }).click();
  await page.getByRole("radio", { name: "1 or 2" }).click();
  await expect(page.getByTestId("size-result")).toContainText("Preset: solo");
  await page.getByTestId("step-submit").click();

  // The easy path skips env. Step: backups (a dev build can skip it).
  await expect(page.getByTestId("step-title")).toHaveText("Backups");
  await page.getByRole("button", { name: "Skip this step" }).click();

  // Step: Care administrator. The password must not be the console password.
  await expect(page.getByTestId("step-title")).toHaveText("Care administrator");
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
  await expect(page.getByTestId("step-title")).toHaveText("Review");
  await expect(page.getByTestId("review")).toContainText("Sunrise Clinic");
  await expect(page.getByTestId("review")).toContainText("solo");
  await page.getByRole("button", { name: "Start the setup" }).click();

  // Progress over SSE: the steps append, then the hand-off page opens.
  await expect(page).toHaveURL(/\/setup\/progress$/);
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
