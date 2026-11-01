import { describe, expect, it } from "vitest";
import { BIP39_ENGLISH } from "@/lib/bip39-english";
import { checkKeyText, checksumOk, expandWord, qrPayload, splitKeyText } from "@/lib/recovery-key";

// The BIP-39 test vector of 256 bits of zeros: 23 times "abandon", then "art".
const ZERO_KEY = [...Array(23).fill("abandon"), "art"] as string[];
// The test vector 0x7f... (256 bits): "legal winner thank year wave sausage worth useful legal winner thank year wave
// sausage worth useful legal winner thank year wave sausage worth title".
const VECTOR_7F =
  "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth title".split(
    " ",
  );

describe("the BIP-39 word list", () => {
  it("has 2048 words with unique first 4 letters", () => {
    expect(BIP39_ENGLISH).toHaveLength(2048);
    expect(BIP39_ENGLISH[0]).toBe("abandon");
    expect(BIP39_ENGLISH[2047]).toBe("zoo");
    expect(new Set(BIP39_ENGLISH.map((w) => w.slice(0, 4))).size).toBe(2048);
  });
});

describe("recovery key words", () => {
  it("accepts a word of the list or its first 4 letters (or more)", () => {
    expect(expandWord("abandon")).toBe("abandon");
    expect(expandWord("aban")).toBe("abandon");
    expect(expandWord("ABANDO")).toBe("abandon");
    expect(expandWord("act")).toBe("act");
    expect(expandWord("acce")).toBe("access");
  });

  it("refuses a word that is not in the list", () => {
    expect(expandWord("abx")).toBeNull();
    expect(expandWord("abandons")).toBeNull();
    expect(expandWord("aba")).toBeNull();
    expect(expandWord("zzzz")).toBeNull();
    expect(expandWord("12ab")).toBeNull();
  });

  it("reads the words from spaces, commas, and lines", () => {
    expect(splitKeyText(" Abandon, ability\nable  about ")).toEqual(["abandon", "ability", "able", "about"]);
  });

  it("reads the QR payload", () => {
    const qr = qrPayload(ZERO_KEY);
    expect(qr).toMatch(/^CARE-PI-RK1:ABANDON( [A-Z]+){23}$/);
    expect(splitKeyText(qr)).toEqual(ZERO_KEY);
  });

  it("reads the .txt file of the agent in the order of the numbers", () => {
    const words = BIP39_ENGLISH.slice(0, 24);
    const rows = Array.from({ length: 8 }, (_, r) =>
      [0, 1, 2]
        .map((c) => `${String(c * 8 + r + 1).padStart(2)} ${words[c * 8 + r]?.padEnd(9)}`)
        .join(" ")
        .trimEnd(),
    );
    const file = [
      "care-pi recovery key",
      "Clinic: Sunrise Clinic",
      "Key id: 1a2b3c4d",
      "Made: 2026-10-30",
      "",
      ...rows,
      "",
      "Keep this sheet in a safe place, away from the device.",
    ].join("\n");
    expect(splitKeyText(file)).toEqual(words);
  });

  it("counts the words and finds the unknown words", () => {
    expect(checkKeyText("")).toMatchObject({ count: 0, complete: false, unknown: [] });
    const short = checkKeyText(ZERO_KEY.slice(0, 20).join(" "));
    expect(short).toMatchObject({ count: 20, complete: false });
    const bad = checkKeyText([...ZERO_KEY.slice(0, 23), "artx"].join(" "));
    expect(bad).toMatchObject({ count: 24, complete: false, unknown: ["artx"] });
    const ok = checkKeyText(ZERO_KEY.map((w) => w.slice(0, 4)).join(" "));
    expect(ok.complete).toBe(true);
    expect(ok.words).toEqual(ZERO_KEY);
  });

  it("checks the checksum of the BIP-39 test vectors", async () => {
    await expect(checksumOk(ZERO_KEY)).resolves.toBe(true);
    await expect(checksumOk(VECTOR_7F)).resolves.toBe(true);
    await expect(checksumOk([...ZERO_KEY.slice(0, 23), "abandon"])).resolves.toBe(false);
    await expect(checksumOk(ZERO_KEY.slice(0, 12))).resolves.toBe(false);
  });
});
