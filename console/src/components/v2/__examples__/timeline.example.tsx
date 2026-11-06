import { CheckIcon, DatabaseBackupIcon, PlayIcon } from "lucide-react";
import { Panel } from "../panel";
import { Timeline } from "../timeline";
import { history } from "./sample-data";

export function TimelineExample() {
  const icons = [PlayIcon, undefined, CheckIcon, DatabaseBackupIcon];
  return (
    <Panel title="Today">
      <Timeline items={history.map((h, i) => ({ ...h, icon: icons[i] }))} />
    </Panel>
  );
}
