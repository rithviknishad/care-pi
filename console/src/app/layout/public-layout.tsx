import type { ReactNode } from "react";
import { Outlet } from "react-router";
import { FocusBackdrop } from "@/components/v2/focus";
import { cn } from "@/lib/utils";
import { PublicBar } from "./top-bar";

// Claim, login, recover, and the public certificate page: the v2 Focus layout (console/design.md), no navigation.
// A page of the router brings its own FocusCard (with the main landmark). Other content (a problem, "not found")
// gets the focus card surface here.
export function PublicLayout({ children, wide = false }: { children?: ReactNode; wide?: boolean }) {
  return (
    <FocusBackdrop top={<PublicBar />}>
      {children ? (
        <main
          id="main"
          className={cn("mx-auto w-full flex-1 px-gutter pt-4 pb-12 sm:pt-10", wide ? "max-w-3xl" : "max-w-[30rem]")}
        >
          <div className="rounded-stage border border-line bg-surface-card p-6 shadow-lift sm:p-8">{children}</div>
        </main>
      ) : (
        <Outlet />
      )}
    </FocusBackdrop>
  );
}
