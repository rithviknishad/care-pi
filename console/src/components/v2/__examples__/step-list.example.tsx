import { Panel } from "../panel";
import { StepList } from "../step-list";
import { useNow } from "../use-now";
import { useArrived } from "./arrival";
import { jobSteps, plannedSteps } from "./sample-data";

export function StepListExample() {
  const arrived = useArrived();
  const now = useNow(true);
  return (
    <Panel
      title="Setup runs"
      description="You can close this page. Setup continues on the device."
      testId="steps-panel"
    >
      <StepList steps={arrived ? jobSteps(now) : plannedSteps()} withLogs testId="steps" />
    </Panel>
  );
}
