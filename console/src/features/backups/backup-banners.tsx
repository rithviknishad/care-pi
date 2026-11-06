import { AlertTriangleIcon, CheckCircle2Icon, InfoIcon, XCircleIcon } from "lucide-react";
import { Link } from "react-router";
import type { BackupBanner, BackupsHealth } from "@/api/types";
import { StatusChip } from "@/components/v2/status-chip";
import { Skeleton } from "@/components/ui/skeleton";
import { formatAgo } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { bannerAction, bannerText, healthSummary } from "./health";

const b = strings.banners;

// The time of the report of the agent: the ages use the clock of the device, not the clock of the browser.
function reportTime(health: BackupsHealth | null | undefined): number {
  const t = Date.parse(health?.generated_at ?? "");
  return Number.isNaN(t) ? Date.now() : t;
}
// The row has one fixed height: below 640 px the text can use 2 lines. A skeleton of the same height shows first.
const ROW =
  "flex h-[4.75rem] min-w-0 items-center gap-3 rounded-card border px-4 type-body sm:h-14 [&>svg]:size-5 [&>svg]:shrink-0";

const SURFACE: Record<string, string> = {
  error: "border-danger-border bg-danger-bg text-danger-fg",
  warning: "border-warn-border bg-warn-bg text-warn-fg",
  info: "border-info-border bg-info-bg text-info-fg",
  ok: "border-ok-border bg-ok-bg text-ok-fg",
  neutral: "border-line bg-surface-tint text-ink-muted",
};

function SeverityIcon({ severity }: { severity: BackupBanner["severity"] | "ok" }) {
  if (severity === "error") return <XCircleIcon aria-hidden="true" />;
  if (severity === "warning") return <AlertTriangleIcon aria-hidden="true" />;
  if (severity === "info") return <InfoIcon aria-hidden="true" />;
  return <CheckCircle2Icon aria-hidden="true" />;
}

function RowLink({ to, label }: { to: string; label: string }) {
  return (
    <Link
      to={to}
      className="ml-auto shrink-0 type-small font-semibold underline underline-offset-4 outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {label}
    </Link>
  );
}

// The banner row of the backups (docs/api/README.md section 13.10, plans/02 section 7): one row with a fixed height
// on the dashboard and on the Backups page. It shows the worst banner and the count of the others, or "OK".
// The space stays the same before the data, with 0 banners, and with 5 banners (docs/console-ui.md section 5).
export function BackupHealthRow({
  health,
  lastCopy,
  loading,
  unavailable = false,
  linkToPage = true,
  onMore,
  moreOpen = false,
}: {
  health: BackupsHealth | null | undefined;
  lastCopy?: string | null | undefined;
  loading: boolean;
  // An agent of Phase 2 has no backups (Status.backups is null).
  unavailable?: boolean;
  // The dashboard links to the Backups page. The Backups page links to the fix of the banner.
  linkToPage?: boolean;
  // The Backups page: the count of the other banners is a button that shows the list below the row.
  onMore?: () => void;
  moreOpen?: boolean;
}) {
  if (loading)
    return <Skeleton className="h-[4.75rem] w-full rounded-card sm:h-14" data-testid="backup-health-skeleton" />;
  const summary = healthSummary(health);
  if (unavailable || !summary) {
    return (
      <div role="status" className={cn(ROW, SURFACE.neutral)} data-testid="backup-health" data-state="unavailable">
        <InfoIcon aria-hidden="true" />
        <span className="line-clamp-2 min-w-0 sm:truncate">{b.notAvailable}</span>
      </div>
    );
  }
  const top = summary.top;
  if (!top) {
    return (
      <div role="status" className={cn(ROW, SURFACE.ok)} data-testid="backup-health" data-state="ok">
        <CheckCircle2Icon aria-hidden="true" />
        <span className="line-clamp-2 min-w-0 sm:truncate">
          {b.ok} {lastCopy ? fmt(b.okDetail, { age: formatAgo(lastCopy, reportTime(health)) }) : b.okNoCopy}
        </span>
        {linkToPage ? <RowLink to="/backups" label={b.open} /> : null}
      </div>
    );
  }
  const action = linkToPage ? { to: "/backups", label: b.open } : bannerAction(top);
  return (
    <div
      role="status"
      className={cn(ROW, SURFACE[top.severity] ?? SURFACE.warning)}
      data-testid="backup-health"
      data-state={summary.state}
      data-code={top.code}
    >
      <SeverityIcon severity={top.severity} />
      <span className="flex min-w-0 flex-1 items-center gap-2">
        <span className="line-clamp-2 min-w-0 sm:truncate" data-testid="backup-health-text">
          {bannerText(top, reportTime(health))}
        </span>
        {summary.more > 0 && onMore ? (
          <button
            type="button"
            onClick={onMore}
            aria-expanded={moreOpen}
            className="shrink-0 rounded-full outline-none focus-visible:outline-2 focus-visible:outline-indigo-500"
            data-testid="backup-health-more"
          >
            <StatusChip tone="neutral" size="sm">
              {fmt(b.more, { count: summary.more })}
            </StatusChip>
          </button>
        ) : summary.more > 0 ? (
          <StatusChip tone="neutral" size="sm" className="shrink-0" testId="backup-health-more">
            {fmt(b.more, { count: summary.more })}
          </StatusChip>
        ) : null}
      </span>
      {action ? <RowLink to={action.to} label={action.label} /> : null}
    </div>
  );
}

// The list of each banner, below the row on the Backups page. It shows only when the operator asks for it.
export function BackupBannerList({ health }: { health: BackupsHealth | null | undefined }) {
  const summary = healthSummary(health);
  if (!summary) return null;
  return (
    <ul className="flex flex-col gap-2" data-testid="backup-banners">
      {summary.banners.map((banner, i) => {
        const action = bannerAction(banner);
        return (
          <li
            key={`${banner.code}-${banner.destination_id ?? ""}-${i}`}
            className={cn(ROW, "h-auto min-h-14 py-3", SURFACE[banner.severity] ?? SURFACE.warning)}
          >
            <SeverityIcon severity={banner.severity} />
            <span className="min-w-0 flex-1">{bannerText(banner, reportTime(health))}</span>
            {action ? <RowLink to={action.to} label={action.label} /> : null}
          </li>
        );
      })}
    </ul>
  );
}
