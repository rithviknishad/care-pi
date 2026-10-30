import { useQueryClient } from "@tanstack/react-query";
import { LogOutIcon, MenuIcon, UserRoundIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { storeSession, useNetwork } from "@/api/queries";
import type { Session } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { strings } from "@/lib/strings";
import { NavList } from "./nav";
import { ThemeToggle } from "./theme-toggle";

function Brand() {
  return (
    <Link to="/" className="flex shrink-0 items-center gap-2 font-semibold tracking-tight">
      <img src="/favicon.svg" alt="" className="size-6" width={24} height={24} />
      <span className="hidden sm:inline">{strings.app.name}</span>
    </Link>
  );
}

// The name that avahi announces now (it can differ from the configured name on a collision).
function DeviceName({ session }: { session: Session | null }) {
  const network = useNetwork(Boolean(session));
  const name = session ? (network.data?.mdns.advertised ?? network.data?.mdns.configured) : window.location.hostname;
  return (
    <span
      className="min-w-0 flex-1 truncate text-sm text-soft-foreground sm:max-w-[22ch]"
      title={strings.app.deviceName}
      data-testid="device-name"
    >
      {name ?? <Skeleton className="inline-block h-4 w-[16ch] align-middle" />}
    </span>
  );
}

function AccountMenu() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const logout = async () => {
    try {
      await call(api.DELETE("/session"));
    } finally {
      // A full page load clears each value in memory: the CSRF token and the query cache.
      storeSession(qc, null);
      window.location.replace("/login");
    }
  };
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={strings.nav.account}>
          <UserRoundIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{strings.nav.account}</DropdownMenuLabel>
          <DropdownMenuItem onClick={() => navigate("/security")}>
            <UserRoundIcon />
            {strings.nav.security}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => void logout()}>
            <LogOutIcon />
            {strings.nav.logout}
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function TopBar({ session, menu }: { session: Session | null; menu?: ReactNode }) {
  return (
    <header className="sticky top-0 z-30 border-b bg-background/95 backdrop-blur print:hidden">
      <a
        href="#main"
        className="sr-only rounded bg-background px-3 py-2 focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50"
      >
        {strings.app.skipToContent}
      </a>
      <div className="mx-auto flex h-14 max-w-7xl items-center gap-2 px-3 sm:gap-3 sm:px-4">
        {menu}
        <Brand />
        <DeviceName session={session} />
        {/* From 640 px: the width of the longest state with a session ("Setup runs"), so a state change moves nothing.
            On a phone the name gets the space. The buttons stay at the right edge. */}
        <div className="shrink-0 text-xs sm:w-[calc(10ch+1rem)]">
          {session ? (
            <Badge variant={session.setup_state === "ready" ? "success" : "info"} size="sm" data-testid="setup-state">
              {strings.setupState[session.setup_state] ?? session.setup_state}
            </Badge>
          ) : null}
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-1">
          <ThemeToggle />
          {session ? <AccountMenu /> : null}
        </div>
      </div>
    </header>
  );
}

export function MobileNav({ session }: { session: Session }) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="ghost" size="icon-sm" className="lg:hidden" aria-label={strings.nav.openMenu}>
          <MenuIcon />
        </Button>
      </SheetTrigger>
      <SheetContent side="left" size="sm" dismissible>
        <SheetHeader>
          <SheetTitle>{strings.nav.menu}</SheetTitle>
        </SheetHeader>
        <SheetBody>
          <NavList state={session.setup_state} onNavigate={() => setOpen(false)} />
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}
