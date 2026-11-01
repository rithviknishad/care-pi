import path from "node:path";
import { cloudflareTest, readD1Migrations } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

// Tests that run in workerd (the Workers runtime) with a local D1 database.
export default defineConfig(async () => {
  const migrations = await readD1Migrations(path.join(import.meta.dirname, "migrations"));
  return {
    plugins: [
      cloudflareTest({
        wrangler: { configPath: "./wrangler.toml" },
        miniflare: {
          bindings: {
            TEST_MIGRATIONS: migrations,
            CF_DNS_API_TOKEN: "test-token",
          },
        },
      }),
    ],
    test: {
      include: ["test/*.test.ts"],
      setupFiles: ["./test/apply-migrations.ts"],
    },
  };
});
