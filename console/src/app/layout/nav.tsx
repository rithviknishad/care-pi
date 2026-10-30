import {
  DatabaseBackupIcon,
  FileBadgeIcon,
  LayoutDashboardIcon,
  LifeBuoyIcon,
  LockIcon,
  PuzzleIcon,
  RefreshCwIcon,
  ScrollTextIcon,
  SlidersHorizontalIcon,
  WandSparklesIcon,
  type LucideIcon,
} from "lucide-react";
import { NavLink } from "react-router";
import type { SetupStateName } from "@/api/types";
import { strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  later?: boolean;
}

function navItems(state: SetupStateName): NavItem[] {
  if (state !== "ready") {
    return [
      { to: "/setup", label: strings.nav.setup, icon: WandSparklesIcon },
      { to: "/certificate", label: strings.nav.certificate, icon: FileBadgeIcon },
      { to: "/logs", label: strings.nav.logs, icon: ScrollTextIcon },
      { to: "/security", label: strings.nav.security, icon: LockIcon },
      { to: "/support", label: strings.nav.support, icon: LifeBuoyIcon },
    ];
  }
  return [
    { to: "/dashboard", label: strings.nav.dashboard, icon: LayoutDashboardIcon },
    { to: "/settings", label: strings.nav.settings, icon: SlidersHorizontalIcon },
    { to: "/logs", label: strings.nav.logs, icon: ScrollTextIcon },
    { to: "/certificate", label: strings.nav.certificate, icon: FileBadgeIcon },
    { to: "/security", label: strings.nav.security, icon: LockIcon },
    { to: "/support", label: strings.nav.support, icon: LifeBuoyIcon },
    { to: "/backups", label: strings.nav.backups, icon: DatabaseBackupIcon, later: true },
    { to: "/updates", label: strings.nav.updates, icon: RefreshCwIcon, later: true },
    { to: "/plugs", label: strings.nav.plugs, icon: PuzzleIcon, later: true },
  ];
}

export function NavList({ state, onNavigate }: { state: SetupStateName; onNavigate?: () => void }) {
  return (
    <nav aria-label={strings.nav.main}>
      <ul className="flex flex-col gap-0.5">
        {navItems(state).map((item) => (
          <li key={item.to}>
            <NavLink
              to={item.to}
              onClick={onNavigate}
              className={({ isActive }) =>
                cn(
                  "flex h-11 items-center gap-3 rounded-md px-3 text-sm outline-none focus-visible:outline-2 focus-visible:outline-indigo-500 lg:h-9",
                  isActive
                    ? "bg-primary-100 font-semibold text-primary-900 dark:bg-primary-400/15 dark:text-primary-300"
                    : "text-muted-foreground hover:bg-muted-background hover:text-foreground",
                  item.later && "text-soft-foreground",
                )
              }
            >
              <item.icon className="size-4 shrink-0" aria-hidden="true" />
              <span className="truncate">{item.label}</span>
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}
