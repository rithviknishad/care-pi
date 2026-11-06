import {
  DatabaseBackupIcon,
  FileBadgeIcon,
  HistoryIcon,
  LayoutDashboardIcon,
  LifeBuoyIcon,
  LockIcon,
  PuzzleIcon,
  RefreshCwIcon,
  ScrollTextIcon,
  SlidersHorizontalIcon,
  WandSparklesIcon,
} from "lucide-react";
import type { SetupStateName } from "@/api/types";
import type { ShellNavItem } from "@/components/v2/app-shell";
import { strings } from "@/lib/strings";

// The navigation of the shell. "primary" items are in the bottom bar of a phone (4 at most); the others are in
// "More". Before the state is ready the shell is minimal (the wizard has the left column): a Menu button opens these.
export function navItems(state: SetupStateName): ShellNavItem[] {
  if (state !== "ready") {
    return [
      { to: "/setup", label: strings.nav.setup, icon: WandSparklesIcon, primary: true },
      { to: "/certificate", label: strings.nav.certificate, icon: FileBadgeIcon, primary: true },
      { to: "/logs", label: strings.nav.logs, icon: ScrollTextIcon, primary: true },
      { to: "/security", label: strings.nav.security, icon: LockIcon },
      { to: "/support", label: strings.nav.support, icon: LifeBuoyIcon, primary: true },
    ];
  }
  return [
    { to: "/dashboard", label: strings.nav.dashboard, icon: LayoutDashboardIcon, primary: true },
    { to: "/backups", label: strings.nav.backups, icon: DatabaseBackupIcon, primary: true },
    { to: "/restore", label: strings.nav.restore, icon: HistoryIcon },
    { to: "/settings", label: strings.nav.settings, icon: SlidersHorizontalIcon, primary: true },
    { to: "/logs", label: strings.nav.logs, icon: ScrollTextIcon },
    { to: "/certificate", label: strings.nav.certificate, icon: FileBadgeIcon },
    { to: "/security", label: strings.nav.security, icon: LockIcon },
    { to: "/support", label: strings.nav.support, icon: LifeBuoyIcon },
    { to: "/updates", label: strings.nav.updates, icon: RefreshCwIcon, primary: true },
    { to: "/plugs", label: strings.nav.plugs, icon: PuzzleIcon },
  ];
}
