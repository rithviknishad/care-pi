// Sample data of the /__v2 gallery (dev server only). The texts here are examples, not console text.
import type { RetentionPolicy } from "../retention";
import type { StepItem } from "../step-list";
import type { TimelineItem } from "../timeline";

export const device = { name: "care.local", version: "0.1.1 (64caa26)" };

export const wizardSteps = [
  { id: "hardware", label: "Check the device", href: "#hardware" },
  { id: "identity", label: "Clinic name", href: "#identity" },
  { id: "size", label: "Size" },
  { id: "backups", label: "Backups" },
  { id: "key", label: "Recovery key" },
  { id: "admin", label: "Care administrator" },
  { id: "review", label: "Review" },
  { id: "install", label: "Install" },
];

export const retention: RetentionPolicy = {
  interval_min: 15,
  keep_within_days: 2,
  keep_daily: 14,
  keep_weekly: 8,
  keep_monthly: 6,
};

const t0 = Date.parse("2026-11-06T09:30:00Z");

// The plan of the job before its events come: each step waits. The rows have their final size (the usual
// duration reserves the second line), so the states change in place.
export function plannedSteps(): StepItem[] {
  return jobSteps(0).map((s) => ({
    id: s.id,
    title: s.title,
    state: "pending",
    expectedSeconds: s.expectedSeconds,
    raw: s.raw,
    log: s.log,
  }));
}

export function jobSteps(now: number): StepItem[] {
  return [
    { id: "validate", title: "Check the settings", state: "done", startedAt: t0, endedAt: t0 + 900, raw: "validate" },
    {
      id: "host",
      title: "Check the device",
      state: "done",
      startedAt: t0 + 900,
      endedAt: t0 + 4200,
      raw: "host-check",
    },
    {
      id: "files",
      title: "Write the settings files",
      state: "done",
      startedAt: t0 + 4200,
      endedAt: t0 + 5100,
      raw: "write-files",
      log: "wrote /etc/care-pi/rendered/care.env\nwrote /etc/containers/systemd/care-api.container\nwrote 13 more files",
    },
    {
      id: "pull",
      title: "Download the Care software",
      state: "done",
      startedAt: t0 + 5100,
      endedAt: t0 + 192_000,
      raw: "pull-images",
      log: "ghcr.io/ohcnetwork/care@sha256:77fe743e51a6… 412 MiB\nlocalhost/care-pi/caddy:2 60 MiB",
    },
    {
      id: "db",
      title: "Start the database",
      state: "running",
      startedAt: now - 83_000,
      expectedSeconds: 220,
      raw: "full-cycle: care-db",
      log: () => "care-db: starting\ncare-db: database system is ready to accept connections\ncare-redis: ready",
    },
    { id: "care", title: "Start Care", state: "pending", raw: "care-api" },
    { id: "admin", title: "Make the Care administrator", state: "pending", raw: "superuser" },
  ];
}

export const history: TimelineItem[] = [
  { id: "1", time: "09:30", title: "Setup started", description: "From the browser of a laptop.", tone: "info" },
  { id: "2", time: "09:33", title: "Care software downloaded", description: "472 MiB in 3 min.", tone: "primary" },
  { id: "3", time: "09:41", title: "Care is ready", description: "The first backup starts in 15 min.", tone: "ok" },
  { id: "4", time: "09:56", title: "First backup saved", description: "To the USB disk “Clinic backup”.", tone: "ok" },
];
