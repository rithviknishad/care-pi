import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { MOCK_URL, boxes, expect, login, test } from "./fixtures";

// The console v2 checks of each page (console/design.md section 14): axe (WCAG 2.1 A and AA: no serious or
// critical violation) in the light and the dark theme, no side scroll at 360 and 390 px, no animation with "reduce
// motion", and the stability of the Jobs page. The dashboard, the Backups page, and the Updates page have their stability tests in their own specs.

type Scenario = "unclaimed" | "setup" | "review" | "ready";

interface PageCase {
  name: string;
  scenario: Scenario;
  session: boolean;
  path: string;
  // A locator that shows when the page has its data.
  ready: (page: Page) => Promise<void>;
  before?: (page: Page) => Promise<void>;
  options?: Record<string, unknown>;
}

const testId = (id: string) => async (page: Page) => {
  await page.getByTestId(id).first().waitFor();
};

const startJob = async (page: Page) => {
  const res = await page.request.post(`${MOCK_URL}/__mock/start-job`, { data: { slow: 40 } });
  expect(res.ok()).toBeTruthy();
  return ((await res.json()) as { id: string }).id;
};

const PAGES: PageCase[] = [
  { name: "claim", scenario: "unclaimed", session: false, path: "/claim", ready: testId("password-strength") },
  {
    name: "login",
    scenario: "ready",
    session: false,
    path: "/login",
    ready: async (p) => p.getByLabel("Console password").waitFor(),
  },
  {
    name: "recover",
    scenario: "ready",
    session: false,
    path: "/recover",
    ready: async (p) => p.getByLabel("Recovery code").waitFor(),
  },
  { name: "certificate (public)", scenario: "ready", session: false, path: "/certificate", ready: testId("ca-steps") },
  {
    name: "setup: recovery codes",
    scenario: "review",
    session: true,
    path: "/setup/recovery-codes",
    ready: testId("step-title"),
  },
  {
    name: "setup: hardware",
    scenario: "review",
    session: true,
    path: "/setup/hardware",
    ready: testId("hardware-summary"),
  },
  {
    name: "setup: identity",
    scenario: "review",
    session: true,
    path: "/setup/identity",
    ready: testId("advertised-name"),
  },
  { name: "setup: tls", scenario: "review", session: true, path: "/setup/tls", ready: testId("tls-offline") },
  { name: "setup: size", scenario: "review", session: true, path: "/setup/size", ready: testId("size-presets") },
  { name: "setup: backups", scenario: "review", session: true, path: "/setup/backups", ready: testId("key-ceremony") },
  {
    name: "setup: care admin",
    scenario: "review",
    session: true,
    path: "/setup/care-admin",
    ready: testId("step-submit"),
  },
  { name: "setup: review", scenario: "review", session: true, path: "/setup/review", ready: testId("review") },
  {
    name: "setup: install",
    scenario: "review",
    session: true,
    path: "/setup/review",
    before: async (p) => {
      await p.getByRole("button", { name: "Install Care" }).click();
      await p.waitForURL(/\/setup\/progress$/);
    },
    ready: testId("install-phase"),
  },
  {
    name: "setup: hand-off",
    scenario: "review",
    session: true,
    path: "/setup/review",
    before: async (p) => {
      await p.getByRole("button", { name: "Install Care" }).click();
      await p.waitForURL(/\/setup\/done$/, { timeout: 30_000 });
    },
    ready: testId("care-url"),
  },
  { name: "dashboard", scenario: "ready", session: true, path: "/dashboard", ready: testId("status-state") },
  { name: "backups", scenario: "ready", session: true, path: "/backups", ready: testId("destination-card") },
  { name: "restore", scenario: "ready", session: true, path: "/restore?kind=pitr", ready: testId("pitr-panel") },
  { name: "updates", scenario: "ready", session: true, path: "/updates", ready: testId("history-row") },
  { name: "plugs", scenario: "ready", session: true, path: "/plugs", ready: testId("solution-name") },
  { name: "settings", scenario: "ready", session: true, path: "/settings", ready: testId("advertised-name") },
  {
    name: "settings: backups",
    scenario: "ready",
    session: true,
    path: "/settings?tab=backups",
    ready: testId("settings-dest-schedule"),
  },
  {
    name: "settings: connectivity",
    scenario: "ready",
    session: true,
    path: "/settings?tab=connectivity",
    ready: testId("public-card"),
  },
  { name: "security", scenario: "ready", session: true, path: "/security", ready: testId("firewall-card") },
  { name: "logs", scenario: "ready", session: true, path: "/logs", ready: testId("log-line") },
  { name: "support", scenario: "ready", session: true, path: "/support", ready: testId("doctor-checks") },
  { name: "certificate", scenario: "ready", session: true, path: "/certificate", ready: testId("ca-steps") },
  {
    name: "jobs",
    scenario: "ready",
    session: true,
    path: "/jobs",
    before: async (p) => void (await startJob(p)),
    ready: testId("job-row"),
  },
];

async function openCase(page: Page, c: PageCase) {
  const res = await page.request.post(`${MOCK_URL}/__mock/reset`, {
    data: { scenario: c.scenario, step_ms: 150, ...c.options },
  });
  expect(res.ok()).toBeTruthy();
  if (c.session) {
    await page.goto(`/login?next=${encodeURIComponent(c.path)}`);
    await login(page);
    await page.waitForURL((u) => u.pathname === c.path.split("?")[0]);
  }
  await c.before?.(page);
  if (!c.session) await page.goto(c.path);
  await c.ready(page);
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(500);
}

async function axe(page: Page): Promise<string[]> {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  return result.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
}

for (const c of PAGES) {
  for (const theme of ["light", "dark"] as const) {
    test(`axe: ${c.name} (${theme})`, async ({ page }, info) => {
      // WebKit: the same DOM as Chromium; the phone width runs in the narrow project.
      test.skip(info.project.name === "mobile", "axe runs in Chromium (desktop and 360 px).");
      test.skip(info.project.name === "narrow" && theme === "dark", "The dark theme runs on the desktop.");
      await page.emulateMedia({ colorScheme: theme });
      await openCase(page, c);
      const bad = await axe(page);
      console.log(`axe ${c.name} ${theme} (${info.project.name}): ${bad.length} serious or critical`);
      expect(bad).toEqual([]);
    });
  }
}

// Each page at 360 px (narrow) and 390 px (mobile, WebKit): no side scroll (docs/console-ui.md section 5.1).
for (const c of PAGES) {
  test(`no side scroll: ${c.name}`, async ({ page }, info) => {
    test.skip(info.project.name === "desktop", "The phone widths only.");
    await openCase(page, c);
    const { scroll, width } = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      width: document.documentElement.clientWidth,
    }));
    expect(scroll, `${c.name}: scrollWidth ${scroll} > ${width}`).toBeLessThanOrEqual(width);
  });
}

// The public progress of a device without a session (S67), and the page of a job with a session.
test("axe: the page of a job, with and without a session", async ({ page, browser }, info) => {
  test.skip(info.project.name === "mobile", "axe runs in Chromium (desktop and 360 px).");
  await page.request.post(`${MOCK_URL}/__mock/reset`, { data: { scenario: "ready", step_ms: 150 } });
  await page.goto("/login?next=%2Fjobs");
  await login(page);
  const id = await startJob(page);
  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await page.goto(`/jobs/${id}`);
    await page.getByTestId("job-step").first().waitFor();
    await page.waitForTimeout(400);
    expect(await axe(page), `job page (${theme})`).toEqual([]);
  }
  const other = await browser.newContext({ baseURL: info.project.use.baseURL, viewport: info.project.use.viewport });
  const anon = await other.newPage();
  for (const theme of ["light", "dark"] as const) {
    await anon.emulateMedia({ colorScheme: theme });
    await anon.goto(`/jobs/${id}`);
    await anon.getByTestId("public-job").getByTestId("job-step").first().waitFor();
    await anon.waitForTimeout(400);
    expect(await axe(anon), `public job page (${theme})`).toEqual([]);
  }
  await other.close();
});

// "Reduce motion": no animation runs on a page with a running job (the spinner, the bar, the live dot).
test("reduced motion: a running job page has no animation", async ({ page }) => {
  await page.request.post(`${MOCK_URL}/__mock/reset`, { data: { scenario: "ready", step_ms: 150 } });
  await page.goto("/login?next=%2Fjobs");
  await login(page);
  const id = await startJob(page);
  const running = () => page.evaluate(() => document.getAnimations().filter((a) => a.playState === "running").length);
  await page.goto(`/jobs/${id}`);
  await page.locator('[data-testid="job-step"][data-state="running"]').first().waitFor();
  const normal = await running();
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.reload();
  await page.locator('[data-testid="job-step"][data-state="running"]').first().waitFor();
  await page.waitForTimeout(300);
  const reduced = await running();
  console.log(`job page animations: ${normal} normally, ${reduced} with reduce motion`);
  expect(normal).toBeGreaterThan(0);
  expect(reduced).toBe(0);
});

// The Jobs page keeps each box when the lists arrive (final-size skeletons; docs/console-ui.md section 5).
test("the Jobs page does not move when the data arrives", async ({ page }) => {
  await page.request.post(`${MOCK_URL}/__mock/reset`, { data: { scenario: "ready", step_ms: 150 } });
  await page.goto("/login?next=%2Fjobs");
  await login(page);
  // One job that ended and one that runs: each list has 1 row, the size of its skeleton.
  await page.request.post(`${MOCK_URL}/__mock/start-job`, { data: { slow: 1 } });
  await expect
    .poll(async () => ((await (await page.request.get(`${MOCK_URL}/_care-pi/status`)).json()) as { job: unknown }).job)
    .toBeNull();
  await startJob(page);
  await page.reload();
  await expect(page.getByTestId("job-row")).toHaveCount(2);
  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route(/\/api\/v1\/jobs\?/, async (route) => {
    await held;
    await route.continue();
  });
  await page.reload();
  await page.evaluate(() => document.fonts.ready);
  const panels = page.locator("main section");
  const before = await boxes(panels);
  release();
  await expect(page.getByTestId("job-row")).toHaveCount(2);
  await page.evaluate(() => document.fonts.ready);
  expect(await boxes(panels)).toEqual(before);
  expect(before.length).toBeGreaterThanOrEqual(3);
});
