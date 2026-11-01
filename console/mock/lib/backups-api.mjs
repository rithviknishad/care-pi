// The backup routes of the mock (docs/api/openapi.yaml, tag backups).

import { Problem, sendJson } from "./http.mjs";
import { startJob } from "./jobs.mjs";
import { now } from "./state.mjs";
import { sshPublicKey } from "./backups-state.mjs";
import {
  LABEL,
  backupsConfig,
  backupsOverview,
  confirmKey,
  destinationState,
  health,
  invalid,
  iso,
  keyOf,
  keyStatus,
  keyText,
  label,
  makeKey,
  recoveryKey,
  requireHostLock,
  requireReady,
  snapId,
  testDestination,
} from "./backups-logic.mjs";

function destinationsOf(state) {
  return backupsConfig(state.file).destinations;
}

// Updates the runtime state of a destination after a snapshot run of the console.
function snapshotDone(state, d, ok, jobId) {
  const r = (state.backups.runtime[d.id] ??= { state: "pending", recent_runs: [] });
  const t = now();
  r.last_attempt_at = t;
  r.running = false;
  if (ok) {
    Object.assign(r, {
      state: "ok",
      last_db_snapshot_at: t,
      last_files_snapshot_at: t,
      last_error: null,
      last_error_code: null,
      failing_since: null,
      size_bytes: (r.size_bytes ?? 52_000_000) + 1_048_576,
      snapshot_count: (r.snapshot_count ?? 0) + 2,
    });
  }
  const run = (kind) => ({
    started_at: t,
    finished_at: t,
    kind,
    source: "console",
    ok,
    snapshot_id: ok ? snapId() : null,
    added_bytes: ok ? 1_048_576 : null,
    error_code: ok ? null : r.last_error_code,
    error: ok ? null : r.last_error,
    job_id: jobId,
  });
  r.recent_runs = [run("files"), run("db"), ...(r.recent_runs ?? [])].slice(0, 10);
}

function addLocalBackup(state, type, reason = null) {
  const b = state.backups;
  const at = Date.now();
  const lastFull = b.backups.find((x) => x.type === "full");
  const l = type === "full" || !lastFull ? label(at, "full") : label(at, "incr", lastFull.label);
  const backup = {
    label: l,
    type: l.endsWith("F") ? "full" : "incr",
    started_at: iso(at - 5000),
    finished_at: iso(at),
    size_bytes: 48_500_000,
    repo_size_bytes: l.endsWith("F") ? 4_720_000 : 25_000,
    reason,
  };
  b.backups.unshift(backup);
  b.walAt = at;
  if (b.localRepository === "none") b.localRepository = b.key ? "derived" : "legacy";
  return backup;
}

function snapshotSteps(state, d) {
  const failing = state.backups.runtime[d.id]?.state === "error";
  const fail = failing
    ? {
        fail: state.backups.runtime[d.id]?.last_error ?? "the destination does not answer",
        problem: "destination-unreachable",
        problemExtra: { destination_id: d.id },
        continueOnFail: true,
      }
    : {};
  return [
    ["snapshot", `snapshot: ${d.id} (db)`, fail],
    ["snapshot", `snapshot: ${d.id} (files)`, failing ? { skip: "the db snapshot failed" } : {}],
    ["forget", `forget: ${d.id}`, failing ? { skip: "the db snapshot failed" } : {}],
  ];
}

function jobAccepted(res, job) {
  sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
}

export const backupsRoutes = [
  { method: "GET", path: "/api/v1/backups", handler: ({ res, state }) => sendJson(res, 200, backupsOverview(state)) },
  { method: "GET", path: "/api/v1/backups/health", handler: ({ res, state }) => sendJson(res, 200, health(state)) },
  {
    method: "GET",
    path: "/api/v1/backups/destinations",
    handler: ({ res, state }) =>
      sendJson(res, 200, { items: destinationsOf(state).map((d) => destinationState(state, d)) }),
  },
  {
    method: "PUT",
    path: "/api/v1/backups/destinations/:id/secrets",
    handler: ({ res, state, params, body }) => {
      const d = (state.file.backups?.destinations ?? []).find((x) => x.id === params.id);
      if (!d) throw new Problem("not-found", `config.yaml has no destination ${params.id}.`);
      if (d.type !== "s3") throw invalid("", `a ${d.type} destination has no secret`, "no-secrets-for-type");
      if (!body.access_key_id || !body.secret_access_key) throw invalid("/access_key_id", "send both keys", "required");
      state.backups.secrets[d.id] = { access_key_id: body.access_key_id, updated_at: now() };
      const k = body.access_key_id;
      sendJson(res, 200, {
        id: d.id,
        type: "s3",
        present: true,
        access_key_id_hint: k.length > 8 ? `${k.slice(0, 4)}…${k.slice(-4)}` : "…",
        updated_at: now(),
      });
    },
  },
  {
    method: "GET",
    path: "/api/v1/backups/ssh-key",
    handler: ({ res, state, query }) => {
      const which = query.get("key") === "pending" ? "pending" : "active";
      sendJson(res, 200, sshPublicKey(keyOf(state, which), which, state.file.identity.hostname));
    },
  },
  {
    method: "GET",
    path: "/api/v1/backups/disks",
    handler: ({ res, state }) => sendJson(res, 200, { items: state.backups.disks, generated_at: now() }),
  },
  {
    method: "POST",
    path: "/api/v1/backups/disks/:id/prepare",
    reauth: true,
    handler: ({ res, state, params, body }) => {
      const disk = state.backups.disks.find((x) => x.id === params.id);
      if (!disk) throw new Problem("not-found", `No disk has the id ${params.id}.`);
      if (body.wipe !== true) throw invalid("/wipe", "must be true", "const");
      const lbl = body.label ?? "care-usb1";
      if (!LABEL.test(lbl)) throw invalid("/label", "must match ^care-[a-z0-9][a-z0-9-]{0,10}$", "pattern");
      if (disk.usage === "system") throw new Problem("disk-in-use", `${disk.device} holds the system.`);
      if (disk.mounted) throw new Problem("disk-in-use", `${disk.partitions[0]?.device} is mounted. Unmount it first.`);
      if (destinationsOf(state).some((d) => d.disk_label === lbl))
        throw new Problem("disk-in-use", `A destination uses the label ${lbl}.`);
      requireHostLock(state);
      const part = `${disk.device}1`;
      const mount = `/var/lib/care-pi/disks/${lbl}`;
      const job = startJob(state, {
        kind: "disk-prepare",
        steps: [
          ["check-disk", `check-disk: ${disk.device}`],
          ["wipe-disk", `wipe-disk: ${disk.device}`],
          ["partition", `partition: ${part}`],
          ["format", `format: ${part} ext4 ${lbl}`],
          ["write-mount-unit", `write-mount-unit: var-lib-care\\x2dpi-disks-${lbl.replace(/-/g, "\\x2d")}.mount`],
          ["mount", `mount: ${mount}`],
          ["check-destinations", "check-destinations"],
        ],
        result: () => ({
          kind: "disk-prepare",
          disk_id: disk.id,
          device: part,
          label: lbl,
          mount_path: mount,
          fs_type: "ext4",
          size_bytes: disk.size_bytes - 7_997_468_672,
        }),
        onDone: (_st, ok) => {
          if (!ok) return;
          Object.assign(disk, {
            usage: "care-backup",
            mounted: true,
            care_label: lbl,
            partitions: [{ device: part, size_bytes: disk.size_bytes - 2_097_152, fs_type: "ext4", label: lbl, mount }],
          });
        },
      });
      jobAccepted(res, job);
    },
  },
  {
    method: "GET",
    path: "/api/v1/backups/recovery-key/status",
    handler: ({ res, state }) => sendJson(res, 200, keyStatus(state.backups)),
  },
  {
    method: "GET",
    path: "/api/v1/backups/recovery-key",
    reauth: true,
    handler: ({ res, state, query }) => {
      const which = query.get("key") === "pending" ? "pending" : "active";
      sendJson(res, 200, recoveryKey(keyOf(state, which), which, state));
    },
  },
  {
    method: "GET",
    path: "/api/v1/backups/recovery-key/download",
    reauth: true,
    handler: ({ res, state, query }) => {
      const which = query.get("key") === "pending" ? "pending" : "active";
      const key = keyOf(state, which);
      const name = recoveryKey(key, which, state).file_name;
      res.writeHead(200, {
        "Content-Type": "text/plain; charset=utf-8",
        "Content-Disposition": `attachment; filename="${name}"`,
        "Cache-Control": "no-store",
      });
      res.end(keyText(key, state));
    },
  },
  {
    method: "POST",
    path: "/api/v1/backups/recovery-key",
    handler: ({ res, state }) => {
      requireHostLock(state);
      const b = state.backups;
      const key = makeKey();
      let which = "active";
      if (!b.key) {
        b.key = key;
      } else {
        b.pending = key;
        which = "pending";
      }
      sendJson(res, 201, { key: recoveryKey(key, which, state), status: keyStatus(b) });
    },
  },
  {
    method: "POST",
    path: "/api/v1/backups/recovery-key/confirm",
    handler: ({ res, state, body }) => {
      confirmKey(state, body.key === "pending" ? "pending" : "active", body.words);
      sendJson(res, 200, keyStatus(state.backups));
    },
  },
  {
    method: "POST",
    path: "/api/v1/backups/recovery-key/rotate",
    reauth: true,
    handler: ({ res, state, body }) => {
      const b = state.backups;
      if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish the setup first.");
      keyOf(state, "active");
      const newKey = body.new_key !== false;
      const removeOld = body.remove_old_repositories !== false;
      if (newKey && !b.pending?.confirmed_at)
        throw new Problem("state-conflict", "Make and confirm the pending key first.");
      if (!newKey && b.localRepository !== "legacy")
        throw new Problem("state-conflict", "The local repository already uses the recovery key.");
      requireHostLock(state);
      const dests = destinationsOf(state);
      const previous = b.key.key_id;
      const job = startJob(state, {
        kind: "recovery-key-rotate",
        reason: body.reason ?? null,
        steps: [
          ["check-destinations", `check-destinations: ${dests.length} destination(s)`],
          ["pause-backups", "pause-backups"],
          ["maintenance-on", "maintenance-on"],
          ["stop-app", "stop-app: care-api care-worker care-beat"],
          ["stop-db", "stop-db: care-db.service"],
          ["rekey-local", "rekey-local: /var/lib/care-pi/backup/pg"],
          ["start-db", "start-db: care-db.service"],
          ["ensure-stanza", "ensure-stanza"],
          ["start-app", "start-app: care.target"],
          ["wait-healthy", "wait-healthy: care-api.service"],
          ["maintenance-off", "maintenance-off"],
          ["backup-full", "backup-full: local"],
          ...dests.flatMap((d) => [
            ...(newKey ? [["init-destination", `init-destination: ${d.id}`]] : []),
            ["snapshot", `snapshot: ${d.id} (db)`],
          ]),
          ...(newKey ? [["activate-key", `activate-key: ${b.pending?.key_id}`]] : []),
          ...(removeOld ? [["remove-old-repositories", "remove-old-repositories"]] : []),
          ["resume-backups", "resume-backups"],
        ],
        result: (st) => ({
          kind: "recovery-key-rotate",
          new_key: newKey,
          key_id: st.backups.key.key_id,
          previous_key_id: newKey ? previous : null,
          local_rekeyed: true,
          full_backup_label: st.backups.backups[0]?.label ?? label(Date.now(), "full"),
          destinations: dests.map((d) => ({
            id: d.id,
            location: `${d.id}/care-pi-${st.backups.key.key_id}`,
            snapshot_id: snapId(),
          })),
          removed_repositories: removeOld ? ["/var/lib/care-pi/backup/pg.legacy"] : [],
        }),
        onDone: (st, ok, j) => {
          st.backups.rotationJobId = j.id;
          if (!ok) return;
          if (newKey) {
            st.backups.key = st.backups.pending;
            st.backups.pending = null;
          }
          st.backups.localRepository = "derived";
          if (removeOld) st.backups.backups = [];
          addLocalBackup(st, "full", "recovery-key-rotate");
          for (const d of dests) snapshotDone(st, d, true, j.id);
        },
      });
      b.rotationJobId = job.id;
      jobAccepted(res, job);
    },
  },
  {
    method: "POST",
    path: "/api/v1/backups/destinations/test",
    handler: async ({ res, state, body }) => {
      await new Promise((r) => setTimeout(r, Math.min(1500, state.stepMs)));
      sendJson(res, 200, testDestination(state, body, { createBucket: body.create_bucket === true }));
    },
  },
  {
    method: "POST",
    path: "/api/v1/backups/run",
    handler: ({ res, state, body }) => {
      requireReady(state);
      const dests = destinationsOf(state);
      const scope = body.scope ?? (body.destination ? "destination" : "all");
      const type = body.type === "full" ? "full" : "incr";
      let targets = [];
      if (scope === "destination") {
        const d = dests.find((x) => x.id === body.destination);
        if (!d) throw new Problem("not-found", `No destination has the id ${body.destination}.`);
        targets = [d];
      } else if (scope === "all") {
        if (dests.length === 0)
          throw new Problem("no-destination", "Add a destination first, or use the scope local-db.");
        targets = dests;
      }
      if (targets.length > 0 && !state.backups.key)
        throw new Problem("no-recovery-key", "Make the recovery key first.");
      const local = scope !== "destination";
      for (const d of targets) (state.backups.runtime[d.id] ??= { state: "pending", recent_runs: [] }).running = true;
      let localBackup = null;
      const job = startJob(state, {
        kind: "backup-run",
        holdsLock: false,
        cancellable: true,
        reason: body.reason ?? null,
        steps: [
          ...(local ? [[`backup-${type}`, `backup-${type}: local`]] : []),
          ...targets.flatMap((d) => snapshotSteps(state, d)),
        ],
        result: (st) => {
          if (local && !localBackup) localBackup = addLocalBackup(st, type, body.reason ?? null);
          return {
            kind: "backup-run",
            scope,
            local: localBackup
              ? {
                  label: localBackup.label,
                  type: localBackup.type === "full" ? "full" : "incr",
                  undo_point: localBackup.finished_at,
                  timeline: st.backups.timeline,
                  repo_size_bytes: localBackup.repo_size_bytes,
                }
              : null,
            destinations: targets.map((d) => ({
              id: d.id,
              db_snapshot_id: snapId(),
              files_snapshot_id: snapId(),
              added_bytes: 2_097_152,
            })),
          };
        },
        onDone: (st, _ok, j) => {
          if (local && !localBackup) localBackup = addLocalBackup(st, type, body.reason ?? null);
          for (const d of targets) {
            const failed = j.steps.some((s) => s.state === "failed" && s.name.startsWith(`snapshot: ${d.id} `));
            snapshotDone(st, d, !failed, j.id);
          }
        },
      });
      jobAccepted(res, job);
    },
  },
  {
    method: "POST",
    path: "/api/v1/backups/verify",
    handler: ({ res, state, body }) => {
      requireReady(state);
      const dests = destinationsOf(state);
      const scope = body.scope ?? (body.destination ? "destination" : "all");
      let targets = scope === "all" ? dests : [];
      if (scope === "destination") {
        const d = dests.find((x) => x.id === body.destination);
        if (!d) throw new Problem("not-found", `No destination has the id ${body.destination}.`);
        targets = [d];
      }
      const pct = body.read_data_subset_pct ?? 5;
      const job = startJob(state, {
        kind: "backup-verify",
        holdsLock: false,
        cancellable: true,
        steps: [
          ...(scope !== "destination" ? [["verify-local", "verify-local: /var/lib/care-pi/backup/pg"]] : []),
          ...targets.flatMap((d) => [
            ...(body.prune
              ? [
                  ["forget", `forget: ${d.id}`],
                  ["prune", `prune: ${d.id}`],
                ]
              : []),
            [
              "verify-destination",
              `verify-destination: ${d.id} (${pct} %)`,
              state.backups.runtime[d.id]?.state === "error"
                ? {
                    fail: state.backups.runtime[d.id].last_error,
                    problem: "destination-unreachable",
                    continueOnFail: true,
                  }
                : {},
            ],
          ]),
        ],
        result: () => ({
          kind: "backup-verify",
          local: scope !== "destination" ? state.backups.verifyLast : null,
          destinations: targets.map((d) => ({
            id: d.id,
            read_data_subset_pct: pct,
            pruned_bytes: body.prune ? 0 : null,
            duration_s: 41.2,
          })),
        }),
        onDone: (st, ok, j) => {
          const outcome = { job_id: j.id, finished_at: now(), ok: true, detail: null };
          if (scope !== "destination") st.backups.verifyLast = outcome;
          for (const d of targets) {
            const r = st.backups.runtime[d.id];
            if (r && r.state !== "error") r.last_verify = outcome;
          }
          void ok;
        },
      });
      jobAccepted(res, job);
    },
  },
  {
    method: "POST",
    path: "/api/v1/backups/drill",
    handler: ({ res, state, body }) => {
      requireReady(state);
      const last = state.backups.backups[0];
      if (!last) throw new Problem("insufficient-space", "No backup exists yet.");
      const started = Date.now();
      const job = startJob(state, {
        kind: "restore-drill",
        holdsLock: false,
        cancellable: true,
        reason: body.reason ?? null,
        steps: [
          ["check-space", "check-space: 97 MB necessary, 221 GB free"],
          ["drill-restore", `drill-restore: ${last.label}`],
          ["drill-start", "drill-start: care-drill-postgres"],
          ["drill-check", "drill-check: pg_controldata, SELECT 1"],
          ["drill-cleanup", "drill-cleanup"],
        ],
        result: () => ({
          kind: "restore-drill",
          outcome: "passed",
          backup_label: last.label,
          restored_bytes: last.size_bytes,
          duration_s: Math.round((Date.now() - started) / 100) / 10,
          checks: [
            { name: "pg_controldata", level: "required", ok: true, detail: "cluster state: in production" },
            { name: "select 1", level: "required", ok: true, detail: "1 row" },
          ],
          skipped_reason: null,
        }),
        onDone: (st, ok, j) => {
          st.backups.drills.unshift({
            job_id: j.id,
            finished_at: now(),
            outcome: ok ? "passed" : "failed",
            duration_s: Math.round((Date.now() - started) / 100) / 10,
            detail: ok ? null : "the test database did not start",
          });
        },
      });
      jobAccepted(res, job);
    },
  },
];
