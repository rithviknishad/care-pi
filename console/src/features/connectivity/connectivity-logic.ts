// The pure logic of the Phase 6 console (docs/api/README.md section 15, docs/connectivity.md).
import type { Status, WifiNetwork } from "@/api/types";

export const PUBLIC_ZONE = "care-pi.rithviknishad.dev";
export const STAGING_CA = "https://acme-staging-v02.api.letsencrypt.org/directory";

const HINT = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const COUNTRY = /^[A-Z]{2}$/;
const PRINTABLE = /^[\x20-\x7e]*$/;

export function validHint(hint: string): boolean {
  return HINT.test(hint);
}

export function validAuthKey(key: string): boolean {
  return key === "" || key.startsWith("tskey-");
}

export interface WifiForm {
  ssid: string;
  psk: string;
  country: string;
  failover: boolean;
}

export type WifiErrors = Partial<Record<"ssid" | "psk" | "country", "invalid">>;

// The SSID has 1 to 32 bytes (UTF-8); the passphrase has 8 to 63 printable ASCII characters.
export function validateWifi(f: WifiForm): WifiErrors {
  const e: WifiErrors = {};
  const bytes = new TextEncoder().encode(f.ssid).length;
  if (bytes < 1 || bytes > 32) e.ssid = "invalid";
  if (f.psk.length < 8 || f.psk.length > 63 || !PRINTABLE.test(f.psk)) e.psk = "invalid";
  if (f.country !== "" && !COUNTRY.test(f.country)) e.country = "invalid";
  return e;
}

// The strength of a network in 4 steps, for the bars of the scan list.
export function signalLevel(signal: number): 0 | 1 | 2 | 3 {
  if (signal >= 70) return 3;
  if (signal >= 45) return 2;
  if (signal >= 20) return 1;
  return 0;
}

// The networks of a scan for the select list: the secured ones first, each group strongest first.
export function sortNetworks(nets: WifiNetwork[]): WifiNetwork[] {
  return [...nets].sort((a, b) => Number(a.security === "") - Number(b.security === "") || b.signal - a.signal);
}

export type HostMode = "public" | "local" | "tailscale";

export interface ServedName {
  host: string;
  mode: HostMode;
}

// The names of Care with their mode, the canonical name first (render.Hostnames of the agent).
export function servedNames(status: Pick<Status, "hostnames" | "tls">): ServedName[] {
  const pub = status.tls.public.hostname;
  const ts = status.tls.tailscale.hostname;
  return status.hostnames.map((host) => ({
    host,
    mode: host === pub ? "public" : host === ts ? "tailscale" : "local",
  }));
}

// base64url without padding: the form of the fragment of the certificate page (services/dns/API.md section 8.2).
export function base64Url(bytes: Uint8Array): string {
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// The QR code of 07 §4.4: the certificate page of the DNS service with the DER root in the fragment.
// The fragment never goes to the server.
export function certPageUrl(pageUrl: string, der: Uint8Array): string {
  return `${pageUrl}#${base64Url(der)}`;
}

// The QR code for a phone without internet: the fingerprint, to compare with the certificate viewer of the phone.
export function fingerprintQr(colon: string): string {
  return `SHA-256 ${colon}`;
}

// The one-time warning after the switch to the name-constrained root (07 §4.1). The browser keeps the
// dismissal for each fingerprint, so a later change warns again.
export const CA_NOTICE_KEY = "care-pi.ca-changed.dismissed";

export function caNoticeVisible(
  changedAt: string | null | undefined,
  fingerprint: string | undefined,
  dismissed: string | null,
): boolean {
  return Boolean(changedAt && fingerprint && dismissed !== fingerprint);
}

export type RebindResult = "ok" | "blocked";

// The DNS rebinding check of 07 §3.8: a no-cors request to the public name. The request ends when the browser
// resolves the name and trusts the certificate; else fetch throws a TypeError.
export async function rebindingCheck(
  url: string,
  fetchFn: typeof fetch = fetch,
  timeoutMs = 8000,
): Promise<RebindResult> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    await fetchFn(url, { mode: "no-cors", cache: "no-store", signal: ctrl.signal });
    return "ok";
  } catch {
    return "blocked";
  } finally {
    clearTimeout(timer);
  }
}

// The kinds of the Phase 6 jobs.
export const CONNECTIVITY_KINDS = ["tls-public-enrol", "tls-tailscale-enrol", "network-wifi"] as const;
