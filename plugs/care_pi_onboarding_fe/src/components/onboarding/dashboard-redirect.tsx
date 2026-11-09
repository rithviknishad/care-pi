import { navigate } from "raviger";
import { type ComponentType, useEffect, useState } from "react";

import { onboardingApi } from "@/lib/api";
import { plugConfig, useHostUser } from "@/lib/host";
import { handOffStatus } from "@/lib/status-handoff";

const SEEN = "care_pi_onboarding_redirected";

/**
 * Override of care_fe's UserDashboard (REACT_MFE_REGISTERED_COMPONENTS=UserDashboard).
 * For a superuser of a new CARE, it opens the wizard once in each browser session, until the
 * facility step is done or someone closes the wizard. Everyone else gets the normal dashboard
 * at once, with no request.
 */
export default function DashboardRedirect(
  props: Record<string, unknown> & { __base?: ComponentType<any> },
) {
  const { __base: Base, ...rest } = props;
  const user = useHostUser();
  const enabled =
    plugConfig().redirect_after_login !== false &&
    Boolean(user?.is_superuser) &&
    sessionStorage.getItem(SEEN) !== "1";
  const [checking, setChecking] = useState(enabled);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    onboardingApi
      .status(null, controller.signal)
      .then((status) => {
        if (status.wizard?.redirect) {
          sessionStorage.setItem(SEEN, "1");
          handOffStatus(status);
          navigate("/onboarding");
          return;
        }
        setChecking(false);
      })
      .catch(() => setChecking(false));
    return () => controller.abort();
  }, [enabled]);

  if (checking) return null;
  return Base ? <Base {...rest} /> : null;
}
