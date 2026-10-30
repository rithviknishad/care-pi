import { describe, expect, it } from "vitest";
import { RESERVED_BACKEND, RESERVED_FRONTEND } from "@/features/setup/env-defaults";
import { modelFromStep, newRow, stepFromModel, validateEnv } from "@/features/setup/env-logic";
import { hardwareWarnings } from "@/features/setup/hardware-logic";
import { validateIdentity } from "@/features/setup/identity-logic";
import { validateSize } from "@/features/setup/size-logic";
import { stableOrder } from "@/hooks/use-stable-order";
import { reservedNames, schemaRange } from "@/lib/schema";
import type { Hardware } from "@/api/types";

const reserved = { backend: RESERVED_BACKEND, frontend: RESERVED_FRONTEND };

describe("env editor", () => {
  it("round-trips the env step", () => {
    const step = {
      backend: { EMAIL_PORT: "587" },
      frontend: { REACT_ALLOWED_LOCALES: "en,ml" },
      fe: { provider: "local" as const },
    };
    expect(stepFromModel(modelFromStep(step))).toEqual({
      ...step,
      fe: { provider: "local", artifact_url: null, artifact_sha256: null },
    });
  });
  it("rejects bad, reserved, and repeated names and line breaks", () => {
    const model = modelFromStep(undefined);
    const bad = newRow("1BAD", "x");
    const reservedRow = newRow("DATABASE_URL", "x");
    const a = newRow("A", "1");
    const dup = newRow("A", "2");
    const lb = newRow("B", "x\ny");
    const api = newRow("REACT_CARE_API_URL", "x");
    const errors = validateEnv({ ...model, backend: [bad, reservedRow, a, dup, lb], frontend: [api] }, reserved);
    expect(Object.keys(errors).sort()).toEqual(
      [
        `backend.${bad.key}.name`,
        `backend.${reservedRow.key}.name`,
        `backend.${dup.key}.name`,
        `backend.${lb.key}.value`,
        `frontend.${api.key}.name`,
      ].sort(),
    );
  });
});

describe("schema readers", () => {
  const schema = {
    properties: {
      size: { properties: { custom: { properties: { gunicorn_workers: { minimum: 1, maximum: 8 } } } } },
      env: { properties: { backend: { propertyNames: { not: { enum: ["X_SECRET"] } } } } },
    },
  };
  it("reads ranges and reserved names with fallbacks", () => {
    expect(schemaRange(schema, ["size", "custom", "gunicorn_workers"], { min: 0, max: 0 })).toEqual({ min: 1, max: 8 });
    expect(schemaRange(undefined, ["size"], { min: 3, max: 4 })).toEqual({ min: 3, max: 4 });
    expect(reservedNames(schema, "backend", [])).toEqual(["X_SECRET"]);
    expect(reservedNames(schema, "frontend", ["F"])).toEqual(["F"]);
  });
  it("checks the custom size values", () => {
    expect(validateSize({ preset: "small-clinic" }, undefined)).toEqual({});
    const errors = validateSize(
      { preset: "custom", custom: { gunicorn_workers: 9, celery_concurrency: 1, pg_shared_buffers_mb: 128 } },
      undefined,
    );
    expect(Object.keys(errors)).toEqual(["gunicorn_workers"]);
  });
  it("checks the identity", () => {
    expect(validateIdentity({ display_name: "Sunrise Clinic", hostname: "care" })).toEqual({});
    expect(Object.keys(validateIdentity({ display_name: "<b>", hostname: "-care" })).sort()).toEqual([
      "display_name",
      "hostname",
    ]);
  });
});

describe("hardware warnings", () => {
  it("warns on an SD card, a small disk, low RAM, and no internet", () => {
    const hw = {
      model: null,
      arch: "arm64",
      ram_bytes: 2 * 1024 ** 3,
      page_size_bytes: 16384,
      cpu_count: 4,
      root_disk: { device: "/dev/mmcblk0", kind: "sd", size_bytes: 32e9 },
      rtc_present: true,
      ethernet: { interface: "eth0", link: true, speed_mbps: 1000, address: "192.168.1.10" },
      internet_reachable: false,
      clock_synchronized: true,
      disk_probe: { write_mb_s: 12, read_mb_s: 40, measured_at: "2026-10-30T10:00:00Z" },
    } satisfies Hardware;
    expect(hardwareWarnings(hw)).toHaveLength(5);
  });
});

describe("stable order", () => {
  it("never reorders known items", () => {
    const key = (x: { id: string }) => x.id;
    const first = stableOrder([], [{ id: "a" }, { id: "b" }, { id: "c" }], key);
    const second = stableOrder(first.order, [{ id: "c" }, { id: "d" }, { id: "a" }], key);
    expect(second.order).toEqual(["a", "c", "d"]);
  });
});
