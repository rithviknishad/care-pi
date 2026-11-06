import { KeyValueList } from "../key-value-list";
import { Panel } from "../panel";
import { useArrived } from "./arrival";

export function KeyValueListExample() {
  const arrived = useArrived();
  return (
    <Panel title="This device" description="The facts that support asks for first." testId="kv-panel">
      <KeyValueList
        loading={!arrived}
        items={[
          { label: "Model", value: "Raspberry Pi 5 Model B Rev 1.1" },
          { label: "Address", value: "192.168.165.91", mono: true, copy: "192.168.165.91" },
          { label: "Name on the network", value: "care.local", mono: true },
          { label: "Care version", value: "v25.2.0 (stable)" },
          { label: "Agent", value: "0.1.1 (64caa26)", mono: true, copy: "0.1.1 (64caa26)" },
        ]}
      />
    </Panel>
  );
}
