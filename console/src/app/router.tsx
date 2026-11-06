import type { QueryClient } from "@tanstack/react-query";
import type { ComponentType } from "react";
import { createBrowserRouter } from "react-router";
import {
  CrashPage,
  HomeRedirect,
  NotFoundPage,
  RequireSession,
  RootLayout,
  SessionOptionalBare,
  SessionOptionalShell,
} from "./guards";
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
          // S67: the page of a job. With a session: the shell and the full stream. Without: the public progress.
          element: <SessionOptionalBare />,
          children: [{ path: "jobs/:id", lazy: page(() => import("@/features/jobs/job-page")) }],
        },
        {
          element: <RequireSession />,
          children: [
            { path: "setup", lazy: page(() => import("@/features/setup/setup-wizard")) },
            { path: "setup/progress", lazy: page(() => import("@/features/setup/progress-page")) },
            { path: "setup/done", lazy: page(() => import("@/features/setup/done-page")) },
            { path: "setup/restore", lazy: page(() => import("@/features/setup/restore-dr-page")) },
            { path: "setup/:page", lazy: page(() => import("@/features/setup/setup-wizard")) },
            { path: "dashboard", lazy: page(() => import("@/features/dashboard/dashboard-page")) },
            { path: "logs", lazy: page(() => import("@/features/logs/logs-page")) },
            { path: "jobs", lazy: page(() => import("@/features/jobs/jobs-page")) },
            { path: "settings", lazy: page(() => import("@/features/settings/settings-page")) },
            { path: "security", lazy: page(() => import("@/features/security/security-page")) },
            { path: "support", lazy: page(() => import("@/features/support/support-page")) },
            { path: "backups", lazy: page(() => import("@/features/backups/backups-page")) },
            // The PITR offer of an update links to /restore?time=…: the page fills in the time.
            { path: "restore", lazy: page(() => import("@/features/backups/restore-page")) },
            { path: "updates", lazy: page(() => import("@/features/updates/updates-page")) },
            { path: "plugs", lazy: page(() => import("@/features/plugs/plugs-page")) },
          ],
        },
        { path: "*", element: <NotFoundPage /> },
      ],
    },
  ]);
}
