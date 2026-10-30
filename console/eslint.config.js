import js from "@eslint/js";
import prettier from "eslint-config-prettier";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import { defineConfig, globalIgnores } from "eslint/config";
import globals from "globals";
import tseslint from "typescript-eslint";

export default defineConfig([
  // src/components/ui is the Care UI registry source (scripts/careui-add.mjs): do not lint it.
  globalIgnores([
    "dist",
    "node_modules",
    "playwright-report",
    "test-results",
    "src/components/ui/**",
    "src/hooks/use-mobile.ts",
    "src/hooks/use-auto-space.ts",
    "src/api/types.gen.ts",
  ]),
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
    },
  },
  {
    files: ["**/*.{js,mjs}"],
    extends: [js.configs.recommended],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: "module",
      globals: globals.node,
    },
  },
  {
    // public/ has plain browser scripts that index.html loads without the bundler.
    files: ["public/**/*.js"],
    languageOptions: { sourceType: "script", globals: globals.browser },
  },
  {
    files: ["e2e/**", "tests/**", "*.config.ts"],
    rules: { "react-refresh/only-export-components": "off" },
  },
  prettier,
]);
