import { describe, expect, it } from "vitest";
import type { PlugSetting } from "@/api/types";
import {
  buildFields,
  changesOf,
  draftOf,
  fieldIdFromPointer,
  groupFields,
  initialDraft,
  serverErrors,
  typeFromSchema,
  validateDraft,
  validateField,
  valueText,
} from "@/features/plugs/settings-form";

const setting = (s: Partial<PlugSetting> & Pick<PlugSetting, "part" | "key" | "type">): PlugSetting => ({
  label: s.key,
  help: null,
  required: false,
  placeholder: null,
  options: null,
  default: null,
  min: null,
  max: null,
  pattern: null,
  advanced: false,
  value: null,
  secret_set: s.type === "secret" ? false : null,
  source: "default",
  ...s,
});

// The settings of care_filly and of a plug with each type (docs/api/README.md section 14.6).
const SETTINGS: PlugSetting[] = [
  setting({
    part: "backend",
    key: "MEDISPEAK_API_KEY",
    type: "secret",
    required: true,
    pattern: "^msk_live_[A-Za-z0-9]{8,}$",
  }),
  setting({ part: "backend", key: "MEDISPEAK_BASE_URL", type: "url", required: true, value: "" }),
  setting({
    part: "frontend",
    key: "config.refresh_seconds",
    type: "number",
    min: 5,
    max: 300,
    default: 30,
    value: 30,
  }),
  setting({ part: "frontend", key: "config.title", type: "string", pattern: "^[^<>]{1,40}$", value: "Waiting room" }),
  setting({ part: "frontend", key: "config.sound", type: "boolean", default: true, value: true, advanced: true }),
  setting({ part: "backend", key: "ALLOWED_HOSTS", type: "list", value: ["*.local", "*.ts.net"] }),
  setting({
    part: "frontend",
    key: "config.payment_methods",
    type: "choices",
    options: [
      { value: "cash", label: "Cash" },
      { value: "debc", label: "Debit card" },
      { value: "ddpo", label: "Direct deposit" },
    ],
    value: ["cash", "ddpo"],
    source: "operator",
  }),
];

const SCHEMA = {
  type: "object",
  properties: {
    backend: {
      type: "object",
      additionalProperties: false,
      required: ["MEDISPEAK_API_KEY", "MEDISPEAK_BASE_URL"],
      properties: {
        MEDISPEAK_API_KEY: { type: ["string", "null"], writeOnly: true, title: "Medispeak API key" },
        MEDISPEAK_BASE_URL: { type: "string", format: "uri", pattern: "^https?://", title: "Medispeak address" },
        ALLOWED_HOSTS: { type: "array", items: { type: "string", minLength: 1, pattern: "^[*a-z0-9.-]+$" } },
        EXTRA_LIMIT: {
          type: "integer",
          minimum: 1,
          maximum: 9,
          title: "Extra limit",
          description: "Only in the schema.",
        },
      },
    },
    frontend: {
      type: "object",
      additionalProperties: false,
      properties: {
        "config.refresh_seconds": { type: "number", minimum: 5, maximum: 300 },
        "config.payment_methods": {
          type: "array",
          uniqueItems: true,
          items: { oneOf: [{ const: "cash", title: "Cash" }] },
        },
      },
    },
  },
};

describe("the settings form of a plug", () => {
  it("maps each schema type to a control type", () => {
    expect(typeFromSchema({ type: "boolean" })).toBe("boolean");
    expect(typeFromSchema({ type: "number" })).toBe("number");
    expect(typeFromSchema({ type: "integer" })).toBe("number");
    expect(typeFromSchema({ type: "string", format: "uri" })).toBe("url");
    expect(typeFromSchema({ type: ["string", "null"], writeOnly: true })).toBe("secret");
    expect(typeFromSchema({ type: "array", items: { type: "string" } })).toBe("list");
    expect(typeFromSchema({ type: "array", items: { oneOf: [] } })).toBe("choices");
    expect(typeFromSchema({ type: "string" })).toBe("string");
  });

  it("makes the fields in the order of the solution, with the limits of the schema, then the schema-only fields", () => {
    const fields = buildFields(SETTINGS, SCHEMA);
    expect(fields.map((f) => f.id)).toEqual([
      "backend/MEDISPEAK_API_KEY",
      "backend/MEDISPEAK_BASE_URL",
      "frontend/config.refresh_seconds",
      "frontend/config.title",
      "frontend/config.sound",
      "backend/ALLOWED_HOSTS",
      "frontend/config.payment_methods",
      "backend/EXTRA_LIMIT",
    ]);
    const hosts = fields.find((f) => f.key === "ALLOWED_HOSTS");
    expect(hosts?.pattern).toBe("^[*a-z0-9.-]+$");
    const url = fields.find((f) => f.key === "MEDISPEAK_BASE_URL");
    expect(url?.pattern).toBeNull();
    expect(url?.required).toBe(true);
    const extra = fields.find((f) => f.key === "EXTRA_LIMIT");
    expect(extra).toMatchObject({ type: "number", min: 1, max: 9, label: "Extra limit", help: "Only in the schema." });
    // Without the schema the form still has each field of Plug.settings.
    expect(buildFields(SETTINGS, null)).toHaveLength(SETTINGS.length);
  });

  it("puts the advanced fields in their own group", () => {
    const { basic, advanced } = groupFields(buildFields(SETTINGS, SCHEMA));
    expect(advanced.map((f) => f.key)).toEqual(["config.sound"]);
    expect(basic).toHaveLength(7);
  });

  it("makes a draft of each value and never shows a secret", () => {
    const fields = buildFields(SETTINGS, SCHEMA);
    const draft = initialDraft(fields);
    expect(draft["backend/MEDISPEAK_API_KEY"]).toEqual({ mode: "keep", text: "" });
    expect(draft["frontend/config.refresh_seconds"]).toBe("30");
    expect(draft["backend/ALLOWED_HOSTS"]).toBe("*.local\n*.ts.net");
    expect(draft["frontend/config.payment_methods"]).toEqual(["cash", "ddpo"]);
    expect(draft["frontend/config.sound"]).toBe(true);
  });

  it("asks for a required value only when the plug is on", () => {
    const fields = buildFields(SETTINGS, SCHEMA);
    const draft = initialDraft(fields);
    expect(validateDraft(fields, draft, false)).toEqual({});
    expect(validateDraft(fields, draft, true)).toEqual({
      "backend/MEDISPEAK_API_KEY": "A value is necessary when the plug is on.",
      "backend/MEDISPEAK_BASE_URL": "A value is necessary when the plug is on.",
    });
  });

  it("checks the patterns, the URL, the limits, and each line of a list", () => {
    const fields = buildFields(SETTINGS, SCHEMA);
    const f = (key: string) => fields.find((x) => x.key === key)!;
    expect(validateField(f("MEDISPEAK_API_KEY"), { mode: "set", text: "sk_wrong" }, true)).toBe(
      "The value does not have the correct format.",
    );
    expect(validateField(f("MEDISPEAK_API_KEY"), { mode: "set", text: "msk_live_0123456789" }, true)).toBeNull();
    expect(validateField(f("MEDISPEAK_BASE_URL"), "medispeak.example.org", true)).toBe(
      "Enter an address that starts with http:// or https://.",
    );
    expect(validateField(f("MEDISPEAK_BASE_URL"), "https://medispeak.example.org/api/v2", true)).toBeNull();
    expect(validateField(f("config.refresh_seconds"), "2", true)).toBe("Use 5 or more.");
    expect(validateField(f("config.refresh_seconds"), "301", true)).toBe("Use 300 or less.");
    expect(validateField(f("config.refresh_seconds"), "abc", true)).toBe("Enter a number.");
    expect(validateField(f("config.title"), "<b>", true)).toBe("The value does not have the correct format.");
    expect(validateField(f("ALLOWED_HOSTS"), "*.local\n\nBAD HOST", true)).toBe(
      "Line 3: the value does not have the correct format.",
    );
  });

  it("sends only the changed keys, a secret only when it changes, and null for the default", () => {
    const fields = buildFields(SETTINGS, SCHEMA);
    const draft = initialDraft(fields);
    expect(changesOf(fields, draft)).toBeNull();
    const next = {
      ...draft,
      "backend/MEDISPEAK_API_KEY": { mode: "set" as const, text: "msk_live_0123456789" },
      "frontend/config.refresh_seconds": "60",
      "backend/ALLOWED_HOSTS": "*.local\n care.example.org \n",
    };
    expect(changesOf(fields, next)).toEqual({
      backend: { MEDISPEAK_API_KEY: "msk_live_0123456789", ALLOWED_HOSTS: ["*.local", "care.example.org"] },
      frontend: { "config.refresh_seconds": 60 },
    });
    expect(changesOf(fields, { ...draft, "backend/MEDISPEAK_API_KEY": { mode: "remove", text: "" } })).toEqual({
      backend: { MEDISPEAK_API_KEY: null },
    });
    expect(changesOf(fields, draft, new Set(["frontend/config.payment_methods"]))).toEqual({
      frontend: { "config.payment_methods": null },
    });
    // The order of the options, not the order of the clicks.
    expect(changesOf(fields, { ...draft, "frontend/config.payment_methods": ["ddpo", "debc", "cash"] })).toEqual({
      frontend: { "config.payment_methods": ["cash", "debc", "ddpo"] },
    });
  });

  it("maps the errors of 422 plug-settings-invalid to the fields (RFC 6901 pointers)", () => {
    expect(fieldIdFromPointer("/settings/backend/MEDISPEAK_API_KEY")).toBe("backend/MEDISPEAK_API_KEY");
    expect(fieldIdFromPointer("/settings/frontend/config.payment_methods/1")).toBe("frontend/config.payment_methods");
    expect(fieldIdFromPointer("/settings/backend/A~1B~0C")).toBe("backend/A/B~C");
    expect(fieldIdFromPointer("/enabled")).toBeNull();
    expect(fieldIdFromPointer("")).toBeNull();
    const fields = buildFields(SETTINGS, SCHEMA);
    expect(
      serverErrors(fields, {
        errors: [
          {
            pointer: "/settings/backend/MEDISPEAK_API_KEY",
            message: "a value is required when the plug is on",
            code: "required",
          },
          { pointer: "/settings/backend/MEDISPEAK_BASE_URL", message: "must be an https URL", code: "format" },
          { pointer: "/settings/backend/MEDISPEAK_BASE_URL", message: "second error", code: "pattern" },
          { pointer: "/settings/frontend/config.refresh_seconds", message: "must be 5 or more", code: "minimum" },
          { pointer: "/settings/frontend/config.title", message: "agent text", code: "other" },
          { pointer: "", message: "body", code: "x" },
        ],
      }),
    ).toEqual({
      "backend/MEDISPEAK_API_KEY": "A value is necessary when the plug is on.",
      "backend/MEDISPEAK_BASE_URL": "Enter an address that starts with http:// or https://.",
      "frontend/config.refresh_seconds": "Use 5 or more.",
      "frontend/config.title": "agent text",
    });
  });

  it("shows a default value in words", () => {
    const fields = buildFields(SETTINGS, SCHEMA);
    const f = (key: string) => fields.find((x) => x.key === key)!;
    expect(valueText(f("config.sound"), true)).toBe("On");
    expect(valueText(f("config.payment_methods"), ["cash", "ddpo"])).toBe("Cash, Direct deposit");
    expect(valueText(f("config.refresh_seconds"), 30)).toBe("30");
    expect(valueText(f("config.title"), null)).toBe("");
    expect(draftOf(f("config.sound"), false)).toBe(false);
  });
});
