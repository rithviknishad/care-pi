import { AlertTriangleIcon, CheckCircle2Icon, InfoIcon, XCircleIcon } from "lucide-react";
import { Link } from "react-router";
import type { BackupBanner, BackupsHealth } from "@/api/types";
import { Alert, AlertAction, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatRelative } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { bannerAction, bannerText, healthSummary, severityVariant } from "./health";

const b = strings.banners;

// The time of the report of the agent: the ages use the clock of the device, not the clock of the browser.
function reportTime(health: BackupsHealth | null | undefined): number {
  const t = Date.parse(health?.generated_at ?? "");
  return Number.isNaN(t) ? Date.now() : t;
}
// The row has one fixed height: below 640 px the text can use 2 lines. A skeleton of the same height shows first.
const ROW = "h-[4.75rem] sm:h-14";

function SeverityIcon({ severity }: { severity: BackupBanner["severity"] | "ok" }) {
  if (severity === "error") return <XCircleIcon aria-hidden="true" />;
  if (severity === "warning") return <AlertTriangleIcon aria-hidden="true" />;
  if (severity === "info") return <InfoIcon aria-hidden="true" />;
  return <CheckCircle2Icon aria-hidden="true" />;
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
  if (loading) return <Skeleton className={cn(ROW, "w-full rounded-lg")} data-testid="backup-health-skeleton" />;
  const summary = healthSummary(health);
  if (unavailable || !summary) {
    return (
      <Alert layout="horizontal" className={ROW} data-testid="backup-health" data-state="unavailable">
        <InfoIcon aria-hidden="true" />
        <AlertDescription className="line-clamp-2 sm:truncate">{b.notAvailable}</AlertDescription>
      </Alert>
    );
  }
  const top = summary.top;
  if (!top) {
    return (
      <Alert variant="success" layout="horizontal" className={ROW} data-testid="backup-health" data-state="ok">
        <CheckCircle2Icon aria-hidden="true" />
        <AlertDescription className="line-clamp-2 sm:truncate">
          {b.ok} {lastCopy ? fmt(b.okDetail, { age: formatRelative(lastCopy, reportTime(health)) }) : b.okNoCopy}
        </AlertDescription>
        {linkToPage ? (
          <AlertAction>
            <Button asChild variant="link" size="sm" className="px-0">
              <Link to="/backups">{b.open}</Link>
            </Button>
          </AlertAction>
        ) : null}
      </Alert>
    );
  }
  const action = linkToPage ? { to: "/backups", label: b.open } : bannerAction(top);
  return (
    <Alert
      variant={severityVariant(top.severity)}
      layout="horizontal"
      className={ROW}
      data-testid="backup-health"
      data-state={summary.state}
      data-code={top.code}
    >
      <SeverityIcon severity={top.severity} />
      <AlertDescription className="flex min-w-0 items-center gap-2">
        <span className="line-clamp-2 min-w-0 sm:truncate" data-testid="backup-health-text">
          {bannerText(top, reportTime(health))}
        </span>
        {summary.more > 0 && onMore ? (
          <button
            type="button"
            onClick={onMore}
            aria-expanded={moreOpen}
            className="shrink-0 rounded-sm outline-none focus-visible:outline-2 focus-visible:outline-indigo-500"
            data-testid="backup-health-more"
          >
            <Badge size="sm" variant="neutral">
              {fmt(b.more, { count: summary.more })}
            </Badge>
          </button>
        ) : summary.more > 0 ? (
          <Badge size="sm" variant="neutral" className="shrink-0" data-testid="backup-health-more">
            {fmt(b.more, { count: summary.more })}
          </Badge>
        ) : null}
      </AlertDescription>
      {action ? (
        <AlertAction>
          <Button asChild variant="link" size="sm" className="px-0">
            <Link to={action.to}>{action.label}</Link>
          </Button>
        </AlertAction>
      ) : null}
    </Alert>
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
          <li key={`${banner.code}-${banner.destination_id ?? ""}-${i}`}>
            <Alert variant={severityVariant(banner.severity)} layout="horizontal">
              <SeverityIcon severity={banner.severity} />
              <AlertDescription>{bannerText(banner, reportTime(health))}</AlertDescription>
              {action ? (
                <AlertAction>
                  <Button asChild variant="link" size="sm" className="px-0">
                    <Link to={action.to}>{action.label}</Link>
                  </Button>
                </AlertAction>
              ) : null}
            </Alert>
          </li>
        );
      })}
    </ul>
  );
}
