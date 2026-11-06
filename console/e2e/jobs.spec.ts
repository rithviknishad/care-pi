import { MOCK_URL, READY_PASSWORD, expect, isMobile, login, noSideScroll, shot, test } from "./fixtures";

// S67: each running job from each tab and each device. S68: "Copy" of a job is the plain-text transcript of the
// agent (GET /jobs/{id}/export).

const banner = (page: import("@playwright/test").Page) =>
  page.getByTestId("maintenance-banner").filter({ visible: true });

async function startJob(request: import("@playwright/test").APIRequestContext, slow = 40): Promise<string> {
  const res = await request.post(`${MOCK_URL}/__mock/start-job`, { data: { slow } });
  expect(res.ok()).toBeTruthy();
  return ((await res.json()) as { id: string }).id;
}

test("a job of another device shows in the banner, on the Jobs page, and on its page after a reload", async ({
  page,
  resetMock,
  request,
  browserName,
}, info) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fdashboard");
  await login(page);
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(banner(page)).toHaveCount(0);

  // The CLI starts an apply. The banner of the open page names it and its step, and links to its page.
  const id = await startJob(request);
  await expect(banner(page)).toBeVisible({ timeout: 20_000 });
  await expect(banner(page)).toContainText("Apply the settings");
  await expect(banner(page)).toContainText(/step \d of \d/);
  await expect(banner(page)).toHaveAttribute("href", `/jobs/${id}`);
  await shot(page, info, "jobs-banner", false);

  // The Jobs page: the job that runs is first.
  await page.goto("/jobs");
  const active = page.getByTestId("jobs-active").getByTestId("job-row");
  await expect(active).toHaveCount(1);
  await expect(active).toHaveAttribute("data-job", id);
  await expect(active).toContainText("Apply the settings");
  await expect(active).toContainText("From the command line");
  if (isMobile(page)) await noSideScroll(page);
  await shot(page, info, "jobs-list");
  await active.click();
  await expect(page).toHaveURL(new RegExp(`/jobs/${id.replace(/\./g, "\\.")}$`));
  await expect(page.getByTestId("job-title")).toHaveText("Apply the settings");
  const steps = page.getByTestId("job-step");
  await expect(steps.nth(1)).toBeVisible({ timeout: 20_000 });

  // A reload reads the job (steps and the log tail), then follows the stream after its last event: not from 0.
  const streams: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes(`/jobs/${id}/events`)) streams.push(r.url());
  });
  await page.reload();
  await expect(steps.first()).toHaveAttribute("data-state", "succeeded");
  await expect.poll(() => streams.length).toBeGreaterThan(0);
  expect(new URL(streams[0] ?? "", "http://x").searchParams.get("after")).not.toBe("0");
  await page.getByText("The whole log").click();
  await expect(page.getByTestId("job-log")).toContainText("write-files");

  // Copy the transcript: the plain text of the agent, with its support header.
  if (browserName === "chromium") {
    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.getByRole("button", { name: "Copy the transcript" }).click();
    await expect(page.getByRole("button", { name: "Copied" }).first()).toBeVisible();
    const text = await page.evaluate(() => navigator.clipboard.readText());
    expect(text).toMatch(new RegExp(`^CARE support text: job ${id.replace(/\./g, "\\.")}\\nDevice: `));
    expect(text).toContain("Steps:");
  }
  await expect(page.getByTestId("job-progress")).toHaveAttribute("data-job-state", "succeeded", { timeout: 45_000 });
  await expect(banner(page)).toHaveCount(0, { timeout: 20_000 });
  if (isMobile(page)) await noSideScroll(page);
  await shot(page, info, "job-page");

  // The job is now in "Ended".
  await page.goto("/jobs");
  await expect(page.getByTestId("jobs-active")).toContainText("No job runs now.");
  await expect(page.getByTestId("jobs-recent").getByTestId("job-row").first()).toHaveAttribute("data-job", id);
});

test("a second device during the setup finish sees the progress, with and without a session", async ({
  page,
  browser,
  resetMock,
}, info) => {
  await resetMock("review", { step_ms: 1500 });
  await page.goto("/login?next=%2Fsetup%2Freview");
  await login(page);
  await page.getByRole("button", { name: "Install Care" }).click();
  await expect(page).toHaveURL(/\/setup\/progress$/);
  await expect(page.getByTestId("job-step").first()).toBeVisible();

  // A device without a session: the start page is the public progress of the setup job (steps only, no log).
  const other = await browser.newContext({
    baseURL: info.project.use.baseURL,
    viewport: info.project.use.viewport ?? null,
    isMobile: info.project.use.isMobile,
    hasTouch: info.project.use.hasTouch,
  });
  const phone = await other.newPage();
  await phone.goto("/");
  await expect(phone).toHaveURL(/\/jobs\/\d{8}T\d{6}\.\d{3}Z-setup$/);
  await expect(phone.getByRole("heading", { name: "Care is being set up" })).toBeVisible();
  await expect(phone.getByTestId("public-job").getByTestId("job-step").first()).toBeVisible({ timeout: 20_000 });
  await expect(phone.getByRole("button", { name: "Copy the transcript" })).toHaveCount(0);
  await expect(phone.getByText("The whole log")).toHaveCount(0);
  await shot(phone, info, "jobs-public");

  // The login link comes back to the job; with the session the device follows the setup progress, not a stale step.
  await phone.getByTestId("public-job-login").click();
  await expect(phone).toHaveURL(/\/login\?next=/);
  await phone.getByLabel("Console password").fill(READY_PASSWORD);
  await phone.getByRole("button", { name: "Log in" }).click();
  await expect(phone.getByTestId("job-page")).toBeVisible();
  await phone.goto("/setup/identity");
  await expect(phone).toHaveURL(/\/setup\/progress$/);
  await expect(phone.getByTestId("step-title")).toHaveText("Installing Care");
  await other.close();
});
