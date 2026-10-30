import { expect, login, test } from "./fixtures";

// Journey 2: a deep link without a session -> login -> back to the deep link -> dashboard.
test("log in and read the dashboard", async ({ page, resetMock, requests }) => {
  await resetMock("ready");
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/login\?next=%2Fdashboard$/);
  await login(page, "not the password");
  await expect(page.getByText("The password is not correct.")).toBeVisible();
  await login(page);
  await expect(page).toHaveURL(/\/dashboard$/);

  await expect(page.getByTestId("status-state")).toHaveText("OK");
  await expect(page.getByTestId("units").locator("tbody tr")).toHaveCount(10);
  await expect(page.getByTestId("fe-build-id")).toHaveText("8d2f0c1a9b7e");
  await expect(page.getByTestId("care-digest")).toHaveText("sha256:5e0c3b2a1f9d");
  await expect(page.getByTestId("fingerprint")).toContainText("3f1d6c0e5a2b9e4d");
  await expect(page.getByTestId("status-problems")).toContainText("No problems.");
  await expect(page.getByTestId("device-name")).toHaveText("care.local");
  expect(requests.some((u) => u.endsWith("/api/v1/status"))).toBeTruthy();
});

// The dashboard must not move when the data arrives and when it refreshes (each 10 s).
test("the dashboard has no layout shift", async ({ page, resetMock, browserName }) => {
  test.skip(browserName === "webkit", "WebKit has no layout-shift entries. The next test checks the card boxes.");
  await resetMock("ready");
  await page.goto("/login?next=%2Fdashboard");
  await login(page);
  await expect(page.getByTestId("status-state")).toHaveText("OK");

  // Measure from a fresh load of the dashboard: skeletons first, then the data, then 2 refreshes.
  await page.addInitScript(() => {
    (window as unknown as { __cls: number }).__cls = 0;
    new PerformanceObserver((list) => {
      for (const e of list.getEntries() as unknown as { value: number; hadRecentInput: boolean }[]) {
        if (!e.hadRecentInput) (window as unknown as { __cls: number }).__cls += e.value;
      }
    }).observe({ type: "layout-shift", buffered: true });
  });
  await page.reload();
  await expect(page.getByTestId("status-state")).toHaveText("OK");
  const first = await page.getByTestId("status-updated").textContent();
  await expect(page.getByTestId("status-updated")).not.toHaveText(first ?? "", { timeout: 15_000 });
  const cls = await page.evaluate(() => (window as unknown as { __cls: number }).__cls);
  console.log(`dashboard CLS after load and 1 refresh (${page.viewportSize()?.width} px): ${cls.toFixed(4)}`);
  expect(cls).toBeLessThan(0.01);
});

// Each card of the dashboard has its final size before the data arrives (final-size skeletons).
test("the dashboard cards keep their size and position when the data arrives", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login?next=%2Fdashboard");
  await login(page);
  await expect(page.getByTestId("status-state")).toHaveText("OK");

  // Hold the next GET /status, so the page shows its skeletons.
  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/v1/status", async (route) => {
    await held;
    await route.continue();
  });
  await page.reload();
  await expect(page.getByTestId("units")).toBeVisible();
  await expect(page.getByTestId("status-state")).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready);

  const cards = page.locator("main [data-slot=card]");
  const boxes = () =>
    cards.evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        return [r.x, r.y, r.width, r.height].map((v) => Math.round(v));
      }),
    );
  const before = await boxes();
  expect(before.length).toBeGreaterThan(5);

  release();
  await expect(page.getByTestId("status-state")).toHaveText("OK");
  await expect(page.getByTestId("fe-build-id")).toHaveText("8d2f0c1a9b7e");
  const after = await boxes();
  console.log(
    `dashboard cards (${page.viewportSize()?.width} px): ${before.length} boxes, equal: ${JSON.stringify(before) === JSON.stringify(after)}`,
  );
  expect(after).toEqual(before);
});

test("theme choice persists and the session ends with logout", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login");
  await login(page);
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.getByRole("button", { name: /^Theme/ }).click();
  await page.getByRole("menuitemradio", { name: "Dark" }).click();
  await expect(page.locator("html")).toHaveClass(/dark/);
  await page.reload();
  await expect(page.locator("html")).toHaveClass(/dark/);
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Log out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.goto("/settings");
  await expect(page).toHaveURL(/\/login\?next=%2Fsettings$/);
});

// 5 failed logins lock the address. The page shows the wait time of Retry-After and blocks the button.
test("a lockout shows the wait time", async ({ page, resetMock }) => {
  await resetMock("ready");
  await page.goto("/login");
  for (let i = 0; i < 5; i++) {
    await login(page, `wrong password ${i}`);
    await expect(page.getByText("The password is not correct.")).toBeVisible();
  }
  await login(page, "wrong password 5");
  const alert = page.locator('[data-problem="locked-out"]');
  await expect(alert).toContainText("Too many failed attempts.");
  await expect(alert).toContainText(/Try again in 1[45] min/);
  await expect(page.getByRole("button", { name: "Log in" })).toBeDisabled();
});

// public/theme.js sets the theme class before the app starts: a dark choice gives no light flash.
test("the theme applies before the app starts", async ({ page, resetMock }) => {
  await resetMock("unclaimed");
  await page.goto("/certificate");
  await page.evaluate(() => localStorage.setItem("care-pi.theme", "dark"));
  await page.route(/\/assets\/.*\.js$/, (route) => route.abort());
  await page.goto("/certificate");
  await expect(page.locator("html")).toHaveClass(/\bdark\b/);
  await expect(page.locator("#root")).toBeEmpty();
});
