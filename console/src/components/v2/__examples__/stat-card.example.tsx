import { ClockIcon, DatabaseBackupIcon, HardDriveIcon, MemoryStickIcon } from "lucide-react";
import { DashboardGrid } from "../dashboard-grid";
import { MetricBar } from "../metric-bar";
import { Panel } from "../panel";
import { StatCard } from "../stat-card";
import { StatusChip } from "../status-chip";
import { useArrived } from "./arrival";

const MIB = 1024 * 1024;

export function StatCardExample() {
  const arrived = useArrived();
  const loading = !arrived;
  return (
    <div className="flex flex-col gap-4" data-testid="stat-example">
      <DashboardGrid>
        <StatCard
          label="Last backup"
          icon={DatabaseBackupIcon}
          value="12"
          unit="min ago"
          hint="Next copy in 3 min"
          loading={loading}
          action={
            loading ? null : (
              <StatusChip tone="ok" size="sm">
                OK
              </StatusChip>
            )
          }
        />
        <StatCard
          label="Memory used"
          icon={MemoryStickIcon}
          value="1.4"
          unit="GiB of 4 GiB"
          hint="35 % · normal"
          loading={loading}
        />
        <StatCard
          label="Free disk"
          icon={HardDriveIcon}
          value="103"
          unit="GiB"
          hint="Enough for about 2 years"
          loading={loading}
        />
        <StatCard
          label="Running for"
          icon={ClockIcon}
          value="6"
          unit="days"
          hint="Updates: Sunday 02:00"
          hintTone="info"
          loading={loading}
        />
      </DashboardGrid>
      <Panel title="Memory of each service" description="Each service has a limit. Amber from 80 %, red from 95 %.">
        <div className="grid gap-5 sm:grid-cols-2">
          <MetricBar label="Care (web and API)" used={269 * MIB} cap={512 * MIB} loading={loading} />
          <MetricBar label="Database" used={95 * MIB} cap={384 * MIB} loading={loading} />
          <MetricBar label="Background work" used={313 * MIB} cap={384 * MIB} loading={loading} />
          <MetricBar label="Files" used={250 * MIB} cap={256 * MIB} loading={loading} />
        </div>
      </Panel>
    </div>
  );
}
