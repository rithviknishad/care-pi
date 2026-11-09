import { useTranslation as useI18nNextTranslation } from "react-i18next";

import { KEY_PREFIX, NAMESPACE, registerStrings } from "@/lib/i18n";

/**
 * react-i18next's useTranslation with the namespace of this plug (care_pi_onboarding_fe) and its key prefix.
 */
export const useTranslation = () => {
  registerStrings();
  return useI18nNextTranslation(NAMESPACE, { keyPrefix: KEY_PREFIX });
};
