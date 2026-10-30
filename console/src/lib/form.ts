import { PASSWORD_MAX, PASSWORD_MIN } from "./codes";
import { strings } from "./strings";

// The ids of the help text and the error line of a FormField, for aria-describedby.
export function describedBy(id: string, help: boolean, error: unknown): string | undefined {
  const ids = [help ? `${id}-help` : null, error ? `${id}-error` : null].filter(Boolean);
  return ids.length > 0 ? ids.join(" ") : undefined;
}

export function validateNewPassword(password: string, confirm: string): { password?: string; confirm?: string } {
  const out: { password?: string; confirm?: string } = {};
  if (password.length < PASSWORD_MIN) out.password = strings.password.tooShort;
  else if (password.length > PASSWORD_MAX) out.password = strings.password.tooLong;
  if (confirm !== password) out.confirm = strings.password.mismatch;
  return out;
}
