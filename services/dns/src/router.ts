// A small router: method + path pattern. HEAD uses the GET route.

import type { Env } from "./env";
import { callerIp, HttpError } from "./http";
import { networkKey } from "./ip";
import { nowSeconds } from "./encoding";
import { health, registerClosed, update } from "./handlers/acme";
import { deleteClinic, getNonce, getStatus, patchSettings, putAddress, register, rotate } from "./handlers/clinics";
import type { Ctx, Handler } from "./handlers/common";
import { lookup } from "./handlers/lookup";
import { certPage, home, STATIC_ASSETS } from "./handlers/pages";

interface Route {
  method: string;
  pattern: RegExp;
  keys: string[];
  handler: Handler;
}

function route(method: string, path: string, handler: Handler): Route {
  const keys: string[] = [];
  const source = path.replace(/:([a-z]+)/g, (_m, key: string) => {
    keys.push(key);
    return "([^/]+)";
  });
  return { method, pattern: new RegExp(`^${source}$`), keys, handler };
}

export const ROUTES: Route[] = [
  route("GET", "/", (ctx) => home(ctx.env)),
  route("GET", "/cert", () => certPage()),
  ...Object.entries(STATIC_ASSETS).map(([path, fn]) => route("GET", path, () => fn())),
  route("GET", "/health", () => health()),
  route("GET", "/lookup", lookup),
  // acme-dns compatible
  route("POST", "/update", update),
  route("POST", "/register", () => registerClosed()),
  // care-pi API
  route("GET", "/v1/nonce", getNonce),
  route("POST", "/v1/register", register),
  route("GET", "/v1/clinics/:id", getStatus),
  route("PATCH", "/v1/clinics/:id", patchSettings),
  route("DELETE", "/v1/clinics/:id", deleteClinic),
  route("PUT", "/v1/clinics/:id/address", putAddress),
  route("POST", "/v1/clinics/:id/rotate", rotate),
];

export async function dispatch(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  const method = request.method === "HEAD" ? "GET" : request.method;
  const allowed = new Set<string>();
  for (const r of ROUTES) {
    const m = r.pattern.exec(url.pathname);
    if (m === null) continue;
    allowed.add(r.method);
    if (r.method !== method) continue;
    const params: Record<string, string> = {};
    r.keys.forEach((k, i) => {
      let value = m[i + 1] ?? "";
      try {
        value = decodeURIComponent(value);
      } catch {
        // Keep the raw text: it fails the id check later.
      }
      params[k] = value;
    });
    const ip = callerIp(request);
    const ctx: Ctx = { request, env, now: nowSeconds(), ip, ipKey: networkKey(ip), params };
    return r.handler(ctx);
  }
  if (allowed.size > 0) {
    const allow = [...allowed, ...(allowed.has("GET") ? ["HEAD"] : [])].join(", ");
    throw new HttpError(405, "method_not_allowed", `Use ${allow} for this path.`, {}, { allow });
  }
  throw new HttpError(404, "not_found", "No such path. The API is in services/dns/API.md of the care-pi repository.");
}
