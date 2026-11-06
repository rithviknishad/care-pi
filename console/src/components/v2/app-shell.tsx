import { EllipsisIcon, MenuIcon, type LucideIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, NavLink } from "react-router";
import { Button } from "@/components/ui/button";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { InlineSkeleton } from "./inline-skeleton";
import { v2Strings } from "./strings";

export interface ShellNavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  /** In the bottom bar of a phone (4 at most). The others are in "More". */
  primary?: boolean;
}

export interface AppShellProps {
  nav: ShellNavItem[];
  /** full: the rail (from 1024 px) and the bottom bar (phones). minimal: neither; a Menu button opens the
   *  navigation (the setup wizard: one thing per screen, the Stage has the left column). */
  navMode?: "full" | "minimal";
  /** The name of the device (Geist Mono). undefined: a skeleton of the same width. */
  deviceName?: string | null;
  /** The state chip (StatusChip with a fixed width). */
  status?: ReactNode;
  /** The banner of the top bar (maintenance, clock). From 640 px it has a fixed slot in the middle of the bar. */
  banner?: ReactNode;
  /** The banner below 640 px: it takes the place of the device name and the state. */
  bannerCompact?: ReactNode;
  /** The buttons at the right of the top bar (the theme, the account). */
  actions?: ReactNode;
  /** The end of the rail (for example the agent version, in mono). */
  railFooter?: ReactNode;
  /** More items in the navigation sheet (for example "Log out"). */
  sheetFooter?: ReactNode;
  labels?: Partial<typeof v2Strings.shell>;
  children: ReactNode;
}

function Brand() {
  return (
    <Link
      to="/"
      className="flex shrink-0 items-center gap-2 rounded-md type-body font-semibold tracking-tight outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <img src="/favicon.svg" alt="" className="size-7" width={28} height={28} />
      <span className="sr-only sm:not-sr-only">{strings.app.name}</span>
    </Link>
  );
}

function NavRows({ items, onNavigate }: { items: ShellNavItem[]; onNavigate?: () => void }) {
  return (
    <ul className="flex flex-col gap-1">
      {items.map((item) => (
        <li key={item.to}>
          <NavLink
            to={item.to}
            onClick={onNavigate}
            className={({ isActive }) =>
              cn(
                "flex h-11 items-center gap-3 rounded-lg px-3 type-body outline-none lg:h-10 lg:type-small",
                "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500",
                "motion-ok:transition-colors motion-ok:duration-(--duration-fast)",
                isActive
                  ? "bg-nav-active-bg font-semibold text-nav-active-fg"
                  : "text-ink-muted hover:bg-surface-tint hover:text-ink",
              )
            }
          >
            {({ isActive }) => (
              <>
                <item.icon
                  className={cn("size-5 shrink-0 lg:size-4.5", isActive ? "text-primary-solid" : "text-ink-soft")}
                  aria-hidden="true"
                />
                <span className="truncate">{item.label}</span>
              </>
            )}
          </NavLink>
        </li>
      ))}
    </ul>
  );
}

function NavSheet({
  items,
  label,
  title,
  trigger,
  side,
  footer,
}: {
  items: ShellNavItem[];
  label: string;
  title: string;
  trigger: ReactNode;
  side: "left" | "bottom";
  footer?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent side={side} size="sm" dismissible>
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
        </SheetHeader>
        <SheetBody>
          <nav aria-label={label}>
            <NavRows items={items} onNavigate={() => setOpen(false)} />
          </nav>
          {footer ? <div className="mt-4 border-t border-line-soft pt-4">{footer}</div> : null}
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}

// The phone bar: up to 5 places. With more than 5 items it shows the primary ones (4 at most) and "More".
function BottomBar({
  items,
  labels,
  footer,
}: {
  items: ShellNavItem[];
  labels: typeof v2Strings.shell;
  footer?: ReactNode;
}) {
  const overflow = items.length > 5;
  const shown = overflow ? items.filter((i) => i.primary).slice(0, 4) : items;
  const cell =
    "flex h-full min-w-0 flex-1 flex-col items-center justify-center gap-1 text-[0.75rem] leading-4 font-medium outline-none focus-visible:outline-2 focus-visible:-outline-offset-4 focus-visible:outline-indigo-500";
  const pill =
    "grid h-8 w-14 place-items-center rounded-full motion-ok:transition-colors motion-ok:duration-(--duration-fast)";
  return (
    <nav
      aria-label={labels.main}
      className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-surface-card/95 pb-[env(safe-area-inset-bottom)] backdrop-blur lg:hidden print:hidden"
    >
      <ul className="mx-auto flex h-bottombar max-w-xl items-stretch px-1">
        {shown.map((item) => (
          <li key={item.to} className="flex min-w-0 flex-1">
            <NavLink
              to={item.to}
              className={({ isActive }) => cn(cell, isActive ? "text-nav-active-fg" : "text-ink-muted")}
            >
              {({ isActive }) => (
                <>
                  <span className={cn(pill, isActive && "bg-nav-active-bg")}>
                    <item.icon className={cn("size-5", isActive && "text-primary-solid")} aria-hidden="true" />
                  </span>
                  <span className="max-w-full truncate px-0.5">{item.label}</span>
                </>
              )}
            </NavLink>
          </li>
        ))}
        {overflow ? (
          <li className="flex min-w-0 flex-1">
            <NavSheet
              items={items}
              label={labels.main}
              title={labels.menu}
              side="bottom"
              footer={footer}
              trigger={
                <button type="button" className={cn(cell, "text-ink-muted")}>
                  <span className={pill}>
                    <EllipsisIcon className="size-5" aria-hidden="true" />
                  </span>
                  <span>{labels.more}</span>
                </button>
              }
            />
          </li>
        ) : null}
      </ul>
    </nav>
  );
}

// The app shell of v2: a top bar (the brand, the device name, the state, a banner slot of fixed height, and the
// actions), a left rail from 1024 px, and a bottom bar on phones. Each slot has a fixed size, so a banner, a
// state change, or a device name that comes later moves nothing.
export function AppShell({
  nav,
  navMode = "full",
  deviceName,
  status,
  banner,
  bannerCompact,
  actions,
  railFooter,
  sheetFooter,
  labels: labelOverrides,
  children,
}: AppShellProps) {
  const labels = { ...v2Strings.shell, ...labelOverrides };
  const full = navMode === "full";
  const hasCompact = Boolean(bannerCompact);
  return (
    <div className="flex min-h-dvh flex-col bg-surface-page">
      <a
        href="#main"
        className="sr-only rounded-md bg-surface-card px-3 py-2 type-small shadow-soft focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50"
      >
        {labels.skipToContent}
      </a>
      <header className="sticky top-0 z-30 h-topbar border-b border-line bg-surface-page/90 backdrop-blur print:hidden">
        <div className="flex h-full items-center gap-2 px-gutter sm:gap-3">
          {full ? null : (
            <NavSheet
              items={nav}
              label={labels.main}
              title={labels.menu}
              side="left"
              footer={sheetFooter}
              trigger={
                <Button variant="ghost" size="icon-sm" className="no-underline" aria-label={labels.openMenu}>
                  <MenuIcon />
                </Button>
              }
            />
          )}
          <Brand />
          <span className="hidden h-6 w-px bg-line sm:block" aria-hidden="true" />
          {/* Below 640 px the compact banner takes the place of the device name and the state. The name fills the
              slot (flex-1), so the state chip keeps its place when the name comes or changes. */}
          <div
            className={cn(
              "flex min-w-0 flex-1 items-center gap-2 sm:w-[30ch] sm:flex-none",
              hasCompact && "hidden sm:flex",
            )}
          >
            <span
              className="min-w-0 flex-1 truncate type-data text-ink-muted"
              title={labels.device}
              data-testid="device-name"
            >
              {deviceName === undefined ? <InlineSkeleton className="h-4 w-[14ch] align-middle" /> : (deviceName ?? "")}
            </span>
            {status}
          </div>
          {hasCompact ? (
            <div className="flex h-banner min-w-0 flex-1 items-center sm:hidden">{bannerCompact}</div>
          ) : null}
          {/* From 640 px: the banner slot. It is always there, so the actions at the right never move. */}
          <div className="hidden h-banner min-w-0 flex-1 basis-0 items-center justify-center sm:flex">{banner}</div>
          <div className="ml-auto flex shrink-0 items-center gap-1">{actions}</div>
        </div>
      </header>
      <div className="flex flex-1">
        {full ? (
          <aside className="sticky top-topbar hidden h-[calc(100dvh-var(--topbar-height))] w-rail shrink-0 flex-col border-r border-line px-3 py-4 lg:flex print:hidden">
            <nav aria-label={labels.main} className="flex-1 overflow-y-auto">
              <NavRows items={nav} />
            </nav>
            {railFooter ? (
              <div className="border-t border-line-soft px-3 pt-3 type-data text-ink-soft">{railFooter}</div>
            ) : null}
          </aside>
        ) : null}
        <main
          id="main"
          className={cn(
            "min-w-0 flex-1 px-gutter pt-6 lg:pt-8",
            full ? "pb-[calc(var(--bottombar-height)+env(safe-area-inset-bottom)+2rem)] lg:pb-12" : "pb-12",
          )}
        >
          <div className="mx-auto w-full max-w-6xl">{children}</div>
        </main>
      </div>
      {full ? <BottomBar items={nav} labels={labels} footer={sheetFooter} /> : null}
    </div>
  );
}
