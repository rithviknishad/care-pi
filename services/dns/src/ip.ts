// IP address parsing, canonical forms, and the address policy of the DDNS records.

const V4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?:\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

export function parseIPv4(text: string): Uint8Array | null {
  if (!V4.test(text)) return null;
  return Uint8Array.from(text.split(".").map((p) => Number(p)));
}

export function formatIPv4(b: Uint8Array): string {
  return `${b[0]}.${b[1]}.${b[2]}.${b[3]}`;
}

const HEXTET = /^[0-9a-f]{1,4}$/;

export function parseIPv6(input: string): Uint8Array | null {
  let text = input.toLowerCase();
  if (text.length === 0 || text.length > 45 || text.includes("%")) return null;
  let tail4: Uint8Array | null = null;
  if (text.includes(".")) {
    const cut = text.lastIndexOf(":");
    if (cut < 0) return null;
    tail4 = parseIPv4(text.slice(cut + 1));
    if (tail4 === null) return null;
    // Keep the colon so that "::1.2.3.4" stays "::" + groups.
    text = text.slice(0, cut + 1) + "0:0";
  }
  const halves = text.split("::");
  if (halves.length > 2) return null;
  const parse = (part: string): number[] | null => {
    if (part === "") return [];
    const out: number[] = [];
    for (const g of part.split(":")) {
      if (!HEXTET.test(g)) return null;
      out.push(Number.parseInt(g, 16));
    }
    return out;
  };
  const head = parse(halves[0] ?? "");
  const tail = halves.length === 2 ? parse(halves[1] ?? "") : [];
  if (head === null || tail === null) return null;
  let groups: number[];
  if (halves.length === 2) {
    const missing = 8 - head.length - tail.length;
    if (missing < 1) return null;
    groups = [...head, ...new Array<number>(missing).fill(0), ...tail];
  } else {
    if (head.length !== 8) return null;
    groups = head;
  }
  const out = new Uint8Array(16);
  groups.forEach((g, i) => {
    out[i * 2] = g >> 8;
    out[i * 2 + 1] = g & 0xff;
  });
  if (tail4 !== null) out.set(tail4, 12);
  return out;
}

// RFC 5952 canonical text form.
export function formatIPv6(b: Uint8Array): string {
  const groups: number[] = [];
  for (let i = 0; i < 16; i += 2) groups.push(((b[i] ?? 0) << 8) | (b[i + 1] ?? 0));
  let bestStart = -1;
  let bestLen = 0;
  for (let i = 0; i < 8; ) {
    if (groups[i] !== 0) {
      i++;
      continue;
    }
    let j = i;
    while (j < 8 && groups[j] === 0) j++;
    if (j - i > bestLen) {
      bestStart = i;
      bestLen = j - i;
    }
    i = j;
  }
  const hex = groups.map((g) => g.toString(16));
  if (bestLen < 2) return hex.join(":");
  return `${hex.slice(0, bestStart).join(":")}::${hex.slice(bestStart + bestLen).join(":")}`;
}

export type V4Class =
  | "private"
  | "cgnat"
  | "documentation"
  | "loopback"
  | "link-local"
  | "unspecified"
  | "multicast"
  | "reserved"
  | "public";

export function classifyIPv4(b: Uint8Array): V4Class {
  const [a = 0, c = 0, d = 0] = [b[0], b[1], b[2]];
  if (a === 10 || (a === 172 && c >= 16 && c <= 31) || (a === 192 && c === 168)) return "private";
  if (a === 100 && c >= 64 && c <= 127) return "cgnat";
  if ((a === 192 && c === 0 && d === 2) || (a === 198 && c === 51 && d === 100) || (a === 203 && c === 0 && d === 113)) {
    return "documentation";
  }
  if (a === 127) return "loopback";
  if (a === 169 && c === 254) return "link-local";
  if (a === 0) return b.every((x) => x === 0) ? "unspecified" : "reserved";
  if (a >= 224 && a <= 239) return "multicast";
  if (a >= 240) return "reserved";
  if ((a === 192 && c === 0 && d === 0) || (a === 198 && (c === 18 || c === 19))) return "reserved";
  return "public";
}

export type V6Class =
  | "ula"
  | "global"
  | "documentation"
  | "loopback"
  | "link-local"
  | "unspecified"
  | "multicast"
  | "ipv4-mapped"
  | "reserved";

export function classifyIPv6(b: Uint8Array): V6Class {
  const b0 = b[0] ?? 0;
  const b1 = b[1] ?? 0;
  if (b.every((x) => x === 0)) return "unspecified";
  if (b.slice(0, 15).every((x) => x === 0) && b[15] === 1) return "loopback";
  if (b.slice(0, 10).every((x) => x === 0) && b[10] === 0xff && b[11] === 0xff) return "ipv4-mapped";
  if (b0 === 0xff) return "multicast";
  if (b0 === 0xfe && (b1 & 0xc0) === 0x80) return "link-local";
  if ((b0 & 0xfe) === 0xfc) return "ula";
  if (b0 === 0x20 && b1 === 0x01 && b[2] === 0x0d && b[3] === 0xb8) return "documentation";
  if ((b0 & 0xe0) === 0x20) return "global";
  return "reserved";
}

export type AddressPolicy = "private" | "any";

export function addressPolicy(value: string | undefined): AddressPolicy {
  return value === "any" ? "any" : "private";
}

// Returns the canonical IPv4 text, or an error text.
export function checkIPv4(text: string, policy: AddressPolicy): { ok: true; value: string } | { ok: false; reason: string } {
  const b = parseIPv4(text);
  if (b === null) return { ok: false, reason: "ipv4 is not a dotted-quad IPv4 address" };
  const cls = classifyIPv4(b);
  const allowed: V4Class[] = policy === "any" ? ["private", "cgnat", "documentation", "public"] : ["private", "cgnat", "documentation"];
  if (!allowed.includes(cls)) return { ok: false, reason: `ipv4 is a ${cls} address; this service accepts ${allowed.join(", ")} addresses` };
  return { ok: true, value: formatIPv4(b) };
}

// IPv6 LAN addresses are usually global unicast, so the policy "private" accepts ULA, global, and documentation addresses.
export function checkIPv6(text: string): { ok: true; value: string } | { ok: false; reason: string } {
  const b = parseIPv6(text);
  if (b === null) return { ok: false, reason: "ipv6 is not an IPv6 address" };
  const cls = classifyIPv6(b);
  const allowed: V6Class[] = ["ula", "global", "documentation"];
  if (!allowed.includes(cls)) return { ok: false, reason: `ipv6 is a ${cls} address; this service accepts ${allowed.join(", ")} addresses` };
  return { ok: true, value: formatIPv6(b) };
}

// The key that groups callers for rate limits and for GET /lookup: the IPv4 address, or the IPv6 /64.
export function networkKey(ip: string): string {
  const v4 = parseIPv4(ip);
  if (v4 !== null) return formatIPv4(v4);
  const v6 = parseIPv6(ip);
  if (v6 !== null) {
    const prefix = new Uint8Array(16);
    prefix.set(v6.slice(0, 8));
    return `${formatIPv6(prefix)}/64`;
  }
  return "unknown";
}
