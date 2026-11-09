/**
 * Fails if any plug chunk reads a React hook off the React copy bundled into
 * the remote instead of the host's federation-shared one.
 *
 * Invisible to `vite build`, `tsc` and eslint — it only appears at runtime,
 * inside the host, as
 *   "Cannot read properties of null (reading 'useSyncExternalStore')"
 * on the first Base UI dialog/popover that mounts.
 *
 * Cause: CommonJS deps (`use-sync-external-store/shim`, pulled in by
 * @base-ui/utils/store/useStore, and zustand@4) do `require("react")`, which
 * @originjs/vite-plugin-federation does NOT rewrite to the shared instance.
 * Fix: alias them to ESM shims that `import * as React from "react"`. See
 * references/care-plug-caveats.md and care_excalidraw_fe/vite.config.ts.
 *
 * Usage: node scripts/check-single-react.mjs   (after `npm run build`)
 */
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const assets = fileURLToPath(new URL("../dist/assets", import.meta.url));

const leaks = [];
for (const file of readdirSync(assets).filter((f) => f.endsWith(".js"))) {
  // The federation shared-module proxies legitimately wrap the CJS React.
  if (file.startsWith("__federation_shared")) continue;

  const src = readFileSync(join(assets, file), "utf8");

  // Hooks reached through a CommonJS interop namespace (`reactExports.useX`,
  // `shimExports.useX`, ...). Anything routed through federation is either
  // destructured from `await importShared('react')` or called bare, so a
  // `<something>Exports.useHook(` call is always a bundled-React read.
  const hooks = new Set(
    [...src.matchAll(/\b(\w*Exports)\.(use[A-Z]\w*)\s*\(/g)].map(
      (m) => `${m[1]}.${m[2]}`,
    ),
  );

  // The CJS use-sync-external-store shim should never survive bundling: React
  // 19 has the hook natively, so its presence means an alias stopped matching.
  if (src.includes("useSyncExternalStoreShim_production")) {
    hooks.add("<bundled use-sync-external-store CJS shim>");
  }

  if (hooks.size) leaks.push({ file, hooks: [...hooks] });
}

if (leaks.length) {
  console.error("FAIL: plug chunks call React hooks on the BUNDLED React:");
  for (const { file, hooks } of leaks) {
    console.error(`  ${file}: ${hooks.join(", ")}`);
  }
  console.error("\nThese will throw inside the host. Alias the offending CJS");
  console.error("dep to an ESM shim in vite.config.mts (see src/shims/).");
  process.exit(1);
}

console.log("ok  no plug chunk reads React hooks off the bundled React");
