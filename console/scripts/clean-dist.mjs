#!/usr/bin/env node
// Removes the old build from dist/ but keeps dist/.gitkeep (the Go embed needs the directory).
// vite.config.ts sets emptyOutDir: false, so Vite does not delete .gitkeep.

import { existsSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../dist");
const KEEP = new Set([".gitkeep"]);

if (!existsSync(DIST)) mkdirSync(DIST, { recursive: true });
for (const entry of readdirSync(DIST)) {
  if (!KEEP.has(entry)) rmSync(path.join(DIST, entry), { recursive: true, force: true });
}
