import type { SizeStep } from "@/api/types";
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
