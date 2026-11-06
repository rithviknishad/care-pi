import { expect, isMobile, login, noSideScroll, shot, test } from "./fixtures";

// The restore on this device (docs/api/README.md sections 13.9.1 and 13.9.2): a point in time with the password
// check and the steps of the job, the undo of that restore, and the files into a side directory.
test("undo a mistake: restore to a point in time, then undo the restore", async ({ page, resetMock }, info) => {
  await resetMock("ready", { force_reauth: true });
  await page.goto("/login?next=%2Frestore");
  await login(page);
  await expect(page).toHaveURL(/\/restore$/);
  await expect(page.getByTestId("restore-kind-device")).toBeVisible();
  await page.getByTestId("restore-kind-pitr").click();
  await expect(page).toHaveURL(/\/restore\?kind=pitr$/);

  // The time: 1 hour ago, typed into the field. The page tells what the operator loses.
  const panel = page.getByTestId("pitr-panel");
  await expect(panel.getByTestId("pitr-slider")).toBeVisible();
  const target = await page.evaluate(() => {
    const d = new Date(Date.now() - 3600_000);
    d.setSeconds(0, 0);
    const p = (n: number) => String(n).padStart(2, "0");
    return {
      input: `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`,
      minute: `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`,
    };
  });
  await panel.getByTestId("pitr-time").fill(target.input);
  await expect(panel.getByTestId("pitr-warning")).toContainText(`You will lose the changes after ${target.minute}.`);
  await expect(panel.getByTestId("pitr-warning")).toContainText("Care stops for about 1 minute.");
  await expect(panel.getByTestId("pitr-files")).toContainText("Nearest file backup:");
  await expect(panel.getByTestId("pitr-files")).toContainText("The files can differ by up to");
  if (isMobile(page)) await noSideScroll(page);
  await shot(page, info, "restore-pitr");

  // A time before the window is not possible.
  await panel.getByTestId("pitr-time").fill("2020-01-01T00:00");
  await expect(panel.getByText("The time is outside the restore window.")).toBeVisible();
  await expect(panel.getByTestId("pitr-start")).toBeDisabled();
  await panel.getByTestId("pitr-time").fill(target.input);

  await panel.getByTestId("pitr-start").click();
  await page.getByTestId("restore-confirm").click();
  await page
    .getByRole("dialog", { name: "Enter your password again" })
    .getByLabel("Console password")
    .fill("correct horse battery staple");
  await page.getByRole("button", { name: "Confirm", exact: true }).click();

  // The steps of the job, in the order of the contract, with the text of the operator.
  const job = page.getByTestId("restore-job");
  const titles = job.getByTestId("job-step-title");
  await expect(titles.nth(0)).toHaveText("Stop the scheduled backups");
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 30_000 });
  await expect(titles).toHaveText([
    "Stop the scheduled backups",
    "Show the maintenance page",
    "Stop Care",
    "Record an undo point",
    "Stop the database",
    "Restore the database",
    "Start the database",
    "Wait until the database accepts changes",
    "Start Care",
    "Wait until Care answers",
    "Remove the maintenance page",
    "Make a full backup",
    "Check the database backups",
    "Start the scheduled backups again",
  ]);
  await expect(page.getByTestId("restore-result")).toContainText(`The database is at ${target.minute}:00 now.`);
  await shot(page, info, "restore-pitr-done");

  // The undo card: the checkpoint of the restore, for 24 hours.
  const undo = page.getByTestId("undo-card");
  await expect(undo).toContainText("Undo the last restore");
  await undo.getByTestId("undo-start").click();
  await page.getByTestId("restore-confirm").click();
  // The job of the undo shows above the panel (the first job of the page).
  await expect(page.getByTestId("restore-job").first().getByTestId("job-progress")).toHaveAttribute(
    "data-job-state",
    "succeeded",
    { timeout: 30_000 },
  );
});

test("files only: restore a files backup into a separate folder", async ({ page, resetMock }, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Frestore%3Fkind%3Dfiles");
  await login(page);
  const panel = page.getByTestId("files-panel");
  await expect(panel.getByTestId("files-snapshot").locator("option")).toHaveCount(6);
  await panel.getByLabel("Only these folders (optional)").fill("patient-bucket/2026/10\n../etc");
  await expect(panel.getByText("Use a path below a bucket")).toBeVisible();
  await expect(panel.getByTestId("files-start")).toBeDisabled();
  await panel.getByLabel("Only these folders (optional)").fill("patient-bucket/2026/10");
  await shot(page, info, "restore-files");
  await panel.getByTestId("files-start").click();
  await page.getByTestId("restore-confirm").click();
  const job = page.getByTestId("restore-job");
  await expect(job.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 30_000 });
  await expect(job.getByTestId("job-step-title")).toHaveText([
    "Check the free disk space",
    "Restore the files",
    "Verify the restored files",
  ]);
  await expect(page.getByTestId("restore-path")).toContainText("/var/lib/care-pi/restore/");
  await expect(page.getByTestId("restore-result")).toContainText("The device read each file back.");
});

test("a new device: this page explains that it is for a new device", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Frestore");
  await login(page);
  await page.getByTestId("restore-kind-device").click();
  await expect(page.getByTestId("restore-device")).toContainText("This device is already set up.");
});
