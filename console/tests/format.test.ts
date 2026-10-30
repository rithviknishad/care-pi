import { describe, expect, it } from "vitest";
import {
  fingerprintLines,
  formatAbout,
  formatBytes,
  formatDateTime,
  formatDuration,
  formatElapsed,
  formatRelative,
  percent,
  shortDigest,
  shortSha,
} from "@/lib/format";

describe("format helpers", () => {
  it("formats bytes with binary units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512 * 1024 ** 2)).toBe("512 MiB");
    expect(formatBytes(2122317824)).toBe("2 GiB");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(null)).toBe("—");
  });
  it("formats durations as the measured help texts", () => {
    expect(formatDuration(200)).toBe("3 min 20 s");
    expect(formatDuration(45)).toBe("45 s");
    expect(formatDuration(180)).toBe("3 min");
    expect(formatDuration(3900)).toBe("1 h 5 min");
    expect(formatDuration(90000)).toBe("1 d 1 h");
    expect(formatAbout(200)).toBe("about 3 min 20 s");
  });
  it("formats an elapsed time with a fixed width", () => {
    expect(formatElapsed(5_000)).toBe("00:05");
    expect(formatElapsed(200_000)).toBe("03:20");
    expect(formatElapsed(3_723_000)).toBe("1:02:03");
  });
  it("shortens digests, shas, and fingerprints", () => {
    expect(
      shortDigest("ghcr.io/ohcnetwork/care@sha256:5e0c3b2a1f9d8e7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c"),
    ).toBe("sha256:5e0c3b2a1f9d");
    expect(shortDigest(null)).toBe("—");
    expect(shortSha("83b007b72cd0e5b1f2a3c4d5e6f708192a3b4c5d", 7)).toBe("83b007b");
    const lines = fingerprintLines("3f1d6c0e5a2b9e4d7c8f1a0b2c3d4e5f60718293a4b5c6d7e8f9011223344556");
    expect(lines).toHaveLength(4);
    expect(lines[0]).toBe("3F:1D:6C:0E:5A:2B:9E:4D");
  });
  it("formats times with a fixed width", () => {
    expect(formatDateTime("2026-10-30T10:15:00Z", "Asia/Kolkata")).toBe("2026-10-30 15:45:00");
    expect(formatDateTime(null)).toBe("—");
    const now = Date.parse("2026-10-30T10:15:00Z");
    expect(formatRelative("2026-10-30T10:10:00Z", now)).toBe("5 min ago");
    expect(formatRelative("2026-10-30T10:18:20Z", now)).toBe("in 3 min 20 s");
  });
  it("computes a bounded percentage", () => {
    expect(percent(50, 200)).toBe(25);
    expect(percent(300, 200)).toBe(100);
    expect(percent(null, 200)).toBeNull();
    expect(percent(1, 0)).toBeNull();
  });
});
