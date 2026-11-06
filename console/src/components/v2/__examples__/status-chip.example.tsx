import { StatusChip } from "../status-chip";

export function StatusChipExample() {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <StatusChip tone="ok">Ready</StatusChip>
      <StatusChip tone="info" live>
        Setup runs
      </StatusChip>
      <StatusChip tone="warn">Clock not checked</StatusChip>
      <StatusChip tone="danger">Backup failed</StatusChip>
      <StatusChip tone="neutral">Not claimed</StatusChip>
      <StatusChip tone="primary" size="sm">
        Recommended
      </StatusChip>
      <StatusChip tone="ok" width={10}>
        Ready
      </StatusChip>
    </div>
  );
}
