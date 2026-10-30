// Format helpers. Each helper is a pure function, so the unit tests can check it.

const KIB = 1024;
const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

export function formatBytes(bytes: number | null | undefined, digits = 1): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return "—";
  if (bytes < KIB) return `${Math.round(bytes)} B`;
  let value = bytes;
  let unit = 0;
  while (value >= KIB && unit < UNITS.length - 1) {
    value /= KIB;
    unit++;
  }
  const fixed = value >= 100 ? value.toFixed(0) : value.toFixed(digits);
  return `${fixed.replace(/\.0+$/, "")} ${UNITS[unit]}`;
}

export function formatMiB(mib: number): string {
  return formatBytes(mib * KIB * KIB);
}

export function percent(part: number | null | undefined, total: number | null | undefined): number | null {
  if (part === null || part === undefined || !total) return null;
  return Math.max(0, Math.min(100, (part / total) * 100));
}

export function formatPercent(value: number | null): string {
  return value === null ? "—" : `${Math.round(value)} %`;
}

// 200 -> "3 min 20 s", 45 -> "45 s", 3900 -> "1 h 5 min", 90000 -> "1 d 1 h".
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.round(totalSeconds));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (d > 0) return h > 0 ? `${d} d ${h} h` : `${d} d`;
  if (h > 0) return m > 0 ? `${h} h ${m} min` : `${h} h`;
  if (m > 0) return sec > 0 ? `${m} min ${sec} s` : `${m} min`;
  return `${sec} s`;
}

export function formatAbout(totalSeconds: number): string {
  return `about ${formatDuration(totalSeconds)}`;
}

// A clock for elapsed time with a fixed width: 00:05, 03:20, 1:02:03.
export function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const mm = String(m).padStart(2, "0");
  const ss = String(sec).padStart(2, "0");
  return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`;
}

export function shortSha(sha: string | null | undefined, length = 12): string {
  if (!sha) return "—";
  return sha.slice(0, length);
}

// "ghcr.io/ohcnetwork/care@sha256:5e0c3b..." -> "sha256:5e0c3b2a1f9d"
export function shortDigest(pull: string | null | undefined, length = 12): string {
  if (!pull) return "—";
  const m = /sha256:([0-9a-f]{12,64})/.exec(pull);
  return m?.[1] ? `sha256:${m[1].slice(0, length)}` : pull;
}

// 64 hex digits -> 4 lines of 8 groups ("3F:1D:...").
export function fingerprintLines(hex: string, groupsPerLine = 8): string[] {
  const pairs =
    hex
      .replace(/[^0-9a-fA-F]/g, "")
      .toUpperCase()
      .match(/../g) ?? [];
  const lines: string[] = [];
  for (let i = 0; i < pairs.length; i += groupsPerLine) lines.push(pairs.slice(i, i + groupsPerLine).join(":"));
  return lines;
}

function pad(n: number, w = 2): string {
  return String(n).padStart(w, "0");
}

// A fixed-width local time: "2026-10-30 15:45:00".
export function formatDateTime(iso: string | null | undefined, timeZone?: string): string {
  if (!iso) return "—";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  if (timeZone) {
    const parts = new Intl.DateTimeFormat("en-CA", {
      timeZone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    }).formatToParts(date);
    const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "00";
    return `${get("year")}-${get("month")}-${get("day")} ${get("hour")}:${get("minute")}:${get("second")}`;
  }
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

export function formatTime(iso: string | null | undefined): string {
  const full = formatDateTime(iso);
  return full === "—" ? full : full.slice(11);
}

export function formatRelative(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const diff = Math.round((t - now) / 1000);
  if (Math.abs(diff) < 5) return "now";
  const text = formatDuration(Math.abs(diff));
  return diff < 0 ? `${text} ago` : `in ${text}`;
}

export function formatMbps(n: number | null | undefined): string {
  return n === null || n === undefined ? "—" : `${n} Mb/s`;
}

export function formatLoad(load: number[]): string {
  return load.map((n) => n.toFixed(2)).join(" ");
}
