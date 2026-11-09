/**
 * Checks the strings of the plug: each t("key") in src/ has an entry in src/locale/en.json (under "cpo"),
 * each entry is used, and the keys are sorted. A missing key renders as the raw key, with no error.
 *
 * Usage: node scripts/check-locale.mjs
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const strings = JSON.parse(
  readFileSync(join(root, "src/locale/en.json"), "utf8"),
).cpo;
const keys = Object.keys(strings);

// Keys that the code builds at run time: t(`step_${step}`) and friends.
const STEPS = [
  "organization",
  "facility",
  "roles",
  "staff",
  "valuesets",
  "questionnaires",
  "catalog",
  "demo_data",
];
const dynamic = [
  ...STEPS.map((s) => `step_${s}`),
  ...STEPS.map((s) => `step_${s}_help`),
  "step_done",
  ...["pending", "done", "skipped", "failed", "locked"].map(
    (s) => `state_${s}`,
  ),
  ...["male", "female", "transgender", "non_binary"].map((g) => `gender_${g}`),
];

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return files(path);
    return /\.(ts|tsx)$/.test(name) && !name.endsWith(".test.ts") ? [path] : [];
  });
}

const used = new Set(dynamic);
for (const file of files(join(root, "src"))) {
  const source = readFileSync(file, "utf8");
  for (const match of source.matchAll(/\bt\(\s*"([a-z0-9_]+)"/g))
    used.add(match[1]);
}

const exists = (key) =>
  key in strings || `${key}_one` in strings || `${key}_other` in strings;
const base = (key) => key.replace(/_(one|other)$/, "");
const problems = [];
for (const key of used)
  if (!exists(key)) problems.push(`missing in en.json: ${key}`);
for (const key of keys)
  if (!used.has(base(key)) && !used.has(key)) problems.push(`not used: ${key}`);
const sorted = [...keys].sort();
if (sorted.join("\n") !== keys.join("\n"))
  problems.push("en.json keys are not sorted");

if (problems.length) {
  console.error(problems.map((p) => `FAIL ${p}`).join("\n"));
  process.exit(1);
}
console.log(`ok  ${keys.length} strings, ${used.size} keys used, sorted`);
