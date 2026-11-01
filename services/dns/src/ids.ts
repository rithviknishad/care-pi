// Clinic ids and credentials.

import { randomBytes, sha256Hex, toBase64Url } from "./encoding";

export const BASE32 = "abcdefghijklmnopqrstuvwxyz234567";
export const SLUG_MAX = 20;
export const SUFFIX_LEN = 4;

// The id is "<slug>-<4 base32>". It is a valid DNS label: [a-z0-9-], no leading or trailing hyphen,
// no "--" (so it never looks like an IDNA "xn--" label), max. 25 characters.
export const ID_PATTERN = /^[a-z0-9](?:[a-z0-9]|-(?!-)){0,19}-[a-z2-7]{4}$/;

export function slugify(hint: string): string {
  const slug = hint
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, SLUG_MAX)
    .replace(/-+$/g, "");
  return slug === "" ? "clinic" : slug;
}

export function randomBase32(n: number): string {
  // 256 is a multiple of 32, so "byte & 31" is uniform.
  return Array.from(randomBytes(n), (b) => BASE32[b & 31]).join("");
}

export function newClinicId(hint: string): string {
  return `${slugify(hint)}-${randomBase32(SUFFIX_LEN)}`;
}

export function isClinicId(text: string): boolean {
  return ID_PATTERN.test(text);
}

export interface AcmeSecret {
  username: string;
  password: string;
  passwordHash: string;
}

// The password is 256 random bits (43 base64url characters). The database keeps only its SHA-256:
// a slow hash (argon2, bcrypt) adds nothing for a random 256-bit secret and costs CPU time in a Worker.
export async function newAcmeSecret(): Promise<AcmeSecret> {
  const password = toBase64Url(randomBytes(32));
  return { username: crypto.randomUUID(), password, passwordHash: await sha256Hex(password) };
}

export function newNonce(): string {
  return toBase64Url(randomBytes(32));
}
