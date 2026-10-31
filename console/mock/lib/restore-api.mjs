// The restore routes of the mock (docs/api/openapi.yaml, tag restore) and the setup step backups.

import { Problem, sendJson } from "./http.mjs";
import { runningHostJob, startJob } from "./jobs.mjs";
import { READY_PASSWORD, STEP_IDS, now } from "./state.mjs";
import { DR_KEY, hostKeyOf } from "./backups-state.mjs";
import {
  confirmKey,
  entropyOf,
  invalid,
  iso,
  keyStatus,
  label,
  requireHostLock,
  testDestination,
} from "./backups-logic.mjs";
import { backupsConfig, restorePoints, snapshotsOf } from "./backups-view.mjs";

const ID = /^[a-z0-9][a-z0-9-]{0,30}$/;
const MIN = 60_000;
const HOUR = 60 * MIN;

// The places where the backup of the old device is (POST /restore/dr/inspect): the disk care-usb1 (scenario restore),
// the folder /mnt/old-backup, the SFTP folder nas.lan:/volume1/care, and the S3 bucket sunrise-care.
function drSourceOk(state, d) {
  if (d.type === "posix") {
    if (d.disk_label) return state.backups.disks.some((x) => x.care_label === d.disk_label && !x.destination_id);
    return d.path === "/mnt/old-backup";
  }
  if (d.type === "sftp") return d.host === "nas.lan" && d.path === "/volume1/care";
  if (d.type === "s3") return d.bucket === "sunrise-care" && Boolean(d.access_key_id && d.secret_access_key);
  return false;
}

function drCheck(state, body) {
  const s = state.setup.state;
  if (s === "ready")
    throw new Problem("setup-finished", "Setup is finished. A restore of a new device needs a new device.");
  if (s === "applying") throw new Problem("job-running", "A job runs now.", { job_id: state.setup.jobId });
  const words = body.recovery_key?.words;
  if (!Array.isArray(words) || words.length !== 24 || !words.every((w) => /^[a-z]{3,8}$/.test(w)))
    throw invalid("/recovery_key/words", "send 24 words in lowercase letters", "minItems");
  const entropy = entropyOf(words);
  if (!entropy) throw invalid("/recovery_key/words", "the words do not have a valid checksum", "checksum");
  const d = body.destination ?? {};
  if (!["posix", "sftp", "s3"].includes(d.type))
    throw invalid("/destination/type", "must be posix, sftp, or s3", "enum");
  if (d.type === "sftp") {
    if (d.host?.includes("down")) throw new Problem("destination-unreachable", `${d.host}: no route to host.`);
    const presented = { ...hostKeyOf(d.host ?? ""), trusted: false };
    if (!d.host_key || d.host_key !== presented.public_key) {
      throw new Problem(
        "destination-untrusted-hostkey",
        `${d.host} presents a host key that is not pinned. Compare the fingerprint with the server.`,
        { host_key: presented },
      );
    }
  }
  if (d.type === "s3" && d.access_key_id === "bad")
    throw new Problem("destination-unreachable", "403 InvalidAccessKeyId.");
  if (!drSourceOk(state, d)) throw new Problem("destination-unreachable", "No care-pi backup is at this place.");
  if (!entropy.equals(DR_KEY.entropy)) {
    throw new Problem(
      "recovery-key-mismatch",
      "The recovery key cannot open a care-pi repository at the destination. A key of another device?",
    );
  }
  return d;
}

function drInspection(state, d) {
  const at = Date.now();
  const dbTime = at - 40 * MIN;
  const filesTime = at - 8 * HOUR;
  const snap = (id, t, contents, size) => ({
    destination: null,
    snapshot_id: id,
    time: iso(t),
    contents,
    size_bytes: size,
    hostname: "care",
    key_id: DR_KEY.key_id,
  });
  const db = snap("4f1e9a2c", dbTime, ["db", "config"], 52_428_800);
  const files = snap("9b8a7c6d", filesTime, ["files", "config"], 734_003_200);
  return {
    key_id: DR_KEY.key_id,
    location:
      d.type === "posix"
        ? `${d.disk_label ? `/var/lib/care-pi/disks/${d.disk_label}` : d.path}/care-pi-${DR_KEY.key_id}`
        : d.type === "sftp"
          ? `sftp:${d.user}@${d.host}:${d.path}/care-pi-${DR_KEY.key_id}`
          : `s3:${d.endpoint}/${d.bucket}${d.prefix ? `/${d.prefix}` : ""}/care-pi-${DR_KEY.key_id}`,
    source: {
      display_name: "Sunrise Clinic",
      hostname: "care",
      agent_version: "v0.4.0",
      solution: "care-clinic",
      config_time: iso(dbTime),
    },
    snapshots: [db, snap("3e2d1c0b", dbTime - 15 * MIN, ["db", "config"], 52_400_000), files],
    newest_db_snapshot: db,
    newest_files_snapshot: files,
    pitr: { from: iso(at - 11 * 24 * HOUR), to: iso(dbTime - MIN) },
    sizes: { db_repository_bytes: 52_428_800, files_bytes: 734_003_200, database_bytes: 48_500_000 },
    space: { path: "/var/lib/care-pi", required_bytes: 1_908_408_320, free_bytes: 221_000_000_000, ok: true },
    warnings: [
      {
        code: "files-older-than-db",
        message: "the newest files snapshot is 7 hours older than the database",
        pointer: null,
      },
    ],
  };
}

// The destination of config.yaml for the destination of a restore of a new device.
function configDestination(d) {
  if (d.type === "posix")
    return d.disk_label
      ? { id: "usb", type: "posix", disk_label: d.disk_label }
      : { id: "backup", type: "posix", path: d.path };
  if (d.type === "sftp")
    return {
      id: "nas",
      type: "sftp",
      host: d.host,
      port: d.port ?? 22,
      user: d.user,
      path: d.path,
      host_key: d.host_key,
    };
  return {
    id: "b2",
    type: "s3",
    endpoint: d.endpoint,
    region: d.region ?? undefined,
    bucket: d.bucket,
    prefix: d.prefix ?? undefined,
  };
}

function validDestination(d, i) {
  const at = (k) => `/destinations/${i}/${k}`;
  const bad = (k, message, code = "pattern") =>
    new Problem("invalid-config", "1 value does not match the JSON Schema.", {
      errors: [{ pointer: at(k), message, code }],
    });
  if (!ID.test(d.id ?? "")) return bad("id", "must match ^[a-z0-9][a-z0-9-]{0,30}$");
  if (d.type === "posix" && Boolean(d.path) === Boolean(d.disk_label))
    return bad("path", "use path or disk_label", "oneOf");
  if (d.type === "sftp" && !(d.host && d.user && d.path))
    return bad("host", "host, user, and path are necessary", "required");
  if (d.type === "s3" && !(d.endpoint && d.bucket))
    return bad("bucket", "endpoint and bucket are necessary", "required");
  if (d.interval_min !== undefined && (d.interval_min < 5 || d.interval_min > 1440))
    return bad("interval_min", "from 5 to 1440", "range");
  return null;
}

export function validateDestinations(list) {
  const ids = new Set();
  for (const [i, d] of list.entries()) {
    const p = validDestination(d, i);
    if (p) return p;
    if (ids.has(d.id)) {
      return new Problem("invalid-config", "2 destinations have the same id.", {
        errors: [{ pointer: `/destinations/${i}/id`, message: "the id is not unique", code: "unique" }],
      });
    }
    ids.add(d.id);
  }
  return null;
}

// The errors of the step backups (StepIssue codes of docs/api/README.md section 13.13).
export function backupsStepErrors(state) {
  const b = state.backups;
  const errors = [];
  if (!b.key) errors.push({ code: "no-recovery-key", message: "make the recovery key", pointer: null });
  else if (!b.key.confirmed_at)
    errors.push({
      code: "recovery-key-unconfirmed",
      message: "confirm the recovery key",
      pointer: "/recovery_key_words",
    });
  if ((state.file.backups?.destinations ?? []).length === 0)
    errors.push({ code: "no-destination", message: "add 1 or more destinations", pointer: "/destinations" });
  return errors;
}

export function backupsDraft(state) {
  const b = state.backups;
  const destinations = state.file.backups?.destinations ?? [];
  const ks = keyStatus(b);
  return {
    destinations,
    recovery_key_confirmed: ks.state === "confirmed",
    recovery_key: ks,
    credentials: Object.fromEntries(
      destinations.filter((d) => d.type === "s3").map((d) => [d.id, b.secrets[d.id] ? "present" : "missing"]),
    ),
  };
}

export function setupRestore(state) {
  const last = state.backups.lastDr;
  return {
    available: (state.setup.state === "claimed" || state.setup.state === "setup") && !runningHostJob(state),
    job_id: last?.id ?? null,
    job_state: last?.state ?? null,
  };
}

function pitrSteps(target) {
  return [
    ["pause-backups", "pause-backups"],
    ["maintenance-on", "maintenance-on"],
    ["stop-app", "stop-app: care-api care-worker care-beat"],
    ["checkpoint", "checkpoint: incr"],
    ["stop-db", "stop-db: care-db.service"],
    ["restore-db", `restore-db: ${target}`],
    ["start-db", "start-db: care-db.service"],
    ["wait-promote", "wait-promote"],
    ["start-app", "start-app: care.target"],
    ["wait-healthy", "wait-healthy: care-api.service"],
    ["maintenance-off", "maintenance-off"],
    ["backup-full", "backup-full: local"],
    ["check-stanza", "check-stanza"],
    ["resume-backups", "resume-backups"],
  ];
}

export const restoreRoutes = [
  {
    method: "GET",
    path: "/api/v1/restore/points",
    handler: ({ res, state }) => sendJson(res, 200, restorePoints(state)),
  },
  {
    method: "POST",
    path: "/api/v1/restore",
    reauth: true,
    handler: ({ res, state, body }) => {
      if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish the setup first.");
      const b = state.backups;
      if (body.kind === "pitr") {
        const points = restorePoints(state);
        if (!points.pitr)
          throw new Problem("restore-window-exceeded", "No backup exists.", {
            restore_window: { from: null, to: null },
          });
        let target;
        let restoredTo;
        if (body.checkpoint) {
          const cp =
            b.checkpoints.find((c) => c.label === body.checkpoint) ??
            b.backups.find((x) => x.label === body.checkpoint);
          if (!cp) throw new Problem("not-found", `No checkpoint has the label ${body.checkpoint}.`);
          target = body.checkpoint;
          restoredTo = cp.undo_point ?? cp.finished_at;
        } else {
          const t = Date.parse(body.time ?? "");
          if (Number.isNaN(t)) throw invalid("/time", "must be a date-time", "format");
          if (t < Date.parse(points.pitr.from) || t > Date.now()) {
            throw new Problem("restore-window-exceeded", `The earliest point in time is ${points.pitr.from}.`, {
              restore_window: { from: points.pitr.from, to: points.pitr.to },
            });
          }
          target = iso(t);
          restoredTo = iso(t);
        }
        requireHostLock(state);
        const started = Date.now();
        const lastFull = b.backups.find((x) => x.type === "full");
        const checkpoint = {
          label: label(started, "incr", lastFull?.label ?? label(started, "full")),
          undo_point: iso(started),
          timeline: b.timeline,
          job_id: null,
          job_kind: "restore-pitr",
          reason: body.reason ?? null,
        };
        const job = startJob(state, {
          kind: "restore-pitr",
          reason: body.reason ?? null,
          steps: pitrSteps(target),
          result: (st) => ({
            kind: "restore-pitr",
            restored_to: restoredTo,
            timeline: st.backups.timeline + 1,
            checkpoint,
            full_backup_label: label(Date.now(), "full"),
            downtime_s: Math.round((Date.now() - started) / 100) / 10,
          }),
          onDone: (st, ok) => {
            if (!ok) return;
            st.backups.timeline += 1;
            st.backups.checkpoints.unshift(checkpoint);
            st.backups.backups.unshift({
              label: checkpoint.label,
              type: "incr",
              started_at: iso(started),
              finished_at: iso(started),
              size_bytes: 48_500_000,
              repo_size_bytes: 25_000,
              reason: body.reason ?? null,
            });
            st.backups.backups.unshift({
              label: label(Date.now(), "full"),
              type: "full",
              started_at: now(),
              finished_at: now(),
              size_bytes: 48_500_000,
              repo_size_bytes: 4_720_000,
              reason: "after a restore",
            });
            st.backups.walAt = Date.now();
          },
        });
        checkpoint.job_id = job.id;
        sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
        return;
      }
      if (body.kind === "files") {
        const snap = snapshotsOf(state).find(
          (s) => s.destination === body.destination && s.snapshot_id === body.snapshot_id,
        );
        if (!snap || !snap.contents.includes("files"))
          throw new Problem("not-found", `No files snapshot ${body.snapshot_id} at ${body.destination}.`);
        const target = body.target === "side-dir" ? "side-dir" : "in-place";
        for (const [i, p] of (body.paths ?? []).entries()) {
          if (!/^[A-Za-z0-9_-][A-Za-z0-9._-]*(\/[A-Za-z0-9_-][A-Za-z0-9._-]*)*\/?$/.test(p))
            throw invalid(`/paths/${i}`, "not a valid path", "pattern");
        }
        requireHostLock(state);
        const steps =
          target === "side-dir"
            ? [
                ["check-space", "check-space: 770 MB necessary, 221 GB free"],
                ["restore-files", `restore-files: ${body.destination} ${body.snapshot_id}`],
                ["verify-files", "verify-files"],
              ]
            : [
                ["pause-backups", "pause-backups"],
                ["check-space", "check-space: 770 MB necessary, 221 GB free"],
                ["maintenance-on", "maintenance-on"],
                ["stop-s3", "stop-s3: care-s3.service"],
                ["restore-files", `restore-files: ${body.destination} ${body.snapshot_id}`],
                ["verify-files", "verify-files"],
                ["start-s3", "start-s3: care-s3.service"],
                ["maintenance-off", "maintenance-off"],
                ["resume-backups", "resume-backups"],
              ];
        const job = startJob(state, {
          kind: "restore-files",
          reason: body.reason ?? null,
          steps,
          result: (_st, j) => ({
            kind: "restore-files",
            destination: body.destination,
            snapshot_id: body.snapshot_id,
            snapshot_time: snap.time,
            target,
            path: target === "side-dir" ? `/var/lib/care-pi/restore/${j.id}` : "/var/lib/care-pi/s3",
            files: body.paths ? 214 : 1873,
            bytes: body.paths ? 88_080_384 : 734_003_200,
            verified: true,
          }),
        });
        sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
        return;
      }
      throw invalid("/kind", "must be pitr or files", "enum");
    },
  },
  {
    method: "POST",
    path: "/api/v1/restore/dr/inspect",
    handler: async ({ res, state, body }) => {
      await new Promise((r) => setTimeout(r, Math.min(1500, state.stepMs)));
      const d = drCheck(state, body);
      sendJson(res, 200, drInspection(state, d));
    },
  },
  {
    method: "POST",
    path: "/api/v1/restore/dr",
    handler: ({ res, state, body }) => {
      const d = drCheck(state, body);
      requireHostLock(state);
      const insp = drInspection(state, d);
      const files = body.files !== false;
      const time = body.time ?? null;
      const job = startJob(state, {
        kind: "restore-dr",
        steps: [
          ["check-key", `check-key: ${DR_KEY.key_id}`],
          ["check-space", "check-space: 1.8 GB necessary, 221 GB free"],
          ["fetch-config", `fetch-config: ${insp.newest_db_snapshot.snapshot_id}`],
          ["ensure-dirs", "ensure-dirs"],
          ["write-files", "write-files: 18 file(s)"],
          ["daemon-reload", "daemon-reload"],
          ["enable-target", "enable-target: care-start.service"],
          ["pull-images", "pull-images: 7 image(s)", { progress: "items" }],
          ["ensure-fe", "ensure-fe", { progress: "percent", stream: "stdout" }],
          ["write-lock", "write-lock"],
          ["fetch-db-repo", `fetch-db-repo: ${insp.newest_db_snapshot.snapshot_id}`],
          ["restore-db", `restore-db: ${time ?? "latest"}`],
          ...(files ? [["fetch-files", `fetch-files: ${insp.newest_files_snapshot.snapshot_id}`]] : []),
          ["start-target", "start-target: care.target"],
          ["wait-promote", "wait-promote"],
          ["wait-healthy", "wait-healthy: care-api.service"],
          ["backup-full", "backup-full: local"],
          ["ensure-repositories", "ensure-repositories: 1 destination(s)"],
          ["ensure-timers", "ensure-timers"],
          ["set-hostname", `set-hostname: ${state.file.identity.hostname} -> care`],
          ["restore-account", "restore-account"],
          ["finish", "finish"],
        ],
        result: () => ({
          kind: "restore-dr",
          key_id: DR_KEY.key_id,
          location: insp.location,
          db_snapshot_id: insp.newest_db_snapshot.snapshot_id,
          db_snapshot_time: insp.newest_db_snapshot.time,
          restored_to: time ?? insp.pitr.to,
          timeline: 2,
          files_snapshot_id: files ? insp.newest_files_snapshot.snapshot_id : null,
          files_snapshot_time: files ? insp.newest_files_snapshot.time : null,
          skew_s: files
            ? Math.round((Date.parse(time ?? insp.pitr.to) - Date.parse(insp.newest_files_snapshot.time)) / 1000)
            : null,
          hostname: "care",
          care_url: "https://care.local/",
          console_url: "https://care.local:8443/",
          urls: ["https://care.local/", "https://192.168.1.10/"],
          backups_paused: body.pause_backups === true,
          console_account: "restored",
        }),
        onDone: (st, ok, j) => {
          st.backups.lastDr = j;
          if (!ok) {
            st.setup.state = "setup";
            st.setup.jobId = j.id;
            return;
          }
          const dest = configDestination(d);
          st.file.identity = { display_name: "Sunrise Clinic", hostname: "care" };
          st.file.backups = { destinations: [dest], ...(body.pause_backups ? { paused: true } : {}) };
          if (d.type === "s3") st.backups.secrets[dest.id] = { access_key_id: d.access_key_id, updated_at: now() };
          st.backups.key = { ...DR_KEY };
          st.backups.pending = null;
          st.backups.localRepository = "derived";
          st.backups.backups = [
            {
              label: label(Date.now(), "full"),
              type: "full",
              started_at: now(),
              finished_at: now(),
              size_bytes: 48_500_000,
              repo_size_bytes: 4_720_000,
              reason: "after a restore",
            },
          ];
          st.backups.walAt = Date.now();
          st.backups.runtime[dest.id] = { state: "pending", recent_runs: [] };
          if (d.type === "posix" && d.disk_label) {
            const disk = st.backups.disks.find((x) => x.care_label === d.disk_label);
            if (disk) Object.assign(disk, { destination_id: dest.id, mounted: true });
          }
          st.setup.state = "ready";
          st.setup.readyAt = now();
          st.setup.jobId = null;
          for (const id of STEP_IDS)
            st.setup.steps[id] = { status: "done", updated_at: now(), errors: [], warnings: [] };
          st.appliedHostname = "care";
          st.feBuilds = [{ id: "8d2f0c1a9b7e", bytes: 13_421_772, modified_at: now(), current: true }];
          // The console account of the old device comes back (docs/api/README.md section 13.9.3).
          st.password = READY_PASSWORD;
          st.totp = { enabled: false, enrolledAt: null, pending: null };
          st.sessions.clear();
        },
      });
      state.setup.state = "applying";
      state.setup.jobId = job.id;
      state.backups.lastDr = job;
      sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
    },
  },
];

// PUT /setup/steps/backups (docs/api/openapi.yaml): setup.mjs gives the helpers of the state machine.
export function backupsStepHandler({ res, state, body }, { markDone, setupState, guardChange }) {
  guardChange(state);
  const b = state.backups;
  const list = body.destinations;
  if (!Array.isArray(list) || list.length === 0)
    throw invalid("/destinations", "add 1 or more destinations", "minItems");
  const bad = validateDestinations(list);
  if (bad) throw bad;
  for (const d of list) {
    if (d.type === "sftp" && !d.host_key) {
      throw new Problem(
        "destination-untrusted-hostkey",
        `${d.host}: the host key is not pinned. Test the destination first.`,
        {
          destination_id: d.id,
          host_key: { ...hostKeyOf(d.host), trusted: false },
        },
      );
    }
  }
  for (const d of list) {
    if (d.type !== "s3") continue;
    const c = body.credentials?.[d.id];
    if (!c && !b.secrets[d.id])
      throw invalid(`/credentials/${d.id}`, `the destination ${d.id} needs its keys`, "missing-credentials");
  }
  if (!b.key) throw new Problem("no-recovery-key", "Make the recovery key first with POST /backups/recovery-key.");
  for (const d of list) {
    const creds = body.credentials?.[d.id] ?? b.secrets[d.id] ?? null;
    const r = testDestination(state, { destination: d, credentials: creds, accept_fingerprint: null });
    if (!r.ok) {
      const failed = r.checks.find((c) => !c.ok && c.level === "required");
      throw new Problem("destination-unreachable", `${d.id}: ${failed?.detail ?? "the test failed"}.`, {
        destination_id: d.id,
      });
    }
  }
  if (body.recovery_key_words) confirmKey(state, "active", body.recovery_key_words);
  else if (!b.key.confirmed_at) throw invalid("/recovery_key_words", "confirm the recovery key", "required");
  for (const d of list) {
    const c = body.credentials?.[d.id];
    if (c) b.secrets[d.id] = { access_key_id: c.access_key_id, updated_at: now() };
  }
  state.file.backups = { ...(state.file.backups ?? {}), destinations: list };
  for (const d of list) b.runtime[d.id] ??= { state: "pending", recent_runs: [] };
  markDone(state, "backups");
  sendJson(res, 200, setupState(state));
}

export { backupsConfig };
