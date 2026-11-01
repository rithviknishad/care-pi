import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PROBLEM_BASE, ProblemError } from "@/api/problem";
import type { Plug, PlugSetting, Problem } from "@/api/types";
import { PlugSettingsForm } from "@/features/plugs/plug-settings-form";

// The schema of GET /plugs/{id}/settings-schema (docs/api/README.md section 14.6).
vi.mock("@/api/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/queries")>();
  return {
    ...actual,
    usePlugSettingsSchema: () => ({
      data: {
        type: "object",
        properties: {
          backend: {
            type: "object",
            required: ["API_KEY"],
            properties: {
              API_KEY: { type: ["string", "null"], writeOnly: true },
              HOSTS: { type: "array", items: { type: "string", minLength: 1 } },
            },
          },
          frontend: {
            type: "object",
            properties: {
              "config.refresh": { type: "number", minimum: 5, maximum: 300 },
              "config.methods": { type: "array", uniqueItems: true, items: { oneOf: [] } },
            },
          },
        },
      },
    }),
  };
});

const setting = (s: Partial<PlugSetting> & Pick<PlugSetting, "part" | "key" | "type" | "label">): PlugSetting => ({
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

const plug = {
  id: "demo",
  label: "Demo",
  settings: [
    setting({
      part: "backend",
      key: "API_KEY",
      type: "secret",
      label: "API key",
      required: true,
      secret_set: true,
      source: "operator",
    }),
    setting({
      part: "frontend",
      key: "config.refresh",
      type: "number",
      label: "Refresh",
      min: 5,
      max: 300,
      value: 30,
      default: 30,
    }),
    setting({ part: "frontend", key: "config.on", type: "boolean", label: "Sound", value: true, default: true }),
    setting({ part: "backend", key: "HOSTS", type: "list", label: "Hosts", value: ["*.local"] }),
    setting({
      part: "frontend",
      key: "config.methods",
      type: "choices",
      label: "Methods",
      options: [
        { value: "cash", label: "Cash" },
        { value: "debc", label: "Debit card" },
      ],
      value: ["cash"],
    }),
    setting({ part: "frontend", key: "config.url", type: "url", label: "Queue address", advanced: true, value: "" }),
  ],
} as unknown as Plug;

function renderForm(props: Partial<Parameters<typeof PlugSettingsForm>[0]> = {}) {
  const onPreview = vi.fn();
  render(
    <PlugSettingsForm
      plug={plug}
      plugOn
      enable={false}
      problem={null}
      pending={false}
      disabled={false}
      onPreview={onPreview}
      {...props}
    />,
  );
  return onPreview;
}

describe("the settings form of a plug", () => {
  it("makes one control for each type, with the advanced fields under a disclosure", async () => {
    renderForm();
    // secret: never the value; "A value is set" with Change and Remove.
    const secret = screen.getByTestId("setting-API_KEY");
    expect(secret).toHaveTextContent("A value is set");
    expect(within(secret).getByRole("button", { name: "Change" })).toBeInTheDocument();
    expect(within(secret).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    expect(document.querySelector('input[type="password"]')).toBeNull();
    // number with its limits, boolean switch, list text area, choices check boxes.
    const refresh = screen.getByTestId("setting-config.refresh");
    expect(refresh).toHaveAttribute("type", "number");
    expect(refresh).toHaveAttribute("min", "5");
    expect(refresh).toHaveAttribute("max", "300");
    expect(refresh).toHaveValue(30);
    expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("setting-HOSTS").tagName).toBe("TEXTAREA");
    expect(screen.getAllByRole("checkbox")).toHaveLength(2);
    expect(screen.getByText("Default: 30")).toBeInTheDocument();
    // advanced: hidden until the operator opens it.
    expect(screen.queryByTestId("setting-config.url")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Advanced settings" }));
    expect(screen.getByTestId("setting-config.url")).toHaveAttribute("type", "url");
  });

  it("validates before the request and sends only the changed keys", async () => {
    const onPreview = renderForm();
    const preview = screen.getByRole("button", { name: "Show the effect" });
    expect(preview).toBeDisabled();
    const refresh = screen.getByTestId("setting-config.refresh");
    await userEvent.clear(refresh);
    await userEvent.type(refresh, "2");
    await userEvent.click(preview);
    expect(screen.getByTestId("setting-error-config.refresh")).toHaveTextContent("Use 5 or more.");
    expect(onPreview).not.toHaveBeenCalled();
    await userEvent.clear(refresh);
    await userEvent.type(refresh, "60");
    await userEvent.click(screen.getByRole("checkbox", { name: "Debit card" }));
    await userEvent.click(within(screen.getByTestId("setting-API_KEY")).getByRole("button", { name: "Change" }));
    await userEvent.type(document.querySelector('input[type="password"]') as HTMLInputElement, "new-secret");
    await userEvent.click(preview);
    expect(onPreview).toHaveBeenCalledWith({
      backend: { API_KEY: "new-secret" },
      frontend: { "config.refresh": 60, "config.methods": ["cash", "debc"] },
    });
  });

  it("shows the errors of 422 plug-settings-invalid at their fields", () => {
    const problem = new ProblemError({
      type: `${PROBLEM_BASE}plug-settings-invalid`,
      title: "The plug settings are not valid",
      status: 422,
      detail: "2 settings are not valid.",
      instance: "/api/v1/plugs/demo",
      errors: [
        { pointer: "/settings/backend/API_KEY", message: "a value is required when the plug is on", code: "required" },
        { pointer: "/settings/frontend/config.refresh", message: "must be 300 or less", code: "maximum" },
      ],
    } as Problem);
    renderForm({ problem, enable: true });
    expect(screen.getByTestId("setting-error-API_KEY")).toHaveTextContent("A value is necessary when the plug is on.");
    expect(screen.getByTestId("setting-error-config.refresh")).toHaveTextContent("Use 300 or less.");
    expect(screen.getByRole("button", { name: "Turn on with these settings" })).toBeEnabled();
  });
});
