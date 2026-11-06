import type { ReactNode } from "react";
import { useMaintenance, useNetwork } from "@/api/queries";
import type { Session, SetupStateName } from "@/api/types";
import { Skeleton } from "@/components/ui/skeleton";
import { AppShell as Shell } from "@/components/v2/app-shell";
import { CopyHeaderProvider } from "@/components/v2/copy-provider";
import { StatusChip } from "@/components/v2/status-chip";
import type { Tone } from "@/components/v2/tone";
import { strings } from "@/lib/strings";
import { ClockBanner } from "./clock-banner";
import { MaintenanceBanner } from "./maintenance-banner";
import { navItems } from "./nav";
import { ThemeToggle } from "./theme-toggle";
import { AccountMenu } from "./top-bar";

const STATE_TONE: Record<SetupStateName, Tone> = {
  unclaimed: "neutral",
  claimed: "info",
  setup: "info",
  applying: "info",
  ready: "ok",
};

// The shell of each page with a session: the v2 AppShell (console/design.md, "Layouts"). Ready: the rail and the
// bottom bar. Before ready: minimal (the setup wizard has the left column; a Menu button opens the navigation).
export function AppShell({ session, children }: { session: Session; children: ReactNode }) {
  const network = useNetwork(true);
  const maintenance = useMaintenance(true);
  const active = Boolean(maintenance.data?.maintenance?.active);
  // S52: the banner "Clock not trusted" has the same slot; the maintenance banner wins it.
  const clock = Boolean(maintenance.data?.clock && !maintenance.data.clock.trusted && !active);
  // The name that avahi announces now (it can differ from the configured name on a collision).
  const name = network.data
    ? (network.data.mdns.advertised ?? network.data.mdns.configured)
    : network.isError
      ? window.location.hostname
      : undefined;
  const state = session.setup_state;
  return (
    <CopyHeaderProvider value={{ device: name ?? window.location.hostname, version: null }}>
      <Shell
        nav={navItems(state)}
        navMode={state === "ready" ? "full" : "minimal"}
        deviceName={name}
        status={
          <StatusChip tone={STATE_TONE[state] ?? "neutral"} live={state === "applying"} width={10} testId="setup-state">
            {strings.setupState[state] ?? state}
          </StatusChip>
        }
        banner={active ? <MaintenanceBanner enabled /> : clock ? <ClockBanner enabled /> : null}
        bannerCompact={active ? <MaintenanceBanner enabled compact /> : clock ? <ClockBanner enabled compact /> : null}
        actions={
          <>
            <ThemeToggle />
            <AccountMenu />
          </>
        }
        labels={{
          main: strings.nav.main,
          menu: strings.nav.menu,
          openMenu: strings.nav.openMenu,
          skipToContent: strings.app.skipToContent,
          device: strings.app.deviceName,
        }}
      >
        {children}
      </Shell>
    </CopyHeaderProvider>
  );
}

// The frame of the shell while the console reads the session. It has the size of the ready shell (the common case):
// the top bar, the rail from 1024 px, and the bottom bar on a phone.
export function ShellSkeleton() {
  return (
    <div className="flex min-h-dvh flex-col bg-surface-page" aria-busy="true">
      <header className="h-topbar border-b border-line" />
      <div className="flex flex-1">
        <aside className="hidden w-rail shrink-0 border-r border-line px-3 py-4 lg:block">
          <div className="flex flex-col gap-1">
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <Skeleton key={i} className="h-10" />
            ))}
          </div>
        </aside>
        <main className="min-w-0 flex-1 px-gutter pt-6 lg:pt-8">
          <div className="mx-auto w-full max-w-6xl">
            <Skeleton className="mb-6 h-32 rounded-stage" />
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
              {[0, 1, 2, 3].map((i) => (
                <Skeleton key={i} className="h-32 rounded-card" />
              ))}
            </div>
          </div>
        </main>
      </div>
      <div className="fixed inset-x-0 bottom-0 h-bottombar border-t border-line bg-surface-card lg:hidden" />
    </div>
  );
}
