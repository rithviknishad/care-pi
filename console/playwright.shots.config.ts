import { defineConfig, devices } from "@playwright/test";

// Screenshots of each page (console/design.md, "Screenshots"): desktop light and dark, phone light. Not a test of the
// gates: run it with SHOTS_DIR=<dir> npx playwright test -c playwright.shots.config.ts (after npm run build).
// The ports differ from playwright.config.ts, so the 2 can run at the same time.
const MOCK_PORT = Number(process.env.E2E_SHOTS_MOCK_PORT ?? 4721);
const PREVIEW_PORT = Number(process.env.E2E_SHOTS_PREVIEW_PORT ?? 4783);
process.env.E2E_MOCK_URL = `http://127.0.0.1:${MOCK_PORT}`;

export default defineConfig({
  testDir: "e2e-shots",
  // Not test-results/: a run of playwright.config.ts at the same time empties that directory.
  outputDir: "node_modules/.cache/shots-results",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  reporter: [["list"]],
  use: { baseURL: `http://localhost:${PREVIEW_PORT}` },
  projects: [
    {
      name: "desktop-light",
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 }, colorScheme: "light" },
    },
    {
      name: "desktop-dark",
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 }, colorScheme: "dark" },
    },
    {
      name: "phone-light",
      use: { ...devices["Pixel 7"], viewport: { width: 390, height: 844 }, colorScheme: "light" },
    },
  ],
  webServer: [
    {
      command: "node mock/server.mjs",
      url: `http://127.0.0.1:${MOCK_PORT}/_care-pi/health`,
      env: { MOCK_PORT: String(MOCK_PORT), MOCK_STEP_MS: "400", MOCK_LOG_MS: "400" },
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
