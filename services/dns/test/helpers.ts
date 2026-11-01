// Test helpers: a fake Cloudflare DNS API, a signing client, and request builders.

import { env } from "cloudflare:workers";
import { vi } from "vitest";
import { canonicalString } from "../src/auth";
import { sha256Hex, toBase64, utf8 } from "../src/encoding";
import type { Env } from "../src/env";
import { handle } from "../src/index";

export interface FakeRecord {
  id: string;
  type: string;
  name: string;
  content: string;
  ttl: number;
  proxied?: boolean;
  comment?: string;
}

export interface FakeCall {
  method: string;
  url: string;
  body: Record<string, unknown> | null;
  authorization: string | null;
}

// An in-memory stand-in for https://api.cloudflare.com/client/v4/zones/{zone}/dns_records.
export class FakeCloudflare {
  records = new Map<string, FakeRecord>();
  calls: FakeCall[] = [];
  failures: { status: number; code: number; message: string }[] = [];
  private seq = 0;

  constructor(readonly zoneId: string) {}

  failNext(status: number, code: number, message = "injected failure"): void {
    this.failures.push({ status, code, message });
  }

  byName(type: string, name: string): FakeRecord[] {
    return [...this.records.values()].filter((r) => r.type === type && r.name === name);
  }

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url);
    const method = (init?.method ?? "GET").toUpperCase();
    const headers = new Headers(init?.headers);
    const body = typeof init?.body === "string" ? (JSON.parse(init.body) as Record<string, unknown>) : null;
    this.calls.push({ method, url: url.href, body, authorization: headers.get("authorization") });
    const reply = (status: number, payload: unknown) =>
      new Response(JSON.stringify(payload), { status, headers: { "content-type": "application/json" } });
    const failure = this.failures.shift();
    if (failure) return reply(failure.status, { success: false, errors: [{ code: failure.code, message: failure.message }], result: null });

    const prefix = `/client/v4/zones/${this.zoneId}/dns_records`;
    if (url.hostname !== "api.cloudflare.com" || !url.pathname.startsWith(prefix)) {
      return reply(404, { success: false, errors: [{ code: 7003, message: "Could not route" }], result: null });
    }
    if (headers.get("authorization") !== "Bearer test-token") {
      return reply(403, { success: false, errors: [{ code: 10000, message: "Authentication error" }], result: null });
    }
    const rest = url.pathname.slice(prefix.length);
    const id = rest.startsWith("/") ? decodeURIComponent(rest.slice(1)) : "";
    if (method === "GET" && id === "") {
      const type = url.searchParams.get("type");
      const name = url.searchParams.get("name.exact");
      const result = [...this.records.values()].filter((r) => (!type || r.type === type) && (!name || r.name === name));
      return reply(200, { success: true, errors: [], result });
    }
    if (method === "POST" && id === "" && body) {
      const dup = [...this.records.values()].find((r) => r.type === body.type && r.name === body.name && r.content === body.content);
      if (dup) return reply(400, { success: false, errors: [{ code: 81058, message: "An identical record already exists." }], result: null });
      const record = { ...(body as unknown as FakeRecord), id: `rec${++this.seq}-${crypto.randomUUID().slice(0, 8)}` };
      this.records.set(record.id, record);
      return reply(200, { success: true, errors: [], result: record });
    }
    const existing = this.records.get(id);
    if (!existing) return reply(404, { success: false, errors: [{ code: 81044, message: "Record does not exist." }], result: null });
    if (method === "PATCH" && body) {
      const record = { ...existing, ...(body as unknown as FakeRecord), id };
      this.records.set(id, record);
      return reply(200, { success: true, errors: [], result: record });
    }
    if (method === "DELETE") {
      this.records.delete(id);
      return reply(200, { success: true, errors: [], result: { id } });
    }
    return reply(405, { success: false, errors: [{ code: 10405, message: "Method not allowed" }], result: null });
  };

  install(): this {
    vi.spyOn(globalThis, "fetch").mockImplementation(this.fetch as typeof fetch);
    return this;
  }
}

export function testEnv(overrides: Partial<Env> = {}): Env {
  return { ...(env as unknown as Env), ...overrides };
}

// A random public IP address for each call, so that the rate limits of one test do not affect another test.
export function freshIp(): string {
  const b = crypto.getRandomValues(new Uint8Array(3));
  return `203.${b[0]}.${b[1]}.${(b[2] ?? 0) | 1}`;
}

export const ORIGIN = "https://care-pi.rithviknishad.dev";

export interface CallOptions {
  body?: string;
  headers?: Record<string, string>;
  ip?: string;
  env?: Env;
}

export async function call(method: string, path: string, opts: CallOptions = {}): Promise<Response> {
  const headers = new Headers(opts.headers);
  headers.set("cf-connecting-ip", opts.ip ?? "198.51.100.1");
  const init: RequestInit = { method, headers };
  if (opts.body !== undefined) init.body = opts.body;
  return handle(new Request(`${ORIGIN}${path}`, init), opts.env ?? testEnv());
}

export async function bodyOf<T = Record<string, unknown>>(res: Response): Promise<T> {
  return (await res.json()) as T;
}

export interface KeyPair {
  privateKey: CryptoKey;
  publicB64: string;
}

export async function newKey(): Promise<KeyPair> {
  const pair = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"])) as CryptoKeyPair;
  const raw = new Uint8Array((await crypto.subtle.exportKey("raw", pair.publicKey)) as ArrayBuffer);
  return { privateKey: pair.privateKey, publicB64: toBase64(raw) };
}

export async function signText(key: KeyPair, text: string): Promise<string> {
  return toBase64(new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, key.privateKey, utf8(text))));
}

export interface SignOptions {
  timestamp?: number;
  newKey?: KeyPair;
  // Sign this path instead of the request path (to test a mismatch).
  signedPath?: string;
  signedBody?: string;
}

// Two identical signed requests in the same second are a replay. The tests step the timestamp back by one
// second for each request, so that each request is new (the window is 300 seconds).
let tick = 0;

export async function signedHeaders(key: KeyPair, method: string, path: string, body: string, opts: SignOptions = {}): Promise<Record<string, string>> {
  const ts = String(opts.timestamp ?? Math.floor(Date.now() / 1000) - (tick++ % 240));
  const canonical = canonicalString(method, opts.signedPath ?? path, ts, await sha256Hex(opts.signedBody ?? body));
  const headers: Record<string, string> = {
    "content-type": "application/json",
    "x-care-pi-timestamp": ts,
    "x-care-pi-signature": await signText(key, canonical),
  };
  if (opts.newKey) headers["x-care-pi-signature-new"] = await signText(opts.newKey, canonical);
  return headers;
}

export async function signedCall(
  key: KeyPair,
  method: string,
  path: string,
  bodyValue: unknown,
  opts: SignOptions & { ip?: string; env?: Env } = {},
): Promise<Response> {
  const body = bodyValue === undefined ? "" : JSON.stringify(bodyValue);
  const headers = await signedHeaders(key, method, path, body, opts);
  const callOpts: CallOptions = { headers };
  if (body !== "") callOpts.body = body;
  if (opts.ip !== undefined) callOpts.ip = opts.ip;
  if (opts.env !== undefined) callOpts.env = opts.env;
  return call(method, path, callOpts);
}

export interface Registered {
  key: KeyPair;
  id: string;
  ip: string;
  acme: { username: string; password: string; subdomain: string; fulldomain: string; server_url: string };
  response: Record<string, unknown>;
}

export async function getNonce(ip: string, e?: Env): Promise<string> {
  const opts: CallOptions = { ip };
  if (e) opts.env = e;
  const res = await call("GET", "/v1/nonce", opts);
  if (res.status !== 200) throw new Error(`nonce: ${res.status} ${await res.text()}`);
  return (await bodyOf<{ nonce: string }>(res)).nonce;
}

export async function registerClinic(opts: { hint?: string; ip?: string; listing?: boolean; env?: Env } = {}): Promise<Registered> {
  const key = await newKey();
  const ip = opts.ip ?? freshIp();
  const nonce = await getNonce(ip, opts.env);
  const body: Record<string, unknown> = { hostname_hint: opts.hint ?? "Lumine Clinic", pubkey: key.publicB64, nonce };
  if (opts.listing !== undefined) body.listing_opt_in = opts.listing;
  const res = await signedCall(key, "POST", "/v1/register", body, opts.env ? { ip, env: opts.env } : { ip });
  if (res.status !== 201) throw new Error(`register: ${res.status} ${await res.text()}`);
  const response = await bodyOf(res);
  return { key, id: response.id as string, ip, acme: response.acme as Registered["acme"], response };
}

export function acmeUpdate(acme: { username: string; password: string }, body: unknown, ip = freshIp(), e?: Env): Promise<Response> {
  const opts: CallOptions = {
    ip,
    headers: { "x-api-user": acme.username, "x-api-key": acme.password, "content-type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  };
  if (e) opts.env = e;
  return call("POST", "/update", opts);
}

// A valid ACME DNS-01 value: base64url of 32 bytes.
export function txtValue(seed: number): string {
  const bytes = new Uint8Array(32).fill(seed & 0xff);
  return toBase64(bytes).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
