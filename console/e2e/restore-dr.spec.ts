import { expect, isMobile, login, mockState, noSideScroll, shot, test, CLAIM_CODE } from "./fixtures";

// The restore of a new device (docs/api/README.md section 13.9.3): the entry of the wizard after the claim, the key,
// the destination, inspect, the job, and the login with the console password of the old device.
test("a new device: restore from a backup folder, then log in as the old device", async ({ page, resetMock }, info) => {
  await resetMock("unclaimed");
  await page.goto("/claim");
  await page.getByRole("button", { name: "Use the claim code" }).click();
  await page.getByLabel("Claim code").fill(CLAIM_CODE);
  await page.getByLabel("New console password").fill("the password of the claim");
  await page.getByLabel("Type the password again").fill("the password of the claim");
  await page.getByRole("button", { name: "Claim the device" }).click();
  await page.getByText("I saved these codes in a safe place.").click();
  await page.getByRole("button", { name: "Continue" }).click();

  // The first page of the wizard offers the restore.
  await expect(page).toHaveURL(/\/setup\/recovery-codes$/);
  await expect(page.getByTestId("restore-entry")).toContainText("Do you replace a device?");
  await page.getByTestId("restore-entry-link").click();
  await expect(page).toHaveURL(/\/setup\/restore$/);

  // The key: a wrong word, then the QR text of the old device.
  const { dr_words: words } = await mockState(page);
  const key = page.getByTestId("dr-key");
  await key.fill([...words.slice(0, 23), "xyzzy"].join(" "));
  await expect(page.getByText("These words are not in the word list: xyzzy.")).toBeVisible();
  await key.fill(`CARE-PI-RK1:${words.map((w) => w.toUpperCase()).join(" ")}`);
  await expect(page.getByTestId("dr-key-count")).toHaveText("24 of 24 words");

  // A folder that has no backup: the agent does not find it.
  await page.getByTestId("dest-choice-usb").click();
  await page.getByRole("tab", { name: "Folder on this device" }).click();
  await page.getByLabel("Folder", { exact: true }).fill("/mnt/nothing");
  await page.getByTestId("dr-inspect").click();
  await expect(page.locator("[data-problem=destination-unreachable]")).toBeVisible();

  // The folder with the backup of the old device.
  await page.getByLabel("Folder", { exact: true }).fill("/mnt/old-backup");
  await page.getByTestId("dr-inspect").click();
  await expect(page.getByTestId("dr-clinic")).toHaveText("Sunrise Clinic");
  await expect(page.getByTestId("dr-inspection")).toContainText("The files are older than the database");
  await expect(page.getByTestId("dr-account")).toContainText("use the console password of the old device");
  if (isMobile(page)) await noSideScroll(page);
  await shot(page, info, "dr-inspection");

  await page.getByTestId("dr-start").click();
  await page.getByTestId("dr-confirm").click();
  await expect(page.getByTestId("job-step-title").first()).toHaveText("Check the recovery key");
  await shot(page, info, "dr-progress", false);

  // The job ends each session: the login page tells which password to use.
  await expect(page).toHaveURL(/\/login(\?next=.*)?$/, { timeout: 30_000 });
  const restored = page.getByTestId("dr-restored");
  await expect(restored).toContainText(
    "This device now runs Sunrise Clinic. Log in with the console password of the old device.",
  );
  // The host name of the old device (care) is not the host of this page (the mock): the new address shows.
  await expect(restored.getByRole("link")).toHaveAttribute("href", "https://care.local:8443/");
  await shot(page, info, "dr-login");
  await login(page, "the password of the claim");
  await expect(page.getByText("The password is not correct.")).toBeVisible();
  await login(page);
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByTestId("status-state")).toHaveText("OK");
  await expect(page.getByTestId("backup-health")).toBeVisible();
});

test("a new device: a wrong key, then SFTP with the host key check", async ({ page, resetMock }) => {
  await resetMock("restore");
  await page.goto("/login?next=%2Fsetup%2Frestore");
  await login(page);
  await expect(page).toHaveURL(/\/setup\/restore$/);
  const { dr_words: words } = await mockState(page);

  // 24 valid words of another key: the checksum of the browser passes, the agent cannot open the repository.
  const other = [...Array(23).fill("abandon"), "art"].join(" ");
  await page.getByTestId("dr-key").fill(other);
  await page.getByTestId("dest-choice-sftp").click();
  await page.getByLabel("Server", { exact: true }).fill("nas.lan");
  await page.getByLabel("User", { exact: true }).fill("care");
  await page.getByLabel("Folder on the server", { exact: true }).fill("/volume1/care");
  await page.getByTestId("dr-inspect").click();
  await expect(page.getByTestId("host-key-fingerprint")).toHaveText(/^SHA256:/);
  await page.getByTestId("host-key-trust").click();
  await expect(page.locator("[data-problem=recovery-key-mismatch]")).toContainText("The recovery key is not correct.");

  // The words of the .txt file (numbered, in 3 columns) also work.
  const rows = Array.from({ length: 8 }, (_, r) =>
    [0, 1, 2].map((c) => `${String(c * 8 + r + 1).padStart(2)} ${words[c * 8 + r]}`).join("   "),
  );
  await page.getByTestId("dr-key").fill(["care-pi recovery key", "Clinic: Sunrise Clinic", "", ...rows].join("\n"));
  await expect(page.getByTestId("dr-key-count")).toHaveText("24 of 24 words");
  await page.getByTestId("dr-inspect").click();
  await expect(page.getByTestId("host-key-fingerprint")).toBeVisible();
  await page.getByTestId("host-key-trust").click();
  await expect(page.getByTestId("dr-clinic")).toHaveText("Sunrise Clinic");
});

test("a failed restore of a new device goes back to the form with the problem", async ({ page, resetMock }) => {
  // The scenario restore: a claimed device with the backup disk of the old device (care-usb1).
  await resetMock("restore", { fail_step: "fetch-db-repo" });
  await page.goto("/login?next=%2Fsetup%2Frestore");
  await login(page);
  const { dr_words: words } = await mockState(page);
  await page.getByTestId("dr-key").fill(words.join(" "));
  await page.getByTestId("dest-choice-usb").click();
  await page.getByTestId("disk-use").click();
  await page.getByTestId("dr-inspect").click();
  await page.getByTestId("dr-start").click();
  await page.getByTestId("dr-confirm").click();
  await expect(page.getByTestId("dr-failed")).toContainText("The restore failed", { timeout: 30_000 });
  await expect(page.getByTestId("dr-failed")).toContainText("fetch-db-repo");
  await expect(page.getByTestId("dr-key")).toBeVisible();
});
