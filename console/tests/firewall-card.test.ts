import { describe, expect, it } from "vitest";
import {
  MAX_ALLOW_PORTS,
  allowPortKey,
  builtinPort,
  checkDraft,
  secondsLeft,
  type AllowDraft,
  type AllowPort,
} from "@/features/security/firewall-logic";

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

// S64: security.firewall.allow_ports. The console checks a new entry as the agent does (validate.go, builtinPort).
describe("firewall extra ports", () => {
  const ctx = { ssh: true, tailscale: false };
  const draft = (d: Partial<AllowDraft>): AllowDraft => ({ port: "", proto: "tcp", iface: "", comment: "", ...d });

  it("knows the ports of care-pi", () => {
    expect(builtinPort(443, "tcp", ctx)).toBe("care");
    expect(builtinPort(8443, "tcp", ctx)).toBe("care");
    expect(builtinPort(443, "udp", ctx)).toBeNull();
    expect(builtinPort(5353, "udp", ctx)).toBe("mdns");
    expect(builtinPort(22, "tcp", ctx)).toBe("ssh");
    expect(builtinPort(22, "tcp", { ssh: false, tailscale: false })).toBeNull();
    expect(builtinPort(41641, "udp", ctx)).toBeNull();
    expect(builtinPort(41641, "udp", { ssh: true, tailscale: true })).toBe("tailscale");
  });

  it("makes an entry from the form", () => {
    expect(checkDraft(draft({ port: " 9100 ", iface: "tailscale0 ", comment: " node exporter " }), [], ctx)).toEqual({
      entry: { port: 9100, proto: "tcp", iface: "tailscale0", comment: "node exporter" },
    });
    expect(checkDraft(draft({ port: "51820", proto: "udp" }), [], ctx)).toEqual({
      entry: { port: 51820, proto: "udp" },
    });
  });

  it("refuses a bad entry", () => {
    for (const port of ["", "0", "65536", "9.5", "x", "-1"]) {
      expect(checkDraft(draft({ port }), [], ctx).errors?.port).toBe("range");
    }
    expect(checkDraft(draft({ port: "443" }), [], ctx).errors).toEqual({ port: "builtin", builtin: "care" });
    expect(checkDraft(draft({ port: "9100", iface: 'eth0"; drop' }), [], ctx).errors?.iface).toBe("pattern");
    expect(checkDraft(draft({ port: "9100", comment: "x".repeat(61) }), [], ctx).errors?.comment).toBe("length");
    const list: AllowPort[] = [{ port: 9100, iface: "eth0" }];
    expect(checkDraft(draft({ port: "9100", iface: "eth0" }), list, ctx).errors?.port).toBe("duplicate");
    expect(checkDraft(draft({ port: "9100", iface: "wlan0" }), list, ctx).entry).toBeDefined();
    expect(checkDraft(draft({ port: "9100", proto: "udp", iface: "eth0" }), list, ctx).entry).toBeDefined();
    const full = Array.from({ length: MAX_ALLOW_PORTS }, (_, i): AllowPort => ({ port: 10000 + i }));
    expect(checkDraft(draft({ port: "9100" }), full, ctx).errors?.list).toBe("full");
  });

  it("keys an entry by port, protocol, and interface", () => {
    expect(allowPortKey({ port: 9100 })).toBe(allowPortKey({ port: 9100, proto: "tcp" }));
    expect(allowPortKey({ port: 9100 })).not.toBe(allowPortKey({ port: 9100, iface: "eth0" }));
  });
});
