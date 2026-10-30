// Small readers of the config JSON Schema (GET /config/schema). Each reader has a fallback,
// so a form works before the schema arrives and with an older agent.

type Json = Record<string, unknown>;

function asObject(v: unknown): Json | null {
  return typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Json) : null;
}

// Follows properties.<key> for each key. It resolves a local "#/$defs/x" reference.
export function schemaAt(schema: unknown, keys: string[]): Json | null {
  const rootSchema = asObject(schema);
  let node = rootSchema;
  for (const key of keys) {
    node = asObject(asObject(node?.properties)?.[key]);
    const ref = node?.$ref;
    if (typeof ref === "string" && ref.startsWith("#/$defs/")) {
      node = asObject(asObject(rootSchema?.$defs)?.[ref.slice(8)]) ?? node;
    }
    if (!node) return null;
  }
  return node;
}

export function schemaRange(schema: unknown, keys: string[], fallback: { min: number; max: number }) {
  const node = schemaAt(schema, keys);
  return {
    min: typeof node?.minimum === "number" ? node.minimum : fallback.min,
    max: typeof node?.maximum === "number" ? node.maximum : fallback.max,
  };
}

export function schemaPattern(schema: unknown, keys: string[], fallback: RegExp): RegExp {
  const pattern = schemaAt(schema, keys)?.pattern;
  if (typeof pattern !== "string") return fallback;
  try {
    return new RegExp(pattern, "u");
  } catch {
    return fallback;
  }
}

export function reservedNames(schema: unknown, part: "backend" | "frontend", fallback: string[]): string[] {
  const names = asObject(asObject(schemaAt(schema, ["env", part])?.propertyNames)?.not)?.enum;
  return Array.isArray(names) && names.every((n) => typeof n === "string") ? (names as string[]) : fallback;
}

export const HOSTNAME_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
export const DISPLAY_NAME_RE = /^[^\r\n<>]*$/;
export const ENV_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
export const CARE_USERNAME_RE = /^[a-z0-9][a-z0-9._-]{2,14}[a-z0-9]$/;
