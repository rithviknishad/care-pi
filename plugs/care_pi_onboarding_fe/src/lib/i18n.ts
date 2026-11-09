import { getI18n } from "react-i18next";

import en from "@/locale/en.json";

export const NAMESPACE = "care_pi_onboarding_fe";
// Each string is under one top-level key. care_fe loads <origin of meta.url>/locale/<lang>.json into this
// namespace; on care-pi that origin is CARE itself, so a flat key could collide with a key of care_fe.
export const KEY_PREFIX = "cpo";

/**
 * Adds the English strings to the i18n instance of the host (react-i18next is shared).
 * The plug does not rely on the host fetching its locale file: care_fe fetches it from the
 * ORIGIN of meta.url, and care-pi serves the bundle on the CARE origin under /plugs/.
 */
export function registerStrings(): void {
  const i18n = getI18n();
  if (
    !i18n ||
    i18n.exists(`${KEY_PREFIX}.title`, { ns: NAMESPACE, lng: "en" })
  ) {
    return;
  }
  // deep merge, keep what is there: a translation that the host loaded wins.
  i18n.addResourceBundle("en", NAMESPACE, en, true, false);
}
