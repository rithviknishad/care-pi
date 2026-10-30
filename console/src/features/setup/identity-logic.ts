import { DISPLAY_NAME_RE, HOSTNAME_RE } from "@/lib/schema";
import { strings } from "@/lib/strings";

export interface IdentityValue {
  display_name: string;
  hostname: string;
}

export function validateIdentity(v: IdentityValue): Partial<Record<keyof IdentityValue, string>> {
  const errors: Partial<Record<keyof IdentityValue, string>> = {};
  const name = v.display_name.trim();
  if (name.length < 1 || name.length > 80 || !DISPLAY_NAME_RE.test(name))
    errors.display_name = strings.identity.displayNameHelp;
  if (!HOSTNAME_RE.test(v.hostname)) errors.hostname = strings.identity.hostnameInvalid;
  return errors;
}
