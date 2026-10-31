import type { Hardware, HostCheckItem } from "@/api/types";
import { strings } from "@/lib/strings";
import { LOW_RAM_BYTES } from "./wizard";

const GB64 = 64_000_000_000;

// The warnings of plans/05 section 3, from the hardware facts.
export function hardwareWarnings(hw: Hardware): string[] {
  const w = strings.hardware.warnings;
  const out: string[] = [];
  if (!hw.ethernet.link) out.push(w.noEthernet);
  if (hw.root_disk.kind === "sd") out.push(w.sd);
  if (hw.root_disk.size_bytes < GB64) out.push(w.small);
  if (hw.disk_probe && (hw.disk_probe.write_mb_s < 20 || hw.disk_probe.read_mb_s < 20)) out.push(w.slow);
  if (hw.ram_bytes < LOW_RAM_BYTES) out.push(w.lowRam);
  if (hw.internet_reachable === false) out.push(w.noInternet);
  if (!hw.clock_synchronized) out.push(w.clock);
  return out;
}

export interface CheckGroups {
  // The failed checks (required and advised), in the order of the agent.
  problems: HostCheckItem[];
  // The passed checks that are not directories.
  passed: HostCheckItem[];
  // The directory checks ("dir /var/lib/care-pi/..."): one row on the page.
  dirs: { ok: number; total: number };
  total: number;
  ok: number;
}

// groupChecks puts the failures first and collapses the directory checks into one row. On lumine the
// hardware step listed 43 checks, 25 of them directories, and the Continue button was below them (wave 4c).
export function groupChecks(checks: HostCheckItem[]): CheckGroups {
  const g: CheckGroups = { problems: [], passed: [], dirs: { ok: 0, total: 0 }, total: checks.length, ok: 0 };
  for (const c of checks) {
    const dir = c.name.startsWith("dir ");
    if (c.ok) g.ok++;
    if (dir) {
      g.dirs.total++;
      if (c.ok) g.dirs.ok++;
    }
    if (!c.ok) g.problems.push(c);
    else if (!dir) g.passed.push(c);
  }
  return g;
}
