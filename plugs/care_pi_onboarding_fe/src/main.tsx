// The dev harness (index.html): the wizard outside care_fe, for `npm run dev` and the Playwright test.
// It is not part of the federation build (vite.config.ts builds src/index.tsx).
import i18next from "i18next";
import { createRoot } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";

import "@/style/index.css";
import OnboardingPage from "@/components/onboarding/onboarding-page";
import { registerStrings } from "@/lib/i18n";

window.CARE_API_URL = window.location.origin;
if (!localStorage.getItem("care_access_token")) {
  localStorage.setItem("care_access_token", "harness-token");
}

void i18next.use(initReactI18next).init({ lng: "en", resources: {} });
registerStrings();

createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18next}>
    <OnboardingPage />
  </I18nextProvider>,
);
