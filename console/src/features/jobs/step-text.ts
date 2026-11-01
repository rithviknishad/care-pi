import type { JobStep, LogUnit } from "@/api/types";
import { formatDateTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";

const s = strings.steps;

// The argument of a step name: "pull-images: a b" -> "a b". A name without ": " has none.
function argumentOf(name: string): string {
  const at = name.indexOf(": ");
  return at >= 0 ? name.slice(at + 2).trim() : "";
}

// shortImage gives "repo:tag" or "repo@sha256:<12 hex>" of an image reference, without the registry
// and the namespace: "ghcr.io/ohcnetwork/care@sha256:77fe…" -> "care@sha256:77fe743e51a6".
export function shortImage(ref: string): string {
  const [path = "", digest] = ref.split("@", 2);
  const last = path.slice(path.lastIndexOf("/") + 1);
  if (digest) {
    const hex = /^sha256:([0-9a-f]+)$/.exec(digest)?.[1];
    const repo = last.replace(/:[^:]*$/, "");
    return hex ? `${repo}@sha256:${hex.slice(0, 12)}` : `${repo}@${digest}`;
  }
  return last;
}

// "care-api.service" -> "care-api".
function shortUnit(unit: string): string {
  return unit.replace(/\.service$/, "");
}

export interface StepTitle {
  text: string;
  // raw: the step has no known action key, so the text is the raw step name.
  raw: boolean;
}

// stepTitle gives the operator text of a step (strings.steps). An unknown key gives the raw step name.
// kind is the kind of the job: a key can have another text in a job of one kind (prune in backup-verify,
// checkpoint in restore-pitr). That text also wins over the text while the step runs.
export function stepTitle(step: Pick<JobStep, "action" | "name" | "state">, kind?: string): StepTitle {
  const key = step.action ?? "";
  const byKind = kind ? s.byKind[kind]?.[key] : undefined;
  const running = step.state === "running" ? s.running[key] : undefined;
  const text = byKind ?? running ?? s.actions[key];
  return text ? { text, raw: false } : { text: step.name, raw: true };
}

// jobKindText gives the operator text of a job kind, or the kind itself for a kind that is not known.
export { jobKindText } from "@/lib/strings";

// stepArgument gives a short text of the argument of a known step, or "".
export function stepArgument(step: Pick<JobStep, "action" | "name">): string {
  const key = step.action ?? "";
  if (!s.actions[key]) return "";
  const arg = argumentOf(step.name);
  if (!arg) return "";
  switch (key) {
    case "write-files":
    case "delete-files": {
      const n = /^(\d+) file/.exec(arg)?.[1];
      return n ? fmt(s.files, { count: n }) : arg;
    }
    case "pull-images": {
      const n = /^(\d+) image/.exec(arg)?.[1];
      if (n) return fmt(s.images, { count: n });
      return arg.split(/\s+/).filter(Boolean).map(shortImage).join(", ");
    }
    case "restart":
    case "enable-target":
    case "wait-healthy":
    case "stop":
    case "start":
      return arg.split(/\s+/).filter(Boolean).map(shortUnit).join(", ");
    case "full-cycle":
    case "start-target": {
      // "care.target (care-db unit changed; …)" -> the reason.
      const reason = /\((.*)\)\s*$/.exec(arg)?.[1];
      return reason ?? "";
    }
    case "set-hostname": {
      const [from, to] = arg.split(/\s+->\s+/);
      return from && to ? fmt(s.hostname, { from, to }) : arg;
    }
    case "snapshot": {
      // "snapshot: usb (db)" -> "usb: database".
      const m = /^(\S+)\s+\((db|files)\)$/.exec(arg);
      if (!m) return arg;
      return fmt(m[2] === "db" ? s.snapshotDb : s.snapshotFiles, { destination: m[1] ?? "" });
    }
    case "restore-db": {
      // "restore-db: 2026-10-30T09:40:00Z" -> the local time.
      return /^\d{4}-\d{2}-\d{2}T/.test(arg) ? formatDateTime(arg) : arg;
    }
    default:
      return arg;
  }
}

const LOG_UNITS: LogUnit[] = ["api", "worker", "beat", "db", "redis", "s3", "caddy", "pod", "clock", "fe-build"];

// logUnitOf finds the first unit "care-<unit>" in an error text, as a LogUnit of GET /logs/{unit}.
// "care.target" gives "target" and "care-pi" gives "agent". null when the text names no unit.
export function logUnitOf(text: string | null | undefined): LogUnit | null {
  if (!text) return null;
  const re = /\bcare(?:-([a-z0-9-]+?)|(\.target))(?=\.service\b|[^a-z0-9-]|$)/g;
  for (const m of text.matchAll(re)) {
    if (m[2]) return "target";
    const name = m[1] ?? "";
    if ((LOG_UNITS as string[]).includes(name)) return name as LogUnit;
    if (name === "pi") return "agent";
  }
  return null;
}
