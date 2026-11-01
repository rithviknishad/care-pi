// Status, network, host check, config, apply, FE, Care, and power routes of the mock.

import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Problem, sendJson } from "./http.mjs";
import { APPLY_STEPS, FE_BUILD_STEPS, runningHostJob, startJob, summary } from "./jobs.mjs";
import { backupsOverview } from "./backups-logic.mjs";
import { validateDestinations } from "./restore-api.mjs";
import {
  ADDRESS,
  PRESETS,
  clockView,
  effectiveConfig,
  etagOf,
  hostCheck,
  network,
  now,
  presetMemory,
} from "./state.mjs";
import { agentVersion, maintenanceOf, runningIdentity, updatesSummary } from "./updates.mjs";
import { publicHost, tailscaleHost, tlsSummary } from "./connectivity.mjs";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const SCHEMA_FILE = path.join(ROOT, "agent/internal/config/schema/config.schema.json");
const MIB = 1024 * 1024;
// The jobs of Phase 4 show the maintenance page only in their cutover (state.u.maintenance), not for the whole job.
const PHASE4_KINDS = ["update-stage", "update-apply", "update-rollback", "os-update", "plug-change"];

function pageOn(state, running) {
  return Boolean(running && !PHASE4_KINDS.includes(running.kind)) || maintenanceOf(state).active;
}

function jitter(base, spread) {
  return Math.round(base + (Math.random() - 0.5) * spread);
}

function units(state) {
  const ready = state.setup.state === "ready";
  const caps = presetMemory(state.file.size.preset);
  const unit = (name, short, mem, cap, health = null) => ({
    unit: name,
    active_state: ready ? "active" : "inactive",
    sub_state: ready ? (name.endsWith(".target") ? "active" : "running") : "dead",
    health: ready ? health : null,
    memory_current_bytes: ready && mem ? jitter(mem * MIB, 12 * MIB) : null,
    memory_peak_bytes: ready && mem ? Math.round(mem * 1.3 * MIB) : null,
    memory_max_bytes: cap ? cap * MIB : null,
    restarts: 0,
    log_unit: short,
  });
  // Setup mode runs the pod and Caddy, so they are active in each state.
  const always = (u, mem) => ({ ...u, active_state: "active", sub_state: "running", memory_current_bytes: mem });
  // The order and the units of GET /status of the agent (agent/internal/status: Units).
  return [
    unit("care.target", "target", null, null),
    { ...unit("care-clock.service", "clock", null, null), active_state: "active", sub_state: "exited" },
    always(unit("care-pod.service", "pod", 2, null), jitter(2 * MIB, MIB)),
    always(unit("care-caddy.service", "caddy", 27, 96), jitter(27 * MIB, 4 * MIB)),
    unit("care-db.service", "db", 92, caps["care-db"].max_mb, "healthy"),
    unit("care-redis.service", "redis", 18, 96, "healthy"),
    unit("care-s3.service", "s3", 24, 256),
    unit("care-beat.service", "beat", 130, 512, "healthy"),
    unit("care-worker.service", "worker", 340, caps["care-worker"].max_mb, "healthy"),
    unit("care-api.service", "api", 300, caps["care-api"].max_mb, "healthy"),
  ];
}

export function status(state) {
  const ready = state.setup.state === "ready";
  const net = network(state);
  const lastId = state.jobOrder[0];
  const running = runningHostJob(state);
  const maintenance = maintenanceOf(state);
  const identity = runningIdentity(state);
  const flags = {
    degraded: false,
    address_changed: false,
    clock_wait: false,
    maintenance: maintenance.active,
    fe_missing: !ready,
    fe_scale_down_left: false,
    low_disk: false,
    low_memory: false,
    mdns_collision: net.mdns.collision,
    hostname_mismatch: false,
    last_job_failed: lastId ? state.jobs.get(lastId).job.state === "failed" : false,
    recovery_codes_low: state.recoveryCodes.length <= 2,
    host_check_failed: false,
  };
  const problems = [];
  if (flags.mdns_collision) problems.push(`mDNS name collision: the host announces ${net.mdns.advertised}`);
  if (flags.recovery_codes_low) problems.push("2 or fewer recovery codes remain");
  const pub = publicHost(state);
  const ts = tailscaleHost(state);
  return {
    hostnames: [...(pub ? [pub] : []), net.mdns.advertised, ...(ts ? [ts] : [])],
    addresses: net.addresses,
    published: net.addresses,
    units: units(state),
    images: [
      {
        name: "care",
        ref: "ghcr.io/ohcnetwork/care:latest",
        pull: identity.care,
      },
      {
        name: "postgres",
        ref: "ghcr.io/rithviknishad/care-pi-postgres:17",
        pull: "ghcr.io/rithviknishad/care-pi-postgres@sha256:9a8b7c6d5e4f30211a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7081",
      },
    ],
    care_fe_sha: ready ? identity.care_fe : null,
    fe_build_id: ready ? "8d2f0c1a9b7e" : null,
    locked_at: ready ? "2026-10-30T10:15:00Z" : null,
    degraded: false,
    problems,
    mode: pageOn(state, running) ? ["maintenance"] : [],
    generated_at: now(),
    state: !ready ? "setup" : pageOn(state, running) ? "maintenance" : "ok",
    flags,
    agent: {
      version: agentVersion(state),
      commit: "1f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c",
      date: "2026-10-30T08:00:00Z",
      dev_build: state.devBuild,
      started_at: state.startedAt,
    },
    setup_state: state.setup.state,
    host: {
      model: "Raspberry Pi 5 Model B Rev 1.0",
      arch: "arm64",
      kernel: "6.18.0-v8-16k+",
      os: "Debian GNU/Linux 13 (trixie)",
      page_size_bytes: 16384,
      cpu_count: 4,
      load_average: [jitter(42, 20) / 100, 0.37, 0.3],
      uptime_s: Math.round((Date.now() - Date.parse(state.startedAt)) / 1000) + 86400,
      memory: {
        total_bytes: state.ramBytes,
        available_bytes: jitter(1_041_235_968, 60 * MIB),
        swap_total_bytes: state.ramBytes,
        swap_free_bytes: jitter(2_015_363_072, 20 * MIB),
      },
    },
    disks: [
      {
        mount: "/",
        device: state.diskKind === "sd" ? "/dev/mmcblk0p2" : "/dev/sda2",
        kind: state.diskKind,
        total_bytes: 250_048_479_232,
        free_bytes: jitter(221_000_000_000, 50 * MIB),
      },
    ],
    clock: {
      ...clockView(state),
      gate: "passed",
      time: now(),
      timezone: "Asia/Kolkata",
      rtc_present: true,
    },
    network: { mdns_configured: net.mdns.configured, mdns_advertised: net.mdns.advertised, internet_reachable: true },
    tls: tlsSummary(state),
    fe: {
      provider: state.file.fe.provider,
      build_id: ready ? "8d2f0c1a9b7e" : null,
      current: ready ? "8d2f0c1a9b7e" : null,
      building: false,
    },
    lock: ready
      ? {
          generated_at: "2026-10-30T10:15:00Z",
          agent_version: "v0.2.0",
          solution: { name: "care-clinic", track: "stable" },
        }
      : null,
    jobs: { running: running ? summary(running) : null, last: lastId ? summary(state.jobs.get(lastId).job) : null },
    backups: state.claimed ? backupsOverview(state) : null,
    updates: updatesSummary(state),
    maintenance,
  };
}

function plan(state, before, after) {
  const changes = [];
  const actions = [];
  const restarts = [];
  let impact = "none";
  let feBuild = null;
  const changed = (a, b) => JSON.stringify(a ?? null) !== JSON.stringify(b ?? null);
  if (changed(before.identity, after.identity)) {
    changes.push(
      { path: "rendered/caddy/Caddyfile", kind: "modify" },
      { path: "rendered/env/care.env", kind: "modify" },
    );
    restarts.push("care-api.service");
    actions.push({
      kind: "reload-caddy",
      text: "reload care-caddy.service",
      units: ["care-caddy.service"],
      paths: [],
      names: [],
      reason: "Caddyfile changed",
    });
    impact = "restart";
  }
  if (changed(before.size, after.size)) {
    changes.push(
      { path: "quadlets/care-api.container", kind: "modify" },
      { path: "quadlets/care-worker.container", kind: "modify" },
    );
    restarts.push("care-api.service", "care-worker.service");
    impact = "restart";
  }
  if (changed(before.env?.backend, after.env?.backend)) {
    changes.push({ path: "rendered/env/care.env", kind: "modify" });
    restarts.push("care-api.service", "care-worker.service", "care-beat.service");
    impact = "restart";
  }
  if (changed(before.env?.frontend, after.env?.frontend) || changed(before.fe, after.fe)) {
    changes.push({ path: "rendered/fe/frontend.env", kind: "modify" });
    feBuild = {
      build_id: "4c1a2b3d9e8f",
      provider: after.fe?.provider === "artifact" ? "artifact" : "local",
      exists: false,
    };
    actions.push({
      kind: "ensure-fe",
      text: "build care_fe 4c1a2b3d9e8f (about 3 min)",
      units: [],
      paths: [],
      names: ["4c1a2b3d9e8f"],
      reason: "env.frontend changed",
    });
    if (impact === "none") impact = "reload";
  }
  if (changed(before.backups, after.backups)) {
    const n = after.backups?.destinations?.length ?? 0;
    const disks = (after.backups?.destinations ?? []).filter((d) => d.disk_label).length;
    changes.push(
      { path: "rendered/backup/timers", kind: "modify" },
      { path: "rendered/backup/ssh_config", kind: "modify" },
    );
    actions.push(
      {
        kind: "write-files",
        text: `write ${changes.length} file(s)`,
        units: [],
        paths: changes.map((c) => c.path),
        names: [],
        reason: "backups changed",
      },
      { kind: "daemon-reload", text: "daemon-reload", units: [], paths: [], names: [], reason: null },
      ...(disks
        ? [{ kind: "ensure-mounts", text: `ensure ${disks} mount(s)`, units: [], paths: [], names: [], reason: null }]
        : []),
      {
        kind: "ensure-repositories",
        text: `restic init where necessary (${n} destination(s))`,
        units: [],
        paths: [],
        names: [],
        reason: null,
      },
      {
        kind: "ensure-timers",
        text: `turn on ${4 + 2 * n} backup timer(s)`,
        units: [],
        paths: [],
        names: [],
        reason: null,
      },
    );
  }
  if (changed(before.updates, after.updates)) {
    // The window and the automatic updates are in the timers (docs/solutions.md section 10.2).
    changes.push(
      { path: "systemd/care-update-apply.timer", kind: "modify" },
      { path: "apt/52care-pi.conf", kind: "modify" },
    );
    actions.push(
      {
        kind: "write-files",
        text: "write 2 file(s)",
        units: [],
        paths: ["systemd/care-update-apply.timer", "apt/52care-pi.conf"],
        names: [],
        reason: "updates changed",
      },
      { kind: "daemon-reload", text: "daemon-reload", units: [], paths: [], names: [], reason: "timer changed" },
    );
  }
  if (
    changed(before.solution?.custom, after.solution?.custom) ||
    changed(before.solution?.track, after.solution?.track)
  ) {
    changes.push({ path: "lock.yaml (target)", kind: "modify" });
  }
  const uniq = [...new Set(restarts)];
  if (uniq.length > 0) {
    actions.unshift({
      kind: "write-files",
      text: `write ${changes.length} file(s)`,
      units: [],
      paths: changes.map((c) => c.path),
      names: [],
      reason: null,
    });
    actions.push({
      kind: "restart",
      text: `restart ${uniq.join(", ")}`,
      units: uniq,
      paths: [],
      names: [],
      reason: "config changed",
    });
  }
  return {
    changed: changes.length > 0,
    summary: {
      solution: "care-clinic",
      track: after.solution?.track ?? "stable",
      preset: after.size?.preset ?? "small-clinic",
      hostnames: [`${after.identity?.hostname ?? "care"}.local`],
      addresses: [ADDRESS],
    },
    changes,
    actions,
    fe_build: feBuild,
    impact,
    restarts: uniq,
    lock_changed: false,
    warnings:
      after.identity?.hostname !== before.identity?.hostname
        ? ["The host name changes. Open the new URL and log in again."]
        : [],
    diff: null,
  };
}

// The steps of an apply: the actions of the plan, or the default steps when the plan has no action.
function applySteps(p) {
  if (!p || p.actions.length === 0) return APPLY_STEPS;
  return p.actions.map((a) => [
    a.kind,
    a.kind === "write-files" ? `write-files: ${a.paths.length} file(s)` : `${a.kind}: ${a.text}`,
  ]);
}

function startApply(state, onDone, p = null) {
  const running = runningHostJob(state);
  if (running)
    throw new Problem("job-running", "Another job holds the apply lock. Wait until it ends.", { job_id: running.id });
  const backupsChanged = state.backups.dirty || (p?.actions ?? []).some((a) => a.kind === "ensure-timers");
  if (backupsChanged) syncDestinations(state);
  return startJob(state, {
    kind: "apply",
    steps: backupsChanged && (!p || p.actions.length === 0) ? backupsApplySteps(state) : applySteps(p),
    result: () => ({ kind: "apply", changed: true, lock_changed: false, fe_build_id: "8d2f0c1a9b7e" }),
    onDone: (st, ok) => {
      if (ok) st.appliedHostname = st.file.identity.hostname;
      onDone?.(st, ok);
    },
  });
}

// An apply renders the units of each destination: a new destination is "pending", a removed one loses its
// state and its secret (docs/api/README.md section 13.5).
function syncDestinations(state) {
  const ids = new Set((state.file.backups?.destinations ?? []).map((d) => d.id));
  for (const id of Object.keys(state.backups.runtime)) if (!ids.has(id)) delete state.backups.runtime[id];
  for (const id of Object.keys(state.backups.secrets)) if (!ids.has(id)) delete state.backups.secrets[id];
  for (const id of ids) state.backups.runtime[id] ??= { state: "pending", recent_runs: [] };
  for (const disk of state.backups.disks) {
    const d = (state.file.backups?.destinations ?? []).find((x) => x.disk_label && x.disk_label === disk.care_label);
    disk.destination_id = d?.id ?? null;
  }
  state.backups.dirty = false;
}

function backupsApplySteps(state) {
  const n = state.file.backups?.destinations?.length ?? 0;
  return [
    ["write-files", "write-files: 2 file(s)"],
    ["daemon-reload", "daemon-reload"],
    ["ensure-mounts", "ensure-mounts"],
    ["ensure-repositories", `ensure-repositories: ${n} destination(s)`],
    ["ensure-timers", `ensure-timers: ${4 + 2 * n} timer(s)`],
  ];
}

function configWrite(ctx, nextFile) {
  const { state, req, res, query } = ctx;
  const ifMatch = req.headers["if-match"];
  if (!ifMatch)
    throw new Problem("precondition-required", "Read the config with GET /config, then send its ETag in If-Match.");
  const etag = etagOf(state.file);
  if (ifMatch !== etag) throw new Problem("config-changed", `The ETag is now ${etag}. Read the config again.`);
  const dryRun = query.get("dry_run") === "true";
  const apply = query.get("apply") === "true";
  if (dryRun && apply)
    throw new Problem("invalid-request", "dry_run and apply cannot be true together.", {
      errors: [{ pointer: "", message: "not both", code: "dependentSchemas", parameter: "apply" }],
    });
  if (apply && state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish setup first.");
  const host = nextFile.identity?.hostname;
  if (host !== undefined && !/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(host)) {
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
  if (nextFile.version !== 1) {
    throw new Problem("invalid-config", "1 value does not match the JSON Schema.", {
      errors: [{ pointer: "/version", message: "must be 1", code: "const" }],
    });
  }
  const badBackups = Array.isArray(nextFile.backups?.destinations)
    ? validateDestinations(nextFile.backups.destinations)
    : null;
  if (badBackups) {
    badBackups.extra.errors = badBackups.extra.errors.map((e) => ({ ...e, pointer: `/backups${e.pointer}` }));
    throw badBackups;
  }
  const p = plan(state, effectiveConfig(state), { ...effectiveConfig({ ...state, file: nextFile }) });
  if (dryRun) {
    sendJson(res, 200, { written: false, etag, plan: p, job: null }, { ETag: etag });
    return;
  }
  const before = state.file;
  state.file = nextFile;
  if (JSON.stringify(before.backups ?? null) !== JSON.stringify(nextFile.backups ?? null)) state.backups.dirty = true;
  let job;
  try {
    job = apply ? startApply(state, undefined, p) : null;
  } catch (e) {
    state.file = before;
    throw e;
  }
  const newTag = etagOf(state.file);
  sendJson(
    res,
    apply ? 202 : 200,
    { written: true, etag: newTag, plan: p, job },
    { ETag: newTag, ...(job ? { Location: `/api/v1/jobs/${job.id}` } : {}) },
  );
}

function mergePatch(target, patch) {
  if (patch === null || typeof patch !== "object" || Array.isArray(patch)) return patch;
  const out = target && typeof target === "object" && !Array.isArray(target) ? { ...target } : {};
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) delete out[k];
    else out[k] = mergePatch(out[k], v);
  }
  return out;
}

export const systemRoutes = [
  {
    method: "GET",
    path: "/_care-pi/status",
    public: true,
    handler: ({ res, state }) => {
      const running = runningHostJob(state);
      const step = running?.steps.findLast((s) => s.state === "running");
      const maintenance = maintenanceOf(state);
      sendJson(res, 200, {
        site: state.setup.state === "ready" ? (pageOn(state, running) ? "maintenance" : "care") : "setup",
        setup_state: state.setup.state,
        claim_methods: state.claimMethods,
        display_name: state.file.identity.display_name,
        console_port: 8443,
        clock: clockView(state),
        care_healthy: state.setup.state === "ready",
        job: running
          ? {
              kind: running.kind,
              step: step?.name ?? null,
              step_index: step?.index ?? null,
              step_count: running.steps.length,
              started_at: running.started_at,
            }
          : null,
        time: now(),
        maintenance,
        firstboot_error: state.setup.state === "ready" ? null : (state.firstbootError ?? null),
      });
    },
  },
  {
    method: "GET",
    path: "/api/v1/health",
    public: true,
    handler: ({ res }) => sendJson(res, 200, { status: "ok", time: now() }),
  },
  {
    method: "GET",
    path: "/_care-pi/health",
    public: true,
    handler: ({ res }) => sendJson(res, 200, { status: "ok", time: now() }),
  },
  { method: "GET", path: "/api/v1/status", handler: ({ res, state }) => sendJson(res, 200, status(state)) },
  { method: "GET", path: "/api/v1/network", handler: ({ res, state }) => sendJson(res, 200, network(state)) },
  {
    method: "GET",
    path: "/api/v1/host/check",
    handler: async ({ res, state, query }) => {
      const probe = query.get("probe") === "true";
      if (probe) await new Promise((r) => setTimeout(r, Math.min(10000, state.stepMs * 3)));
      sendJson(res, 200, hostCheck(state, probe));
    },
  },
  {
    method: "GET",
    path: "/api/v1/config",
    handler: ({ res, state, query }) => {
      const view = query.get("view") ?? "effective";
      sendJson(res, 200, view === "file" ? state.file : effectiveConfig(state), { ETag: etagOf(state.file) });
    },
  },
  { method: "PUT", path: "/api/v1/config", handler: (ctx) => configWrite(ctx, ctx.body) },
  {
    method: "PATCH",
    path: "/api/v1/config",
    contentType: "application/merge-patch+json",
    handler: (ctx) => configWrite(ctx, mergePatch(ctx.state.file, ctx.body)),
  },
  {
    method: "GET",
    path: "/api/v1/config/schema",
    handler: ({ res }) => {
      res.writeHead(200, { "Content-Type": "application/schema+json", "Cache-Control": "no-store" });
      res.end(readFileSync(SCHEMA_FILE));
    },
  },
  {
    method: "GET",
    path: "/api/v1/config/presets",
    handler: ({ res, state }) => {
      const lowRam = state.ramBytes < 3 * 1024 ** 3;
      const rec = lowRam ? "small-clinic" : "busy-counter";
      sendJson(res, 200, {
        default: "small-clinic",
        host_ram_bytes: state.ramBytes,
        items: [
          ...Object.entries(PRESETS).map(([name, p]) => ({
            name,
            gunicorn_workers: p.gunicorn_workers,
            celery_concurrency: p.celery_concurrency,
            pg_shared_buffers_mb: p.pg_shared_buffers_mb,
            memory: presetMemory(name),
            recommended: name === rec,
            warning:
              name === "busy-counter" && lowRam ? "busy-counter needs 4 GiB RAM or more; this host has 2 GiB" : null,
          })),
          {
            name: "custom",
            gunicorn_workers: null,
            celery_concurrency: null,
            pg_shared_buffers_mb: null,
            memory: null,
            recommended: false,
            warning: null,
          },
        ],
      });
    },
  },
  {
    method: "POST",
    path: "/api/v1/apply",
    handler: ({ res, state }) => {
      if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish setup first.");
      const job = startApply(state);
      sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
    },
  },
  {
    method: "GET",
    path: "/api/v1/apply/plan",
    handler: ({ res, state }) => {
      const running = runningHostJob(state);
      if (running) throw new Problem("job-running", "A job changes the host now.", { job_id: running.id });
      sendJson(res, 200, plan(state, effectiveConfig(state), effectiveConfig(state)));
    },
  },
  {
    method: "GET",
    path: "/api/v1/fe",
    handler: ({ res, state }) => {
      const ready = state.setup.state === "ready";
      const building = [...state.jobs.values()].some((e) => e.job.kind === "fe-build" && e.job.state === "running");
      sendJson(res, 200, {
        provider: state.file.fe.provider,
        build_id: ready ? "8d2f0c1a9b7e" : null,
        care_fe_sha: ready ? runningIdentity(state).care_fe : null,
        current: ready ? "8d2f0c1a9b7e" : null,
        last_build: ready
          ? { provider: "local", built_at: "2026-10-30T10:19:00Z", duration: "3m12s", url: null, sha256: null }
          : null,
        builds: state.feBuilds,
        scale_down_left: false,
        building,
      });
    },
  },
  {
    method: "POST",
    path: "/api/v1/fe/build",
    handler: ({ res, state }) => {
      if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish setup first.");
      const running = runningHostJob(state);
      if (running) throw new Problem("job-running", "Another job holds the apply lock.", { job_id: running.id });
      const job = startJob(state, {
        kind: "fe-build",
        steps: FE_BUILD_STEPS,
        cancellable: true,
        result: () => ({ kind: "fe-build", build_id: "8d2f0c1a9b7e", provider: "local", duration_s: 192.4 }),
      });
      sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
    },
  },
  {
    method: "POST",
    path: "/api/v1/fe/prune",
    handler: ({ res, state }) => {
      if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish setup first.");
      const job = startJob(state, {
        kind: "fe-prune",
        steps: [["prune", "prune: keep 2"]],
        result: () => ({ kind: "fe-prune", removed: [] }),
      });
      sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
    },
  },
  {
    method: "GET",
    path: "/api/v1/care",
    handler: ({ res, state }) => {
      const host = network(state).mdns.advertised;
      sendJson(res, 200, {
        api_healthy: state.setup.state === "ready",
        app_version: state.setup.state === "ready" ? "3.0.0-dev" : null,
        primary_url: `https://${host}/`,
        urls: [`https://${host}/`, `https://${ADDRESS}/`],
        handoff_url: `https://${host}/login`,
      });
    },
  },
  {
    method: "POST",
    path: "/api/v1/power/reboot",
    handler: ({ res, state, body }) => {
      const running = runningHostJob(state);
      if (running) throw new Problem("job-running", "A job changes the host now.", { job_id: running.id });
      const delay = body?.delay_s ?? 5;
      sendJson(res, 202, {
        action: "reboot",
        at: new Date(Date.now() + delay * 1000).toISOString().replace(/\.\d{3}Z$/, "Z"),
      });
    },
  },
  {
    method: "POST",
    path: "/api/v1/power/shutdown",
    reauth: true,
    handler: ({ res, state, body }) => {
      const running = runningHostJob(state);
      if (running) throw new Problem("job-running", "A job changes the host now.", { job_id: running.id });
      const delay = body?.delay_s ?? 5;
      sendJson(res, 202, {
        action: "shutdown",
        at: new Date(Date.now() + delay * 1000).toISOString().replace(/\.\d{3}Z$/, "Z"),
      });
    },
  },
];
