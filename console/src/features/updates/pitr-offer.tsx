import { DatabaseBackupIcon } from "lucide-react";
import { Link } from "react-router";
import type { PitrOffer } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { fmt, strings } from "@/lib/strings";
import { restoreLink } from "./update-text";
import { formatZoned } from "./window";

const u = strings.updates;

// The PITR hand-off to Phase 3 (docs/api/README.md section 14.5): the code went back, the database did not.
// The card links to the Restore page with the time of the checkpoint (/restore?time=<target_time>). The Restore
// page opens "Undo a mistake" with that time; its start asks for the reauthentication and sends POST /restore with
// that time (offer.restore). The agent never starts a restore by itself, and neither does the console.
export function PitrOfferCard({
  offer,
  version,
  tz,
  onDismiss,
}: {
  offer: PitrOffer;
  version: string;
  tz: string;
  onDismiss: () => void;
}) {
  const time = formatZoned(offer.target_time, tz, "time");
  return (
    <Card
      size="sm"
      className="ring-amber-400 dark:ring-amber-700"
      data-testid="pitr-offer"
      data-target-time={offer.target_time}
    >
      <CardHeader>
        <CardTitle className="flex items-start gap-2">
          <DatabaseBackupIcon className="mt-0.5 size-4 shrink-0 text-amber-600" aria-hidden="true" />
          <h2>{fmt(u.pitrTitle, { time })}</h2>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <p>{fmt(u.pitrCode, { version })}</p>
        {offer.available ? (
          <p>{fmt(u.pitrLoss, { time })}</p>
        ) : (
          <Alert variant="warning">
            <AlertDescription>
              {fmt(u.pitrUnavailable, { time, reason: offer.reason ?? strings.problems.unknown })}
            </AlertDescription>
          </Alert>
        )}
        <DataList>
          <DataRow label={u.pitrTime} value={formatZoned(offer.target_time, tz)} testId="pitr-time" />
          {offer.restore_point ? (
            <DataRow
              label={u.pitrPoint}
              value={<code className="font-mono text-xs">{offer.restore_point}</code>}
              title={offer.restore_point}
            />
          ) : null}
        </DataList>
      </CardContent>
      <CardFooter className="flex flex-wrap gap-2">
        {offer.available ? (
          <Button asChild>
            <Link to={restoreLink(offer)} data-testid="pitr-open">
              <DatabaseBackupIcon data-icon="inline-start" />
              {u.pitrOpen}
            </Link>
          </Button>
        ) : null}
        <Button type="button" variant="outline" onClick={onDismiss} data-testid="pitr-dismiss">
          {u.pitrLater}
        </Button>
      </CardFooter>
    </Card>
  );
}
