import { formatElapsed } from "@/lib/format";

// Plain durations for operators (console/design.md, "Copy"): minutes, never seconds above 1 minute.
// 40 -> "1 min", 200 -> "4 min", 3900 -> "1 h 5 min".
export function aboutMinutes(totalSeconds: number): string {
  const min = Math.max(1, Math.round(totalSeconds / 60));
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  const m = min % 60;
  return m ? `${h} h ${m} min` : `${h} h`;
}

// A clock of fixed width for elapsed time (mono): 00:05, 03:20, 1:02:03.
export function clock(ms: number | null | undefined): string {
  return ms === null || ms === undefined ? "--:--" : formatElapsed(ms);
}
