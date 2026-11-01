// A small client for the Cloudflare DNS records API (https://developers.cloudflare.com/api/resources/dns/subresources/records/).
// The token needs "Zone - DNS - Edit" on one zone only.

export type RecordType = "A" | "AAAA" | "TXT";

export interface DnsRecord {
  id: string;
  type: string;
  name: string;
  content: string;
  ttl: number;
  proxied?: boolean;
  comment?: string | null;
}

export interface RecordInput {
  type: RecordType;
  name: string;
  content: string;
  ttl: number;
  comment?: string;
}

interface Envelope<T> {
  success: boolean;
  errors?: { code: number; message: string }[];
  result: T;
}

export class CfDnsError extends Error {
  constructor(
    readonly status: number,
    readonly codes: number[],
    message: string,
  ) {
    super(message);
  }

  // The record is not there (deleted by hand, or never created).
  get notFound(): boolean {
    return this.status === 404 || this.codes.includes(81044);
  }
}

export type FetchFn = (input: string, init: RequestInit) => Promise<Response>;

export interface CfDnsOptions {
  token: string;
  zoneId: string;
  fetch?: FetchFn;
  baseUrl?: string;
  timeoutMs?: number;
}

export const CF_API_BASE = "https://api.cloudflare.com/client/v4";

export class CfDns {
  private readonly token: string;
  private readonly zoneId: string;
  private readonly fetchFn: FetchFn;
  private readonly baseUrl: string;
  private readonly timeoutMs: number;

  constructor(opts: CfDnsOptions) {
    this.token = opts.token;
    this.zoneId = opts.zoneId;
    // Look up the global fetch at call time, so that tests can replace it.
    this.fetchFn = opts.fetch ?? ((input, init) => fetch(input, init));
    this.baseUrl = opts.baseUrl ?? CF_API_BASE;
    this.timeoutMs = opts.timeoutMs ?? 10_000;
  }

  private async call<T>(method: string, path: string, body?: unknown): Promise<T> {
    const url = `${this.baseUrl}/zones/${encodeURIComponent(this.zoneId)}/dns_records${path}`;
    const init: RequestInit = {
      method,
      headers: { authorization: `Bearer ${this.token}`, "content-type": "application/json" },
      signal: AbortSignal.timeout(this.timeoutMs),
    };
    if (body !== undefined) init.body = JSON.stringify(body);
    const res = await this.fetchFn(url, init);
    let payload: Envelope<T> | null = null;
    try {
      payload = (await res.json()) as Envelope<T>;
    } catch {
      payload = null;
    }
    if (!res.ok || payload === null || payload.success !== true) {
      const errors = payload?.errors ?? [];
      const message = errors.map((e) => `${e.code}: ${e.message}`).join("; ") || `HTTP ${res.status}`;
      throw new CfDnsError(res.status, errors.map((e) => e.code), `Cloudflare DNS API ${method} failed: ${message}`);
    }
    return payload.result;
  }

  private static body(input: RecordInput): Record<string, unknown> {
    const out: Record<string, unknown> = { type: input.type, name: input.name, content: input.content, ttl: input.ttl };
    // A and AAAA records hold LAN addresses: never proxy them.
    if (input.type !== "TXT") out.proxied = false;
    if (input.comment !== undefined) out.comment = input.comment;
    return out;
  }

  list(type: RecordType, name: string): Promise<DnsRecord[]> {
    const q = new URLSearchParams({ type, "name.exact": name, per_page: "100" });
    return this.call<DnsRecord[]>("GET", `?${q.toString()}`);
  }

  create(input: RecordInput): Promise<DnsRecord> {
    return this.call<DnsRecord>("POST", "", CfDns.body(input));
  }

  update(id: string, input: RecordInput): Promise<DnsRecord> {
    return this.call<DnsRecord>("PATCH", `/${encodeURIComponent(id)}`, CfDns.body(input));
  }

  // Returns false if the record was not there.
  async delete(id: string): Promise<boolean> {
    try {
      await this.call<{ id: string }>("DELETE", `/${encodeURIComponent(id)}`);
      return true;
    } catch (err) {
      if (err instanceof CfDnsError && err.notFound) return false;
      throw err;
    }
  }

  // Makes sure that exactly one record of this type and name exists, with this content.
  // knownId is the record id from the database; the client falls back to a search by name.
  async upsert(input: RecordInput, knownId?: string | null): Promise<DnsRecord> {
    if (knownId) {
      try {
        return await this.update(knownId, input);
      } catch (err) {
        if (!(err instanceof CfDnsError && err.notFound)) throw err;
      }
    }
    const existing = await this.list(input.type, input.name);
    const [first, ...extra] = existing;
    for (const r of extra) await this.delete(r.id);
    if (first !== undefined) return this.update(first.id, input);
    return this.create(input);
  }

  // Deletes all records of this type and name. Returns the number of deleted records.
  async deleteByName(type: RecordType, name: string): Promise<number> {
    let n = 0;
    for (const r of await this.list(type, name)) {
      if (await this.delete(r.id)) n++;
    }
    return n;
  }
}
