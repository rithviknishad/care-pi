// The health of the backups: the banners of GET /backups/health (docs/api/README.md section 13.10) and the states of
// the destinations, mapped to the console texts and the careui variants. Pure functions, so the unit tests can check them.

import type { BackupBanner, BackupsHealth, BackupsOverview, DestinationState, LocalBackupState } from "@/api/types";
import { formatDuration } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";

export type AlertVariant = "destructive" | "warning" | "info" | "success";
export type BadgeVariant = "success" | "warning" | "destructive" | "info" | "neutral";

const SEVERITY_ORDER: Record<string, number> = { error: 0, warning: 1, info: 2 };

export function severityVariant(severity: BackupBanner["severity"] | "ok"): AlertVariant {
  if (severity === "error") return "destructive";
  if (severity === "warning") return "warning";
  if (severity === "info") return "info";
  return "success";
}

// The console text of a banner. An unknown code (a later version) shows the message of the agent.
// "for 1 d 2 h" is shorter than a date, so the row of a phone shows it (2 lines at most).
export function bannerText(b: BackupBanner, now: number = Date.now()): string {
  const destination = b.destination_id ?? "";
  if (b.code === "destination-unreachable" && !b.since) {
    return fmt(strings.banners.unreachableNoSince, { destination });
  }
  const text = strings.banners.codes[b.code];
  if (!text) return b.message;
  const age = b.since ? formatDuration(Math.max(60, Math.round((now - Date.parse(b.since)) / 60_000) * 60)) : "";
  return fmt(text, { destination, age });
}

// The page that fixes a banner, and the text of its link. null: the Backups page has no better place for it.
export function bannerAction(b: BackupBanner): { to: string; label: string } | null {
  const label = strings.banners.actions[b.code];
  if (!label) return null;
  if (b.code === "no-destination") return { to: "/backups?add=1", label };
  if (b.code === "no-recovery-key" || b.code === "recovery-key-unconfirmed" || b.code === "local-repository-legacy-key")
    return { to: "/backups#recovery-key", label };
  if (b.code === "backups-paused") return { to: "/settings?tab=backups", label };
  if (b.destination_id) return { to: `/backups#destination-${b.destination_id}`, label };
  return { to: "/logs?unit=backup", label };
}

// The worst banner first (the agent sorts them; the console sorts again for an older agent).
export function sortBanners(banners: readonly BackupBanner[]): BackupBanner[] {
  return [...banners].sort((a, b) => (SEVERITY_ORDER[a.severity] ?? 3) - (SEVERITY_ORDER[b.severity] ?? 3));
}

export interface HealthSummary {
  state: BackupsHealth["state"];
  top: BackupBanner | null;
  more: number;
  banners: BackupBanner[];
}

export function healthSummary(health: BackupsHealth | null | undefined): HealthSummary | null {
  if (!health) return null;
  const banners = sortBanners(health.banners);
  return { state: health.state, top: banners[0] ?? null, more: Math.max(0, banners.length - 1), banners };
}

export function destinationBadge(state: DestinationState["state"]): { text: string; variant: BadgeVariant } {
  const text = strings.backupsPage.destStates[state] ?? state;
  switch (state) {
    case "ok":
      return { text, variant: "success" };
    case "degraded":
      return { text, variant: "warning" };
    case "error":
      return { text, variant: "destructive" };
    case "pending":
      return { text, variant: "info" };
    default:
      return { text, variant: "neutral" };
  }
}

export function localBadge(state: LocalBackupState["state"]): { text: string; variant: BadgeVariant } {
  const text = strings.backupsPage.localStates[state] ?? state;
  switch (state) {
    case "ok":
      return { text, variant: "success" };
    case "degraded":
      return { text, variant: "warning" };
    case "error":
      return { text, variant: "destructive" };
    case "none":
      return { text, variant: "info" };
    default:
      return { text, variant: "neutral" };
  }
}

// The newest successful database copy at a destination, or null.
export function lastOffsiteCopy(overview: Pick<BackupsOverview, "destinations"> | null | undefined): string | null {
  let best: string | null = null;
  for (const d of overview?.destinations ?? []) {
    const t = d.last_db_snapshot_at;
    if (t && (!best || Date.parse(t) > Date.parse(best))) best = t;
  }
  return best;
}
