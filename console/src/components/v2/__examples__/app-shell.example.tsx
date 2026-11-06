import { CheckIcon, ClockIcon, DatabaseBackupIcon, HardDriveIcon, LogOutIcon, MemoryStickIcon } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { AppShell } from "../app-shell";
import { DashboardGrid } from "../dashboard-grid";
import { Hero } from "../hero";
import { KeyValueList } from "../key-value-list";
import { MetricBar } from "../metric-bar";
import { Panel } from "../panel";
import { StatCard } from "../stat-card";
import { StatusChip } from "../status-chip";
import { ThemeToggle } from "../theme-toggle";
import { Timeline } from "../timeline";
import { useArrived } from "./arrival";
import { BannerSample } from "./banner-sample";
import { readyNav } from "./nav-sample";
import { device, history } from "./sample-data";

const MIB = 1024 * 1024;

// The app shell with a dashboard (Monitor surface): a compact status hero, fixed-size cards, then details.
export function AppShellExample({ banner: initialBanner = false }: { banner?: boolean }) {
  const arrived = useArrived();
  const loading = !arrived;
  const [banner, setBanner] = useState(initialBanner);
  return (
    <AppShell
      nav={readyNav}
      deviceName={arrived ? device.name : undefined}
      status={
        <StatusChip tone="ok" width={10} testId="setup-state">
          Ready
        </StatusChip>
      }
      banner={banner ? <BannerSample /> : null}
      bannerCompact={banner ? <BannerSample compact /> : null}
      actions={<ThemeToggle />}
      railFooter={<span>Agent {device.version}</span>}
      sheetFooter={
        <Button variant="ghost" className="w-full justify-start no-underline">
          <LogOutIcon data-icon="inline-start" />
          Log out
        </Button>
      }
    >
      <div className="flex flex-col gap-6">
        <Hero
          size="compact"
          title="All good"
          description="Care runs. The last copy of the data is 12 min old."
          illustration="pi-box"
          loading={loading}
          actions={
            <Button size="sm" variant="secondary" onClick={() => setBanner((b) => !b)}>
              {banner ? "Hide the banner" : "Show the banner"}
            </Button>
          }
          testId="shell-hero"
        />
        <DashboardGrid>
          <StatCard
            label="Last backup"
            icon={DatabaseBackupIcon}
            value="12"
            unit="min ago"
            hint="Next copy in 3 min"
            loading={loading}
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
            loading={loading}
          />
        </DashboardGrid>
        <div className="grid gap-6 lg:grid-cols-2">
          <Panel
            title="Services"
            description="Memory of each service against its limit."
            action={
              <StatusChip tone="ok" size="sm">
                10 of 10 run
              </StatusChip>
            }
          >
            <div className="flex flex-col gap-5">
              <MetricBar label="Care (web and API)" used={269 * MIB} cap={512 * MIB} loading={loading} />
              <MetricBar label="Background work" used={313 * MIB} cap={384 * MIB} loading={loading} />
              <MetricBar label="Database" used={95 * MIB} cap={384 * MIB} loading={loading} />
            </div>
          </Panel>
          <Panel title="This device">
            <KeyValueList
              loading={loading}
              items={[
                { label: "Name on the network", value: device.name, mono: true },
                { label: "Address", value: "192.168.165.91", mono: true, copy: "192.168.165.91" },
                { label: "Certificate", value: "Valid until 7 September 2036" },
              ]}
            />
          </Panel>
        </div>
        <Panel title="Today" action={<CheckIcon className="size-4 text-ok-solid" aria-hidden="true" />}>
          <Timeline items={history} />
        </Panel>
      </div>
    </AppShell>
  );
}
