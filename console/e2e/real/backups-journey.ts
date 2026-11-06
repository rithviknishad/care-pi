// Helpers of the backup journeys against a real device (e2e/real/README.md): the recovery key ceremony and a
// destination from the variables BACKUP_*. setup-journey.ts uses them for the step backups, backups.spec.ts for
// the Backups page.
import fs from "node:fs";
import path from "node:path";
import type { Locator, Page, TestInfo } from "@playwright/test";
import { expect } from "../fixtures";
import { env, note, shot } from "./real";

const e = process.env;

export const backupEnv = {
  // usb (prepare BACKUP_DISK, or use a care-pi disk), folder (BACKUP_FOLDER), sftp (BACKUP_SFTP_*), s3 (BACKUP_S3_*).
  // Empty: the setup journey skips the step (a dev build only).
  dest: (e.BACKUP_DEST ?? "") as "" | "usb" | "folder" | "sftp" | "s3",
  name: e.BACKUP_NAME ?? "",
  folder: e.BACKUP_FOLDER ?? "",
  // The disk id of GET /backups/disks (or its model). A disk that is not a care-pi disk is erased only with BACKUP_DISK_WIPE=1.
  disk: e.BACKUP_DISK ?? "",
  diskWipe: e.BACKUP_DISK_WIPE === "1",
  sftpHost: e.BACKUP_SFTP_HOST ?? "",
  sftpPort: e.BACKUP_SFTP_PORT ?? "22",
  sftpUser: e.BACKUP_SFTP_USER ?? "",
  sftpPath: e.BACKUP_SFTP_PATH ?? "",
  // The expected SHA-256 fingerprint of the server (ssh-keygen -lf). Empty: the journey trusts the first key.
  sftpFingerprint: e.BACKUP_SFTP_FINGERPRINT ?? "",
  // The time to wait for the key of the device on the server (the journey writes the line to STATE_DIR).
  sftpWaitMs: Number(e.BACKUP_SFTP_WAIT_S ?? 180) * 1000,
  s3Endpoint: e.BACKUP_S3_ENDPOINT ?? "",
  s3Region: e.BACKUP_S3_REGION ?? "",
  s3Bucket: e.BACKUP_S3_BUCKET ?? "",
  s3Prefix: e.BACKUP_S3_PREFIX ?? "",
  s3Access: e.BACKUP_S3_ACCESS_KEY ?? "",
  s3Secret: e.BACKUP_S3_SECRET_KEY ?? "",
  // Opt-in journeys of backups.spec.ts.
  jobs: e.BACKUP_JOBS === "1",
  pitr: e.BACKUP_PITR === "1",
  addDest: e.BACKUP_ADD_DEST === "1",
};

// keyFile is <STATE_DIR>/recovery-key.txt: the words of the key that the journey made. Keep STATE_DIR private.
export const keyFile = () => path.resolve(env.stateDir, "recovery-key.txt");

// keyCeremony makes the recovery key in `scope` (the step or a dialog), saves the words and the .txt file in
// STATE_DIR, and confirms 3 words.
export async function keyCeremony(page: Page, info: TestInfo, scope: Locator, prefix: string): Promise<string[]> {
  await scope.getByTestId("key-make").click();
  const items = scope.getByTestId("key-words").locator("li");
  await expect(items).toHaveCount(24);
  const words = (await items.locator("span:last-child").allTextContents()).map((w) => w.trim());
  fs.mkdirSync(env.stateDir, { recursive: true });
  fs.writeFileSync(keyFile(), `${words.join(" ")}\n`, { mode: 0o600 });
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    scope.getByRole("button", { name: "Download (.txt)" }).click(),
  ]);
  const txt = path.resolve(env.stateDir, download.suggestedFilename());
  await download.saveAs(txt);
  const text = fs.readFileSync(txt, "utf8");
  for (const w of words) expect(text).toContain(w);
  note(info, `recovery key: 24 words in ${keyFile()}, file ${download.suggestedFilename()}`);
  await shot(page, info, `${prefix}-key-words`);
  await scope.getByText("I saved the recovery key").click();
  await scope.getByTestId("key-saved-continue").click();
  const inputs = scope.getByTestId("key-confirm").locator("input[data-position]");
  await expect(inputs).toHaveCount(3);
  for (let i = 0; i < 3; i++) {
    const n = Number(await inputs.nth(i).getAttribute("data-position"));
    await inputs.nth(i).fill(words[n - 1] ?? "");
  }
  await scope.getByTestId("key-confirm-submit").click();
  await expect(scope.getByTestId("key-ceremony")).toHaveAttribute("data-phase", "confirmed");
  return words;
}

// addDestination fills the destination form of BACKUP_DEST in `scope`, runs the test (and the host key check of
// SFTP, the key of the device on the server, the bucket of S3), then presses the add button.
export async function addDestination(page: Page, info: TestInfo, scope: Locator, prefix: string): Promise<void> {
  const b = backupEnv;
  const choice = b.dest === "folder" ? "usb" : b.dest;
  await scope.getByTestId(`dest-choice-${choice}`).click();
  const form = scope.getByTestId("dest-form");
  if (b.name) await form.getByLabel("Name", { exact: true }).fill(b.name);
  const result = form.getByTestId("dest-test-result");
  if (b.dest === "folder") {
    await form.getByRole("tab", { name: "Use a folder (advanced)" }).click();
    await form.getByLabel("Folder", { exact: true }).fill(b.folder);
    await form.getByTestId("dest-test").click();
  } else if (b.dest === "usb") {
    const disk = form
      .getByTestId("disk")
      .filter({ hasText: b.disk || /./ })
      .filter({ hasNot: page.getByText("This is the system disk") });
    await expect(disk.first()).toBeVisible();
    note(info, `disks: ${(await form.getByTestId("disk").allTextContents()).join(" | ")}`);
    if ((await disk.first().getByTestId("disk-use").count()) > 0) {
      await disk.first().getByTestId("disk-use").click();
    } else {
      expect(b.diskWipe, "the disk is not a care-pi disk: set BACKUP_DISK_WIPE=1 to erase it").toBe(true);
      await disk.first().getByTestId("disk-prepare").click();
      const dialog = page.getByRole("alertdialog");
      const label = /Type (care-[a-z0-9-]+) to confirm/.exec((await dialog.textContent()) ?? "")?.[1] ?? "";
      await dialog.getByTestId("wipe-confirm-input").fill(label);
      await dialog.getByTestId("wipe-confirm").click();
      const reauth = page.getByRole("dialog", { name: "Enter your password again" });
      if (await reauth.isVisible().catch(() => false)) {
        await reauth.getByLabel("Console password").fill(env.consolePassword);
        await page.getByRole("button", { name: "Confirm", exact: true }).click();
      }
    }
  } else if (b.dest === "sftp") {
    await form.getByLabel("Server", { exact: true }).fill(b.sftpHost);
    await form.getByLabel("Port", { exact: true }).fill(b.sftpPort);
    await form.getByLabel("User", { exact: true }).fill(b.sftpUser);
    await form.getByLabel("Folder on the server", { exact: true }).fill(b.sftpPath);
    await form.getByTestId("dest-test").click();
    await expect(form.getByTestId("host-key-fingerprint")).toBeVisible({ timeout: 60_000 });
    const fingerprint = ((await form.getByTestId("host-key-fingerprint").textContent()) ?? "").trim();
    note(info, `sftp host key: ${await form.getByTestId("host-key-type").textContent()} ${fingerprint}`);
    if (b.sftpFingerprint) expect(fingerprint).toBe(b.sftpFingerprint);
    await shot(page, info, `${prefix}-sftp-hostkey`);
    await form.getByTestId("host-key-trust").click();
    await expect(form.getByTestId("install-key").or(result)).toBeVisible({ timeout: 60_000 });
    if (await form.getByTestId("install-key").isVisible()) {
      const line = ((await form.getByTestId("ssh-key-line").textContent()) ?? "").trim();
      const file = path.resolve(env.stateDir, "sftp-authorized-key.txt");
      fs.writeFileSync(file, `${line}\n`);
      note(info, `sftp: add the line of ${file} to ~/.ssh/authorized_keys of ${b.sftpUser}@${b.sftpHost}`);
      await shot(page, info, `${prefix}-sftp-key`);
      // Test again each 10 s until the server accepts the key (the panel goes away) or the time ends.
      const deadline = Date.now() + b.sftpWaitMs;
      while ((await form.getByTestId("install-key").isVisible()) && Date.now() < deadline) {
        await page.waitForTimeout(10_000);
        await form.getByTestId("dest-test-again").click();
        await expect(form.getByText("The device tests the destination.")).toHaveCount(0, { timeout: 60_000 });
      }
    }
  } else if (b.dest === "s3") {
    await form.getByLabel("Endpoint", { exact: true }).fill(b.s3Endpoint);
    if (b.s3Region) await form.getByLabel("Region", { exact: true }).fill(b.s3Region);
    await form.getByLabel("Bucket", { exact: true }).fill(b.s3Bucket);
    if (b.s3Prefix) await form.getByLabel("Prefix", { exact: true }).fill(b.s3Prefix);
    await form.getByLabel("Access key id", { exact: true }).fill(b.s3Access);
    await form.getByLabel("Secret access key", { exact: true }).fill(b.s3Secret);
    await form.getByTestId("dest-test").click();
    await expect(form.getByTestId("create-bucket").or(result)).toBeVisible({ timeout: 60_000 });
    if (await form.getByTestId("create-bucket").isVisible()) await form.getByTestId("create-bucket-button").click();
  }
  await expect(result).toHaveAttribute("data-next", "none", { timeout: 120_000 });
  note(info, `destination test: ${await result.textContent()}`);
  await shot(page, info, `${prefix}-dest-tested`);
  await form.getByTestId("dest-add").click();
}

// backupsWizardStep is the step backups of the setup journey: the key and BACKUP_DEST, or the skip of a dev build.
export async function backupsWizardStep(page: Page, info: TestInfo): Promise<void> {
  await expect(page.getByTestId("step-title")).toHaveText("Keep a copy of your data");
  if (!backupEnv.dest) {
    await expect(page.getByText("This is a dev build. You can skip this step.")).toBeVisible();
    await shot(page, info, "10-backups");
    await page.getByRole("button", { name: "Skip this step" }).click();
    return;
  }
  const step = page.locator("form").filter({ has: page.getByTestId("backups-destinations") });
  await keyCeremony(page, info, step, "10-backups");
  await addDestination(page, info, step, "10-backups");
  await expect(page.getByTestId("dest-row")).toHaveCount(1);
  await shot(page, info, "10-backups-done");
  await page.getByTestId("step-submit").click();
}
