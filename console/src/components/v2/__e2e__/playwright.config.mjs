// The checks of the v2 design system on the /__v2 gallery (console/design.md, "Verification"): axe, layout
// stability (boxes and CLS), reduced motion, font loading, and the contrast of the tokens.
// Run: npm run test:v2 (the Vite dev server: /__v2 exists only there). V2_PORT changes the port.
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.V2_PORT ?? 4799);

export default defineConfig({
  testDir: ".",
  testMatch: "*.spec.ts",
  // Its own folder in console/test-results (git ignores it): a run does not delete the files of the e2e tests.
  outputDir: "../../../../test-results/v2",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [["list"]],
  use: { baseURL: `http://localhost:${PORT}`, trace: "retain-on-failure" },
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } } },
    {
      name: "phone",
      use: { ...devices["Desktop Chrome"], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true },
    },
  ],
  webServer: {
    command: `npx vite --port ${PORT} --strictPort`,
    cwd: new URL("../../../..", import.meta.url).pathname,
    url: `http://localhost:${PORT}/__v2`,
    reuseExistingServer: false,
  },
});
