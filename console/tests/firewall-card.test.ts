import { describe, expect, it } from "vitest";
import { secondsLeft } from "@/features/security/firewall-logic";

describe("firewall rollback countdown", () => {
  it("counts the seconds until the rollback", () => {
    const now = Date.parse("2026-11-02T10:00:00Z");
    expect(secondsLeft({ pending_until: null }, now)).toBeNull();
    expect(secondsLeft(undefined, now)).toBeNull();
    expect(secondsLeft({ pending_until: "2026-11-02T10:01:30Z" }, now)).toBe(90);
    expect(secondsLeft({ pending_until: "2026-11-02T10:00:00.400Z" }, now)).toBe(1);
    expect(secondsLeft({ pending_until: "2026-11-02T09:59:00Z" }, now)).toBe(0);
  });
});
