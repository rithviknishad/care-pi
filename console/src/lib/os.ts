import type { ClientOs } from "@/api/types";

export const CLIENT_OSES: ClientOs[] = ["ios", "android", "windows", "macos", "linux", "chromeos"];

// The OS of this device from the user agent. iPadOS 13+ reports "Macintosh" with touch support.
export function detectOs(userAgent: string, maxTouchPoints = 0): ClientOs {
  const ua = userAgent.toLowerCase();
  if (/iphone|ipad|ipod/.test(ua)) return "ios";
  if (ua.includes("macintosh") && maxTouchPoints > 1) return "ios";
  if (ua.includes("android")) return "android";
  if (ua.includes("cros")) return "chromeos";
  if (ua.includes("windows")) return "windows";
  if (ua.includes("mac os x") || ua.includes("macintosh")) return "macos";
  return "linux";
}

export function isClientOs(value: string | null): value is ClientOs {
  return value !== null && (CLIENT_OSES as string[]).includes(value);
}
