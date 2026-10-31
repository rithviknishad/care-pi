import types from "@/api/types.gen.ts?raw";
import { describe, expect, it } from "vitest";
import { logUnitOf, shortImage, stepArgument, stepTitle } from "@/features/jobs/step-text";
import { groupChecks } from "@/features/setup/hardware-logic";
import { strings } from "@/lib/strings";
import type { HostCheckItem } from "@/api/types";

// The action keys of the contract (src/api/types.gen.ts, made from docs/api/openapi.yaml; npm run api:check):
// the enum PlanActionKind and the "Other keys" of JobStep.action.
function contractKeys(): string[] {
  const plan = /PlanActionKind: ([^;]+);/.exec(types)?.[1] ?? "";
  const other = /Other keys: ([\s\S]*?)\.\s*\n/.exec(types)?.[1] ?? "";
  const keys = [...plan.matchAll(/"([a-z-]+)"/g), ...other.matchAll(/`([a-z-]+)`/g)].map((m) => m[1] ?? "");
  return keys.filter(Boolean);
}

describe("step names", () => {
  it("has an operator sentence for each action key of the contract", () => {
    const keys = contractKeys();
    expect(keys.length).toBeGreaterThanOrEqual(26);
    expect(keys).toContain("resolve-lock");
    for (const k of keys) {
      const t = stepTitle({ action: k, name: `${k}: x`, state: "succeeded" });
      expect(t.raw, k).toBe(false);
      expect(t.text, k).not.toMatch(/^[a-z-]+:/);
    }
    expect(Object.keys(strings.steps.actions).sort()).toEqual([...keys].sort());
  });

  it("shows the raw name for an unknown key", () => {
    expect(stepTitle({ action: "scale-down", name: "scale-down: solo", state: "running" })).toEqual({
      text: "scale-down: solo",
      raw: true,
    });
    expect(stepTitle({ action: null, name: "something old", state: "succeeded" }).raw).toBe(true);
  });

  it("gives the lock resolution its expected time while it runs", () => {
    expect(
      stepTitle({ action: "resolve-lock", name: "resolve-lock: image digests and git refs", state: "running" }).text,
    ).toBe("Resolve the versions of the images (about 30 s)");
    expect(stepTitle({ action: "resolve-lock", name: "resolve-lock: x", state: "succeeded" }).text).toBe(
      "Resolve the versions of the images",
    );
  });

  it("shortens the image names to repo:tag or repo@sha256:12", () => {
    expect(shortImage("localhost/care-pi/caddy:2")).toBe("caddy:2");
    expect(
      shortImage("ghcr.io/ohcnetwork/care@sha256:77fe743e51a6eb322e89e18183c6012fa6ab8efdd447e8ff59dfb387e6da6ec3"),
    ).toBe("care@sha256:77fe743e51a6");
    expect(shortImage("docker.io/library/redis:8-alpine@sha256:21090af93f8324db2f0f0f4f38a7c603")).toBe(
      "redis@sha256:21090af93f83",
    );
    expect(shortImage("redis")).toBe("redis");
  });

  it("gives a short argument of each known step", () => {
    const arg = (action: string, name: string) => stepArgument({ action, name });
    expect(
      arg(
        "pull-images",
        "pull-images: localhost/care-pi/caddy:2 ghcr.io/ohcnetwork/care@sha256:77fe743e51a6eb322e89e18183c6012fa6ab8efdd447e8ff59dfb387e6da6ec3",
      ),
    ).toBe("caddy:2, care@sha256:77fe743e51a6");
    expect(arg("pull-images", "pull-images: 7 image(s)")).toBe("7 images");
    expect(arg("write-files", "write-files: 15 file(s)")).toBe("15 files");
    expect(arg("restart", "restart: care-beat.service care-api.service")).toBe("care-beat, care-api");
    expect(arg("full-cycle", "full-cycle: care.target (care-db unit changed; pod changed)")).toBe(
      "care-db unit changed; pod changed",
    );
    expect(arg("set-hostname", "set-hostname: care -> lumine")).toBe("care → lumine");
    expect(arg("create-care-superuser", "create-care-superuser: w4admin")).toBe("w4admin");
    expect(arg("daemon-reload", "daemon-reload")).toBe("");
    expect(arg("scale-down", "scale-down: solo")).toBe("");
  });

  it("finds the unit of an error for the Logs page", () => {
    expect(
      logUnitOf(
        "apply failed at ensure-stanza: podman exec --user postgres care-db pg_isready --quiet: exit status 255",
      ),
    ).toBe("db");
    expect(logUnitOf("care-api.service did not become healthy in 10m0s.")).toBe("api");
    expect(logUnitOf("care-fe-build.service failed")).toBe("fe-build");
    expect(logUnitOf("cannot start care.target")).toBe("target");
    expect(logUnitOf("care-pi-firewall is off; care-pi.service stopped")).toBe("agent");
    expect(logUnitOf("ghcr.io/ohcnetwork/care@sha256:1 and care_fe develop")).toBeNull();
    expect(logUnitOf(null)).toBeNull();
  });
});

describe("host checks", () => {
  const check = (name: string, ok = true, level: "required" | "advised" = "advised"): HostCheckItem => ({
    name,
    level,
    ok,
    detail: "",
  });

  it("puts the failures first and collapses the 25 directories into one row (lumine: 43 checks)", () => {
    const checks = [
      check("podman", true, "required"),
      ...Array.from({ length: 25 }, (_, i) => check(`dir /var/lib/care-pi/d${i}`)),
      ...Array.from({ length: 16 }, (_, i) => check(`c${i}`)),
      check("clock", false),
    ];
    const g = groupChecks(checks);
    expect(g.total).toBe(43);
    expect(g.ok).toBe(42);
    expect(g.problems.map((c) => c.name)).toEqual(["clock"]);
    expect(g.passed).toHaveLength(17);
    expect(g.dirs).toEqual({ ok: 25, total: 25 });
  });

  it("lists a failed directory with the failures and counts it in the row", () => {
    const g = groupChecks([check("dir /etc/care-pi", false), check("dir /var/lib/care-pi")]);
    expect(g.problems.map((c) => c.name)).toEqual(["dir /etc/care-pi"]);
    expect(g.dirs).toEqual({ ok: 1, total: 2 });
    expect(g.passed).toHaveLength(0);
  });
});
