import { expect, test, type Page, type TestInfo } from "@playwright/test";

// One screenshot of each page of the console (desktop light and dark, phone light) into SHOTS_DIR, for the review
// of the design (console/design.md). SHOTS_ONLY=<name,name> takes some pages only.
const MOCK_URL = process.env.E2E_MOCK_URL ?? "http://127.0.0.1:4721";
const DIR = process.env.SHOTS_DIR ?? "test-results/shots";
const ONLY = (process.env.SHOTS_ONLY ?? "").split(",").filter(Boolean);
const PASSWORD = "correct horse battery staple";

type Scenario = "unclaimed" | "setup" | "review" | "ready" | "restore";

async function reset(page: Page, scenario: Scenario, options: Record<string, unknown> = {}) {
  const res = await page.request.post(`${MOCK_URL}/__mock/reset`, { data: { scenario, step_ms: 400, ...options } });
  expect(res.ok()).toBeTruthy();
}

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Console password").fill(PASSWORD);
  await page.getByRole("button", { name: "Log in" }).click();
  await page.waitForURL((u) => !u.pathname.startsWith("/login"));
}

async function settle(page: Page) {
  await page.evaluate(() => document.fonts.ready);
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(600);
}

async function snap(page: Page, info: TestInfo, name: string) {
  await settle(page);
  // A full-page shot from the top: the sticky top bar stays at the top of the image.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: `${DIR}/${info.project.name}-${name}.png`, fullPage: true, animations: "disabled" });
}

interface Shot {
  name: string;
  scenario: Scenario;
  options?: Record<string, unknown>;
  session?: boolean;
  path: string;
  before?: (page: Page) => Promise<void>;
  after?: (page: Page) => Promise<void>;
}

const startJob = (opts: Record<string, unknown>) => async (page: Page) => {
  await page.request.post(`${MOCK_URL}/__mock/start-job`, { data: opts });
};

const SHOTS: Shot[] = [
  { name: "01-claim", scenario: "unclaimed", path: "/claim" },
  {
    name: "02-claim-code",
    scenario: "unclaimed",
    path: "/claim",
    after: async (p) => {
      await p.getByRole("button", { name: "Use the claim code" }).click();
      await p.getByLabel("New console password").fill("a long console password");
    },
  },
  { name: "03-login", scenario: "ready", path: "/login" },
  { name: "04-recover", scenario: "ready", path: "/recover" },
  { name: "05-certificate-public", scenario: "ready", path: "/certificate" },
  { name: "10-setup-recovery-codes", scenario: "review", session: true, path: "/setup/recovery-codes" },
  { name: "11-setup-hardware", scenario: "review", session: true, path: "/setup/hardware" },
  { name: "12-setup-identity", scenario: "review", session: true, path: "/setup/identity" },
  { name: "13-setup-tls", scenario: "review", session: true, path: "/setup/tls" },
  { name: "14-setup-size", scenario: "review", session: true, path: "/setup/size" },
  {
    name: "15-setup-size-advanced",
    scenario: "review",
    session: true,
    path: "/setup/size",
    after: async (p) => {
      await p.getByRole("button", { name: "Advanced", exact: true }).click();
    },
  },
  { name: "16-setup-backups", scenario: "review", session: true, path: "/setup/backups" },
  {
    // The key ceremony, then the form of a destination with the schedule pickers and the explainer (S66).
    name: "17-setup-backups-schedule",
    scenario: "setup",
    session: true,
    path: "/setup/backups",
    after: async (p) => {
      await p.getByTestId("key-make").click();
      const words = p.getByTestId("key-words").locator("li span:last-child");
      await expect(words).toHaveCount(24);
      const list = (await words.allTextContents()).map((w) => w.trim());
      await p.getByText("I saved the recovery key").click();
      await p.getByTestId("key-saved-continue").click();
      const inputs = p.getByTestId("key-confirm").locator("input[data-position]");
      for (let i = 0; i < 3; i++) {
        const n = Number(await inputs.nth(i).getAttribute("data-position"));
        await inputs.nth(i).fill(list[n - 1] ?? "");
      }
      await p.getByTestId("key-confirm-submit").click();
      await expect(p.getByTestId("key-ceremony")).toHaveAttribute("data-phase", "confirmed");
      await p.getByTestId("dest-choice-sftp").click();
      await p.getByTestId("dest-form").waitFor();
    },
  },
  { name: "18-setup-care-admin", scenario: "review", session: true, path: "/setup/care-admin" },
  { name: "19-setup-review", scenario: "review", session: true, path: "/setup/review" },
  {
    name: "20-setup-install",
    scenario: "review",
    session: true,
    path: "/setup/review",
    after: async (p) => {
      await p.getByRole("button", { name: "Install Care" }).click();
      await p.waitForURL(/\/setup\/progress$/);
      await p.getByTestId("job-step").nth(4).waitFor();
    },
  },
  {
    name: "21-setup-done",
    scenario: "review",
    session: true,
    path: "/setup/review",
    after: async (p) => {
      await p.getByRole("button", { name: "Install Care" }).click();
      await p.waitForURL(/\/setup\/done$/, { timeout: 60_000 });
    },
  },
  { name: "30-dashboard", scenario: "ready", session: true, path: "/dashboard" },
  { name: "31-backups", scenario: "ready", session: true, path: "/backups" },
  { name: "32-backups-degraded", scenario: "ready", options: { backups: "degraded" }, session: true, path: "/backups" },
  { name: "33-restore", scenario: "ready", session: true, path: "/restore" },
  { name: "33b-restore-pitr", scenario: "ready", session: true, path: "/restore?kind=pitr" },
  { name: "34-updates", scenario: "ready", session: true, path: "/updates" },
  {
    name: "35-updates-available",
    scenario: "ready",
    options: { updates: "available" },
    session: true,
    path: "/updates",
  },
  {
    name: "35b-updates-recovered",
    scenario: "ready",
    options: { updates: "recovered" },
    session: true,
    path: "/updates",
  },
  { name: "36-plugs", scenario: "ready", session: true, path: "/plugs" },
  { name: "37-settings", scenario: "ready", session: true, path: "/settings" },
  { name: "38-settings-backups", scenario: "ready", session: true, path: "/settings?tab=backups" },
  { name: "39-settings-connectivity", scenario: "ready", session: true, path: "/settings?tab=connectivity" },
  { name: "40-security", scenario: "ready", session: true, path: "/security" },
  { name: "41-logs", scenario: "ready", session: true, path: "/logs" },
  { name: "42-support", scenario: "ready", session: true, path: "/support" },
  { name: "43-certificate", scenario: "ready", session: true, path: "/certificate" },
  {
    name: "44-jobs",
    scenario: "ready",
    session: true,
    path: "/jobs",
    before: startJob({ slow: 40 }),
  },
  {
    name: "45-job",
    scenario: "ready",
    session: true,
    path: "/jobs",
    before: startJob({ slow: 40 }),
    after: async (p) => {
      await p.getByTestId("job-row").first().click();
      await p.getByTestId("job-step").first().waitFor();
    },
  },
  {
    name: "46-job-public",
    scenario: "ready",
    path: "/jobs",
    before: startJob({ slow: 40 }),
    after: async (p) => {
      const res = await p.request.get(`${MOCK_URL}/_care-pi/status`);
      const id = ((await res.json()) as { job: { id: string } }).job.id;
      await p.goto(`/jobs/${id}`);
      await p.getByTestId("job-step").first().waitFor();
    },
  },
  {
    name: "47-banner",
    scenario: "ready",
    session: true,
    path: "/dashboard",
    before: startJob({ slow: 40 }),
  },
];

for (const s of SHOTS) {
  test(s.name, async ({ page }, info) => {
    test.skip(ONLY.length > 0 && !ONLY.some((o) => s.name.includes(o)), "not selected");
    await reset(page, s.scenario, s.options);
    if (s.session) await login(page);
    await s.before?.(page);
    await page.goto(s.path);
    await s.after?.(page);
    await snap(page, info, s.name);
  });
}
