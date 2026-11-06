import { expect, isMobile, login, mockState, noSideScroll, shot, test } from "./fixtures";

// The step backups of the setup wizard (docs/api/README.md sections 13.4 to 13.6): the recovery key ceremony, then a
// network folder (SFTP) with the host key check (TOFU) and the SSH key of the device. Desktop and phone.
test("setup: the recovery key ceremony and an SFTP destination with TOFU", async ({ page, resetMock }, info) => {
  await resetMock("setup", { dev_build: false });
  await page.goto("/login?next=%2Fsetup%2Fbackups");
  await login(page);
  await expect(page.getByTestId("step-title")).toHaveText("Keep a copy of your data");
  // A release build cannot skip the step.
  await expect(page.getByRole("button", { name: "Skip this step" })).toHaveCount(0);
  await expect(page.getByTestId("step-submit")).toBeDisabled();
  await shot(page, info, "wizard-backups-start");

  // 1. Make the key: 24 numbered words, the QR code, Print, and the .txt file.
  await page.getByTestId("key-make").click();
  const words = page.getByTestId("key-words").locator("li");
  await expect(words).toHaveCount(24);
  await expect(page.getByRole("img", { name: "The QR code has the same 24 words." })).toBeVisible();
  const list = (await words.locator("span:last-child").allTextContents()).map((w) => w.trim());
  expect(list.every((w) => /^[a-z]{3,8}$/.test(w))).toBe(true);
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Download (.txt)" }).click(),
  ]);
  expect(download.suggestedFilename()).toBe("care-recovery-care.txt");
  await shot(page, info, "wizard-key-words");
  await expect(page.getByTestId("key-saved-continue")).toBeDisabled();
  await page.getByText("I saved the recovery key").click();
  await page.getByTestId("key-saved-continue").click();

  // The words hide. A reminder shows until the confirmation. Type 3 words: first a wrong one.
  await expect(page.getByTestId("key-words")).toHaveCount(0);
  await expect(page.getByTestId("key-reminder")).toBeVisible();
  const inputs = page.getByTestId("key-confirm").locator("input[data-position]");
  await expect(inputs).toHaveCount(3);
  const positions = await inputs.evaluateAll((els) => els.map((el) => Number(el.getAttribute("data-position"))));
  const right = list[(positions[0] ?? 1) - 1] ?? "";
  await inputs.nth(0).fill(right === "zoo" ? "abandon" : "zoo");
  await inputs.nth(1).fill(list[(positions[1] ?? 1) - 1] ?? "");
  await inputs.nth(2).fill(list[(positions[2] ?? 1) - 1] ?? "");
  await page.getByTestId("key-confirm-submit").click();
  await expect(page.getByText(`This is not word ${positions[0]} of the key.`)).toBeVisible();
  await shot(page, info, "wizard-key-confirm");
  await inputs.nth(0).fill((list[(positions[0] ?? 1) - 1] ?? "").slice(0, 4));
  await page.getByTestId("key-confirm-submit").click();
  await expect(page.getByTestId("key-ceremony")).toHaveAttribute("data-phase", "confirmed");
  await expect(page.getByTestId("key-reminder")).toHaveCount(0);

  // 2. Where should backups go? Network folder (SFTP).
  await page.getByTestId("dest-choice-sftp").click();
  const form = page.getByTestId("dest-form");
  await form.getByLabel("Server", { exact: true }).fill("nas.lan");
  await form.getByLabel("User", { exact: true }).fill("care");
  await form.getByLabel("Folder on the server", { exact: true }).fill("/volume1/care");
  await form.getByTestId("dest-test").click();

  // First contact: the key type and the SHA-256 fingerprint. The operator compares them, then trusts the server.
  await expect(form.getByTestId("host-key-type")).toHaveText("ecdsa-sha2-nistp256");
  await expect(form.getByTestId("host-key-fingerprint")).toHaveText(/^SHA256:[A-Za-z0-9+/]{43}$/);
  await shot(page, info, "wizard-sftp-hostkey");
  await form.getByTestId("host-key-trust").click();

  // The server does not accept the key of the device yet: the line for authorized_keys, with a copy button.
  await expect(form.getByTestId("install-key")).toBeVisible();
  await expect(form.getByTestId("ssh-key-line")).toHaveText(/^restrict ssh-ed25519 \S+ care-pi@care-[0-9a-f]{8}$/);
  await shot(page, info, "wizard-sftp-key");
  await form.getByTestId("dest-test-again").click();
  await expect(form.getByTestId("dest-test-result")).toHaveAttribute("data-next", "none");
  await expect(form.getByTestId("host-key-trusted")).toContainText("ecdsa-sha2-nistp256");
  // S66, item 2: the frequency, "how far back", the RPO sentence, and the explainer of the policy.
  await expect(form.getByTestId("dest-rpo")).toHaveText("If the device breaks, you lose at most 20 minutes of work.");
  await expect(form.getByRole("radio", { name: /Standard/ })).toBeChecked();
  await expect(form.getByTestId("dest-explainer-summary")).toContainText(
    "Every 15 minutes we save a copy. You can go back to any moment in the last 2 days",
  );
  await form.getByTestId("dest-interval").getByRole("button", { name: "Every 30 minutes" }).click();
  await expect(form.getByTestId("dest-rpo")).toContainText("35 minutes");
  await expect(form.getByTestId("dest-explainer-summary")).toContainText("Every 30 minutes we save a copy.");
  await form.getByTestId("dest-keep-longer").click();
  await expect(form.getByTestId("dest-explainer-summary")).toContainText("to any month in the last 2 years");
  await form.getByTestId("dest-keep-custom").first().click();
  await form.getByLabel("1 copy each month, for (months)").fill("12");
  await expect(form.getByTestId("dest-explainer-summary")).toContainText("to any month in the last year");
  await form.getByTestId("dest-add").click();

  await expect(page.getByTestId("dest-row")).toHaveCount(1);
  await expect(page.getByTestId("dest-row")).toContainText("care@nas.lan:/volume1/care");
  await expect(page.getByTestId("dest-add-another")).toBeVisible();
  if (isMobile(page)) await noSideScroll(page);
  await shot(page, info, "wizard-backups-done");
  await page.getByTestId("step-submit").click();
  await expect(page.getByTestId("step-title")).toHaveText("Make the Care administrator");

  // The review shows the backups; the step is done.
  await page.goto("/setup/review");
  await expect(page.getByTestId("review")).toContainText("Destinations: 1. Recovery key: confirmed.");
  const state = await mockState(page);
  expect(state.key_words).toEqual(list);
});

test("setup: a USB disk needs the label typed again before the erase", async ({ page, resetMock }, info) => {
  await resetMock("setup", { force_reauth: true });
  await page.goto("/login?next=%2Fsetup%2Fbackups");
  await login(page);
  // A dev build can skip the step, but this test makes the key and prepares the disk.
  await page.getByTestId("key-make").click();
  await expect(page.getByTestId("key-words").locator("li")).toHaveCount(24);
  const list = (await page.getByTestId("key-words").locator("li span:last-child").allTextContents()).map((w) =>
    w.trim(),
  );
  await page.getByText("I saved the recovery key").click();
  await page.getByTestId("key-saved-continue").click();
  const inputs = page.getByTestId("key-confirm").locator("input[data-position]");
  for (let i = 0; i < 3; i++) {
    const n = Number(await inputs.nth(i).getAttribute("data-position"));
    await inputs.nth(i).fill(list[n - 1] ?? "");
  }
  await page.getByTestId("key-confirm-submit").click();
  await expect(page.getByTestId("key-ceremony")).toHaveAttribute("data-phase", "confirmed");

  await page.getByTestId("dest-choice-usb").click();
  const disks = page.getByTestId("disk");
  await expect(disks).toHaveCount(2);
  await expect(disks.first()).toHaveAttribute("data-usage", "system");
  await expect(disks.first()).toContainText("This is the system disk. You cannot use it.");
  await shot(page, info, "wizard-usb-disks");
  await disks.nth(1).getByTestId("disk-prepare").click();
  const confirm = page.getByRole("alertdialog");
  await expect(confirm).toContainText("deletes everything on the disk SanDisk Extreme 55AE");
  await expect(confirm.getByTestId("wipe-confirm")).toBeDisabled();
  await confirm.getByTestId("wipe-confirm-input").fill("care-usb");
  await expect(confirm.getByTestId("wipe-confirm")).toBeDisabled();
  await confirm.getByTestId("wipe-confirm-input").fill("care-usb1");
  await confirm.getByTestId("wipe-confirm").click();

  // The prepare needs a recent authentication.
  await page
    .getByRole("dialog", { name: "Enter your password again" })
    .getByLabel("Console password")
    .fill("correct horse battery staple");
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.locator("[data-testid=job-step][data-action=wipe-disk]")).toBeVisible();
  await expect(page.getByTestId("dest-test-result")).toHaveAttribute("data-next", "none", { timeout: 20_000 });
  await page.getByTestId("dest-add").click();
  await expect(page.getByTestId("dest-row")).toContainText("care-usb1");
  await page.getByTestId("step-submit").click();
  await expect(page.getByTestId("step-title")).toHaveText("Make the Care administrator");
});
