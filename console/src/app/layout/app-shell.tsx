import type { ReactNode } from "react";
import type { Session } from "@/api/types";
import { Skeleton } from "@/components/ui/skeleton";
import { NavList } from "./nav";
import { MobileNav, TopBar } from "./top-bar";

export function AppShell({ session, children }: { session: Session; children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col">
      <TopBar session={session} menu={<MobileNav session={session} />} />
      <div className="mx-auto flex w-full max-w-7xl flex-1">
        <aside className="sticky top-14 hidden h-[calc(100dvh-3.5rem)] w-56 shrink-0 overflow-y-auto border-r p-3 lg:block print:hidden">
          <NavList state={session.setup_state} />
        </aside>
        <main id="main" className="min-w-0 flex-1 px-4 py-6 sm:px-6 lg:px-8">
          {children}
        </main>
      </div>
    </div>
  );
}

// The frame of the shell while the console reads the session. It has the size of the shell.
export function ShellSkeleton() {
  return (
    <div className="flex min-h-dvh flex-col" aria-busy="true">
      <header className="h-14 border-b" />
      <div className="mx-auto flex w-full max-w-7xl flex-1">
        <aside className="hidden w-56 shrink-0 border-r p-3 lg:block">
          <div className="flex flex-col gap-2">
            {[0, 1, 2, 3, 4].map((i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        </aside>
        <main className="min-w-0 flex-1 px-4 py-6 sm:px-6 lg:px-8">
          <Skeleton className="mb-6 h-8 w-48" />
          <Skeleton className="h-40" />
        </main>
      </div>
    </div>
  );
}
