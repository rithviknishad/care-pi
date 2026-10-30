#!/usr/bin/env node
// npm run mock: Prism (the contract examples) on port 4010 and the stateful mock on port 4011.
// The stateful mock sends each route that it does not implement to Prism.
// Vite (npm run dev, npm run preview) sends /api and /_care-pi to port 4011.

import { spawn } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const PRISM_PORT = process.env.PRISM_PORT ?? "4010";

const children = [
  spawn(
    path.join(ROOT, "node_modules/.bin/prism"),
    [
      "mock",
      path.resolve(ROOT, "../docs/api/openapi.yaml"),
      "--dynamic=false",
      "--port",
      PRISM_PORT,
      "--host",
      "127.0.0.1",
      "--errors=false",
    ],
    { cwd: ROOT, stdio: "inherit", env: { ...process.env, SCARF_ANALYTICS: "false" } },
  ),
  spawn(process.execPath, [path.join(ROOT, "mock/server.mjs")], {
    cwd: ROOT,
    stdio: "inherit",
    env: { ...process.env, PRISM_URL: `http://127.0.0.1:${PRISM_PORT}` },
  }),
];

function stop(code) {
  for (const c of children) if (c.exitCode === null) c.kill("SIGTERM");
  process.exit(code);
}

for (const c of children) c.on("exit", (code) => stop(code ?? 0));
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => stop(0));
