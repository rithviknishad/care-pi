import type { Env } from "../env";

export interface Ctx {
  request: Request;
  env: Env;
  now: number;
  // The public IP address of the caller, and its network key (IPv4 address or IPv6 /64).
  ip: string;
  ipKey: string;
  params: Record<string, string>;
}

export type Handler = (ctx: Ctx) => Promise<Response> | Response;

export function log(event: string, fields: Record<string, unknown>): void {
  console.log(JSON.stringify({ event, ...fields }));
}
