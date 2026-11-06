import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Explainer } from "../explainer";
import { Panel } from "../panel";
import type { RetentionPolicy } from "../retention";
import { retention } from "./sample-data";

const SMALL: RetentionPolicy = {
  interval_min: 60,
  keep_within_days: 1,
  keep_daily: 7,
  keep_weekly: 4,
  keep_monthly: 12,
};

export function ExplainerExample() {
  const [policy, setPolicy] = useState(retention);
  return (
    <Panel
      title="Backups"
      action={
        <div className="flex gap-2">
          <Button
            size="xs"
            variant={policy === retention ? "secondary" : "outline"}
            onClick={() => setPolicy(retention)}
          >
            Normal
          </Button>
          <Button size="xs" variant={policy === SMALL ? "secondary" : "outline"} onClick={() => setPolicy(SMALL)}>
            Small disk
          </Button>
        </div>
      }
    >
      <Explainer policy={policy} testId="explainer" />
    </Panel>
  );
}
