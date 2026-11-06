import {
  ArrowLeftIcon,
  ArrowRightIcon,
  BuildingIcon,
  CloudIcon,
  FolderIcon,
  UsbIcon,
  UserRoundIcon,
  UsersRoundIcon,
} from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { AppShell } from "../app-shell";
import { ChoiceCards } from "../choice-cards";
import { Explainer } from "../explainer";
import { Stage } from "../stage";
import { StatusChip } from "../status-chip";
import { ThemeToggle } from "../theme-toggle";
import { readyNav } from "./nav-sample";
import { device, retention, wizardSteps } from "./sample-data";

function SizeStep() {
  const [value, setValue] = useState<string | null>("small-clinic");
  return (
    <ChoiceCards
      name="preset"
      legend="How many people use Care at the same time?"
      value={value}
      onChange={setValue}
      columns={1}
      choices={[
        {
          value: "solo",
          title: "Just me",
          fit: "1 or 2 people at a time.",
          icon: UserRoundIcon,
          meta: "A 2 GB device is enough",
        },
        {
          value: "small-clinic",
          title: "Small clinic",
          fit: "3 or 4 staff at the same time.",
          icon: UsersRoundIcon,
          recommended: true,
          meta: "A 4 GB device is better",
        },
        {
          value: "busy-counter",
          title: "Busy counter",
          fit: "5 to 10 staff at the same time.",
          icon: BuildingIcon,
          meta: "A 4 GB device or more",
        },
      ]}
    />
  );
}

function BackupsStep() {
  const [value, setValue] = useState<string | null>("usb");
  return (
    <>
      <ChoiceCards
        name="destination"
        legend="Where should the copies go?"
        description="Use 2 places if you can: one near the device and one in another building."
        value={value}
        onChange={setValue}
        columns={3}
        choices={[
          {
            value: "usb",
            title: "USB disk",
            fit: "A disk that stays next to the device.",
            icon: UsbIcon,
            recommended: true,
          },
          { value: "folder", title: "Network folder", fit: "A shared folder on another computer.", icon: FolderIcon },
          { value: "cloud", title: "Cloud bucket", fit: "Storage on the internet (S3).", icon: CloudIcon },
        ]}
      />
      <Explainer policy={retention} />
    </>
  );
}

// The setup wizard (Configure surface): AppShell in minimal mode, the Stage rail at the left, one task at the right.
export function StageExample({ step = "size" }: { step?: "size" | "backups" }) {
  const current = step === "size" ? 2 : 3;
  return (
    <AppShell
      nav={readyNav.slice(5)}
      navMode="minimal"
      deviceName={device.name}
      status={
        <StatusChip tone="info" width={10}>
          Setup
        </StatusChip>
      }
      actions={<ThemeToggle />}
    >
      <Stage
        steps={wizardSteps}
        current={current}
        railTitle="Set up your Care box"
        railHint="About 10 min in total"
        illustration={step === "size" ? "clinic" : "backup-vault"}
        title={step === "size" ? "How big is your clinic?" : "Keep a copy of your data"}
        description={
          step === "size"
            ? "We size Care for the number of people who use it at the same time. You can change it later."
            : "We save a copy of the clinic data at regular times. If the device breaks, you get your data back."
        }
        back={
          <Button variant="ghost" className="no-underline">
            <ArrowLeftIcon data-icon="inline-start" />
            Back
          </Button>
        }
        primary={
          <Button size="lg">
            Continue
            <ArrowRightIcon data-icon="inline-end" />
          </Button>
        }
        next={step === "size" ? "Next, you select where the backups go." : "Next, you write down the recovery key."}
      >
        {step === "size" ? <SizeStep /> : <BackupsStep />}
      </Stage>
    </AppShell>
  );
}
