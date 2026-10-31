// The settings form of a plug (docs/api/README.md section 14.6): pure helpers that make the fields from
// GET /plugs/{plug_id}/settings-schema and Plug.settings, validate a draft, map the errors of 422
// plug-settings-invalid to fields, and make the body member settings of PUT /plugs/{plug_id} with only the changes.
// Unit tests: tests/settings-form.test.ts.

import type { JsonSchema } from "@/api/queries";
import type { PlugSetting, PlugSettingValue, PlugSettingsValues, Problem, ValidationError } from "@/api/types";
import { fmt, strings } from "@/lib/strings";

const p = strings.plugs;

export type Part = "backend" | "frontend";
export type FieldType = PlugSetting["type"];

export interface FormField {
  id: string;
  part: Part;
  key: string;
  label: string;
  help: string | null;
  type: FieldType;
  required: boolean;
  placeholder: string | null;
  options: { value: string; label: string }[];
  min: number | null;
  max: number | null;
  pattern: string | null;
  advanced: boolean;
  defaultValue: PlugSettingValue;
  value: PlugSettingValue;
  secretSet: boolean;
  source: "default" | "operator";
}

// A secret is write-only: "keep" sends nothing, "set" sends the text, "remove" sends null.
export interface SecretDraft {
  mode: "keep" | "set" | "remove";
  text: string;
}

export type DraftValue = string | boolean | string[] | SecretDraft;
export type Draft = Record<string, DraftValue>;

export function fieldId(part: string, key: string): string {
  return `${part}/${key}`;
}

type Obj = Record<string, unknown>;

function obj(v: unknown): Obj | null {
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Obj) : null;
}

function num(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

function str(v: unknown): string | null {
  return typeof v === "string" ? v : null;
}

function partOf(schema: JsonSchema | null | undefined, part: Part): { props: Obj; required: string[] } {
  const ps = obj(obj(obj(schema)?.properties)?.[part]);
  const required = Array.isArray(ps?.required) ? (ps.required as unknown[]).filter((k) => typeof k === "string") : [];
  return { props: obj(ps?.properties) ?? {}, required: required as string[] };
}

function typesOf(prop: Obj): string[] {
  const t = prop.type;
  return Array.isArray(t) ? (t as string[]) : typeof t === "string" ? [t] : [];
}

// The field type of a schema property (the table of docs/api/README.md section 14.6, read backwards).
export function typeFromSchema(prop: Obj): FieldType {
  const types = typesOf(prop);
  if (prop.writeOnly === true) return "secret";
  if (types.includes("boolean")) return "boolean";
  if (types.includes("number") || types.includes("integer")) return "number";
  if (types.includes("array")) return obj(prop.items)?.oneOf ? "choices" : "list";
  if (prop.format === "uri") return "url";
  return "string";
}

function optionsFromSchema(prop: Obj): { value: string; label: string }[] {
  const oneOf = obj(prop.items)?.oneOf;
  if (!Array.isArray(oneOf)) return [];
  return oneOf
    .map((o) => obj(o))
    .filter((o): o is Obj => o !== null && typeof o.const === "string")
    .map((o) => ({ value: o.const as string, label: str(o.title) ?? (o.const as string) }));
}

function patternFor(type: FieldType, prop: Obj): string | null {
  if (type === "list") return str(obj(prop.items)?.pattern);
  // The url pattern of the schema (^https?://) is the url check of validateField.
  if (type === "url") return null;
  return str(prop.pattern);
}

function fromSchemaOnly(part: Part, key: string, prop: Obj, required: boolean): FormField {
  const type = typeFromSchema(prop);
  return {
    id: fieldId(part, key),
    part,
    key,
    label: str(prop.title) ?? key,
    help: str(prop.description),
    type,
    required,
    placeholder: null,
    options: optionsFromSchema(prop),
    min: num(prop.minimum),
    max: num(prop.maximum),
    pattern: patternFor(type, prop),
    advanced: false,
    defaultValue: (prop.default as PlugSettingValue) ?? null,
    value: null,
    secretSet: false,
    source: "default",
  };
}

// buildFields makes the form: the fields of Plug.settings in their order, with the limits of the schema,
// then each property that only the schema has. Plug.settings gives the values, "advanced", and the placeholders.
export function buildFields(settings: readonly PlugSetting[], schema?: JsonSchema | null): FormField[] {
  const parts = { backend: partOf(schema, "backend"), frontend: partOf(schema, "frontend") };
  const seen = new Set<string>();
  const fields: FormField[] = settings.map((s) => {
    const prop = obj(parts[s.part].props[s.key]) ?? {};
    const base = fromSchemaOnly(s.part, s.key, prop, parts[s.part].required.includes(s.key));
    seen.add(base.id);
    return {
      ...base,
      label: s.label || base.label,
      help: s.help ?? base.help,
      type: s.type,
      required: s.required || base.required,
      placeholder: s.placeholder,
      options: s.options ?? base.options,
      min: s.min ?? base.min,
      max: s.max ?? base.max,
      pattern: s.pattern ?? patternFor(s.type, prop),
      advanced: s.advanced,
      defaultValue: s.default ?? base.defaultValue,
      value: s.value,
      secretSet: s.secret_set === true,
      source: s.source,
    };
  });
  for (const part of ["backend", "frontend"] as const) {
    for (const [key, prop] of Object.entries(parts[part].props)) {
      if (seen.has(fieldId(part, key))) continue;
      fields.push(fromSchemaOnly(part, key, obj(prop) ?? {}, parts[part].required.includes(key)));
    }
  }
  return fields;
}

export function groupFields(fields: readonly FormField[]): { basic: FormField[]; advanced: FormField[] } {
  return { basic: fields.filter((f) => !f.advanced), advanced: fields.filter((f) => f.advanced) };
}

// The draft of a value: text for the text fields (so a half-typed number stays), lines for a list.
export function draftOf(field: FormField, value: PlugSettingValue = field.value ?? field.defaultValue): DraftValue {
  switch (field.type) {
    case "boolean":
      return value === true;
    case "secret":
      return { mode: "keep", text: "" };
    case "list":
      return Array.isArray(value) ? value.join("\n") : typeof value === "string" ? value : "";
    case "choices":
      return Array.isArray(value) ? value.map(String) : [];
    default:
      return value === null || value === undefined ? "" : String(value);
  }
}

export function initialDraft(fields: readonly FormField[]): Draft {
  return Object.fromEntries(fields.map((f) => [f.id, draftOf(f)]));
}

function lines(text: string): string[] {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
}

// The value of a draft for the request body. A secret in the mode "keep" has no value (undefined).
export function valueOf(field: FormField, d: DraftValue | undefined): PlugSettingValue | undefined {
  switch (field.type) {
    case "boolean":
      return d === true;
    case "secret": {
      const s = d as SecretDraft | undefined;
      if (!s || s.mode === "keep") return undefined;
      return s.mode === "remove" ? null : s.text;
    }
    case "number": {
      const t = String(d ?? "").trim();
      return t === "" ? null : Number(t);
    }
    case "list":
      return lines(String(d ?? ""));
    case "choices": {
      const set = new Set(Array.isArray(d) ? d : []);
      const known = field.options.map((o) => o.value).filter((v) => set.has(v));
      return [...known, ...[...set].filter((v) => !known.includes(v))];
    }
    default:
      return String(d ?? "").trim();
  }
}

function matches(pattern: string | null, value: string): boolean {
  if (!pattern) return true;
  try {
    return new RegExp(pattern, "u").test(value);
  } catch {
    return true;
  }
}

function isUrl(value: string): boolean {
  if (!/^https?:\/\/\S+$/.test(value)) return false;
  try {
    new URL(value);
    return true;
  } catch {
    return false;
  }
}

// validateField gives the error text of one field, or null. A required field needs a value only when the
// plug is on after the change (docs/api/README.md section 14.6).
export function validateField(field: FormField, d: DraftValue | undefined, plugOn: boolean): string | null {
  const need = field.required && plugOn;
  switch (field.type) {
    case "boolean":
      return null;
    case "secret": {
      const s = (d as SecretDraft | undefined) ?? { mode: "keep", text: "" };
      if (s.mode === "set") {
        if (s.text === "") return need ? p.requiredOn : p.required;
        return matches(field.pattern, s.text) ? null : p.pattern;
      }
      if (need && (s.mode === "remove" || !field.secretSet)) return p.requiredOn;
      return null;
    }
    case "number": {
      const t = String(d ?? "").trim();
      if (t === "") return need ? p.requiredOn : null;
      const n = Number(t);
      if (!Number.isFinite(n)) return p.number;
      if (field.min !== null && n < field.min) return fmt(p.min, { min: field.min });
      if (field.max !== null && n > field.max) return fmt(p.max, { max: field.max });
      return null;
    }
    case "list": {
      const all = String(d ?? "").split("\n");
      let count = 0;
      for (let i = 0; i < all.length; i++) {
        const line = all[i]?.trim() ?? "";
        if (!line) continue;
        count++;
        if (!matches(field.pattern, line)) return fmt(p.listLine, { line: i + 1 });
      }
      return need && count === 0 ? p.requiredOn : null;
    }
    case "choices":
      return need && (!Array.isArray(d) || d.length === 0) ? p.requiredOn : null;
    default: {
      const t = String(d ?? "").trim();
      if (t === "") return need ? p.requiredOn : null;
      if (field.type === "url" && !isUrl(t)) return p.url;
      return matches(field.pattern, t) ? null : p.pattern;
    }
  }
}

export function validateDraft(fields: readonly FormField[], draft: Draft, plugOn: boolean): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of fields) {
    const e = validateField(f, draft[f.id], plugOn);
    if (e) out[f.id] = e;
  }
  return out;
}

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

// changesOf makes the member settings of PUT /plugs/{plug_id}: only the changed keys. A field in `resets`
// sends null (the default of the solution applies again). null when nothing changed.
export function changesOf(
  fields: readonly FormField[],
  draft: Draft,
  resets: ReadonlySet<string> = new Set(),
): PlugSettingsValues | null {
  const out: { backend: Record<string, PlugSettingValue>; frontend: Record<string, PlugSettingValue> } = {
    backend: {},
    frontend: {},
  };
  for (const f of fields) {
    if (resets.has(f.id) && f.type !== "secret") {
      out[f.part][f.key] = null;
      continue;
    }
    const next = valueOf(f, draft[f.id]);
    if (next === undefined) continue;
    if (f.type === "secret" || !same(next, valueOf(f, draftOf(f)))) out[f.part][f.key] = next;
  }
  const body: PlugSettingsValues = {};
  if (Object.keys(out.backend).length > 0) body.backend = out.backend;
  if (Object.keys(out.frontend).length > 0) body.frontend = out.frontend;
  return body.backend || body.frontend ? body : null;
}

// "/settings/backend/A~1B" -> "backend/A/B" (RFC 6901: ~1 is /, ~0 is ~). A pointer into a list item
// ("/settings/frontend/config.payment_methods/1") gives its field. null for another pointer.
export function fieldIdFromPointer(pointer: string): string | null {
  const segs = pointer.split("/");
  const part = segs[2];
  const raw = segs[3];
  if (segs[0] !== "" || segs[1] !== "settings" || (part !== "backend" && part !== "frontend") || !raw) return null;
  return fieldId(part, raw.replace(/~1/g, "/").replace(/~0/g, "~"));
}

// The console text of a validation error of the agent. The code selects the text; else the message of the agent.
function errorText(field: FormField | undefined, e: ValidationError): string {
  switch (e.code) {
    case "required":
      return p.requiredOn;
    case "pattern":
      return p.pattern;
    case "format":
      return field?.type === "url" ? p.url : p.pattern;
    case "minimum":
      return field?.min !== null && field?.min !== undefined ? fmt(p.min, { min: field.min }) : e.message;
    case "maximum":
      return field?.max !== null && field?.max !== undefined ? fmt(p.max, { max: field.max }) : e.message;
    default:
      return e.message;
  }
}

// serverErrors maps errors[] of 422 plug-settings-invalid to the fields. The first error of a field wins.
export function serverErrors(fields: readonly FormField[], problem: Pick<Problem, "errors"> | null | undefined) {
  const out: Record<string, string> = {};
  for (const e of problem?.errors ?? []) {
    const id = fieldIdFromPointer(e.pointer);
    if (!id || out[id]) continue;
    out[id] = errorText(
      fields.find((f) => f.id === id),
      e,
    );
  }
  return out;
}

// The text of a value for "Default: …".
export function valueText(field: FormField, value: PlugSettingValue): string {
  if (value === null || value === undefined || value === "") return "";
  if (typeof value === "boolean") return value ? strings.common.on : strings.common.off;
  if (Array.isArray(value)) {
    return value.map((v) => field.options.find((o) => o.value === v)?.label ?? String(v)).join(", ");
  }
  return String(value);
}
