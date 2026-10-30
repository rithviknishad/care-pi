import { defineConfig, devices } from "@playwright/test";

// The journeys of e2e/real run against a real device, not against the mock. The default config ignores them.
// Read e2e/real/README.md for the variables. Example:
//   BASE_URL=https://care.local:8443 CLAIM_CODE=… CA_CERT=root.crt npx playwright test -c playwright.real.config.ts
const BASE_URL = process.env.BASE_URL ?? "https://care.local:8443";
const proxy = process.env.PROXY ? { server: process.env.PROXY } : undefined;
// Chromium has no option to trust one CA. It can accept the certificates with these SPKI hashes
// (base64 SHA-256, comma-separated). Give the hash of the intermediate after a check against CA_CERT
// (e2e/real/README.md). Without it, Chromium ignores the certificate errors. WebKit always ignores them.
// Each journey also checks the served chain against CA_CERT in Node (e2e/real/real.ts, checkServedChain).
const spki = process.env.CHROMIUM_SPKI;

export default defineConfig({
  testDir: "e2e/real",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  // The setup job runs for about 10 minutes.
  timeout: 20 * 60_000,
  expect: { timeout: 15_000 },
  reporter: [["list"]],
  outputDir: process.env.OUTPUT_DIR ?? "test-results/real",
  use: {
    baseURL: BASE_URL,
    proxy,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    acceptDownloads: true,
  },
  projects: [
    {
      name: "desktop",
      testMatch: /desktop\.[a-z0-9-]+\.spec\.ts$/,
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1280, height: 800 },
        ignoreHTTPSErrors: !spki,
        launchOptions: spki ? { args: [`--ignore-certificate-errors-spki-list=${spki}`] } : {},
      },
    },
    {
      name: "phone",
      testMatch: /phone\.[a-z0-9-]+\.spec\.ts$/,
      use: { ...devices["iPhone 13"], ignoreHTTPSErrors: true },
    },
  ],
});
