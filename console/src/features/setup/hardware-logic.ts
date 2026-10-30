import type { Hardware } from "@/api/types";
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
