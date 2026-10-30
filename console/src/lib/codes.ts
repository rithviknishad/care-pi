// Claim codes, recovery codes, and the password strength hint.

const CROCKFORD = /[^0-9A-HJKMNP-TV-Z]/g;

function crockford(text: string): string {
  return text.toUpperCase().replace(/[IL]/g, "1").replace(/O/g, "0").replace(CROCKFORD, "");
}

// "4f7k q2mx-9pda" -> "4F7K-Q2MX-9PDA". The agent ignores case, spaces, and hyphens.
export function formatClaimCode(input: string): string {
  const raw = crockford(input).slice(0, 12);
  return raw.match(/.{1,4}/g)?.join("-") ?? "";
}

export function isCompleteClaimCode(input: string): boolean {
  return crockford(input).length === 12;
}

// The form of the claim code that the installer prints, for example 4F7K-Q2MX-9PDA.
const CLAIM_CODE_SHAPE = /^[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$/;

// True when an operator types or pastes a claim code into the field of another claim method.
export function looksLikeClaimCode(input: string): boolean {
  return CLAIM_CODE_SHAPE.test(input.trim());
}

// "7kq2m x9pda" -> "7KQ2M-X9PDA"
export function formatRecoveryCode(input: string): string {
  const raw = crockford(input).slice(0, 10);
  return raw.match(/.{1,5}/g)?.join("-") ?? "";
}

export function isCompleteRecoveryCode(input: string): boolean {
  return crockford(input).length === 10;
}

export const PASSWORD_MIN = 12;
export const PASSWORD_MAX = 128;

export type PasswordStrength = 0 | 1 | 2 | 3 | 4;

// A hint only. The agent applies the rules (12 to 128 characters, not a common password).
export function passwordStrength(password: string): PasswordStrength {
  if (password.length < PASSWORD_MIN) return 0;
  let classes = 0;
  if (/[a-z]/.test(password)) classes++;
  if (/[A-Z]/.test(password)) classes++;
  if (/[0-9]/.test(password)) classes++;
  if (/[^a-zA-Z0-9]/.test(password)) classes++;
  const unique = new Set(password).size;
  if (unique < 5) return 1;
  let score = 1;
  if (password.length >= 16) score++;
  if (password.length >= 20 || classes >= 3) score++;
  if (password.length >= 24 && classes >= 2) score++;
  return Math.min(4, score) as PasswordStrength;
}

export function recoveryCodesText(codes: string[], host: string, generatedAt: string): string {
  return [
    "care-pi recovery codes",
    `Device: ${host}`,
    `Made: ${generatedAt}`,
    "",
    "Each code works one time. Keep this file in a safe place.",
    "Use a code on the login page: Use a recovery code.",
    "",
    ...codes.map((c, i) => `${i + 1}. ${c}`),
    "",
  ].join("\n");
}
