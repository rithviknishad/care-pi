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
} from "lucide-react";
import type { ShellNavItem } from "../app-shell";

export const readyNav: ShellNavItem[] = [
  { to: "/dashboard", label: "Home", icon: LayoutDashboardIcon, primary: true },
  { to: "/backups", label: "Backups", icon: DatabaseBackupIcon, primary: true },
  { to: "/restore", label: "Restore", icon: HistoryIcon },
  { to: "/updates", label: "Updates", icon: RefreshCwIcon, primary: true },
  { to: "/settings", label: "Settings", icon: SlidersHorizontalIcon, primary: true },
  { to: "/logs", label: "Logs", icon: ScrollTextIcon },
  { to: "/certificate", label: "Certificate", icon: FileBadgeIcon },
  { to: "/security", label: "Security", icon: LockIcon },
  { to: "/support", label: "Support", icon: LifeBuoyIcon },
  { to: "/plugs", label: "Solution and plugs", icon: PuzzleIcon },
];
