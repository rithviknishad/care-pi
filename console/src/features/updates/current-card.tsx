import { AlertTriangleIcon } from "lucide-react";
import type { Status, UpdatesOverview } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { SectionCard } from "@/components/app/section-card";
import { ShortId } from "@/components/app/short-id";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { fmt, strings } from "@/lib/strings";
import { trackBadge, versionText } from "./update-text";
import { formatZoned } from "./window";

const u = strings.updates;

// The running version: the solution, the lock version and its track, the identities of Care and of the web app,
// the agent, and the release state (withdrawn). The care image and the care_fe sha come from the components of
// GET /updates, else from GET /status (GET /updates has no components without a target).
export function CurrentCard({
  o,
  status,
  tz,
}: {
  o: UpdatesOverview | undefined;
  status: Status | undefined;
  tz: string;
}) {
  const loading = !o;
  const track = trackBadge(o?.track);
  const care =
    o?.components.find((c) => c.id === "care")?.current ?? status?.images.find((i) => i.name === "care")?.pull;
  const careFe = o?.components.find((c) => c.id === "care_fe")?.current ?? status?.care_fe_sha;
  const current = o?.current ?? null;
  const withdrawn = Boolean(current?.withdrawn);
  const release = !current
    ? "—"
    : withdrawn
      ? u.withdrawn
      : current.source === "lock"
        ? u.supported
        : (u.sources[current.source] ?? current.source);
  return (
    <SectionCard title={u.current} testId="current-card">
      <DataList>
        <DataRow
          label={u.solution}
          loading={loading}
          value={o ? `${o.solution.title} (${o.solution.name})` : ""}
          title={o?.solution.name}
        />
        <DataRow
          label={u.version}
          loading={loading}
          value={
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate font-semibold" data-testid="current-version">
                {versionText(current)}
              </span>
              <Badge size="sm" variant={track.variant} data-testid="track-badge">
                {track.text}
              </Badge>
            </span>
          }
        />
        <DataRow label={u.installed} loading={loading} value={formatZoned(current?.installed_at, tz)} />
        <DataRow
          label={u.careImage}
          loading={loading || !status}
          value={<ShortId value={care} label={u.careImage} testId="care-image" />}
        />
        <DataRow
          label={u.careFe}
          loading={loading || !status}
          value={<ShortId value={careFe} label={u.careFe} testId="care-fe-sha" />}
        />
        <DataRow label={u.agent} loading={!status} value={status?.agent.version} testId="agent-version" />
        <DataRow
          label={u.release}
          loading={loading}
          testId="release-state"
          value={
            withdrawn ? (
              <Badge size="sm" variant="destructive">
                {release}
              </Badge>
            ) : (
              release
            )
          }
        />
      </DataList>
      {withdrawn ? (
        <Alert variant="warning" data-testid="withdrawn-warning">
          <AlertTriangleIcon />
          <AlertTitle>{u.withdrawnTitle}</AlertTitle>
          <AlertDescription>
            {fmt(u.withdrawnText, { reason: current?.withdrawn_reason ?? u.withdrawnNoReason })}
          </AlertDescription>
        </Alert>
      ) : null}
    </SectionCard>
  );
}
