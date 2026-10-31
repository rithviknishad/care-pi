// Setup routes of the mock: the state machine of docs/api/README.md section 5.

import { Problem, sendJson } from "./http.mjs";
import { SETUP_STEPS, runningHostJob, startJob } from "./jobs.mjs";
import { STEP_IDS, normalizeCode, now, PRESETS } from "./state.mjs";

const REQUIRED = { env: false };
const PHASE = { backups: 3 };
const HOSTNAME = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const USERNAME = /^[a-z0-9][a-z0-9._-]{2,14}[a-z0-9]$/;
const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
const RESERVED_BACKEND = new Set([
  "ADDITIONAL_PLUGS",
  "BUCKET_ENDPOINT",
  "BUCKET_EXTERNAL_ENDPOINT",
  "BUCKET_KEY",
  "BUCKET_PROVIDER",
  "BUCKET_SECRET",
  "CELERY_BROKER_URL",
  "CELERY_WORKER_CONCURRENCY",
  "CSRF_TRUSTED_ORIGINS",
  "DATABASE_URL",
  "DJANGO_ALLOWED_HOSTS",
  "DJANGO_SECRET_KEY",
  "GUNICORN_WORKERS",
  "JWKS_BASE64",
  "POSTGRES_DB",
  "POSTGRES_HOST",
  "POSTGRES_PASSWORD",
  "POSTGRES_PORT",
  "POSTGRES_USER",
  "REDIS_URL",
]);

function skippable(state, id) {
  return id === "env" || (id === "backups" && state.devBuild);
}

export function recompute(state) {
  const s = state.setup;
  let current = null;
  for (const id of STEP_IDS) {
    const st = s.steps[id];
    if (st.status === "done" || st.status === "skipped") continue;
    if (!current) {
      current = id;
      st.status = "current";
    } else {
      st.status = "pending";
    }
  }
  s.current = current;
}

export function missingSteps(state) {
  return STEP_IDS.filter((id) => {
    const st = state.setup.steps[id];
    if (st.status === "done") return false;
    if (st.status === "skipped") return id === "env" || (id === "backups" && state.devBuild) ? false : true;
    return REQUIRED[id] !== false;
  });
}

export function setupState(state) {
  recompute(state);
  const s = state.setup;
  const f = state.file;
  return {
    state: s.state,
    dev_build: state.devBuild,
    current_step: s.current,
    steps: STEP_IDS.map((id) => ({
      id,
      ...s.steps[id],
      required: REQUIRED[id] !== false,
      skippable: skippable(state, id),
      phase: PHASE[id] ?? 2,
    })),
    draft: {
      identity: { display_name: f.identity.display_name, hostname: f.identity.hostname },
      tls: f.tls,
      size: f.size,
      env: { backend: f.env.backend ?? {}, frontend: f.env.frontend ?? {}, fe: f.fe },
      care_admin: {
        username: state.careAdmin.username,
        email: state.careAdmin.email,
        password_set: Boolean(state.careAdmin.password),
      },
      backups: null,
    },
    can_finish: (s.state === "setup" || s.state === "claimed") && missingSteps(state).length === 0,
    job_id: s.jobId,
    claimed_at: s.claimedAt,
    ready_at: s.readyAt,
  };
}

function guardChange(state) {
  if (state.setup.state === "ready") throw new Problem("setup-finished", "Setup is finished. Use /config.");
  if (state.setup.state === "applying") {
    throw new Problem("job-running", "The setup job runs now.", { job_id: state.setup.jobId });
  }
}

function markDone(state, id, warnings = []) {
  state.setup.steps[id] = { status: "done", updated_at: now(), errors: [], warnings };
  if (state.setup.state === "claimed") state.setup.state = "setup";
}

function invalid(pointer, message, code) {
  return new Problem("invalid-request", message, { errors: [{ pointer, message, code }] });
}

function stepRoute(id, apply) {
  return {
    method: "PUT",
    path: `/api/v1/setup/steps/${id}`,
    handler: (ctx) => {
      const { state, res } = ctx;
      guardChange(state);
      const warnings = apply(ctx) ?? [];
      markDone(state, id, warnings);
      sendJson(res, 200, setupState(state));
    },
  };
}

export const setupRoutes = [
  { method: "GET", path: "/api/v1/setup", handler: ({ res, state }) => sendJson(res, 200, setupState(state)) },
  stepRoute("recovery-codes", ({ state, body }) => {
    const code = normalizeCode(body.confirm_code ?? "");
    if (!state.recoveryCodes.some((c) => normalizeCode(c) === code)) {
      throw invalid("/confirm_code", "The code is not one of the recovery codes.", "not-a-recovery-code");
    }
  }),
  stepRoute("hardware", ({ body }) => {
    if (body.acknowledged !== true) throw invalid("/acknowledged", "must be true", "const");
  }),
  stepRoute("identity", ({ state, body }) => {
    const name = body.display_name;
    if (typeof name !== "string" || name.length < 1 || name.length > 80 || /[\r\n<>]/.test(name)) {
      throw new Problem("invalid-config", "1 value does not match the JSON Schema.", {
        errors: [{ pointer: "/identity/display_name", message: "1 to 80 characters, no < or >", code: "pattern" }],
      });
    }
    if (typeof body.hostname !== "string" || !HOSTNAME.test(body.hostname)) {
      throw new Problem("invalid-config", "1 value does not match the JSON Schema.", {
        errors: [
          {
            pointer: "/identity/hostname",
            message: "must match the pattern ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$",
            code: "pattern",
          },
        ],
      });
    }
    state.file.identity = { display_name: name, hostname: body.hostname };
    return body.hostname !== state.appliedHostname
      ? [
          {
            code: "hostname-changes-url",
            message: `the URL changes to https://${body.hostname}.local`,
            pointer: "/hostname",
          },
        ]
      : [];
  }),
  stepRoute("tls", ({ state, body }) => {
    if (body.public?.enabled || body.tailscale?.enabled) {
      throw new Problem("unsupported-config", "tls.public needs Phase 6.", {
        phase: 6,
        errors: [
          {
            pointer: body.public?.enabled ? "/public/enabled" : "/tailscale/enabled",
            message: "this mode comes in Phase 6",
            code: "phase",
          },
        ],
      });
    }
    state.file.tls = {
      local_ca: { enabled: true },
      public: { enabled: false, clinic_id: null },
      tailscale: { enabled: false },
    };
  }),
  stepRoute("size", ({ state, body }) => {
    if (!["solo", "small-clinic", "busy-counter", "custom"].includes(body.preset))
      throw invalid("/preset", "unknown preset", "enum");
    if (body.preset === "custom") {
      const c = body.custom ?? {};
      const bad = [
        ["gunicorn_workers", 1, 8],
        ["celery_concurrency", 1, 4],
        ["pg_shared_buffers_mb", 32, 2048],
      ].find(([k, lo, hi]) => !Number.isInteger(c[k]) || c[k] < lo || c[k] > hi);
      if (bad) {
        throw new Problem("invalid-config", "1 value does not match the JSON Schema.", {
          errors: [
            {
              pointer: `/size/custom/${bad[0]}`,
              message: `must be an integer from ${bad[1]} to ${bad[2]}`,
              code: "range",
            },
          ],
        });
      }
      state.file.size = { preset: "custom", custom: c };
    } else {
      state.file.size = { preset: body.preset };
    }
    return body.preset === "busy-counter" && state.ramBytes < 3 * 1024 ** 3
      ? [
          {
            code: "busy-counter-low-ram",
            message: "busy-counter needs 4 GiB RAM or more; this host has 2 GiB",
            pointer: "/preset",
          },
        ]
      : [];
  }),
  stepRoute("env", ({ state, body }) => {
    const errors = [];
    for (const part of ["backend", "frontend"]) {
      for (const [k, v] of Object.entries(body[part] ?? {})) {
        if (!ENV_NAME.test(k))
          errors.push({ pointer: `/env/${part}/${k}`, message: "not a valid variable name", code: "pattern" });
        else if (part === "backend" && RESERVED_BACKEND.has(k))
          errors.push({ pointer: `/env/${part}/${k}`, message: "care-pi sets this variable", code: "not" });
        else if (part === "frontend" && k === "REACT_CARE_API_URL")
          errors.push({ pointer: `/env/${part}/${k}`, message: "care-pi sets this variable", code: "not" });
        else if (typeof v !== "string" || /[\r\n]/.test(v))
          errors.push({
            pointer: `/env/${part}/${k}`,
            message: "a value cannot contain a line break",
            code: "pattern",
          });
      }
    }
    const fe = body.fe ?? { provider: "local" };
    if (!["none", "artifact", "local"].includes(fe.provider))
      errors.push({ pointer: "/fe/provider", message: "unknown provider", code: "enum" });
    if (fe.artifact_url && !/^(https|file):\/\/\S+$/.test(fe.artifact_url))
      errors.push({ pointer: "/fe/artifact_url", message: "use an https:// or file:// URL", code: "pattern" });
    if (fe.artifact_sha256 && !/^[A-Fa-f0-9]{64}$/.test(fe.artifact_sha256))
      errors.push({ pointer: "/fe/artifact_sha256", message: "use 64 hex digits", code: "pattern" });
    if (errors.length > 0)
      throw new Problem("invalid-config", `${errors.length} value(s) do not match the JSON Schema.`, { errors });
    state.file.env = { backend: body.backend ?? {}, frontend: body.frontend ?? {} };
    state.file.fe = {
      provider: fe.provider,
      artifact_url: fe.artifact_url ?? null,
      artifact_sha256: fe.artifact_sha256 ?? null,
    };
    return fe.provider === "local"
      ? [{ code: "fe-local-needs-internet", message: "the local FE build needs the internet", pointer: "/fe/provider" }]
      : [];
  }),
  {
    method: "PUT",
    path: "/api/v1/setup/steps/backups",
    phase: 3,
    handler: () => {},
  },
  stepRoute("care-admin", ({ state, body }) => {
    if (typeof body.username !== "string" || !USERNAME.test(body.username) || /[._-]{2}/.test(body.username)) {
      throw invalid("/username", "4 to 16 characters: a-z, 0-9, . _ -", "pattern");
    }
    if (typeof body.email !== "string" || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(body.email)) {
      throw invalid("/email", "not a valid email address", "format");
    }
    if (typeof body.password !== "string" || body.password.length < 12) {
      throw new Problem("weak-password", "Use 12 characters or more.", {
        errors: [{ pointer: "/password", message: "use 12 characters or more", code: "minLength" }],
      });
    }
    if (body.password === state.password) {
      throw new Problem("weak-password", "The Care password must not be the console password.", {
        errors: [{ pointer: "/password", message: "same as the console password", code: "reused-password" }],
      });
    }
    state.careAdmin = { username: body.username, email: body.email, password: body.password };
  }),
  {
    method: "POST",
    path: "/api/v1/setup/steps/:step/skip",
    handler: ({ state, res, params }) => {
      guardChange(state);
      if (!STEP_IDS.includes(params.step)) throw invalid("", "unknown step", "enum");
      if (!skippable(state, params.step))
        throw new Problem("step-not-skippable", `The step ${params.step} cannot be skipped.`);
      state.setup.steps[params.step] = { status: "skipped", updated_at: now(), errors: [], warnings: [] };
      if (state.setup.state === "claimed") state.setup.state = "setup";
      sendJson(res, 200, setupState(state));
    },
  },
  {
    method: "POST",
    path: "/api/v1/setup/finish",
    handler: ({ state, res }) => {
      guardChange(state);
      const missing = missingSteps(state);
      if (missing.length > 0) {
        throw new Problem("setup-incomplete", "Complete the steps in missing_steps first.", { missing_steps: missing });
      }
      const running = runningHostJob(state);
      if (running) throw new Problem("job-running", "Another job holds the apply lock.", { job_id: running.id });
      const job = startJob(state, {
        kind: "setup",
        steps: SETUP_STEPS.map(([a, n, o]) => [
          a,
          a === "create-care-superuser" ? `create-care-superuser: ${state.careAdmin.username}` : n,
          o,
        ]),
        result: (st) => {
          const host = `${st.file.identity.hostname}.local`;
          return {
            kind: "setup",
            care_url: `https://${host}/`,
            console_url: `https://${host}:8443/`,
            handoff_url: `https://${host}/login`,
            urls: [`https://${host}/`, "https://192.168.1.10/"],
            care_username: st.careAdmin.username,
          };
        },
        onDone: (st, ok, job) => {
          if (ok) {
            st.setup.state = "ready";
            st.setup.readyAt = now();
            st.setup.jobId = null;
            st.appliedHostname = st.file.identity.hostname;
            st.feBuilds = [{ id: "8d2f0c1a9b7e", bytes: 13_421_772, modified_at: now(), current: true }];
            st.careAdmin.password = null;
          } else {
            st.setup.state = "setup";
            st.setup.jobId = job.id;
          }
        },
      });
      state.setup.state = "applying";
      state.setup.jobId = job.id;
      sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
    },
  },
];

export { PRESETS };
