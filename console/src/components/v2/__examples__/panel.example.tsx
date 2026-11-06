import { Button } from "@/components/ui/button";
import { Panel, WhatNext } from "../panel";
import { StatusChip } from "../status-chip";

export function PanelExample() {
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Panel
        title="Updates"
        description="A new version of Care is ready to install."
        action={<StatusChip tone="info">New</StatusChip>}
        footer={
          <div className="flex flex-col gap-2">
            <Button>Install now</Button>
            <WhatNext>Care stops for about 4 min. Staff see a maintenance page.</WhatNext>
          </div>
        }
      >
        <p className="type-body text-ink-muted">Version v25.3.0 fixes the print of the discharge summary.</p>
      </Panel>
      <Panel variant="tint" title="A tinted section" as="h3">
        <p className="type-body text-ink-muted">Use the tint to group related controls inside a card or a page.</p>
      </Panel>
    </div>
  );
}
