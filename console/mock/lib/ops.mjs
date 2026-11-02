// Jobs, logs, support bundle, and TLS routes of the mock.

import { gzipSync } from "node:zlib";
import { createHash } from "node:crypto";
import { Problem, openSse, sendJson } from "./http.mjs";
import { SUPPORT_STEPS, startJob, summary } from "./jobs.mjs";
import { FINGERPRINT, now } from "./state.mjs";
import { currentRoot } from "./connectivity.mjs";

const UNITS = {
  api: "care-api.service",
  worker: "care-worker.service",
  beat: "care-beat.service",
  db: "care-db.service",
  redis: "care-redis.service",
  s3: "care-s3.service",
  caddy: "care-caddy.service",
  pod: "care-pod.service",
  clock: "care-clock.service",
  "fe-build": "care-fe-build.service",
  target: "care.target",
  agent: "care-pi.service",
  backup: "care-backup-*.service",
};

const MESSAGES = {
  api: [
    "[INFO] Booting worker with pid: {n}",
    "GET /api/v1/users/getcurrentuser/ 200 41ms",
    "GET /api/v1/facility/ 200 63ms",
    "POST /api/v1/auth/login/ 200 212ms",
  ],
  worker: [
    "celery@care ready.",
    "Task care.facility.tasks.summarisation succeeded in 0.42s",
    "Received task: retrieve_asset_config",
  ],
  beat: ["Scheduler: Sending due task summarise_patient", "beat: Starting..."],
  db: [
    "checkpoint starting: time",
    "checkpoint complete: wrote 12 buffers",
    "archived write-ahead log file 0000000100000000000000{n}",
  ],
  redis: ["DB saved on disk", "1 changes in 3600 seconds. Saving..."],
  s3: ["PUT /patient-bucket/{n}.jpg 200", "GET /facility-bucket/cover.png 200"],
  caddy: ['{"level":"info","msg":"handled request","status":200,"uri":"/api/v1/facility/"}'],
  pod: ["pod care started"],
  clock: ["clock gate passed: ntp synchronized"],
  "fe-build": ["npm ci: added 1456 packages", "vite v8.0.3 building for production..."],
  target: ["Reached target care.target."],
  agent: ["GET /api/v1/status 200 3ms", "session: login from 192.168.1.23", "apply: plan has 0 changes"],
  backup: [
    "care-backup-db@usb.service: snapshot 4f1e9a2c saved (db), 1.0 MiB added",
    "care-backup-db@b2.service: Fatal: unable to open repository: dial tcp: no such host",
    "care-backup-db-incr.service: backup 20261026-010002F_20261030-020003I (incr) completed",
  ],
};

let cursorSeq = 0x1a2b;

function line(unit, i, time = new Date()) {
  const msgs = MESSAGES[unit];
  const n = cursorSeq++;
  return {
    time: time.toISOString().replace(/\.\d{3}Z$/, "Z"),
    cursor: `s=8c1f;i=${n.toString(16)};b=77e0;m=11;t=62a1;x=4f`,
    priority: i % 17 === 0 ? 4 : i % 41 === 0 ? 3 : 6,
    unit: UNITS[unit],
    message: msgs[i % msgs.length].replace("{n}", String(1000 + (i % 900))),
  };
}

function backlog(unit, n) {
  const start = Date.now() - n * 7000;
  return Array.from({ length: n }, (_, i) => line(unit, i, new Date(start + i * 7000)));
}

function streamJob(ctx, entry) {
  const { req, res, query } = ctx;
  const header = req.headers["last-event-id"];
  const after = Number(header ?? query.get("after") ?? 0) || 0;
  const sse = openSse(res);
  for (const ev of entry.events) if (ev.id > after) sse.send(ev.id, ev.event, ev.data);
  if (entry.job.state !== "running" && entry.job.state !== "queued") {
    sse.close();
    return;
  }
  const listener = (ev) => {
    sse.send(ev.id, ev.event, ev.data);
    if (ev.event === "done") {
      entry.listeners.delete(listener);
      sse.close();
    }
  };
  entry.listeners.add(listener);
  res.on("close", () => entry.listeners.delete(listener));
}

function jobEntry(state, id) {
  const entry = state.jobs.get(id);
  if (!entry) throw new Problem("job-not-found", `No job has the id ${id}.`);
  return entry;
}

const PEM = `-----BEGIN CERTIFICATE-----
MIIBozCCAUmgAwIBAgIQMockCareRootCAforConsoleMAoGCCqGSM49BAMCMDAx
LjAsBgNVBAMTJWNhcmUtcGkgY2FyZS5sb2NhbCBSb290IENBIChtb2NrIG9ubHkp
-----END CERTIFICATE-----
`;

// The keys, the order, and the parameters of the agent (agent/internal/service/tls.go, Instructions).
const STEPS = {
  ios: [
    ["ios.download"],
    ["ios.open-settings"],
    ["ios.install-profile"],
    ["compare-fingerprint", "fingerprint"],
    ["ios.trust-settings", "name"],
  ],
  android: [
    ["android.download", "filename"],
    ["compare-fingerprint", "fingerprint"],
    ["android.open-settings"],
    ["android.install-anyway", "filename"],
  ],
  windows: [
    ["windows.download", "filename"],
    ["compare-fingerprint", "fingerprint"],
    ["windows.open"],
    ["windows.store"],
    ["windows.confirm"],
  ],
  macos: [
    ["macos.download", "filename"],
    ["compare-fingerprint", "fingerprint"],
    ["macos.open"],
    ["macos.trust", "name"],
    ["macos.confirm"],
  ],
  linux: [["linux.download", "filename"], ["compare-fingerprint", "fingerprint"], ["linux.system"], ["linux.browser"]],
  chromeos: [
    ["chromeos.download", "filename"],
    ["compare-fingerprint", "fingerprint"],
    ["chromeos.open-settings"],
    ["chromeos.import", "filename"],
  ],
};
const NOTES = {
  ios: [["ios.ipados"]],
  android: [["android.vendor-path"], ["android.firefox"]],
  windows: [["windows.restart-browser"]],
};

function instructions(prefix, os, state) {
  if (!STEPS[os])
    throw new Problem("invalid-request", "Unknown OS.", {
      errors: [{ pointer: "", message: "not one of the OS values", code: "enum", parameter: "os" }],
    });
  const apple = os === "ios";
  const pem = os === "linux";
  const now = currentRoot(state);
  const filename = apple
    ? "care-pi-care-root-ca.mobileconfig"
    : pem
      ? "care-pi-care-root-ca.pem"
      : "care-pi-care-root-ca.crt";
  const params = { name: "care-pi care.local Root CA", filename, fingerprint: now.colon };
  const pick = (keys) => Object.fromEntries(keys.map((k) => [k, params[k]]));
  return {
    os,
    root: {
      fingerprint_sha256: now.fingerprint,
      fingerprint_sha256_colon: now.colon,
      filename,
      download_path: apple
        ? `${prefix}/tls/root-ca/mobileconfig`
        : `${prefix}/tls/root-ca?format=${pem ? "pem" : "der"}`,
      media_type: apple
        ? "application/x-apple-aspen-config"
        : pem
          ? "application/x-pem-file"
          : "application/x-x509-ca-cert",
    },
    steps: STEPS[os].map(([k, ...keys]) => ({ key: `tls.install.${k}`, params: pick(keys) })),
    notes: [...(NOTES[os] ?? []), ["common.one-certificate-per-pi"]].map(([k]) => ({
      key: `tls.note.${k}`,
      params: {},
    })),
  };
}

function rootCa({ res, query }) {
  const pem = query.get("format") === "pem";
  const body = pem ? Buffer.from(PEM) : Buffer.from(PEM.split("\n").slice(1, -2).join(""), "base64");
  res.writeHead(200, {
    "Content-Type": pem ? "application/x-pem-file" : "application/x-x509-ca-cert",
    "Content-Disposition": `attachment; filename="care-pi-care-root-ca.${pem ? "pem" : "crt"}"`,
    "X-Care-Pi-Fingerprint-SHA256": FINGERPRINT,
    "Cache-Control": "public, max-age=300",
  });
  res.end(body);
}

function mobileconfig({ res }) {
  res.writeHead(200, {
    "Content-Type": "application/x-apple-aspen-config",
    "Content-Disposition": 'attachment; filename="care-pi-care-root-ca.mobileconfig"',
    "X-Care-Pi-Fingerprint-SHA256": FINGERPRINT,
    "Cache-Control": "public, max-age=300",
  });
  res.end(
    `<?xml version="1.0" encoding="UTF-8"?>\n<plist version="1.0"><dict><key>PayloadDisplayName</key><string>care-pi care.local Root CA (mock)</string></dict></plist>\n`,
  );
}

export const opsRoutes = [
  {
    method: "GET",
    path: "/api/v1/jobs",
    handler: ({ res, state, query }) => {
      const kind = query.get("kind");
      const st = query.get("state");
      const limit = Number(query.get("limit") ?? 20);
      const items = state.jobOrder
        .map((id) => state.jobs.get(id).job)
        .filter((j) => (!kind || j.kind === kind) && (!st || j.state === st))
        .slice(0, limit)
        .map(summary);
      sendJson(res, 200, { items, next_before: null });
    },
  },
  {
    method: "GET",
    path: "/api/v1/jobs/:id",
    handler: ({ res, state, params }) => sendJson(res, 200, jobEntry(state, params.id).job),
  },
  {
    method: "GET",
    path: "/api/v1/jobs/:id/events",
    handler: (ctx) => {
      const entry = jobEntry(ctx.state, ctx.params.id);
      if ((ctx.req.headers.accept ?? "").includes("text/event-stream")) {
        streamJob(ctx, entry);
        return;
      }
      const after = Number(ctx.query.get("after") ?? 0) || 0;
      const events = entry.events.filter((e) => e.id > after).slice(0, 1000);
      sendJson(ctx.res, 200, {
        job_id: entry.job.id,
        events,
        last_event_id: events.at(-1)?.id ?? after,
        done: entry.job.state !== "running" && entry.job.state !== "queued",
      });
    },
  },
  {
    method: "POST",
    path: "/api/v1/jobs/:id/cancel",
    handler: ({ res, state, params }) => {
      const entry = jobEntry(state, params.id);
      if (!entry.job.cancellable || entry.job.state !== "running")
        throw new Problem("job-not-cancellable", "The job cannot be cancelled, or it ended.");
      entry.cancelled = true;
      sendJson(res, 202, entry.job, { Location: `/api/v1/jobs/${entry.job.id}` });
    },
  },
  {
    method: "GET",
    path: "/api/v1/logs",
    handler: ({ res }) => sendJson(res, 200, { items: Object.entries(UNITS).map(([name, unit]) => ({ name, unit })) }),
  },
  {
    method: "GET",
    path: "/api/v1/logs/:unit",
    handler: ({ req, res, params, query, state }) => {
      const unit = params.unit;
      if (!UNITS[unit]) {
        throw new Problem("invalid-request", "Unknown unit.", {
          errors: [{ pointer: "", message: "not a unit", code: "enum", parameter: "unit" }],
        });
      }
      if (unit === "fe-build" && state.setup.state !== "ready")
        throw new Problem("unit-not-found", "care-fe-build.service has no journal on this host.");
      const n = Math.min(5000, Math.max(1, Number(query.get("n") ?? 100)));
      const lines = backlog(unit, n);
      if (query.get("follow") === "true" && (req.headers.accept ?? "").includes("text/event-stream")) {
        const sse = openSse(res);
        if (!req.headers["last-event-id"]) for (const l of lines) sse.send(l.cursor, "log", l);
        let i = n;
        const timer = setInterval(
          () => {
            const l = line(unit, i++);
            sse.send(l.cursor, "log", l);
          },
          Number(process.env.MOCK_LOG_MS ?? 1000),
        );
        res.on("close", () => clearInterval(timer));
        return;
      }
      sendJson(res, 200, { unit, lines, next_cursor: lines.at(-1)?.cursor ?? null });
    },
  },
  {
    method: "POST",
    path: "/api/v1/support-bundle",
    handler: ({ res, state }) => {
      const job = startJob(state, {
        kind: "support-bundle",
        steps: SUPPORT_STEPS,
        cancellable: true,
        holdsLock: false,
        result: (_st, j) => {
          const id = `${j.id.replace(/-support-bundle$/, "")}-support-bundle`;
          const data = bundleBytes(id);
          state.bundles = { ...(state.bundles ?? {}), [id]: data };
          return {
            kind: "support-bundle",
            bundle_id: id,
            filename: `care-pi-support-${id}.tar.gz`,
            size_bytes: data.length,
            sha256: createHash("sha256").update(data).digest("hex"),
            expires_at: new Date(Date.now() + 24 * 3600 * 1000).toISOString().replace(/\.\d{3}Z$/, "Z"),
            download_path: `/api/v1/support-bundle/${id}`,
          };
        },
      });
      sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
    },
  },
  {
    method: "GET",
    path: "/api/v1/support-bundle/:id",
    handler: ({ res, state, params }) => {
      const data = state.bundles?.[params.id];
      if (!data) throw new Problem("not-found", `No bundle has the id ${params.id}.`);
      res.writeHead(200, {
        "Content-Type": "application/gzip",
        "Content-Disposition": `attachment; filename="care-pi-support-${params.id}.tar.gz"`,
        "Cache-Control": "no-store",
      });
      res.end(data);
    },
  },
  { method: "GET", path: "/api/v1/tls/root-ca", public: true, handler: rootCa },
  { method: "GET", path: "/_care-pi/tls/root-ca", public: true, handler: rootCa },
  { method: "GET", path: "/api/v1/tls/root-ca/mobileconfig", public: true, handler: mobileconfig },
  { method: "GET", path: "/_care-pi/tls/root-ca/mobileconfig", public: true, handler: mobileconfig },
  {
    method: "GET",
    path: "/api/v1/tls/instructions/:os",
    public: true,
    handler: ({ res, params, state }) => sendJson(res, 200, instructions("/api/v1", params.os, state)),
  },
  {
    method: "GET",
    path: "/_care-pi/tls/instructions/:os",
    public: true,
    handler: ({ res, params, state }) => sendJson(res, 200, instructions("/_care-pi", params.os, state)),
  },
];

function bundleBytes(id) {
  return gzipSync(Buffer.from(`care-pi support bundle ${id} (mock)\ngenerated ${now()}\n`));
}

// Operations of a later phase answer 501 after the auth checks. Phase 3 is in lib/backups-api.mjs and
// lib/restore-api.mjs; Phase 4 is in lib/updates.mjs (reset option phase4: false gives 501 there).
// Phase 6 is in lib/connectivity.mjs.
export const phaseRoutes = [["PUT", "/api/v1/security/ssh", 7]].map(([method, path, phase]) => ({
  method,
  path,
  phase,
  handler: () => {},
}));
