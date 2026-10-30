import type { ReactNode } from "react";
import { Outlet } from "react-router";
import { cn } from "@/lib/utils";
import { TopBar } from "./top-bar";

// Claim, login, recover, and the public certificate page: no navigation, one column.
export function PublicLayout({ children, wide = false }: { children?: ReactNode; wide?: boolean }) {
  return (
    <div className="flex min-h-dvh flex-col">
      <TopBar session={null} />
      <main id="main" className={cn("mx-auto w-full flex-1 px-4 py-8 sm:py-12", wide ? "max-w-5xl" : "max-w-lg")}>
        {children ?? <Outlet />}
      </main>
    </div>
  );
}
