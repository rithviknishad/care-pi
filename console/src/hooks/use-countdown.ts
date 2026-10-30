import { useEffect, useState } from "react";

// Counts down from `seconds` (for Retry-After). Returns 0 when the time is over.
// A new `key` (for example a new error) starts the count again.
export function useCountdown(seconds: number | null | undefined, key?: unknown): number {
  const [remaining, setRemaining] = useState(0);
  useEffect(() => {
    const end = seconds && seconds > 0 ? Date.now() + seconds * 1000 : 0;
    const tick = () => setRemaining(end ? Math.max(0, Math.ceil((end - Date.now()) / 1000)) : 0);
    const first = setTimeout(tick, 0);
    const timer = end ? setInterval(tick, 1000) : undefined;
    return () => {
      clearTimeout(first);
      clearInterval(timer);
    };
  }, [seconds, key]);
  return remaining;
}

// The time since `since` in ms, updated each second while `active`.
export function useElapsed(
  since: string | null | undefined,
  until: string | null | undefined,
  active: boolean,
): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active]);
  if (!since) return 0;
  const start = Date.parse(since);
  const stop = until ? Date.parse(until) : now;
  return Math.max(0, stop - start);
}
