// HTTP helpers of the mock: problem+json, JSON bodies, cookies, SSE.

export const PROBLEM_BASE = "https://care-pi.rithviknishad.dev/problems/";

const TYPES = {
  "malformed-request": [400, "The request is malformed"],
  "invalid-request": [422, "The request is not valid"],
  "invalid-config": [422, "The config is not valid"],
  "unsupported-config": [422, "This version cannot apply the config"],
  "weak-password": [422, "The password is too weak"],
  "host-check-failed": [422, "A required host check failed"],
  "not-authenticated": [401, "Log in first"],
  "bad-credentials": [401, "The credentials are not correct"],
  "totp-required": [401, "A TOTP code is necessary"],
  csrf: [403, "The CSRF token or the origin is not correct"],
  "reauth-required": [403, "Enter the password again"],
  "not-found": [404, "The resource does not exist"],
  "job-not-found": [404, "The job does not exist"],
  "unit-not-found": [404, "The unit does not exist"],
  "not-claimed": [409, "The device is not claimed"],
  "already-claimed": [409, "The device is already claimed"],
  "setup-incomplete": [409, "Setup is not complete"],
  "setup-finished": [409, "Setup is finished"],
  "step-not-skippable": [409, "The step cannot be skipped"],
  "job-running": [409, "A job runs now"],
  "job-not-cancellable": [409, "The job cannot be cancelled"],
  "care-user-exists": [409, "The Care user exists"],
  "state-conflict": [409, "The state does not allow this operation"],
  "config-changed": [412, "The config changed"],
  "payload-too-large": [413, "The body is too large"],
  "unsupported-media-type": [415, "The media type is not supported"],
  "precondition-required": [428, "An If-Match header is necessary"],
  "rate-limited": [429, "Too many requests"],
  "locked-out": [429, "Too many failed attempts"],
  internal: [500, "Internal error"],
  "not-implemented-in-this-version": [501, "This version does not have the feature"],
  "care-not-running": [503, "Care does not run"],
  unavailable: [503, "The agent is not ready"],
};

export class Problem extends Error {
  constructor(slug, detail, extra = {}) {
    super(detail);
    this.slug = slug;
    this.detail = detail;
    this.extra = extra;
  }
}

export function sendProblem(res, req, slug, detail, extra = {}) {
  const [status, title] = TYPES[slug] ?? [500, "Internal error"];
  const body = {
    type: PROBLEM_BASE + slug,
    title,
    status,
    detail,
    instance: req.url.split("?")[0],
    ...extra,
  };
  const headers = { "Content-Type": "application/problem+json", "Cache-Control": "no-store" };
  if (extra.retry_after_s) headers["Retry-After"] = String(extra.retry_after_s);
  res.writeHead(status, headers);
  res.end(JSON.stringify(body));
}

export function sendJson(res, status, body, headers = {}) {
  res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store", ...headers });
  res.end(JSON.stringify(body));
}

export function sendEmpty(res, status = 204, headers = {}) {
  res.writeHead(status, { "Cache-Control": "no-store", ...headers });
  res.end();
}

export function readBody(req, limit = 1024 * 1024) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on("data", (c) => {
      size += c.length;
      if (size <= limit) chunks.push(c);
    });
    req.on("end", () => {
      if (size > limit) reject(new Problem("payload-too-large", "The body is larger than 1 MiB."));
      else resolve(Buffer.concat(chunks).toString("utf8"));
    });
    req.on("error", reject);
  });
}

export function parseCookies(header = "") {
  const out = {};
  for (const part of header.split(";")) {
    const i = part.indexOf("=");
    if (i > 0) out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}

// The mock serves plain HTTP, the same as `care-pi serve --insecure-dev`. That listener uses the cookie
// care_pi_session without Secure, because WebKit (Safari) keeps no Secure cookie from http://localhost.
// The agent on a device uses __Host-care_pi_session with Secure (docs/api/README.md section 2.5).
export const COOKIE = "care_pi_session";
const ATTRS = "Path=/; HttpOnly; SameSite=Strict";

export function sessionCookie(id) {
  return `${COOKIE}=${id}; Max-Age=86400; ${ATTRS}`;
}

export function clearCookie() {
  return `${COOKIE}=; Max-Age=0; ${ATTRS}`;
}

// Opens an SSE response. The caller sends the events and closes the stream.
export function openSse(res, heartbeatMs = 15000) {
  res.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-store",
    Connection: "keep-alive",
  });
  res.write("retry: 3000\n\n");
  const heartbeat = setInterval(() => {
    res.write(`event: heartbeat\ndata: ${JSON.stringify({ time: new Date().toISOString() })}\n\n`);
  }, heartbeatMs);
  res.on("close", () => clearInterval(heartbeat));
  return {
    send(id, event, data) {
      res.write(`${id === null ? "" : `id: ${id}\n`}event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
    },
    close() {
      clearInterval(heartbeat);
      res.end();
    },
  };
}

export function clientAddress(req) {
  const fwd = req.headers["x-forwarded-for"];
  return (typeof fwd === "string" ? fwd : (req.socket.remoteAddress ?? "127.0.0.1")).split(",")[0].trim();
}
