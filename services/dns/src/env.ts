export interface Env {
  DB: D1Database;
  // Cloudflare API token with "Zone - DNS - Edit" on the zone ZONE_ID only. Secret.
  CF_DNS_API_TOKEN?: string;
  ZONE_ID: string;
  ZONE_NAME: string;
  // Clinic names are "<id>.<BASE_DOMAIN>". The Worker serves https://<BASE_DOMAIN>.
  BASE_DOMAIN: string;
  // The acme-dns server_url that the service gives to the Pi.
  SERVER_URL: string;
  RECORD_TTL: string;
  RATE_LIMITS?: string;
  ADDRESS_POLICY?: string;
  // Local development only: another base URL for the Cloudflare API (scripts/fake-cf-api.mjs).
  CF_API_BASE?: string;
}

export function recordTtl(env: Env): number {
  const ttl = Number.parseInt(env.RECORD_TTL ?? "", 10);
  return Number.isFinite(ttl) && ttl >= 60 ? ttl : 60;
}

export function clinicName(env: Env, id: string): string {
  return `${id}.${env.BASE_DOMAIN}`;
}

export function challengeName(env: Env, id: string): string {
  return `_acme-challenge.${id}.${env.BASE_DOMAIN}`;
}
