import { describe, expect, it, vi } from "vitest";
import {
  base64Url,
  caNoticeVisible,
  certPageUrl,
  fingerprintQr,
  rebindingCheck,
  servedNames,
  signalLevel,
  sortNetworks,
  validAuthKey,
  validHint,
  validateWifi,
} from "@/features/connectivity/connectivity-logic";
import type { WifiNetwork } from "@/api/types";

describe("connectivity logic", () => {
  it("checks the hint of the public name like the agent", () => {
    for (const ok of ["care", "sunrise-clinic", "a1", "x"]) expect(validHint(ok), ok).toBe(true);
    for (const bad of ["", "-care", "care-", "Care", "care clinic", "a".repeat(64)])
      expect(validHint(bad), bad).toBe(false);
  });

  it("accepts only a Tailscale auth key or nothing", () => {
    expect(validAuthKey("")).toBe(true);
    expect(validAuthKey("tskey-auth-abc")).toBe(true);
    expect(validAuthKey("abc")).toBe(false);
  });

  it("validates the Wi-Fi form", () => {
    expect(validateWifi({ ssid: "Sunrise-Staff", psk: "a wifi passphrase", country: "IN", failover: true })).toEqual(
      {},
    );
    expect(validateWifi({ ssid: "", psk: "short", country: "in", failover: true })).toEqual({
      ssid: "invalid",
      psk: "invalid",
      country: "invalid",
    });
    // 32 bytes at most: 11 three-byte characters are 33 bytes.
    expect(validateWifi({ ssid: "चिकि".repeat(3), psk: "12345678", country: "", failover: false }).ssid).toBe(
      "invalid",
    );
    expect(validateWifi({ ssid: "x", psk: "pässword1", country: "", failover: false }).psk).toBe("invalid");
  });

  it("orders the scan: secured first, then by signal", () => {
    const n = (ssid: string, signal: number, security = "WPA2"): WifiNetwork => ({
      ssid,
      signal,
      security,
      channel: 1,
      in_use: false,
    });
    expect(sortNetworks([n("open", 90, ""), n("weak", 20), n("strong", 80)]).map((x) => x.ssid)).toEqual([
      "strong",
      "weak",
      "open",
    ]);
    expect([signalLevel(10), signalLevel(30), signalLevel(50), signalLevel(90)]).toEqual([0, 1, 2, 3]);
  });

  it("gives each name of Care its mode", () => {
    const names = servedNames({
      hostnames: ["sunrise-k3x9.care-pi.rithviknishad.dev", "care.local", "care.tail1234.ts.net"],
      tls: {
        local_ca: { enabled: true, fingerprint_sha256: null, not_after: null },
        public: { enabled: true, hostname: "sunrise-k3x9.care-pi.rithviknishad.dev" },
        tailscale: { enabled: true, hostname: "care.tail1234.ts.net" },
      },
    });
    expect(names.map((x) => x.mode)).toEqual(["public", "local", "tailscale"]);
  });

  it("puts the DER root in the fragment as base64url without padding (services/dns/API.md 8.2)", () => {
    const der = new Uint8Array([0xfb, 0xff, 0xfe, 0x01]);
    expect(base64Url(der)).toBe("-__-AQ");
    expect(certPageUrl("https://care-pi.rithviknishad.dev/cert", der)).toBe(
      "https://care-pi.rithviknishad.dev/cert#-__-AQ",
    );
    expect(fingerprintQr("AB:CD")).toBe("SHA-256 AB:CD");
  });

  it("shows the warning of a changed root until the browser dismisses this fingerprint", () => {
    expect(caNoticeVisible("2026-11-01T10:00:00Z", "abc", null)).toBe(true);
    expect(caNoticeVisible("2026-11-01T10:00:00Z", "abc", "abc")).toBe(false);
    expect(caNoticeVisible("2026-11-01T10:00:00Z", "def", "abc")).toBe(true);
    expect(caNoticeVisible(null, "abc", null)).toBe(false);
  });

  it("reports a blocked public name when the fetch fails", async () => {
    const ok = vi.fn().mockResolvedValue(new Response(null, { status: 200 }));
    expect(await rebindingCheck("https://x.care-pi.rithviknishad.dev/_care-pi/health", ok)).toBe("ok");
    expect(ok).toHaveBeenCalledWith(
      "https://x.care-pi.rithviknishad.dev/_care-pi/health",
      expect.objectContaining({ mode: "no-cors" }),
    );
    const fail = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    expect(await rebindingCheck("https://x/", fail)).toBe("blocked");
  });
});
