import { defineConfig, devices } from "@playwright/test";

// The e2e tests run against the production build (vite preview) and the stateful mock.
// Run `npm run build` first (npm run test:e2e does it).
const MOCK_PORT = 4711;
const PREVIEW_PORT = 4773;

export default defineConfig({
  testDir: "e2e",
  // One mock serves the tests, and each test resets it: run the tests one after the other.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [["list"]],
  use: {
    baseURL: `http://localhost:${PREVIEW_PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  // Install the browsers one time: npx playwright install chromium webkit
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } } },
    // An iPhone (390 x 664, touch) in WebKit, the engine of each iPhone browser.
    { name: "mobile", use: { ...devices["iPhone 13"] } },
    // The smallest width that the console supports (360 px), in Chromium.
    { name: "narrow", use: { ...devices["Galaxy S8"] } },
  ],
  webServer: [
    {
      command: "node mock/server.mjs",
      url: `http://127.0.0.1:${MOCK_PORT}/_care-pi/health`,
      env: { MOCK_PORT: String(MOCK_PORT), MOCK_STEP_MS: "150", MOCK_LOG_MS: "200" },
      reuseExistingServer: false,
    },
    {
      command: `npx vite preview --port ${PREVIEW_PORT} --strictPort`,
      url: `http://localhost:${PREVIEW_PORT}`,
      env: { CARE_PI_API: `http://127.0.0.1:${MOCK_PORT}` },
      reuseExistingServer: false,
    },
  ],
});
