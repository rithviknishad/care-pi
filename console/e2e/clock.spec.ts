import { MOCK_URL, boxes, expect, noSideScroll, shot, test } from "./fixtures";

// S52 (docs/api/README.md section 16): a device without internet. The claim sends the time of the browser, and the
// agent sets the clock from it. The console shows "Clock not trusted" in the slot of the top bar until NTP
// synchronizes the clock.
test("the claim gives the clock of the browser; the banner shows until NTP", async ({
  page,
  resetMock,
  request,
}, info) => {
  await resetMock("unclaimed", { clock: "untrusted" });
  await page.goto("/");
  await expect(page).toHaveURL(/\/claim$/);
  // No session, no banner.
  await expect(page.getByTestId("clock-banner")).toHaveCount(0);

  await page.getByRole("button", { name: "Use the claim code" }).click();
  await page.getByLabel("Claim code").fill("4F7K-Q2MX-9PDA");
  await page.getByLabel("New console password").fill("a long console password");
  await page.getByLabel("Type the password again").fill("a long console password");
  const before = Date.now();
  await page.getByRole("button", { name: "Claim the device" }).click();
  await expect(page.getByTestId("recovery-codes").locator("li")).toHaveCount(8);

  const state = (await (await request.get(`${MOCK_URL}/__mock/state`)).json()) as {
    clock: { source: string; trusted: boolean; clientTime: string | null };
  };
  expect(state.clock.source).toBe("browser");
  expect(state.clock.trusted).toBe(false);
  expect(state.clock.clientTime).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/);
  expect(Math.abs(Date.parse(state.clock.clientTime ?? "") - before)).toBeLessThan(60_000);

  await page.getByText("I saved these codes in a safe place.").click();
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page).toHaveURL(/\/setup\/recovery-codes$/);

  // The banner of the width of the window: in the slot of the top bar (in place of the device name below 640 px).
  const banner = page.locator('[data-testid="clock-banner"]:visible');
  await expect(banner).toHaveCount(1);
  await expect(banner).toHaveText(/Clock not trusted/);
  await expect(banner).toHaveAttribute("data-source", "browser");
  await expect(page.getByTestId("step-title")).toBeVisible();
  await noSideScroll(page);
  await shot(page, info, "clock-01-untrusted", false);
  const mainBefore = await boxes(page.locator("#main"));

  // NTP synchronizes the clock: the banner goes at the next status read, and the page does not move.
  const sync = await request.post(`${MOCK_URL}/__mock/ntp-sync`);
  expect(sync.ok()).toBeTruthy();
  await expect(page.getByTestId("clock-banner")).toHaveCount(0, { timeout: 20_000 });
  expect(await boxes(page.locator("#main"))).toEqual(mainBefore);
  await shot(page, info, "clock-02-trusted", false);
});
