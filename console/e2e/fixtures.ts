import { expect, test as base, type Page, type TestInfo } from "@playwright/test";

export const MOCK_URL = "http://127.0.0.1:4711";
export const READY_PASSWORD = "correct horse battery staple";
export const CLAIM_CODE = "4F7K-Q2MX-9PDA";

interface Fixtures {
  // Each request of the page. Each test checks that no request leaves the origin.
  requests: string[];
  resetMock: (scenario: "unclaimed" | "setup" | "review" | "ready", options?: Record<string, unknown>) => Promise<void>;
}

// Playwright gives each fixture a function that runs the test. These fixtures call it "runTest".
export const test = base.extend<Fixtures>({
  requests: [
    async ({ page, baseURL }, runTest) => {
      const urls: string[] = [];
      page.on("request", (r) => urls.push(r.url()));
      await runTest(urls);
      const origin = new URL(baseURL ?? "http://localhost").origin;
      const foreign = urls.filter((u) => !u.startsWith(origin) && !u.startsWith("data:") && !u.startsWith("blob:"));
      expect(foreign, "requests outside the origin").toEqual([]);
      const paths = urls.filter((u) => u.startsWith(origin)).map((u) => new URL(u).pathname);
      const unexpected = paths.filter(
        (p) =>
          !p.startsWith("/api/v1/") &&
          !p.startsWith("/_care-pi/") &&
          !p.startsWith("/assets/") &&
          p !== "/favicon.svg" &&
          p !== "/theme.js" &&
          !/^\/[a-z/-]*$/.test(p),
      );
      expect(unexpected, "same-origin requests other than the API, assets, and page routes").toEqual([]);
    },
    { auto: true },
  ],
  resetMock: async ({ request }, runTest) => {
    await runTest(async (scenario, options = {}) => {
      const res = await request.post(`${MOCK_URL}/__mock/reset`, { data: { scenario, step_ms: 150, ...options } });
      expect(res.ok()).toBeTruthy();
    });
  },
});

export { expect };

export async function login(page: Page, password = READY_PASSWORD) {
  await page.getByLabel("Console password").fill(password);
  await page.getByRole("button", { name: "Log in" }).click();
}

export function isMobile(page: Page): boolean {
  return (page.viewportSize()?.width ?? 1280) < 640;
}

// shot saves a screenshot to SHOTS_DIR as <project>-<name>.png when SHOTS_DIR is set. fullPage false keeps
// the window only: a page with a fixed or sticky bar shows the bar where the operator sees it.
export async function shot(page: Page, info: TestInfo, name: string, fullPage = true) {
  const dir = process.env.SHOTS_DIR;
  if (!dir) return;
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `${dir}/${info.project.name}-${name}.png`, fullPage, animations: "disabled" });
}

// noSideScroll checks that the page and each given element do not scroll sideways.
export async function noSideScroll(page: Page, testIds: string[] = []) {
  const doc = await page.evaluate(() => {
    const el = document.scrollingElement ?? document.documentElement;
    return { scroll: el.scrollWidth, client: el.clientWidth };
  });
  expect(doc.scroll, `page ${doc.scroll} > ${doc.client} px`).toBeLessThanOrEqual(doc.client);
  for (const id of testIds) {
    const box = await page.getByTestId(id).evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
    expect(box.scroll, `${id} ${box.scroll} > ${box.client} px`).toBeLessThanOrEqual(box.client);
  }
}
