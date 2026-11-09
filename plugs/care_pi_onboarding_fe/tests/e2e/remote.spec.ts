import { expect, test } from "@playwright/test";

import { PREVIEW_PORT } from "../../playwright.config";

const ENTRY = `http://127.0.0.1:${PREVIEW_PORT}/assets/remoteEntry.js`;

// care_fe loads ./manifest from remoteEntry.js with a cross-origin import (src/PluginEngine.tsx).
// This test does the same from another origin (another port): the import needs Access-Control-Allow-Origin.
test("the built remote loads cross-origin and exposes the manifest that care_fe reads", async ({
  page,
  request,
}) => {
  const head = await request.get(ENTRY);
  expect(head.status()).toBe(200);

  // An empty page that the dev server serves (another port), so the import below is cross-origin.
  // A page from page.route would count as a public address, and Chrome then blocks loopback requests.
  await page.goto("/tests/e2e/host.html");
  const manifest = await page.evaluate(async (url) => {
    const remote = await import(url);
    await remote.init({});
    const factory = await remote.get("./manifest");
    const mod = await factory();
    const m = (mod.default ?? mod) as Record<string, any>;
    return {
      plugin: m.plugin,
      routes: Object.keys(m.routes ?? {}),
      overrides: (m.overrides ?? []).map((o: any) => o.component),
      replacement: typeof m.overrides?.[0]?.replacement,
      admin: (m.adminNavItems ?? []).map((i: any) => i.url),
    };
  }, ENTRY);

  expect(manifest).toEqual({
    plugin: "care_pi_onboarding_fe",
    routes: ["/onboarding", "/admin/onboarding"],
    overrides: ["UserDashboard"],
    replacement: "function",
    admin: ["/admin/onboarding"],
  });
});
