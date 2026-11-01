// The recovery key: 24 words of the BIP-39 English list (docs/api/README.md section 13.3).
// The console checks the words before it sends them. The agent checks them again (and the checksum).

import { BIP39_ENGLISH } from "./bip39-english";

export const KEY_WORDS = 24;
export const QR_PREFIX = "CARE-PI-RK1:";

const INDEX = new Map(BIP39_ENGLISH.map((w, i) => [w, i]));
// The first 4 letters of each word are unique (BIP-39). The agent accepts them.
const BY_PREFIX = new Map(BIP39_ENGLISH.map((w) => [w.slice(0, 4), w]));

// expandWord gives the word of the list for a typed word: the full word, or its first 4 letters (or more).
// null: the word is not in the list.
export function expandWord(typed: string): string | null {
  const w = typed.trim().toLowerCase();
  if (!/^[a-z]{3,12}$/.test(w)) return null;
  if (INDEX.has(w)) return w;
  if (w.length < 4) return null;
  const full = BY_PREFIX.get(w.slice(0, 4));
  return full && full.startsWith(w) ? full : null;
}

// splitKeyText reads the words from text that the operator typed or pasted: words with spaces, commas, or lines;
// the QR payload (CARE-PI-RK1:WORD WORD ...); or the .txt file of the agent (numbered words in columns).
export function splitKeyText(text: string): string[] {
  let t = text.trim();
  const qr = t.toUpperCase().indexOf(QR_PREFIX);
  if (qr >= 0) t = t.slice(qr + QR_PREFIX.length);
  // The .txt file has numbered words in 3 columns: " 1 abandon    9 absurd    17 acoustic".
  const numbered = [...t.matchAll(/(?:^|\s)(\d{1,2})[.)]?\s+([A-Za-z]{3,12})\b/g)];
  if (numbered.length >= KEY_WORDS) {
    const byNumber = new Map<number, string>();
    for (const m of numbered) {
      const n = Number(m[1]);
      if (n >= 1 && n <= KEY_WORDS && !byNumber.has(n)) byNumber.set(n, (m[2] ?? "").toLowerCase());
    }
    if (byNumber.size === KEY_WORDS) return Array.from({ length: KEY_WORDS }, (_, i) => byNumber.get(i + 1) ?? "");
  }
  return t
    .split(/[^A-Za-z]+/)
    .filter(Boolean)
    .map((w) => w.toLowerCase());
}

export interface KeyCheck {
  // The words as the list has them; null for a word that is not in the list.
  words: (string | null)[];
  count: number;
  // The typed words that are not in the list.
  unknown: string[];
  // 24 words, each in the list.
  complete: boolean;
}

export function checkKeyText(text: string): KeyCheck {
  const typed = splitKeyText(text);
  const words = typed.map(expandWord);
  const unknown = typed.filter((_, i) => words[i] === null);
  return {
    words,
    count: typed.length,
    unknown,
    complete: typed.length === KEY_WORDS && unknown.length === 0,
  };
}

// checksumOk checks the 8-bit checksum of 24 words (256 bits of entropy). It needs SHA-256 of Web Crypto
// (a secure context). null: the browser cannot check it; the agent checks it.
export async function checksumOk(words: readonly string[]): Promise<boolean | null> {
  if (words.length !== KEY_WORDS) return false;
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return null;
  let bits = "";
  for (const w of words) {
    const i = INDEX.get(w);
    if (i === undefined) return false;
    bits += i.toString(2).padStart(11, "0");
  }
  const entropy = new Uint8Array(32);
  for (let i = 0; i < 32; i++) entropy[i] = parseInt(bits.slice(i * 8, i * 8 + 8), 2);
  const hash = new Uint8Array(await subtle.digest("SHA-256", entropy));
  return (hash[0] ?? -1) === parseInt(bits.slice(256, 264), 2);
}

// The QR payload of the contract: CARE-PI-RK1: and the 24 words in capital letters, with one space between them.
export function qrPayload(words: readonly string[]): string {
  return QR_PREFIX + words.map((w) => w.toUpperCase()).join(" ");
}
