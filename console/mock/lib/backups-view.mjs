// The views of the backup state of the mock: GET /backups, /backups/health, /backups/recovery-key/status,
// /restore/points. The agent reads its state files; the mock derives the same objects from state.backups.

import { DAY, HOUR, MIN, iso, sshPublicKey, hostKeyOf } from "./backups-state.mjs";

export const RETENTION_DEFAULTS = { keep_within_days: 14, keep_daily: 14, keep_weekly: 8, keep_monthly: 6 };

// backups.* of config.yaml with the defaults of docs/config.md.
export function backupsConfig(file) {
  const f = file.backups ?? {};
  return {
    wal_archive_timeout_min: 5,
    alert_after_hours: 24,
    paused: false,
    drill_enabled: true,
    ...f,
    schedule: {
      db_full: "Sun 01:00",
      db_incr: "daily 02:00",
      files: "daily 02:30",
      verify: "Sat 03:00",
      drill: "*-*-01 04:00",
      ...(f.schedule ?? {}),
    },
    retention: { db_full: 2, archive_days: 14, files_keep_daily: 14, files_keep_weekly: 8, ...(f.retention ?? {}) },
    destinations: (f.destinations ?? []).map(withDefaults),
  };
}

export function withDefaults(d) {
  return { ...d, interval_min: d.interval_min ?? 15, retention: { ...RETENTION_DEFAULTS, ...(d.retention ?? {}) } };
}

export function location(d, keyId) {
  const repo = `care-pi-${keyId ?? "00000000"}`;
  if (d.type === "posix") return d.disk_label ? `/var/lib/care-pi/disks/${d.disk_label}/${repo}` : `${d.path}/${repo}`;
  if (d.type === "sftp") return `sftp:${d.user}@${d.host}:${d.path}/${repo}`;
  return `s3:${d.endpoint}/${d.bucket}${d.prefix ? `/${d.prefix}` : ""}/${repo}`;
}

export function keyStatus(b) {
  const k = b.key;
  return {
    state: !k ? "none" : k.confirmed_at ? "confirmed" : "unconfirmed",
    key_id: k?.key_id ?? null,
    created_at: k?.created_at ?? null,
    confirmed_at: k?.confirmed_at ?? null,
    confirm_positions: k && !k.confirmed_at ? k.positions : null,
    pending: b.pending
      ? {
          key_id: b.pending.key_id,
          created_at: b.pending.created_at,
          confirmed_at: b.pending.confirmed_at,
          confirm_positions: b.pending.confirmed_at ? null : b.pending.positions,
        }
      : null,
    local_repository: b.localRepository,
    rotation_job_id: b.rotationJobId,
  };
}

function nextAt(ms, step) {
  return iso(Math.ceil(ms / step) * step);
}

export function destinationState(state, d) {
  const b = state.backups;
  const cfg = backupsConfig(state.file);
  const r = b.runtime[d.id] ?? { state: "pending", recent_runs: [] };
  const now = Date.now();
  const dd = withDefaults(d);
  const missing = d.type === "s3" && !b.secrets[d.id];
  const timersOn = !cfg.paused && state.setup.state === "ready";
  return {
    id: d.id,
    type: d.type,
    state: missing ? "error" : (r.state ?? "pending"),
    last_snapshot_at: r.last_db_snapshot_at ?? null,
    size_bytes: r.size_bytes ?? null,
    free_bytes: r.free_bytes ?? (d.type === "sftp" ? 1_200_000_000_000 : null),
    last_error: missing ? "the access keys are missing" : (r.last_error ?? null),
    location: location(d, b.key?.key_id),
    destination: dd,
    credentials: d.type === "s3" ? (b.secrets[d.id] ? "present" : "missing") : "not-needed",
    host_key_fingerprint:
      d.type === "sftp" && d.host_key ? hostKeyOf(d.host, b.servers[d.host]?.generation ?? 0).fingerprint_sha256 : null,
    last_db_snapshot_at: r.last_db_snapshot_at ?? null,
    last_files_snapshot_at: r.last_files_snapshot_at ?? null,
    last_attempt_at: r.last_attempt_at ?? null,
    last_error_code: missing ? "missing-credentials" : (r.last_error_code ?? null),
    failing_since: r.failing_since ?? null,
    next_db_run_at: timersOn ? nextAt(now, dd.interval_min * MIN) : null,
    next_files_run_at: timersOn ? iso(Math.ceil((now - 2.5 * HOUR) / DAY) * DAY + 2.5 * HOUR) : null,
    rpo_minutes: dd.interval_min + cfg.wal_archive_timeout_min,
    snapshot_count: r.snapshot_count ?? null,
    last_verify: r.last_verify ?? null,
    running: Boolean(r.running),
    recent_runs: r.recent_runs ?? [],
  };
}

export function localState(state) {
  const b = state.backups;
  const now = Date.now();
  const fulls = b.backups.filter((x) => x.type === "full");
  const incrs = b.backups.filter((x) => x.type !== "full");
  const oldestFull = fulls.at(-1);
  const timersOn = !backupsConfig(state.file).paused && state.setup.state === "ready";
  return {
    last_full_at: fulls[0]?.finished_at ?? null,
    last_incr_at: incrs[0]?.finished_at ?? null,
    last_wal_archived_at: b.walAt ? iso(b.walAt) : null,
    window_from: oldestFull?.finished_at ?? null,
    window_to: b.walAt ? iso(b.walAt) : null,
    size_bytes: b.backups.length ? b.backups.reduce((s, x) => s + x.repo_size_bytes, 0) : null,
    state: b.backups.length === 0 ? "none" : "ok",
    repo_path: "/var/lib/care-pi/backup/pg",
    key: b.backups.length === 0 && b.localRepository === "none" ? "none" : b.localRepository,
    timeline: b.backups.length ? b.timeline : null,
    backup_count: b.backups.length,
    last_backup: b.backups[0] ?? null,
    last_error: null,
    wal_archive_failing_since: null,
    free_bytes: 221_000_000_000,
    next_full_at: timersOn ? iso(Math.ceil(now / (7 * DAY)) * 7 * DAY + HOUR) : null,
    next_incr_at: timersOn ? iso(Math.ceil((now - 2 * HOUR) / DAY) * DAY + 2 * HOUR) : null,
    last_verify: b.verifyLast,
  };
}

const SEVERITY = { error: 0, warning: 1, info: 2 };

export function health(state) {
  const b = state.backups;
  const cfg = backupsConfig(state.file);
  const banners = [];
  const add = (code, severity, extra = {}) =>
    banners.push({ code, severity, destination_id: null, since: null, message: code.replace(/-/g, " "), ...extra });
  if (cfg.destinations.length === 0) add("no-destination", "error");
  if (!b.key) add("no-recovery-key", "error");
  else if (!b.key.confirmed_at) add("recovery-key-unconfirmed", "warning", { since: b.key.created_at });
  for (const d of cfg.destinations) {
    const s = destinationState(state, d);
    if (s.state === "error") {
      add("destination-unreachable", "error", {
        destination_id: d.id,
        since: s.last_db_snapshot_at,
        message: `${d.id} has no successful snapshot for ${cfg.alert_after_hours} hours`,
      });
    }
  }
  if (b.verifyLast && !b.verifyLast.ok) add("verify-failed", "error", { since: b.verifyLast.finished_at });
  if (b.drills[0]?.outcome === "failed") add("drill-failed", "warning", { since: b.drills[0].finished_at });
  if (cfg.paused) add("backups-paused", "warning");
  if (b.localRepository === "legacy") add("local-repository-legacy-key", "info");
  banners.sort((x, y) => SEVERITY[x.severity] - SEVERITY[y.severity]);
  return { state: banners[0]?.severity ?? "ok", banners, generated_at: iso(Date.now()) };
}

function schedule(state) {
  const cfg = backupsConfig(state.file);
  const local = localState(state);
  const on = !cfg.paused && state.setup.state === "ready";
  const entry = (id, kind, unit, calendar, next, extra = {}) => ({
    id,
    kind,
    destination_id: null,
    unit,
    calendar,
    interval_min: null,
    enabled: on,
    next_run_at: on ? next : null,
    last_run_at: null,
    last_ok: null,
    ...extra,
  });
  const out = [
    entry("db-full", "db-full", "care-backup-db-full.timer", cfg.schedule.db_full, local.next_full_at, {
      last_run_at: local.last_full_at,
      last_ok: local.last_full_at ? true : null,
    }),
    entry("db-incr", "db-incr", "care-backup-db-incr.timer", cfg.schedule.db_incr, local.next_incr_at, {
      last_run_at: local.last_incr_at,
      last_ok: local.last_incr_at ? true : null,
    }),
    entry("verify", "verify", "care-backup-verify.timer", cfg.schedule.verify, iso(Date.now() + 2 * DAY)),
    entry("drill", "drill", "care-backup-drill.timer", cfg.schedule.drill, iso(Date.now() + 9 * DAY), {
      enabled: on && cfg.drill_enabled,
    }),
  ];
  for (const d of cfg.destinations) {
    const s = destinationState(state, d);
    out.push(
      entry(`db:${d.id}`, "snapshot-db", `care-backup-db@${d.id}.timer`, null, s.next_db_run_at, {
        destination_id: d.id,
        interval_min: d.interval_min,
        last_run_at: s.last_attempt_at,
        last_ok: s.last_attempt_at ? s.state === "ok" : null,
      }),
      entry(
        `files:${d.id}`,
        "snapshot-files",
        `care-backup-files@${d.id}.timer`,
        cfg.schedule.files,
        s.next_files_run_at,
        {
          destination_id: d.id,
          last_run_at: s.last_files_snapshot_at,
          last_ok: s.last_files_snapshot_at ? true : null,
        },
      ),
    );
  }
  return out;
}

export function overview(state, runningJob) {
  const b = state.backups;
  const cfg = backupsConfig(state.file);
  const destinations = cfg.destinations.map((d) => destinationState(state, d));
  const ks = keyStatus(b);
  return {
    local: localState(state),
    destinations,
    recovery_key_confirmed: ks.state === "confirmed",
    generated_at: iso(Date.now()),
    configured: destinations.length > 0,
    paused: cfg.paused,
    health: health(state),
    recovery_key: ks,
    schedule: schedule(state),
    retention: {
      db_full: cfg.retention.db_full,
      files_keep_daily: cfg.retention.files_keep_daily,
      files_keep_weekly: cfg.retention.files_keep_weekly,
    },
    rpo: {
      wal_archive_timeout_min: cfg.wal_archive_timeout_min,
      local_minutes: cfg.wal_archive_timeout_min,
      offsite_minutes: destinations.length ? Math.min(...destinations.map((d) => d.rpo_minutes)) : null,
    },
    verify: {
      schedule: cfg.schedule.verify,
      next_run_at: state.setup.state === "ready" && !cfg.paused ? iso(Date.now() + 2 * DAY) : null,
      last: b.verifyLast,
    },
    drill: {
      enabled: cfg.drill_enabled,
      schedule: cfg.schedule.drill,
      next_run_at: state.setup.state === "ready" && cfg.drill_enabled && !cfg.paused ? iso(Date.now() + 9 * DAY) : null,
      last: b.drills[0] ?? null,
    },
    ssh_key: b.key ? sshPublicKey(b.key, "active", state.file.identity.hostname) : null,
    running_job: runningJob ?? null,
  };
}

// The snapshots of each destination: a db snapshot each interval for the last 2 hours, and the daily files snapshots.
export function snapshotsOf(state) {
  const b = state.backups;
  const out = [];
  for (const d of backupsConfig(state.file).destinations) {
    const r = b.runtime[d.id];
    if (!r?.last_db_snapshot_at) continue;
    const lastDb = Date.parse(r.last_db_snapshot_at);
    for (let i = 0; i < 8; i++) {
      const t = lastDb - i * d.interval_min * MIN;
      out.push({
        destination: d.id,
        snapshot_id: hex(`${d.id}db${t}`),
        time: iso(t),
        contents: ["db", "config"],
        size_bytes: 52_428_800,
        hostname: state.file.identity.hostname,
        key_id: b.key?.key_id ?? "00000000",
      });
    }
    if (r.last_files_snapshot_at) {
      const lastFiles = Date.parse(r.last_files_snapshot_at);
      for (let i = 0; i < 3; i++) {
        const t = lastFiles - i * DAY;
        out.push({
          destination: d.id,
          snapshot_id: hex(`${d.id}files${t}`),
          time: iso(t),
          contents: ["files", "config"],
          size_bytes: 734_003_200 - i * 3_000_000,
          hostname: state.file.identity.hostname,
          key_id: b.key?.key_id ?? "00000000",
        });
      }
    }
  }
  return out.sort((x, y) => Date.parse(y.time) - Date.parse(x.time));
}

function hex(seed) {
  let h = 2166136261;
  for (const c of seed) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
  return h.toString(16).padStart(8, "0");
}

export function restorePoints(state) {
  const b = state.backups;
  const local = localState(state);
  return {
    pitr: local.window_from ? { from: local.window_from, to: local.window_to, timeline: b.timeline } : null,
    file_snapshots: snapshotsOf(state),
    generated_at: iso(Date.now()),
    db_backups: b.backups,
    checkpoints: b.checkpoints,
    destinations: backupsConfig(state.file).destinations.map((d) => {
      const r = b.runtime[d.id];
      return {
        id: d.id,
        listed_at: r?.last_attempt_at ?? null,
        ok: r?.last_attempt_at ? r.state !== "error" : null,
        error: r?.state === "error" ? r.last_error : null,
        snapshot_count: r?.snapshot_count ?? null,
      };
    }),
  };
}
