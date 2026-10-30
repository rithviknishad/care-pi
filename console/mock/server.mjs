#!/usr/bin/env node
// The stateful mock of the care-pi agent API (docs/api/openapi.yaml).
// It implements the claim, the login, CSRF and Origin checks, the setup state machine,
// jobs with SSE events, logs with SSE follow, and If-Match on /config.
// Unknown routes go to Prism (PRISM_URL) when it runs; else they get 404 not-found.
//
//   node mock/server.mjs                 port 4011 (MOCK_PORT)
//   MOCK_SCENARIO=ready node mock/server.mjs
//   curl -X POST localhost:4011/__mock/reset -d '{"scenario":"ready","step_ms":200}'
//
// Credentials: claim code 4F7K-Q2MX-9PDA, Imager password "imager-password",
// console password of the ready scenario "correct horse battery staple", TOTP code 000000.

import http from "node:http";
import { Problem, clientAddress, parseCookies, readBody, sendJson, sendProblem, COOKIE } from "./lib/http.mjs";
import { authRoutes, reauthRecent } from "./lib/auth.mjs";
import { setupRoutes } from "./lib/setup.mjs";
import { systemRoutes } from "./lib/system.mjs";
import { opsRoutes, phaseRoutes } from "./lib/ops.mjs";
import { freshState, now } from "./lib/state.mjs";

const PORT = Number(process.env.MOCK_PORT ?? 4011);
const PRISM_URL = process.env.PRISM_URL ?? null;
let state = freshState({ scenario: process.env.MOCK_SCENARIO ?? "unclaimed" });

const PHASE_TEXT = {
  3: "Backups and restore come in Phase 3.",
  4: "Updates and plugs come in Phase 4.",
  6: "This feature comes in Phase 6.",
  7: "This feature comes in Phase 7.",
};

function compile(route) {
  const keys = [];
  const re = new RegExp(
    "^" +
      route.path.replace(/[.]/g, "\\.").replace(/:([a-z_]+)/g, (_m, k) => {
        keys.push(k);
        return "([^/]+)";
      }) +
      "$",
  );
  return { ...route, re, keys };
}

const ROUTES = [...systemRoutes, ...authRoutes, ...setupRoutes, ...opsRoutes, ...phaseRoutes].map(compile);
const UNSAFE = new Set(["POST", "PUT", "PATCH", "DELETE"]);

function match(method, pathname) {
  let pathOnly = false;
  for (const r of ROUTES) {
    const m = r.re.exec(pathname);
    if (!m) continue;
    if (r.method !== method) {
      pathOnly = true;
      continue;
    }
    return { route: r, params: Object.fromEntries(r.keys.map((k, i) => [k, decodeURIComponent(m[i + 1])])) };
  }
  return { route: null, pathOnly };
}

function sessionOf(req) {
  const id = parseCookies(req.headers.cookie)[COOKIE];
  const s = id ? state.sessions.get(id) : null;
  if (s) s.last_seen_at = now();
  return s ?? null;
}

// The agent accepts Origin https://<served host>:8443 only. The mock accepts the origin of the Host header.
function originOk(req) {
  const origin = req.headers.origin;
  if (!origin) return false;
  try {
    return new URL(origin).host === req.headers.host;
  } catch {
    return false;
  }
}

async function control(req, res, pathname) {
  if (pathname === "/__mock/reset" && req.method === "POST") {
    const raw = await readBody(req);
    const opts = raw ? JSON.parse(raw) : {};
    state = freshState(opts);
    sendJson(res, 200, { scenario: state.scenario, step_ms: state.stepMs });
    return true;
  }
  if (pathname === "/__mock/state" && req.method === "GET") {
    sendJson(res, 200, {
      scenario: state.scenario,
      setup_state: state.setup.state,
      claim_code: state.claimCode,
      recovery_codes: state.recoveryCodes,
      sessions: state.sessions.size,
      file: state.file,
    });
    return true;
  }
  return false;
}

async function forwardToPrism(req, res, url) {
  const body = UNSAFE.has(req.method) ? await readBody(req) : undefined;
  const target = new URL(url.pathname.replace(/^\/api\/v1/, "") + url.search, PRISM_URL);
  const headers = { ...req.headers };
  delete headers.host;
  const upstream = await fetch(target, { method: req.method, headers, body });
  res.writeHead(upstream.status, Object.fromEntries(upstream.headers));
  res.end(Buffer.from(await upstream.arrayBuffer()));
}

async function handle(req, res) {
  const url = new URL(req.url, `http://${req.headers.host ?? "localhost"}`);
  const pathname = url.pathname;
  if (await control(req, res, pathname)) return;

  const { route, params, pathOnly } = match(req.method, pathname);
  if (!route) {
    if (PRISM_URL && pathname.startsWith("/api/v1/")) return forwardToPrism(req, res, url);
    if (pathOnly) throw new Problem("malformed-request", `The method ${req.method} is not allowed here.`);
    throw new Problem("not-found", `No resource has the path ${pathname}.`);
  }

  const session = sessionOf(req);
  if (!route.public && !session)
    throw new Problem("not-authenticated", "The session cookie is not there, or the session ended.");

  let body = {};
  if (UNSAFE.has(req.method)) {
    if (!originOk(req)) throw new Problem("csrf", "The Origin header is not the origin of the console.");
    if (session && !route.public && req.headers["x-csrf-token"] !== session.csrf) {
      throw new Problem("csrf", "The X-CSRF-Token header is not there, or it is not correct.");
    }
    const type = (req.headers["content-type"] ?? "").split(";")[0].trim();
    const want = route.contentType ?? "application/json";
    if (type !== want) throw new Problem("unsupported-media-type", `Use Content-Type: ${want}.`);
    const raw = await readBody(req);
    if (raw.trim()) {
      try {
        body = JSON.parse(raw);
      } catch {
        throw new Problem("malformed-request", "The body is not valid JSON.");
      }
    }
  }
  if (route.reauth && !reauthRecent(session)) {
    throw new Problem(
      "reauth-required",
      "This operation needs an authentication in the last 5 minutes. Use POST /session/reauth.",
    );
  }
  if (route.phase) {
    throw new Problem("not-implemented-in-this-version", PHASE_TEXT[route.phase], { phase: route.phase });
  }
  await route.handler({ req, res, state, params, query: url.searchParams, body, session, address: clientAddress(req) });
}

const server = http.createServer((req, res) => {
  handle(req, res).catch((err) => {
    if (res.headersSent) {
      res.end();
      return;
    }
    if (err instanceof Problem) sendProblem(res, req, err.slug, err.detail, err.extra);
    else {
      console.error(err);
      sendProblem(res, req, "internal", String(err?.message ?? err));
    }
  });
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(
    `care-pi mock agent: http://127.0.0.1:${PORT} (scenario ${state.scenario}${PRISM_URL ? `, fallback ${PRISM_URL}` : ""})`,
  );
});

for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, () => {
    server.closeAllConnections();
    server.close(() => process.exit(0));
  });
}
