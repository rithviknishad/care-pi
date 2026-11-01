// care-pi DNS/ACME side service. Read docs/dns-service.md and services/dns/API.md.

import { CfDnsError } from "./cf-dns";
import { housekeeping } from "./cron";
import { nowSeconds } from "./encoding";
import type { Env } from "./env";
import { log } from "./handlers/common";
import { errorResponse, HttpError, json, withCommonHeaders } from "./http";
import { dispatch } from "./router";

export async function handle(request: Request, env: Env): Promise<Response> {
  let response: Response;
  try {
    response = await dispatch(request, env);
  } catch (err) {
    if (err instanceof HttpError) {
      response = errorResponse(err);
    } else if (err instanceof CfDnsError) {
      log("dns_error", { path: new URL(request.url).pathname, status: err.status, codes: err.codes, message: err.message });
      response = json(502, { error: "dns_error", message: "The Cloudflare DNS API failed. Try again later." });
    } else {
      console.error(JSON.stringify({ event: "internal_error", error: String(err), stack: err instanceof Error ? err.stack : undefined }));
      response = json(500, { error: "internal", message: "Internal error." });
    }
  }
  if (request.method === "HEAD") response = new Response(null, response);
  return withCommonHeaders(response);
}

export default {
  fetch(request, env): Promise<Response> {
    return handle(request, env);
  },
  scheduled(_controller, env, ctx): void {
    ctx.waitUntil(housekeeping(env, nowSeconds()));
  },
} satisfies ExportedHandler<Env>;
