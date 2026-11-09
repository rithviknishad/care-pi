import { defineConfig } from "vite";
import federation from "@originjs/vite-plugin-federation";
import path from "path";
import react from "@vitejs/plugin-react-swc";
import tailwindcss from "@tailwindcss/vite";
import { readFileSync } from "fs";
import type { Plugin } from "vite";
import { scopeTailwindOutput } from "./scripts/postcss-scope-plugin";

// The strings are bundled (src/lib/i18n.ts). This copy at <base>/locale/en.json serves a host that
// fetches the locale file from the origin of the plug.
function emitLocale(): Plugin {
  return {
    name: "care-pi-onboarding-emit-locale",
    apply: "build",
    generateBundle() {
      this.emitFile({
        type: "asset",
        fileName: "locale/en.json",
        source: readFileSync(
          path.resolve(__dirname, "src/locale/en.json"),
          "utf8",
        ),
      });
    },
  };
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    federation({
      name: "care_pi_onboarding_fe",
      filename: "remoteEntry.js",
      exposes: {
        "./manifest": "./src/manifest.tsx",
      },
      // Each library that care_fe also owns, as imported by this plug (subpaths need their own entry).
      // care_fe mounts the only <Toaster/>; a second sonner instance would drop every toast.
      shared: ["react", "react-dom", "react-i18next", "raviger", "sonner"],
    }),
    tailwindcss(),
    scopeTailwindOutput(),
    emitLocale(),
    react(),
  ],
  build: {
    target: "esnext",
    minify: true,
    cssCodeSplit: false,
    modulePreload: {
      polyfill: false,
    },
    rollupOptions: {
      external: [],
      input: {
        main: "./src/index.tsx",
      },
      output: {
        format: "esm",
      },
    },
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      // @base-ui/react's stores import the CommonJS-only
      // `use-sync-external-store` shim. Under @originjs/vite-plugin-federation,
      // a CJS `require("react")` is NOT rewritten to the shared (host) React,
      // so it binds to the remote's *bundled* React whose dispatcher is null:
      //   "Cannot read properties of null (reading 'useSyncExternalStore')"
      // Aliasing to ESM files that import React as a bare specifier lets
      // federation route them to the single shared instance.
      // Same fix as care_excalidraw_fe/vite.config.ts.
      "use-sync-external-store/shim/with-selector.js": path.resolve(
        __dirname,
        "./src/shims/with-selector.ts",
      ),
      "use-sync-external-store/shim/with-selector": path.resolve(
        __dirname,
        "./src/shims/with-selector.ts",
      ),
      "use-sync-external-store/shim/index.js": path.resolve(
        __dirname,
        "./src/shims/shim.ts",
      ),
      "use-sync-external-store/shim": path.resolve(
        __dirname,
        "./src/shims/shim.ts",
      ),
    },
  },
  server: {
    port: 10142,
  },
  preview: {
    port: 10142,
    allowedHosts: true,
    host: "0.0.0.0",
    cors: true,
  },
});
