import { ClipboardList } from "lucide-react";
import { lazy, Suspense } from "react";

import DashboardRedirect from "@/components/onboarding/dashboard-redirect";
import { registerStrings } from "@/lib/i18n";

registerStrings();

const OnboardingPage = lazy(
  () => import("@/components/onboarding/onboarding-page"),
);

const page = () => (
  <Suspense fallback={null}>
    <OnboardingPage />
  </Suspense>
);

/**
 * The contract with care_fe (src/pluginTypes.ts PluginManifest): routes, adminNavItems, overrides.
 * The UserDashboard override needs REACT_MFE_REGISTERED_COMPONENTS=UserDashboard in the care_fe build.
 */
const manifest = {
  plugin: "care_pi_onboarding_fe",
  routes: {
    "/onboarding": page,
    "/admin/onboarding": page,
  },
  adminNavItems: [
    {
      name: "Clinic setup",
      url: "/admin/onboarding",
      icon: <ClipboardList className="size-4" />,
    },
  ],
  overrides: [
    {
      component: "UserDashboard",
      replacement: DashboardRedirect,
      description:
        "care_pi_onboarding_fe: opens the clinic setup for a new CARE",
    },
  ],
};

export default manifest;
