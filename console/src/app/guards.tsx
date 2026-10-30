import type { QueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Link, Navigate, Outlet, ScrollRestoration, useLocation, useRouteError } from "react-router";
import { toast } from "sonner";
import { onUnauthenticated } from "@/api/client";
import { qk, storeSession, usePublicStatus, useSession } from "@/api/queries";
import type { Session } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { homeFor } from "@/features/setup/wizard";
import { strings } from "@/lib/strings";
import { AppShell, ShellSkeleton } from "./layout/app-shell";
import { PublicLayout } from "./layout/public-layout";
import { loginPath } from "./session";

// The routes that need the setup state ready.
const READY_ONLY = ["/dashboard", "/settings", "/backups", "/updates", "/plugs"];
// The setup routes that stay open after the state becomes ready.
const AFTER_SETUP = ["/setup/progress", "/setup/done"];

export function RootLayout({ queryClient }: { queryClient: QueryClient }) {
  useEffect(() => {
    onUnauthenticated(() => {
      const had = queryClient.getQueryData<Session | null>(qk.session);
      storeSession(queryClient, null);
      if (had) toast.warning(strings.app.sessionEnded, { id: "session-ended" });
    });
  }, [queryClient]);
  return (
    <>
      <Outlet />
      <ScrollRestoration />
    </>
  );
}

export function HomeRedirect() {
  const session = useSession();
  const pub = usePublicStatus(session.data === null);
  if (session.isPending) return <ShellSkeleton />;
  if (session.data) return <Navigate to={homeFor(session.data.setup_state, true)} replace />;
  if (session.isError)
    return (
      <PublicLayout>
        <ProblemAlert error={session.error} />
      </PublicLayout>
    );
  if (pub.isPending) return <ShellSkeleton />;
  if (pub.isError)
    return (
      <PublicLayout>
        <ProblemAlert error={pub.error} />
      </PublicLayout>
    );
  return <Navigate to={homeFor(null, pub.data.setup_state !== "unclaimed")} replace />;
}

export function RequireSession() {
  const session = useSession();
  const location = useLocation();
  if (session.isPending) return <ShellSkeleton />;
  if (session.isError) {
    return (
      <PublicLayout>
        <ProblemAlert error={session.error} />
      </PublicLayout>
    );
  }
  if (!session.data) return <Navigate to={loginPath(location)} replace />;
  const state = session.data.setup_state;
  const path = location.pathname;
  if (state !== "ready" && READY_ONLY.some((p) => path.startsWith(p))) return <Navigate to="/setup" replace />;
  if (state === "ready" && path.startsWith("/setup") && !AFTER_SETUP.some((p) => path.startsWith(p))) {
    return <Navigate to="/dashboard" replace />;
  }
  return (
    <AppShell session={session.data}>
      <Outlet />
    </AppShell>
  );
}

// The certificate page is public (staff devices have no session). With a session it has the full shell.
export function SessionOptionalShell() {
  const session = useSession();
  if (session.isPending) return <ShellSkeleton />;
  if (session.data) {
    return (
      <AppShell session={session.data}>
        <Outlet />
      </AppShell>
    );
  }
  return (
    <PublicLayout wide>
      <Outlet />
    </PublicLayout>
  );
}

export function NotFoundPage() {
  return (
    <PublicLayout>
      <div className="flex flex-col items-start gap-4">
        <h1 className="text-xl font-semibold">{strings.app.notFound}</h1>
        <Button asChild variant="outline">
          <Link to="/">{strings.app.goHome}</Link>
        </Button>
      </div>
    </PublicLayout>
  );
}

export function CrashPage() {
  const error = useRouteError();
  if (import.meta.env.DEV) console.error(error);
  return (
    <PublicLayout>
      <div className="flex flex-col items-start gap-4">
        <h1 className="text-xl font-semibold">{strings.app.crash}</h1>
        <Button type="button" onClick={() => window.location.reload()}>
          {strings.app.reload}
        </Button>
      </div>
    </PublicLayout>
  );
}
