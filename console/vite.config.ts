/// <reference types="vitest/config" />
import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin, type ProxyOptions } from "vite";

// The API that the dev server and the preview server send /api and /_care-pi to.
// Default: the stateful mock (npm run mock). For the Go dev server, set
// CARE_PI_API=http://127.0.0.1:8443.
const apiTarget = process.env.CARE_PI_API ?? "http://127.0.0.1:4011";

// The agent accepts an unsafe request only when Origin is the origin of the
// console. The proxy sets Host and Origin to the target, so the dev origin
// (http://localhost:5173) passes the check.
const proxy: ProxyOptions = {
  target: apiTarget,
  changeOrigin: true,
  configure(server) {
    server.on("proxyReq", (req) => {
      if (req.getHeader("origin")) req.setHeader("origin", new URL(apiTarget).origin);
    });
  },
};

// /__v2: the gallery of the v2 design system (console/design.md). The dev server only (apply: "serve"), outside
// the router of the app: the build does not include it.
function v2Gallery(): Plugin {
  return {
    name: "care-pi-v2-gallery",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const match = req.url?.match(/^\/__v2\/?(\?.*)?$/);
        if (match) req.url = `/src/components/v2/__examples__/index.html${match[1] ?? ""}`;
        next();
      });
    },
  };
}

export default defineConfig({
  base: "/",
  plugins: [react(), tailwindcss(), v2Gallery()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
      clsx: "cn",
      "tailwind-merge": "cn",
    },
  },
  server: {
    proxy: { "/api": proxy, "/_care-pi": proxy },
  },
  build: {
    outDir: "dist",
    // dist/.gitkeep belongs to the Go embed. scripts/clean-dist.mjs removes the old build.
    emptyOutDir: false,
    assetsDir: "assets",
    sourcemap: false,
    reportCompressedSize: true,
    rolldownOptions: {
      output: {
        // Libraries go to their own chunks, so a console change does not change their hashes.
        // Each page is a lazy chunk (src/app/router.tsx).
        codeSplitting: {
          groups: [
            { name: "react", test: /node_modules[\\/](react|react-dom|scheduler)[\\/]/, priority: 40 },
            { name: "router", test: /node_modules[\\/]react-router[\\/]/, priority: 30 },
            { name: "query", test: /node_modules[\\/](@tanstack|openapi-fetch)[\\/]/, priority: 30 },
            { name: "base-ui", test: /node_modules[\\/]@base-ui[\\/]/, priority: 20 },
            { name: "vendor", test: /node_modules[\\/]/, priority: 10, minSize: 20_000 },
          ],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["tests/setup.ts"],
    include: ["tests/**/*.test.{ts,tsx}", "src/**/__tests__/*.test.{ts,tsx}"],
    css: false,
  },
});
