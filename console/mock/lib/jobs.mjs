// Jobs of the mock: a journal with steps, an event list, and live SSE listeners.
// A job runs one step each state.stepMs milliseconds (default 1 s).

import { now } from "./state.mjs";

const LOG_LINES = {
  // One line for each network resolution, as "care-pi apply" prints them (agent/internal/lock).
  "resolve-lock": [
    "resolve docker.io/library/node:22-bookworm-slim … 4b150b48b411",
    "resolve docker.io/library/redis:8-alpine … 21090af93f83",
    "resolve docker.io/versity/versitygw:latest … 89cad13cc981",
    "resolve ghcr.io/ohcnetwork/care:latest … 77fe743e51a6",
    "resolve care_fe develop … 44f07eaa8573",
  ],
  "pull-images": [
    "Trying to pull ghcr.io/ohcnetwork/care@sha256:5e0c3b2a…",
    "Copying blob 1f2e3d4c done",
    "Writing manifest to image destination",
  ],
  "ensure-fe": [
    "npm ci: added 1456 packages",
    "vite v8.0.3 building for production...",
    "✓ 4231 modules transformed.",
    "build 8d2f0c1a9b7e: 13 MB",
  ],
  "start-target": [
    "care-db.service: database system is ready to accept connections",
    "care-beat.service: Running migrations: 312 of 312 applied",
  ],
  "wait-healthy": ["waiting for care-api.service to become healthy", "care-api.service is healthy"],
  "create-care-superuser": ["Superuser created successfully."],
};

// The error of a failed step, as the agent writes it (lumine, wave 4c: the stanza step of the first setup).
const STEP_ERRORS = {
  "ensure-stanza":
    "podman exec --user postgres care-db pg_isready --quiet: exit status 255: Error: unable to find user postgres: no matching entries in passwd file",
  "wait-healthy": "care-api.service did not become healthy in 10m0s.",
};

export function jobId(kind) {
  const d = new Date();
  const p = (n, w = 2) => String(n).padStart(w, "0");
  const stamp = `${d.getUTCFullYear()}${p(d.getUTCMonth() + 1)}${p(d.getUTCDate())}T${p(d.getUTCHours())}${p(d.getUTCMinutes())}${p(d.getUTCSeconds())}.${p(d.getUTCMilliseconds(), 3)}Z`;
  return `${stamp}-${kind}`;
}

export function summary(job) {
  const running = job.steps.find((s) => s.state === "running");
  return {
    id: job.id,
    kind: job.kind,
    state: job.state,
    source: job.source,
    created_at: job.created_at,
    started_at: job.started_at,
    finished_at: job.finished_at,
    step: running?.name ?? null,
    error_title: job.error?.title ?? null,
  };
}

export function runningHostJob(state) {
  for (const id of state.jobOrder) {
    const entry = state.jobs.get(id);
    if (entry.holdsLock && (entry.job.state === "running" || entry.job.state === "queued")) return entry.job;
  }
  return null;
}

// spec: { kind, steps: [[action, name, {progress?: unit}]], cancellable, holdsLock, result(state), onDone(state, ok) }
export function startJob(state, spec) {
  const id = jobId(spec.kind);
  const job = {
    id,
    kind: spec.kind,
    state: "running",
    source: "console",
    reason: spec.reason ?? null,
    created_at: now(),
    started_at: now(),
    finished_at: null,
    cancellable: Boolean(spec.cancellable),
    steps: [],
    error: null,
    result: null,
    last_event_id: 0,
  };
  const entry = { job, events: [], listeners: new Set(), holdsLock: spec.holdsLock ?? true, cancelled: false };
  state.jobs.set(id, entry);
  state.jobOrder.unshift(id);

  const emit = (event, data) => {
    const ev = { id: ++job.last_event_id, event, data };
    entry.events.push(ev);
    for (const l of entry.listeners) l(ev);
  };

  let index = 0;
  const finish = (ok, problem) => {
    job.state = entry.cancelled ? "cancelled" : ok ? "succeeded" : "failed";
    job.finished_at = now();
    job.error = problem ?? null;
    job.result = ok && spec.result ? spec.result(state, job) : null;
    spec.onDone?.(state, ok, job);
    emit("done", structuredClone(job));
  };

  const runStep = () => {
    if (entry.cancelled) {
      finish(false, null);
      return;
    }
    if (index >= spec.steps.length) {
      finish(true);
      return;
    }
    const [action, name, opts = {}] = spec.steps[index];
    const step = { index, name, action, state: "running", started_at: now(), finished_at: null, detail: null };
    job.steps[index] = step;
    emit("step", { ...step });
    const lines = LOG_LINES[action] ?? [`${name}: start`];
    let tick = 0;
    const ticks = Math.max(lines.length, opts.progress ? 4 : 1);
    const timer = setInterval(
      () => {
        if (tick < lines.length) {
          emit("log", { time: now(), step_index: index, stream: opts.stream ?? "agent", line: lines[tick] });
        }
        if (opts.progress) {
          const total = opts.progress === "bytes" ? 1_900_000_000 : opts.progress === "items" ? 7 : 100;
          emit("progress", {
            step_index: index,
            current: Math.round((total * (tick + 1)) / ticks),
            total,
            unit: opts.progress,
            message: opts.progress === "items" ? `${tick + 1} of 7 images` : null,
          });
        }
        tick++;
        if (tick >= ticks) {
          clearInterval(timer);
          const failed = state.failStep && state.failStep === action;
          const error = STEP_ERRORS[action] ?? `${action}: exit status 1`;
          step.state = failed ? "failed" : "succeeded";
          step.finished_at = now();
          step.detail = failed ? error : null;
          emit("step", { ...step });
          if (failed) {
            state.failStep = null;
            finish(false, {
              type: "https://care-pi.rithviknishad.dev/problems/internal",
              title: "Internal error",
              status: 500,
              detail: `apply failed at ${action}: ${error}`,
              instance: `/api/v1/jobs/${id}`,
            });
            return;
          }
          index++;
          setTimeout(runStep, 10);
        }
      },
      Math.max(20, Math.floor(state.stepMs / ticks)),
    );
  };
  setTimeout(runStep, 20);
  return job;
}

// The steps of the setup job of the agent (lumine, wave 4c, with the resolve-lock step of wave 4d).
export const SETUP_STEPS = [
  ["validate", "validate: config.yaml"],
  ["host-check", "host-check: required checks"],
  ["resolve-lock", "resolve-lock: image digests and git refs"],
  ["ensure-dirs", "ensure-dirs"],
  ["write-files", "write-files: 15 file(s)"],
  ["daemon-reload", "daemon-reload"],
  ["enable-target", "enable-target: care-start.service"],
  [
    "pull-images",
    "pull-images: localhost/care-pi/caddy:2 ghcr.io/ohcnetwork/care@sha256:77fe743e51a6eb322e89e18183c6012fa6ab8efdd447e8ff59dfb387e6da6ec3 docker.io/library/redis@sha256:21090af93f8324db2f0f0f4f38a7c60369d47d917e000c3423d89d2ae2883537",
    { progress: "items" },
  ],
  [
    "full-cycle",
    "full-cycle: care.target (care-db unit changed; care-redis unit changed; care-s3 unit changed; pod changed)",
  ],
  ["ensure-buckets", "ensure-buckets"],
  ["ensure-stanza", "ensure-stanza"],
  ["ensure-fe", "ensure-fe", { progress: "percent", stream: "stdout" }],
  ["write-lock", "write-lock"],
  ["wait-healthy", "wait-healthy: care-api.service"],
  ["create-care-superuser", "create-care-superuser: admin"],
];

export const APPLY_STEPS = [
  ["write-files", "write-files: 2 file(s)"],
  ["daemon-reload", "daemon-reload"],
  ["restart", "restart: care-api.service"],
  ["reload-caddy", "reload-caddy"],
  ["write-lock", "write-lock"],
];

export const FE_BUILD_STEPS = [
  ["scale-down", "scale-down: solo, care-worker stopped"],
  ["ensure-fe", "ensure-fe: build 8d2f0c1a9b7e", { progress: "percent", stream: "stdout" }],
  ["scale-up", "scale-up: small-clinic"],
];

export const SUPPORT_STEPS = [
  ["collect", "collect: status, host check, config"],
  ["journal", "journal: 12 unit(s)"],
  ["redact", "redact: secrets"],
  ["archive", "archive: tar.gz"],
];
