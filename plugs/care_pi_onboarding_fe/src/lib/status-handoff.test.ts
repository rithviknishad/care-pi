import { describe, expect, it } from "vitest";

import { handOffStatus, takeHandedStatus } from "@/lib/status-handoff";
import type { Status } from "@/lib/wizard";

const status = { steps: [], next_step: "organization" } as unknown as Status;

describe("status handoff", () => {
  it("gives the status once", () => {
    handOffStatus(status, 1_000);
    expect(takeHandedStatus(10_000, 2_000)).toBe(status);
    expect(takeHandedStatus(10_000, 2_000)).toBeNull();
  });

  it("gives nothing when the status is too old", () => {
    handOffStatus(status, 1_000);
    expect(takeHandedStatus(10_000, 12_000)).toBeNull();
  });

  it("gives nothing when there was no redirect", () => {
    expect(takeHandedStatus()).toBeNull();
  });
});
