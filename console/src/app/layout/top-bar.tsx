import { useQueryClient } from "@tanstack/react-query";
import { LogOutIcon, UserRoundIcon } from "lucide-react";
import { Link, useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { storeSession } from "@/api/queries";
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
import { FocusBar } from "@/components/v2/focus";
import { strings } from "@/lib/strings";
import { ThemeToggle } from "./theme-toggle";

export function AccountMenu() {
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
        <Button variant="ghost" size="icon-sm" className="no-underline" aria-label={strings.nav.account}>
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

// The bar of the public pages (claim, login, recover, the public certificate page): on the gradient band, each text
// on a scrim pill. The device name is the host of the address (no session: no API read).
export function PublicBar() {
  return (
    <FocusBar
      start={
        <Link
          to="/"
          className="flex h-9 min-w-0 items-center gap-2 rounded-full bg-scrim-full pr-3 pl-1.5 outline-none focus-visible:ring-3 focus-visible:ring-white/60"
        >
          <img src="/favicon.svg" alt="" className="size-6" width={24} height={24} />
          <span className="font-semibold">{strings.app.name}</span>
          <span
            className="truncate type-data text-on-gradient-muted"
            title={strings.app.deviceName}
            data-testid="device-name"
          >
            {window.location.hostname}
          </span>
        </Link>
      }
      end={
        <span className="rounded-full bg-scrim-full">
          <ThemeToggle className="text-on-gradient hover:bg-white/10" />
        </span>
      }
    />
  );
}
