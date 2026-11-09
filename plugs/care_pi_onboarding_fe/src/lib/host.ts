import { createContext, useContext } from "react";
import type { Context } from "react";

interface HostUser {
  username?: string;
  is_superuser?: boolean;
}

declare global {
  interface Window {
    CARE_API_URL: string;
    AuthUserContext?: Context<{ user?: HostUser } | null>;
    __CARE_PLUGIN_RUNTIME__?: { meta?: Record<string, Record<string, any>> };
  }
}

// Outside care_fe (the dev harness) there is no host context.
const NO_HOST = createContext<{ user?: HostUser } | null>(null);

/**
 * The signed-in user of care_fe, read from the context that the host puts on window.
 * React is shared, so the plug reads the same context object without a request.
 */
export function useHostUser(): HostUser | undefined {
  const ctx = useContext(window.AuthUserContext ?? NO_HOST);
  return ctx?.user;
}

/** The meta of this plug in plug_config (frozen by the host). */
export function plugConfig(): Record<string, any> {
  return (
    window.__CARE_PLUGIN_RUNTIME__?.meta?.care_pi_onboarding_fe?.config ?? {}
  );
}
