// Pure helpers for the texts and badges of the Solution and plugs page.

import type { Plug, PlugChange, PlugChangePlan } from "@/api/types";
import { formatDuration } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import type { BadgeSpec } from "@/features/updates/update-text";

const p = strings.plugs;

export type BundleState = NonNullable<Plug["frontend"]>["bundle"]["state"];
export type PlugConfigState = NonNullable<Plug["frontend"]>["plug_config"]["state"];

export function kindBadge(kind: Plug["kind"]): BadgeSpec {
  return {
    text: p.kinds[kind] ?? kind,
    variant: kind === "frontend" ? "info" : kind === "backend" ? "primary" : "neutral",
  };
}

export function sourceBadge(source: Plug["source"]): BadgeSpec {
  return {
    text: p.sources[source] ?? source,
    variant: source === "operator" ? "primary" : source === "custom" ? "warning" : "neutral",
  };
}

export function bundleBadge(state: BundleState): BadgeSpec {
  const text = p.bundleStates[state] ?? state;
  switch (state) {
    case "present":
      return { text, variant: "success" };
    case "external":
      return { text, variant: "warning" };
    case "failed":
      return { text, variant: "destructive" };
    case "downloading":
    case "building":
      return { text, variant: "info" };
    default:
      return { text, variant: "neutral" };
  }
}

export function plugConfigBadge(state: PlugConfigState): BadgeSpec {
  const text = p.plugConfigStates[state] ?? state;
  return { text, variant: state === "synced" ? "success" : state === "differs" ? "warning" : "neutral" };
}

// The title of the plan dialog: on, off, the default of the solution, or a change of the settings.
export function planTitle(change: PlugChange, label: string): string {
  if (change.enabled === true) return fmt(p.planTitles.on ?? "", { label });
  if (change.enabled === false) return fmt(p.planTitles.off ?? "", { label });
  if (change.enabled === null) return fmt(p.planTitles.default ?? "", { label });
  return fmt(p.planTitles.settings ?? "", { label });
}

// What the change does to Care, in plain words (docs/api/README.md section 14.7).
export function planEffect(plan: Pick<PlugChangePlan, "path" | "estimated_downtime_s">): string {
  const template = p.paths[plan.path] ?? plan.path;
  const seconds = plan.estimated_downtime_s;
  const time = seconds && seconds > 0 ? formatDuration(seconds) : formatDuration(60);
  return fmt(template, { time });
}

// A plug with the auto rule (care_multihost_files_be): the console shows "Automatic" and no switch.
export function isAutomatic(plug: Pick<Plug, "auto">): boolean {
  return plug.auto === "when_multihost";
}
