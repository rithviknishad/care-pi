// A fake Cloudflare DNS records API for local development (wrangler dev). It keeps the records in memory.
//
//   node scripts/fake-cf-api.mjs            # listens on 127.0.0.1:8788
//   echo 'CF_DNS_API_TOKEN=dev-token' > .dev.vars
//   npx wrangler dev --var CF_API_BASE:http://127.0.0.1:8788/client/v4
//
// GET /_records lists all records (for a quick look).

import { createServer } from "node:http";
import { randomUUID } from "node:crypto";

const port = Number(process.env.PORT ?? 8788);
const token = process.env.FAKE_CF_TOKEN ?? "dev-token";
const records = new Map();

function send(res, status, payload) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(payload));
}

const fail = (res, status, code, message) => send(res, status, { success: false, errors: [{ code, message }], result: null });

createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", `http://127.0.0.1:${port}`);
  if (url.pathname === "/_records") return send(res, 200, [...records.values()]);
  let raw = "";
  for await (const chunk of req) raw += chunk;
  const body = raw ? JSON.parse(raw) : null;
  const m = /^\/client\/v4\/zones\/([^/]+)\/dns_records(?:\/([^/]+))?$/.exec(url.pathname);
  if (!m) return fail(res, 404, 7003, "Could not route");
  if (req.headers.authorization !== `Bearer ${token}`) return fail(res, 403, 10000, "Authentication error");
  const id = m[2];
  console.log(req.method, url.pathname + url.search, body ? JSON.stringify(body) : "");
  if (req.method === "GET" && !id) {
    const type = url.searchParams.get("type");
    const name = url.searchParams.get("name.exact");
    return send(res, 200, { success: true, errors: [], result: [...records.values()].filter((r) => (!type || r.type === type) && (!name || r.name === name)) });
  }
  if (req.method === "POST" && !id) {
    const record = { ...body, id: randomUUID().replace(/-/g, "") };
    records.set(record.id, record);
    return send(res, 200, { success: true, errors: [], result: record });
  }
  const existing = id ? records.get(id) : undefined;
  if (!existing) return fail(res, 404, 81044, "Record does not exist.");
  if (req.method === "PATCH") {
    const record = { ...existing, ...body, id };
    records.set(id, record);
    return send(res, 200, { success: true, errors: [], result: record });
  }
  if (req.method === "DELETE") {
    records.delete(id);
    return send(res, 200, { success: true, errors: [], result: { id } });
  }
  return fail(res, 405, 10405, "Method not allowed");
}).listen(port, "127.0.0.1", () => console.log(`fake Cloudflare DNS API on http://127.0.0.1:${port}/client/v4`));
