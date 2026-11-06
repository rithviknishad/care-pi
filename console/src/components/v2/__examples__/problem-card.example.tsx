import { ScrollTextIcon } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ProblemCard } from "../problem-card";

export function ProblemCardExample() {
  const [retrying, setRetrying] = useState(false);
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <ProblemCard
        title="We could not reach the backup disk"
        description="The disk “Clinic backup” did not answer. Your data is safe on this device. The next copy waits for the disk."
        steps={["Make sure that the disk is connected.", "Press Try again."]}
        technical={
          "type: https://care-pi.dev/problems/destination-unreachable\nstatus: 502\ndetail: mount /run/care-pi/usb/3F1D: no such device\njob: 20261106T094102.311Z-backup-run"
        }
        onRetry={() => {
          setRetrying(true);
          setTimeout(() => setRetrying(false), 1200);
        }}
        retrying={retrying}
        actions={
          <Button size="sm" variant="ghost" className="no-underline">
            <ScrollTextIcon data-icon="inline-start" />
            Open the log
          </Button>
        }
      />
      <ProblemCard
        tone="warn"
        title="The clock of this device is not checked"
        description="The device has no internet, so it took the time of your browser. Backups and certificates use this time."
        steps={["Connect the device to the internet. The clock then corrects itself."]}
      />
    </div>
  );
}
