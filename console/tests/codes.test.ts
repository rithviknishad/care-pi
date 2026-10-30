import { describe, expect, it } from "vitest";
import {
  formatClaimCode,
  formatRecoveryCode,
  isCompleteClaimCode,
  isCompleteRecoveryCode,
  passwordStrength,
  recoveryCodesText,
} from "@/lib/codes";
import { detectOs } from "@/lib/os";
import { validateNewPassword } from "@/lib/form";

describe("codes", () => {
  it("formats a claim code as the operator types it", () => {
    expect(formatClaimCode("4f7k q2mx-9pda")).toBe("4F7K-Q2MX-9PDA");
    expect(formatClaimCode("4f7k")).toBe("4F7K");
    expect(isCompleteClaimCode("4F7K-Q2MX-9PDA")).toBe(true);
    expect(isCompleteClaimCode("4F7K-Q2MX")).toBe(false);
  });
  it("reads I and L as 1 and O as 0 (Crockford base32)", () => {
    expect(formatRecoveryCode("7kq2m xopdl")).toBe("7KQ2M-X0PD1");
    expect(isCompleteRecoveryCode("7KQ2M-X9PDA")).toBe(true);
  });
  it("makes the text file of the recovery codes", () => {
    const text = recoveryCodesText(["7KQ2M-X9PDA", "1B3C5-D7E9F"], "care.local:8443", "2026-10-30T10:00:00Z");
    expect(text).toContain("1. 7KQ2M-X9PDA");
    expect(text).toContain("care.local:8443");
  });
});

describe("passwords", () => {
  it("gives a hint of the strength", () => {
    expect(passwordStrength("short")).toBe(0);
    expect(passwordStrength("aaaaaaaaaaaaaaa")).toBe(1);
    expect(passwordStrength("correct horse battery staple")).toBeGreaterThanOrEqual(3);
  });
  it("checks the length and the confirmation", () => {
    expect(validateNewPassword("short", "short").password).toBeDefined();
    expect(validateNewPassword("a long console password", "other").confirm).toBeDefined();
    expect(validateNewPassword("a long console password", "a long console password")).toEqual({});
  });
});

describe("OS detection", () => {
  it("detects the OS of the device", () => {
    expect(detectOs("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)")).toBe("ios");
    expect(detectOs("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", 5)).toBe("ios");
    expect(detectOs("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", 0)).toBe("macos");
    expect(detectOs("Mozilla/5.0 (Linux; Android 15; Pixel 9)")).toBe("android");
    expect(detectOs("Mozilla/5.0 (Windows NT 10.0; Win64; x64)")).toBe("windows");
    expect(detectOs("Mozilla/5.0 (X11; CrOS x86_64 15000.0.0)")).toBe("chromeos");
    expect(detectOs("Mozilla/5.0 (X11; Linux x86_64)")).toBe("linux");
  });
});
