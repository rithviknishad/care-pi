import { DatabaseBackupIcon } from "lucide-react";
import { Link } from "react-router";
import type { PitrOffer } from "@/api/types";
import { Button } from "@/components/ui/button";
import { KeyValueList } from "@/components/v2/key-value-list";
import { WhatNext } from "@/components/v2/panel";
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
    <section
      className="flex min-w-0 gap-3 rounded-card border border-warn-border bg-warn-bg p-4 sm:gap-4 sm:p-6"
      data-testid="pitr-offer"
      data-target-time={offer.target_time}
      aria-labelledby="pitr-offer-title"
    >
      <DatabaseBackupIcon className="mt-0.5 size-6 shrink-0 text-warn-solid" aria-hidden="true" />
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        <h2 id="pitr-offer-title" className="type-h3 text-warn-fg">
          {fmt(u.pitrTitle, { time })}
        </h2>
        <p className="type-body text-ink">{fmt(u.pitrCode, { version })}</p>
        <p className="type-body text-ink">
          {offer.available
            ? fmt(u.pitrLoss, { time })
            : fmt(u.pitrUnavailable, { time, reason: offer.reason ?? strings.problems.unknown })}
        </p>
        <KeyValueList
          items={[
            {
              id: "time",
              label: u.pitrTime,
              value: formatZoned(offer.target_time, tz),
              mono: true,
              testId: "pitr-time",
            },
            ...(offer.restore_point
              ? [
                  {
                    id: "point",
                    label: u.pitrPoint,
                    value: offer.restore_point,
                    mono: true,
                    title: offer.restore_point,
                  },
                ]
              : []),
          ]}
        />
        <div className="flex flex-wrap gap-2">
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
        </div>
        {offer.available ? <WhatNext>{u.pitrNext}</WhatNext> : null}
      </div>
    </section>
  );
}
