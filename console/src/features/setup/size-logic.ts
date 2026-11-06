import type { Preset, PresetName, SizeStep } from "@/api/types";
import { schemaRange } from "@/lib/schema";
import { fmt, strings } from "@/lib/strings";

export type CustomKey = "gunicorn_workers" | "celery_concurrency" | "pg_shared_buffers_mb";

export const CUSTOM_KEYS: CustomKey[] = ["gunicorn_workers", "celery_concurrency", "pg_shared_buffers_mb"];

const FALLBACK: Record<CustomKey, { min: number; max: number }> = {
  gunicorn_workers: { min: 1, max: 8 },
  celery_concurrency: { min: 1, max: 4 },
  pg_shared_buffers_mb: { min: 32, max: 2048 },
};

export function customRanges(schema: unknown): Record<CustomKey, { min: number; max: number }> {
  return {
    gunicorn_workers: schemaRange(schema, ["size", "custom", "gunicorn_workers"], FALLBACK.gunicorn_workers),
    celery_concurrency: schemaRange(schema, ["size", "custom", "celery_concurrency"], FALLBACK.celery_concurrency),
    pg_shared_buffers_mb: schemaRange(
      schema,
      ["size", "custom", "pg_shared_buffers_mb"],
      FALLBACK.pg_shared_buffers_mb,
    ),
  };
}

export function validateSize(step: SizeStep, schema: unknown): Partial<Record<CustomKey, string>> {
  if (step.preset !== "custom") return {};
  const ranges = customRanges(schema);
  const errors: Partial<Record<CustomKey, string>> = {};
  for (const key of CUSTOM_KEYS) {
    const v = step.custom?.[key];
    const { min, max } = ranges[key];
    if (v === undefined || !Number.isInteger(v)) errors[key] = strings.size.noValue;
    else if (v < min || v > max) errors[key] = fmt(strings.size.range, { min, max });
  }
  return errors;
}

type PresetValues = Pick<Preset, "name" | "gunicorn_workers" | "celery_concurrency" | "pg_shared_buffers_mb">;

// The preset that the custom values are near (the hint "≈ Small clinic" of the advanced size step): the same values,
// or the preset with the smallest relative distance when each value is within 50 % of it. null: between the presets.
export function nearestPreset(values: Record<CustomKey, number>, presets: PresetValues[]): PresetName | null {
  if (CUSTOM_KEYS.some((k) => !Number.isFinite(values[k]) || values[k] <= 0)) return null;
  let best: { name: PresetName; d: number } | null = null;
  for (const p of presets) {
    if (p.name === "custom") continue;
    let d = 0;
    let within = true;
    for (const k of CUSTOM_KEYS) {
      const ref = p[k];
      if (!ref) {
        within = false;
        break;
      }
      const rel = Math.abs(values[k] - ref) / ref;
      if (rel > 0.5) within = false;
      d += rel;
    }
    if (within && (!best || d < best.d)) best = { name: p.name, d };
  }
  return best?.name ?? null;
}
