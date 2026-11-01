import { defineConfig } from "vitest/config";

// Tests that run in Node: the browser scripts of the static pages.
export default defineConfig({
  test: {
    include: ["test/node/*.test.ts"],
    environment: "node",
  },
});
