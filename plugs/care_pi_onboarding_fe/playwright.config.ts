import { defineConfig, devices } from "@playwright/test";

const PORT = 10143;
export const PREVIEW_PORT = 10144;

// The wizard runs in the dev harness (index.html, src/main.tsx); tests/e2e/mock-backend.ts answers the API.
// remote.spec.ts loads the built remoteEntry.js from `vite preview`, so run `npm run build` first.
export default defineConfig({
  testDir: "tests/e2e",
  timeout: 60_000,
  fullyParallel: false,
  reporter: [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      command: `npx vite --port ${PORT} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${PORT}`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      // The built bundle (npm run build first), as a host fetches it: cross-origin.
      command: `npx vite preview --port ${PREVIEW_PORT} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${PREVIEW_PORT}/assets/remoteEntry.js`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
});
