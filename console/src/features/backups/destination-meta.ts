import { CloudIcon, ServerIcon, UsbIcon, type LucideIcon } from "lucide-react";
import type { Destination, DestinationTestResult } from "@/api/types";

// The 3 choices of "Where should backups go?" (plans/02 section 7) and their icons.
export type Choice = "usb" | "sftp" | "s3";
export const CHOICES: { id: Choice; icon: LucideIcon }[] = [
  { id: "usb", icon: UsbIcon },
  { id: "sftp", icon: ServerIcon },
  { id: "s3", icon: CloudIcon },
];

export const TYPE_ICONS: Record<string, LucideIcon> = { posix: UsbIcon, sftp: ServerIcon, s3: CloudIcon };

// The place of a destination in one line: the disk label, the folder, the server, or the bucket.
export function destinationPlace(d: Destination): string {
  if (d.type === "posix") return d.disk_label ?? d.path ?? "";
  if (d.type === "sftp") {
    return `${d.user ?? ""}@${d.host ?? ""}${d.port && d.port !== 22 ? `:${d.port}` : ""}:${d.path ?? ""}`;
  }
  return `${d.bucket ?? ""}${d.prefix ? `/${d.prefix}` : ""} (${(d.endpoint ?? "").replace(/^https?:\/\//, "")})`;
}

// The root-disk check of a posix destination failed: the path is on the root disk (docs/api/README.md section 13.5).
export function onRootDisk(result: DestinationTestResult | null | undefined): boolean {
  return Boolean(result?.checks.some((c) => c.name === "root-disk" && !c.ok));
}

// The next free label care-usb<N> (ext4 allows 16 bytes; the contract allows care- and 1 to 11 characters).
export function nextDiskLabel(taken: readonly (string | null | undefined)[]): string {
  for (let n = 1; ; n++) if (!taken.includes(`care-usb${n}`)) return `care-usb${n}`;
}
