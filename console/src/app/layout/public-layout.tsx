import type { ReactNode } from "react";
import { Outlet } from "react-router";
import { FocusBackdrop } from "@/components/v2/focus";
import { cn } from "@/lib/utils";
import { PublicBar } from "./top-bar";

// Claim, login, recover, and the public certificate page: the v2 Focus layout (console/design.md), no navigation.
// A page of the router brings its own card. Other content (a problem, "not found") gets the focus card surface.
// wide (the certificate page) has the band only behind the bar until the page moves to FocusCard.
export function PublicLayout({ children, wide = false }: { children?: ReactNode; wide?: boolean }) {
  return (
    <FocusBackdrop top={<PublicBar />} band={wide ? "bar" : "tall"}>
      <main
        id="main"
        className={cn("mx-auto w-full flex-1 px-gutter pt-4 pb-12 sm:pt-10", wide ? "max-w-5xl" : "max-w-lg")}
      >
        {children ? (
          <div className="rounded-stage border border-line bg-surface-card p-6 shadow-lift sm:p-8">{children}</div>
        ) : (
          <Outlet />
        )}
      </main>
    </FocusBackdrop>
  );
}
