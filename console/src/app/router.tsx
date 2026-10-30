import type { QueryClient } from "@tanstack/react-query";
import type { ComponentType } from "react";
import { createBrowserRouter } from "react-router";
import { CrashPage, HomeRedirect, NotFoundPage, RequireSession, RootLayout, SessionOptionalShell } from "./guards";
import { ShellSkeleton } from "./layout/app-shell";
import { PublicLayout } from "./layout/public-layout";

// Each page is a separate chunk. The entry chunk has the shell and the API client only.
function page(load: () => Promise<{ default: ComponentType }>) {
  return async () => ({ Component: (await load()).default });
}

export function createAppRouter(queryClient: QueryClient) {
  return createBrowserRouter([
    {
      element: <RootLayout queryClient={queryClient} />,
      errorElement: <CrashPage />,
      hydrateFallbackElement: <ShellSkeleton />,
      children: [
        { index: true, element: <HomeRedirect /> },
        {
          element: <PublicLayout />,
          children: [
            { path: "claim", lazy: page(() => import("@/features/claim/claim-page")) },
            { path: "login", lazy: page(() => import("@/features/login/login-page")) },
            { path: "recover", lazy: page(() => import("@/features/login/recover-page")) },
          ],
        },
        {
          element: <SessionOptionalShell />,
          children: [{ path: "certificate", lazy: page(() => import("@/features/certificate/certificate-page")) }],
        },
        {
          element: <RequireSession />,
          children: [
            { path: "setup", lazy: page(() => import("@/features/setup/setup-wizard")) },
            { path: "setup/progress", lazy: page(() => import("@/features/setup/progress-page")) },
            { path: "setup/done", lazy: page(() => import("@/features/setup/done-page")) },
            { path: "setup/:page", lazy: page(() => import("@/features/setup/setup-wizard")) },
            { path: "dashboard", lazy: page(() => import("@/features/dashboard/dashboard-page")) },
            { path: "logs", lazy: page(() => import("@/features/logs/logs-page")) },
            { path: "settings", lazy: page(() => import("@/features/settings/settings-page")) },
            { path: "security", lazy: page(() => import("@/features/security/security-page")) },
            { path: "support", lazy: page(() => import("@/features/support/support-page")) },
            { path: "backups", lazy: page(() => import("@/features/unavailable/backups-page")) },
            { path: "updates", lazy: page(() => import("@/features/unavailable/updates-page")) },
            { path: "plugs", lazy: page(() => import("@/features/unavailable/plugs-page")) },
          ],
        },
        { path: "*", element: <NotFoundPage /> },
      ],
    },
  ]);
}
