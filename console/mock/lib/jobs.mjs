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
  checkpoint: "pgbackrest --stanza=care --type=incr backup: exit status 82: unable to find primary cluster",
  migrate: "care-beat.service did not become healthy. emr.0187 raised IntegrityError.",
  verify: "GET https://care.local/ping/ gave HTTP 502.",
  rollback: "care-api.service did not become healthy in 5m0s after the rollback.",
};

const PROBLEM_TITLES = {
  internal: "Internal error",
  "dns-service-unreachable": "The DNS service does not answer",
  "wifi-auth-failed": "The Wi-Fi network refused the passphrase",
  "tailscale-login-required": "Tailscale needs a login",
  "destination-unreachable": "The backup destination does not answer",
  "verify-failed": "The verify failed",
  "insufficient-space": "Not enough free disk space",
};
const PROBLEM_STATUS = {
  "destination-unreachable": 422,
  "verify-failed": 500,
  "insufficient-space": 409,
  "dns-service-unreachable": 502,
  "wifi-auth-failed": 422,
  "tailscale-login-required": 409,
};

// The planned step count of each job (PublicJobProgress.step_count): the scripted steps, then the recovery steps after
// a failure. The mock knows the plan of each scripted job.
const PLANNED = new WeakMap();

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

// spec: { kind, steps: [[action, name, opts]], cancellable, holdsLock, reason, source,
//   result(state, job), onDone(state, ok, job), onStep(state, step, "start" | "done" | "failed", job),
//   failAt: the action of a step that fails, recovery(action): the steps that run after that failure (a rollback),
//   failure(state, job, action): the problem of the failed job }
// opts: { progress?: unit, slow?: factor, stream?, lines?: [log lines], skip?: detail, detail?, fail?: detail,
//   problem?: slug, problemExtra?: {members}, continueOnFail?: bool }.
// A step with fail fails with that detail. With continueOnFail the job continues and ends failed with the
// problem of the first failed step (the backup-run job of docs/api/README.md section 13.8 does this).
export function startJob(state, spec) {
  const id = jobId(spec.kind);
  const job = {
    id,
    kind: spec.kind,
    state: "running",
    source: spec.source ?? "console",
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
  PLANNED.set(job, spec.steps.length);
  state.jobs.set(id, entry);
  state.jobOrder.unshift(id);

  const emit = (event, data) => {
    const ev = { id: ++job.last_event_id, event, data };
    entry.events.push(ev);
    for (const l of entry.listeners) l(ev);
  };

  let index = 0;
  const steps = [...spec.steps];
  // The action of the first failed step when the job runs recovery steps after it (an update that rolls back).
  let failedAt = null;
  // The problem of the first failed step of continueOnFail (a backup-run job).
  let firstProblem = null;
  const finish = (ok, problem) => {
    job.state = entry.cancelled ? "cancelled" : ok ? "succeeded" : "failed";
    job.finished_at = now();
    job.error = problem ?? null;
    // onDone first: a result can read the state after the job (the check of updates reads the new index).
    spec.onDone?.(state, ok, job);
    job.result = ok && spec.result ? spec.result(state, job) : null;
    emit("done", structuredClone(job));
  };

  const runStep = () => {
    if (entry.cancelled) {
      finish(false, null);
      return;
    }
    if (index >= steps.length) {
      if (failedAt) finish(false, spec.failure?.(state, job, failedAt) ?? null);
      else finish(!firstProblem, firstProblem ?? undefined);
      return;
    }
    const [action, name, opts = {}] = steps[index];
    const step = { index, name, action, state: "running", started_at: now(), finished_at: null, detail: null };
    job.steps[index] = step;
    spec.onStep?.(state, step, "start", job);
    emit("step", { ...step });
    const lines = opts.lines ?? LOG_LINES[action] ?? [`${name}: start`];
    let tick = 0;
    // opts.waitUntil: the step waits (at most opts.maxTicks ticks) until the function gives true (a login).
    const ticks = opts.waitUntil ? (opts.maxTicks ?? 400) : Math.max(lines.length, opts.progress ? 4 : 1);
    const timer = setInterval(
      () => {
        if (opts.waitUntil?.()) tick = ticks - 1;
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
          if (opts.skip) {
            step.state = "skipped";
            step.finished_at = now();
            step.detail = opts.skip;
            spec.onStep?.(state, step, "done", job);
            emit("step", { ...step });
            index++;
            setTimeout(runStep, 10);
            return;
          }
          const injected = state.failStep && state.failStep === action;
          const failed = injected || Boolean(opts.fail) || (!failedAt && spec.failAt === action);
          const error = opts.fail ?? STEP_ERRORS[action] ?? `${action}: exit status 1`;
          step.state = failed ? "failed" : "succeeded";
          step.finished_at = now();
          step.detail = failed ? error : (opts.detail ?? null);
          spec.onStep?.(state, step, failed ? "failed" : "done", job);
          emit("step", { ...step });
          if (failed) {
            if (injected) state.failStep = null;
            if (spec.recovery) {
              failedAt = action;
              steps.splice(index + 1, steps.length, ...spec.recovery(action));
              PLANNED.set(job, steps.length);
              index++;
              setTimeout(runStep, 10);
              return;
            }
            if (spec.failure) {
              finish(false, spec.failure(state, job, action));
              return;
            }
            const slug = opts.problem ?? "internal";
            const problem = {
              type: `https://care-pi.rithviknishad.dev/problems/${slug}`,
              title: PROBLEM_TITLES[slug] ?? "Internal error",
              status: PROBLEM_STATUS[slug] ?? 500,
              detail:
                slug === "internal"
                  ? `${spec.kind === "setup" || spec.kind === "apply" ? "apply" : spec.kind} failed at ${action}: ${error}`
                  : error,
              instance: `/api/v1/jobs/${id}`,
              ...(opts.problemExtra ?? {}),
            };
            if (opts.continueOnFail) {
              firstProblem ??= problem;
            } else {
              finish(false, problem);
              return;
            }
          }
          index++;
          setTimeout(runStep, 10);
        }
      },
      Math.max(20, Math.floor((state.stepMs * (opts.slow ?? 1)) / ticks)),
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
  ["create-care-superuser", "create-care-superuser", { detail: "admin" }],
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

// Contract 0.9.0 (S67, S68): the public job stream, the groups of GET /jobs?state=, the log tail, and the exports.

// The title of a kind for a person (PublicJobProgress.title): the texts of strings.jobs.kinds of the console.
export const KIND_TITLES = {
  apply: "Apply the settings",
  setup: "Set up the device",
  "fe-build": "Build the Care web app",
  "fe-prune": "Delete the old builds of the web app",
  "support-bundle": "Make a support bundle",
  "backup-run": "Back up",
  "backup-verify": "Verify the backups",
  "restore-pitr": "Restore the database to a point in time",
  "restore-files": "Restore files",
  "restore-dr": "Restore the device from a backup",
  "disk-prepare": "Prepare a backup disk",
  "recovery-key-rotate": "Change the recovery key",
  "restore-drill": "Test restore",
  "update-check": "Check for updates",
  "update-stage": "Download an update",
  "update-apply": "Install an update",
  "update-rollback": "Go back to the previous version",
  "os-update": "Install the OS updates",
  "plug-change": "Change a plug",
  "tls-public-enrol": "Change the public name",
  "tls-tailscale-enrol": "Change Tailscale",
  "network-wifi": "Change the Wi-Fi",
  "tls-local-ca-rotate": "Make a new root certificate",
};

const ACTIVE = new Set(["queued", "running"]);

// The groups of GET /jobs?state=: active (queued, running) and recent (the jobs that ended).
export function stateFilter(filter) {
  if (!filter) return () => true;
  if (filter === "active") return (j) => ACTIVE.has(j.state);
  if (filter === "recent") return (j) => !ACTIVE.has(j.state);
  return (j) => j.state === filter;
}

export function publicSummary(job) {
  return {
    id: job.id,
    kind: job.kind,
    state: job.state,
    started_at: job.started_at,
    finished_at: job.finished_at,
  };
}

export function publicProgress(job) {
  const step = job.steps.findLast((s) => s.state === "running") ?? job.steps.at(-1) ?? null;
  return {
    id: job.id,
    kind: job.kind,
    state: job.state,
    title: KIND_TITLES[job.kind] ?? job.kind,
    step: step ? publicStepName(step) : null,
    step_index: step?.index ?? null,
    step_count: PLANNED.has(job) ? Math.max(PLANNED.get(job), job.steps.length) : null,
    started_at: job.started_at,
  };
}

function publicProblem(p) {
  return p ? { type: p.type, title: p.title, status: p.status } : null;
}

// The public name of a step: its action key, else the first word before ":" (the argument can name the Care user).
export function publicStepName(step) {
  return step.action || (step.name ?? "").split(":")[0].trim().split(/\s+/)[0] || "";
}

function publicStep(step) {
  return { ...step, name: publicStepName(step), detail: null };
}

// The event of the public stream (docs/api/README.md section 6.3), or null: no log, no progress, no detail.
export function publicEvent(ev) {
  switch (ev.event) {
    case "step":
      return { ...ev, data: publicStep(ev.data) };
    case "error":
      return { ...ev, data: publicProblem(ev.data) };
    case "done": {
      const j = ev.data;
      return {
        ...ev,
        data: {
          id: j.id,
          kind: j.kind,
          state: j.state,
          created_at: j.created_at,
          started_at: j.started_at,
          finished_at: j.finished_at,
          steps: j.steps.map(publicStep),
          error: publicProblem(j.error),
          last_event_id: j.last_event_id,
        },
      };
    }
    default:
      return null;
  }
}

// The last 200 log lines of the job (Job.log_tail), oldest first.
export function logTail(entry) {
  const logs = entry.events.filter((e) => e.event === "log").map((e) => e.data);
  return { lines: logs.slice(-200), total: logs.length };
}

export function supportHeader(state, what) {
  return [
    `CARE support text: ${what}`,
    `Device: ${state.file.identity.display_name} (${state.file.identity.hostname ?? "care"}.local)`,
    `Agent: ${state.u?.agentVersion ?? "v0.4.1"}, commit 1f2e3d4, built 2026-10-30`,
    `Lock: ${state.u?.installed?.lock?.version ?? "2026.11.0"} (solution care, track stable, source lock)`,
    `Time: ${now()}`,
  ].join("\n");
}

// GET /jobs/{id}/export: the job, each step with its times, each log line, each problem.
export function jobExport(state, entry) {
  const j = entry.job;
  const lines = [supportHeader(state, `job ${j.id}`), ""];
  lines.push(`Job: ${j.id} (${j.kind}, ${j.state}, source ${j.source})`);
  lines.push(`Started: ${j.started_at ?? "-"}  Finished: ${j.finished_at ?? "-"}`, "", "Steps:");
  for (const s of j.steps) {
    lines.push(`  ${s.index + 1}. ${s.name} [${s.state}] ${s.started_at ?? "-"} - ${s.finished_at ?? "-"}`);
    if (s.detail) lines.push(`     ${s.detail}`);
  }
  lines.push("", "Log:");
  for (const e of entry.events) {
    if (e.event === "log") lines.push(`  ${e.data.time} [${e.data.step_index}] ${e.data.line}`);
    if (e.event === "error") lines.push(`  problem: ${e.data.title} ${e.data.detail ?? ""}`.trimEnd());
  }
  if (j.error) lines.push("", `Problem: ${j.error.title} (${j.error.type}, ${j.error.status})`, j.error.detail ?? "");
  return `${lines.join("\n")}\n`;
}
