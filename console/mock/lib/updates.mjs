// Phase 4 of the mock: updates, OS updates, plugs, and solutions (docs/api/README.md section 14,
// docs/solutions.md). The state lives in state.u (freshUpdates). Reset options:
//   updates: idle | available | staged | updated | recovered | agent-too-old | withdrawn   (default idle)
//   window_open: true | false | "real"   (default false: the window is closed; "real" computes it from the clock)
//   update_fail: checkpoint | migrate | verify | rollback   (one update fails at this step, then rolls back)
//   phase4: false   (each Phase 4 operation answers 501, as an agent of Phase 2 or 3)
//   no_plugs: true  (a solution without plugs)

import { createHash } from "node:crypto";
import { Problem, problemOf, sendJson, sendSchema } from "./http.mjs";
import { runningHostJob, startJob, summary } from "./jobs.mjs";
import { effectiveConfig, etagOf, now } from "./state.mjs";

const TZ = "Asia/Kolkata";
const TZ_OFFSET_MIN = 330;
const SOLUTION = {
  name: "care-clinic",
  title: "CARE Clinic",
  description: "Outpatient clinic. Appointments, encounters, prescriptions, and invoices.",
};
const REL = "https://github.com/rithviknishad/care-pi/releases/";
const DIST = `${REL}download/`;
const UNITS = ["care-beat.service", "care-worker.service", "care-api.service"];
const OFF = { active: false, kind: null, job_id: null, step: null, started_at: null };
const UPDATE_KINDS = ["update-check", "update-stage", "update-apply", "update-rollback", "os-update", "plug-change"];

// The locks of care-clinic. care/care_fe are the identities of the running components.
const L0 = {
  version: "2026.11.0",
  released_at: "2026-11-14T10:00:00Z",
  summary: "First lock of CARE Clinic. care develop of 2026-11-13, care_fe develop of 2026-11-13.",
  min_agent_version: "v0.4.0",
  lock_sha256: "4df1613845b512b3d89c77298d30d34222a5ae7e9c22ef726f59f43eb4b3bd74",
  care: "sha256:5e0c3b2a1f9d8e7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c",
  care_fe: "83b007b72cd0e5b1f2a3c4d5e6f708192a3b4c5d",
  withdrawn: false,
  withdrawn_reason: null,
};
const L1 = {
  version: "2026.11.1",
  released_at: "2026-11-21T08:55:00Z",
  summary: "care develop of 2026-11-20. Faster appointments plug 2026-11-18.",
  min_agent_version: "v0.4.0",
  lock_sha256: "da007b85a27bdadce321def754cce1984b7af1a00927e026357ce1dd3b6efc5b",
  care: "sha256:a626f7197cb8b77c940893a599ce00c723cac7a758d6d3fab0134b12e6de0a9f",
  care_fe: "e4d292b313571fdb6916395fd8cebc655dcf24f7",
  withdrawn: false,
  withdrawn_reason: null,
};
// The lock of the Phase 2 install: no solution lock (solution-refs).
const L_REFS = {
  version: null,
  released_at: null,
  summary: null,
  min_agent_version: "v0.2.0",
  lock_sha256: null,
  care: "sha256:77fe743e51a6eb322e89e18183c6012fa6ab8efdd447e8ff59dfb387e6da6ec3",
  care_fe: "44f07eaa857300b1e2f9c3d4a5b6c7d8e9f0a1b2",
  withdrawn: false,
  withdrawn_reason: null,
};

const POSTGRES = "sha256:d80dc0a202c84e8fa4dcb3023143dff30cb5d99dae8c4731d84dab05aab52efb";

function hex16(text) {
  return createHash("sha256").update(text).digest("hex").slice(0, 16);
}

function lockId(date = new Date()) {
  return date
    .toISOString()
    .replace(/[-:]/g, "")
    .replace(/\.\d{3}Z$/, "Z");
}

function addSeconds(iso, s) {
  return new Date(Date.parse(iso) + s * 1000).toISOString().replace(/\.\d{3}Z$/, "Z");
}

function semverLess(a, b) {
  const pa = String(a).replace(/^v/, "").split(/[.-]/).map(Number);
  const pb = String(b).replace(/^v/, "").split(/[.-]/).map(Number);
  for (let i = 0; i < 3; i++) if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) < (pb[i] ?? 0);
  return false;
}

function versionLess(a, b) {
  const pa = a.split(".").map(Number);
  const pb = b.split(".").map(Number);
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return pa[i] < pb[i];
  return false;
}

function checkpointOf(_jobId, at) {
  return {
    taken_at: at,
    target_time: addSeconds(at, 28),
    // The checkpoint of Phase 3 (docs/api/README.md section 13.11) makes no named restore point.
    restore_point: null,
    backup_label: `20261115-010000F_${lockId(new Date(at)).slice(0, 8)}-${lockId(new Date(at)).slice(9, 15)}I`,
    duration_s: 28.4,
  };
}

function offerOf(cp) {
  return {
    available: true,
    reason: null,
    checkpoint_at: cp.taken_at,
    target_time: cp.target_time,
    restore_point: cp.restore_point,
    restore: { kind: "pitr", time: cp.target_time },
  };
}

function historyEntry(job, fields) {
  return {
    job_id: job.id,
    kind: fields.kind,
    source: job.source ?? "console",
    reason: job.reason ?? null,
    started_at: job.started_at,
    finished_at: now(),
    outcome: fields.outcome,
    from_version: fields.from_version ?? null,
    to_version: fields.to_version ?? null,
    from_lock: fields.from_lock ?? null,
    to_lock: fields.to_lock ?? null,
    plug_id: fields.plug_id ?? null,
    checkpoint: fields.checkpoint ?? null,
    pitr_offer: fields.pitr_offer ?? null,
    problem: fields.problem ?? null,
    downtime_s: fields.downtime_s ?? null,
  };
}

// The plugs of solutions/care-clinic/solution.yaml, and one external FE plug of the mock (number, string, advanced).
const PLUGS = [
  {
    id: "care_onboarding_fe",
    label: "CARE Onboarding",
    description:
      "Sets up a new clinic. The plug creates the geography, facility, departments, staff, number formats, questionnaires, and report templates.",
    enabled: true,
    requires_internet: true,
    frontend: {
      slug: "care_onboarding_fe",
      repo: "https://github.com/ohcnetwork/care_onboarding_fe",
      ref: "main",
      sha: "0785ff3b4dc6f0c9fa3abb7ba4f01cc854cff8b6",
      source: "built",
      size: 2097152,
      meta: { config: { redirect_after_login: true } },
    },
    settings: [
      {
        part: "frontend",
        key: "config.redirect_after_login",
        label: "Open the clinic setup after sign-in",
        help: "After sign-in, an administrator goes to the setup. This continues until the clinic has a facility.",
        type: "boolean",
        default: true,
      },
    ],
  },
  {
    id: "care_multihost_files_be",
    label: "Files on every address",
    description: "Lets staff open and upload files when they reach CARE through more than one address.",
    enabled: false,
    auto: "when_multihost",
    backend: {
      name: "care_multihost_files_be",
      package: "git+https://github.com/ohcnetwork/care_multihost_files_be.git",
      ref: "main",
      sha: "3edf06b127830e4fbf14823ac69568460eaed291",
      configs: { ALLOWED_HOSTS: ["*.local", "*.ts.net"] },
    },
    settings: [
      {
        part: "backend",
        key: "ALLOWED_HOSTS",
        label: "Addresses that can open files",
        help: "Enter one address for each line. A * at the start matches any name. For example, *.local matches care.local.",
        type: "list",
      },
    ],
  },
  {
    id: "care_fasterappointments_fe",
    label: "Faster appointments",
    description:
      "Speeds up the front desk. After an appointment is booked, the plug issues the token and the invoice. The plug remembers the last practitioner, location, and service.",
    enabled: false,
    requires_internet: true,
    frontend: {
      slug: "care_fasterappointments_fe",
      repo: "https://github.com/ohcnetwork/care_fasterappointments_fe",
      ref: "main",
      sha: "4cef80be6c1372876609851f7ad15bbdd7928e5f",
      source: "built",
      size: 1258291,
      meta: {
        config: {
          auto_create_invoice: true,
          collect_payment_on_appointment_page: true,
          mark_balanced_on_full_payment: true,
          payment_methods: ["cash", "ddpo"],
          print_invoice_on_appointment_page: true,
          redirect_to_invoice: false,
        },
      },
    },
    settings: [
      {
        part: "frontend",
        key: "config.auto_create_invoice",
        label: "Create the invoice when an appointment is booked",
        type: "boolean",
      },
      {
        part: "frontend",
        key: "config.collect_payment_on_appointment_page",
        label: "Collect payment on the appointment page",
        type: "boolean",
      },
      {
        part: "frontend",
        key: "config.print_invoice_on_appointment_page",
        label: "Print the invoice from the appointment page",
        type: "boolean",
      },
      {
        part: "frontend",
        key: "config.redirect_to_invoice",
        label: "Open the invoice instead of the payment sheet on the appointment page",
        type: "boolean",
      },
      {
        part: "frontend",
        key: "config.mark_balanced_on_full_payment",
        label: "Mark an invoice as balanced after full payment",
        type: "boolean",
      },
      {
        part: "frontend",
        key: "config.payment_methods",
        label: "Payment methods to offer",
        type: "choices",
        options: [
          { value: "cash", label: "Cash" },
          { value: "ccca", label: "Credit card" },
          { value: "debc", label: "Debit card" },
          { value: "chck", label: "Cheque" },
          { value: "cchk", label: "Cashier's cheque" },
          { value: "cdac", label: "Credit or debit account" },
          { value: "ddpo", label: "Direct deposit" },
        ],
      },
    ],
  },
  {
    id: "care_filly",
    label: "Filly",
    description: "AI scribe. Medispeak dictation fills CARE forms. The clinic needs a Medispeak account.",
    enabled: false,
    requires_internet: true,
    backend: {
      name: "care_filly",
      package: "git+https://github.com/ohcnetwork/care_filly.git",
      ref: "develop",
      sha: "c171e841d882640977ce5b340133fc374f509e58",
      configs: { MEDISPEAK_API_KEY: "", MEDISPEAK_BASE_URL: "" },
    },
    settings: [
      {
        part: "backend",
        key: "MEDISPEAK_API_KEY",
        label: "Medispeak API key",
        help: "The key of the Medispeak account of the clinic. The key starts with msk_live_.",
        type: "secret",
        required: true,
        placeholder: "msk_live_...",
        pattern: "^msk_live_[A-Za-z0-9]{8,}$",
      },
      {
        part: "backend",
        key: "MEDISPEAK_BASE_URL",
        label: "Medispeak address",
        help: "The address of the Medispeak v2 API that the account uses.",
        type: "url",
        required: true,
        placeholder: "https://example.org/api/v2",
      },
    ],
    setup_steps: ["After the agent applies the change, turn on the Filly screens in the plugin marketplace of CARE."],
  },
  {
    id: "care_token_display_fe",
    label: "Token display",
    description: "Shows the token queue of the front desk on a screen in the waiting room.",
    enabled: false,
    requires_internet: true,
    frontend: {
      slug: "care_token_display_fe",
      repo: null,
      ref: null,
      sha: null,
      source: "external",
      url: "https://token-display.example.org/assets/remoteEntry.js",
      meta: { config: { refresh_seconds: 30, title: "Waiting room", sound: true } },
    },
    settings: [
      {
        part: "frontend",
        key: "config.refresh_seconds",
        label: "Refresh time (seconds)",
        help: "How often the screen reads the queue.",
        type: "number",
        min: 5,
        max: 300,
        default: 30,
      },
      {
        part: "frontend",
        key: "config.title",
        label: "Screen title",
        type: "string",
        pattern: "^[^<>]{1,40}$",
        default: "Waiting room",
      },
      {
        part: "frontend",
        key: "config.sound",
        label: "Play a sound for a new token",
        type: "boolean",
        default: true,
        advanced: true,
      },
      {
        part: "frontend",
        key: "config.queue_url",
        label: "Queue address",
        help: "Empty: the queue of this Care.",
        type: "url",
        placeholder: "https://care.local/api/v1/",
        advanced: true,
      },
    ],
  },
];

export function freshUpdates(options = {}) {
  const scenario = options.updates ?? "idle";
  const tooOld = { ...L1, min_agent_version: "v0.5.0" };
  const withdrawnL0 = {
    ...L0,
    withdrawn: true,
    withdrawn_reason: "The appointment list of care develop of 2026-11-13 is slow.",
  };
  const u = {
    scenario,
    phase4: options.phase4 !== false,
    agentVersion: "v0.4.1",
    remote: [L1, L0],
    cached: [L0],
    installed: { lock: L0, at: "2026-11-15T02:07:41Z", id: "20261115T020741Z" },
    previous: { lock: L_REFS, id: "20261108T120000Z", pitr: null },
    staging: {
      state: "none",
      version: null,
      target_id: null,
      job_id: null,
      staged_at: null,
      download_bytes: null,
      needs_build: false,
      error: null,
    },
    lastCheckAt: "2026-11-21T06:30:12Z",
    lastCheckError: null,
    maintenance: { ...OFF },
    failedState: false,
    history: [],
    windowOpen: options.window_open ?? false,
    failAt: options.update_fail ?? null,
    rebootScheduledAt: null,
    secrets: {},
    runtime: {},
    defs: options.no_plugs ? [] : PLUGS,
    hostnames: 1,
    os: {
      upgradable: [
        { name: "libc6", installed: "2.41-12", candidate: "2.41-12+deb13u1", security: true },
        { name: "openssl", installed: "3.5.1-1", candidate: "3.5.1-1+deb13u1", security: true },
        { name: "raspi-firmware", installed: "1:1.20250430-1", candidate: "1:1.20251020-1", security: false },
      ],
      held: [
        {
          name: "podman",
          installed: "5.4.2+ds1-2",
          candidate: "5.4.2+ds1-2+deb13u1",
          tested: "5.4.2+ds1-2",
          action: "keep",
        },
        { name: "crun", installed: "1.21-1", candidate: "1.21-1", tested: "1.21-1", action: "keep" },
        { name: "netavark", installed: "1.14.1-1", candidate: "1.14.1-1", tested: "1.14.1-1", action: "keep" },
        { name: "aardvark-dns", installed: "1.14.0-3", candidate: "1.14.0-3", tested: "1.14.0-3", action: "keep" },
        { name: "conmon", installed: "2.1.12-4", candidate: "2.1.12-4", tested: "2.1.12-4", action: "keep" },
        {
          name: "skopeo",
          installed: "1.18.0+ds1-1",
          candidate: "1.18.0+ds1-1+deb13u1",
          tested: "1.18.0+ds1-1+deb13u1",
          action: "upgrade",
        },
      ],
      reboot_required: false,
      reboot_required_packages: [],
      checked_at: "2026-11-21T06:40:00Z",
      unattended_last_run_at: "2026-11-21T06:41:00Z",
      last_full_upgrade: null,
    },
  };
  // The first apply of the device installed 2026.11.0 (Phase 4 agent on a Phase 2 device).
  u.history.push({
    job_id: "20261115T020300.000Z-apply",
    kind: "apply",
    source: "cli",
    reason: "care-pi apply --update",
    started_at: "2026-11-15T02:03:00Z",
    finished_at: "2026-11-15T02:07:41Z",
    outcome: "succeeded",
    from_version: null,
    to_version: "2026.11.0",
    from_lock: "20261108T120000Z",
    to_lock: null,
    plug_id: null,
    checkpoint: null,
    pitr_offer: null,
    problem: null,
    downtime_s: 64.2,
  });
  if (scenario === "available" || scenario === "staged") {
    u.cached = [L1, L0];
    u.lastCheckAt = "2026-11-21T09:12:00Z";
  }
  if (scenario === "staged") {
    u.staging = {
      state: "staged",
      version: L1.version,
      target_id: hex16(L1.version),
      job_id: "20261121T091205.000Z-update-stage",
      staged_at: "2026-11-21T09:19:40Z",
      download_bytes: 412316860,
      needs_build: false,
      error: null,
    };
  }
  if (scenario === "updated") {
    const cp = checkpointOf("20261122T020300.000Z-update-apply", "2026-11-22T02:05:12Z");
    u.cached = [L1, L0];
    u.installed = { lock: L1, at: "2026-11-22T02:09:31Z", id: "20261122T020931Z" };
    u.previous = { lock: L0, id: "20261115T020741Z", pitr: offerOf(cp) };
    u.lastCheckAt = "2026-11-22T01:58:00Z";
    u.history.unshift({
      job_id: "20261122T020300.000Z-update-apply",
      kind: "update",
      source: "timer",
      reason: "window Sun 02:00",
      started_at: "2026-11-22T02:03:00Z",
      finished_at: "2026-11-22T02:09:31Z",
      outcome: "succeeded",
      from_version: "2026.11.0",
      to_version: "2026.11.1",
      from_lock: "20261115T020741Z",
      to_lock: null,
      plug_id: null,
      checkpoint: cp,
      pitr_offer: null,
      problem: null,
      downtime_s: 162.5,
    });
  }
  // S69: a power loss during the cutover of 2026.11.1. At the next start the agent marked the update failed
  // ("interrupted") and a recovery job (update-rollback, source timer, reason "recovery <job id>") installed the lock
  // on disk again, with the PITR offer of the interrupted job (docs/solutions.md section 8.6).
  if (scenario === "recovered") {
    const cp = checkpointOf("20261122T020300.000Z-update-apply", "2026-11-22T02:05:12Z");
    const offer = offerOf(cp);
    u.cached = [L1, L0];
    u.lastCheckAt = "2026-11-22T01:58:00Z";
    u.history.unshift({
      job_id: "20261122T020300.000Z-update-apply",
      kind: "update",
      source: "timer",
      reason: "window Sun 02:00",
      started_at: "2026-11-22T02:03:00Z",
      finished_at: "2026-11-22T02:06:40Z",
      outcome: "rolled-back",
      from_version: "2026.11.0",
      to_version: "2026.11.1",
      from_lock: "20261115T020741Z",
      to_lock: null,
      plug_id: null,
      checkpoint: cp,
      pitr_offer: offer,
      problem: {
        type: "https://care-pi.rithviknishad.dev/problems/internal",
        title: "Internal error",
        status: 500,
        detail: "interrupted: no process holds the apply lock",
      },
      downtime_s: null,
    });
    u.history.unshift({
      job_id: "20261122T021500.000Z-update-rollback",
      kind: "rollback",
      source: "timer",
      reason: "recovery 20261122T020300.000Z-update-apply",
      started_at: "2026-11-22T02:15:00Z",
      finished_at: "2026-11-22T02:17:31Z",
      outcome: "rolled-back",
      from_version: "2026.11.1",
      to_version: "2026.11.0",
      from_lock: null,
      to_lock: "20261115T020741Z",
      plug_id: null,
      checkpoint: null,
      pitr_offer: offer,
      problem: null,
      downtime_s: 91.0,
    });
  }
  if (scenario === "agent-too-old") {
    u.remote = [tooOld, L0];
    u.cached = [tooOld, L0];
    u.lastCheckAt = "2026-11-21T09:12:00Z";
  }
  if (scenario === "withdrawn") {
    u.remote = [L1, withdrawnL0];
    u.cached = [L1, withdrawnL0];
    u.installed = { ...u.installed, lock: withdrawnL0 };
  }
  for (const def of u.defs) {
    const on = def.auto ? false : def.enabled;
    u.runtime[def.id] = {
      on,
      settingsPending: false,
      bundle: def.frontend?.source === "built" ? (on ? "present" : "missing") : null,
    };
  }
  return u;
}

function cfg(state) {
  return effectiveConfig(state);
}

// The window in the time zone of the host (Asia/Kolkata, UTC+05:30, no daylight saving time).
function windowState(state) {
  const c = cfg(state).updates;
  const w = c.window;
  const toMin = (t) => Number(t.slice(0, 2)) * 60 + Number(t.slice(3, 5));
  const s = toMin(w.start);
  const e = toMin(w.end);
  const len = e > s ? e - s : e < s ? 1440 - s + e : 1440;
  const nowMs = Date.now();
  const localNow = nowMs + TZ_OFFSET_MIN * 60000;
  const midnight = Math.floor(localNow / 86400000) * 86400000;
  const names = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  let open = false;
  let closesAt = null;
  let next = null;
  for (let d = -1; d <= 8; d++) {
    const day = midnight + d * 86400000;
    if (!w.days.includes(names[new Date(day).getUTCDay()])) continue;
    const openLocal = day + s * 60000;
    const closeLocal = openLocal + len * 60000;
    if (openLocal <= localNow && localNow < closeLocal) {
      open = true;
      closesAt = closeLocal - TZ_OFFSET_MIN * 60000;
    }
    if (openLocal > localNow && next === null) next = openLocal - TZ_OFFSET_MIN * 60000;
  }
  const forced = state.u.windowOpen;
  if (forced === true && !open) {
    open = true;
    closesAt = nowMs + 2 * 3600 * 1000;
  }
  if (forced === false) {
    open = false;
    closesAt = null;
  }
  const iso = (ms) => (ms === null ? null : new Date(ms).toISOString().replace(/\.\d{3}Z$/, "Z"));
  return {
    days: w.days,
    start: w.start,
    end: w.end,
    timezone: TZ,
    open,
    next_open_at: iso(next),
    closes_at: iso(closesAt),
  };
}

function nextNoon() {
  const localNow = Date.now() + TZ_OFFSET_MIN * 60000;
  let noon = Math.floor(localNow / 86400000) * 86400000 + 12 * 3600000;
  if (noon <= localNow) noon += 86400000;
  return new Date(noon - TZ_OFFSET_MIN * 60000).toISOString().replace(/\.\d{3}Z$/, "Z");
}

function availableLock(state, l) {
  return {
    version: l.version,
    released_at: l.released_at,
    changelog_url: `${REL}tag/lock-care-clinic-${l.version}`,
    summary: l.summary,
    min_agent_version: l.min_agent_version,
    installable: !l.withdrawn && !semverLess(state.u.agentVersion, l.min_agent_version),
    withdrawn: l.withdrawn,
    withdrawn_reason: l.withdrawn_reason,
  };
}

function targetLock(state) {
  const u = state.u;
  const c = cfg(state).solution;
  const current = u.installed.lock.version;
  if (c.track === "pinned") {
    const pin = c.pin;
    const lock = [...u.cached, ...u.remote].find((l) => l.version === pin);
    if (!lock || lock.version === current) return { lock: null, missing: !lock && Boolean(pin) };
    return { lock, missing: false };
  }
  const latest = u.cached.find((l) => !l.withdrawn) ?? null;
  if (!latest) return { lock: null, missing: false };
  if (current && !versionLess(current, latest.version)) return { lock: null, missing: false };
  return { lock: latest, missing: false };
}

function blockerOf(state, lock, missing) {
  if (missing) {
    return {
      code: "lock-not-found",
      detail: `care-clinic has no lock ${cfg(state).solution.pin}.`,
      min_agent_version: null,
    };
  }
  if (lock && semverLess(state.u.agentVersion, lock.min_agent_version)) {
    return {
      code: "agent-too-old",
      detail: `care-clinic ${lock.version} needs agent ${lock.min_agent_version} or newer. Run the installer again.`,
      min_agent_version: lock.min_agent_version,
    };
  }
  return null;
}

function runningUpdateJob(state) {
  for (const id of state.jobOrder) {
    const j = state.jobs.get(id).job;
    if (UPDATE_KINDS.includes(j.kind) && (j.state === "running" || j.state === "queued")) return j;
  }
  return null;
}

function staged(state, lock) {
  const s = state.u.staging;
  return Boolean(lock && s.state === "staged" && s.version === lock.version);
}

export function overview(state) {
  const u = state.u;
  const c = cfg(state);
  const { lock, missing } = targetLock(state);
  const blocked = blockerOf(state, lock, missing);
  const latest = u.cached.find((l) => !l.withdrawn) ?? null;
  const cur = u.installed.lock;
  const target = lock
    ? {
        kind: c.solution.track === "custom" ? "custom" : "lock",
        version: lock.version,
        target_id: hex16(lock.version),
        released_at: lock.released_at,
        changelog_url: `${REL}tag/lock-care-clinic-${lock.version}`,
        summary: lock.summary,
        min_agent_version: lock.min_agent_version,
        path: "cutover",
        staged: staged(state, lock),
        needs_build: false,
        needs_internet: !staged(state, lock),
        estimated_downtime_s: 180,
      }
    : null;
  const components = lock
    ? [
        {
          id: "care",
          label: "CARE backend",
          kind: "image",
          ref: "latest",
          tracked: true,
          source: "lock",
          current: cur.care,
          target: lock.care,
          changed: cur.care !== lock.care,
        },
        {
          id: "care_fe",
          label: "CARE frontend",
          kind: "git",
          ref: "develop",
          tracked: true,
          source: "lock",
          current: cur.care_fe,
          target: lock.care_fe,
          changed: cur.care_fe !== lock.care_fe,
        },
        {
          id: "images.postgres",
          label: "PostgreSQL",
          kind: "image",
          ref: "17",
          tracked: true,
          source: "lock",
          current: POSTGRES,
          target: POSTGRES,
          changed: false,
        },
      ]
    : [];
  const running = runningUpdateJob(state);
  const updateAvailable = Boolean(lock && !blocked);
  let machine = "idle";
  if (u.failedState) machine = "failed";
  else if (running?.kind === "update-check") machine = "checking";
  else if (running?.kind === "update-stage") machine = "staging";
  else if (running?.kind === "update-apply") machine = u.maintenance.active ? "applying" : "staging";
  else if (running?.kind === "update-rollback") machine = "rolling-back";
  else if (updateAvailable && target?.staged) machine = "staged";
  const win = windowState(state);
  const prev = u.previous;
  return {
    solution: { name: SOLUTION.name, title: SOLUTION.title },
    track: c.solution.track,
    pin: c.solution.pin ?? null,
    current: {
      version: cur.version,
      source: cur.version ? "lock" : "solution-refs",
      track: c.solution.track,
      lock_sha256: cur.lock_sha256,
      installed_at: u.installed.at,
      agent_version: "v0.4.0",
      withdrawn: cur.withdrawn,
      withdrawn_reason: cur.withdrawn_reason,
    },
    latest: latest ? availableLock(state, latest) : null,
    target,
    update_available: updateAvailable,
    available: u.cached
      .filter((l) => !cur.version || versionLess(cur.version, l.version))
      .map((l) => availableLock(state, l)),
    components,
    state: machine,
    blocked_by: blocked,
    auto: Boolean(c.updates.auto),
    window: win,
    next_check_at: nextNoon(),
    next_apply_at:
      (c.updates.auto && updateAvailable) || c.updates.os_full || u.rebootScheduledAt ? win.next_open_at : null,
    last_check_at: u.lastCheckAt,
    last_check_error: u.lastCheckError,
    index: {
      url: `${DIST}index/index.json`,
      dist_base: DIST,
      fetched_at: u.lastCheckAt,
      generated_at: "2026-11-21T09:00:00Z",
    },
    staging: { ...u.staging },
    maintenance: { ...u.maintenance },
    last_result: u.history[0] ?? null,
    rollback: {
      available: Boolean(prev),
      to_lock: prev?.id ?? null,
      to_version: prev?.lock.version ?? null,
      reason: prev ? null : "/var/lib/care-pi/state/locks has no lock before the running one.",
      images_present: true,
      pitr_offer: prev?.pitr ?? null,
    },
    os: {
      security_auto: c.updates.os_security !== false,
      reboot_required: u.os.reboot_required,
      reboot_scheduled_at: u.rebootScheduledAt,
      upgradable_count: u.os.upgradable.length + u.os.held.filter((h) => h.action === "upgrade").length,
      held_upgradable_count: u.os.held.filter((h) => h.action === "upgrade").length,
      last_full_upgrade_at: u.os.last_full_upgrade?.finished_at ?? null,
    },
    job: running ? summary(running) : null,
  };
}

// Status.updates (UpdatesSummary).
export function updatesSummary(state) {
  if (!state.u?.phase4 || state.setup.state !== "ready") return null;
  const o = overview(state);
  const last = o.last_result;
  return {
    track: o.track,
    current_version: o.current?.version ?? null,
    latest_version: o.latest?.version ?? null,
    update_available: o.update_available,
    target_version: o.target?.version ?? null,
    staged_version: o.staging.state === "staged" ? o.staging.version : null,
    state: o.state,
    blocked_by: o.blocked_by?.code ?? null,
    next_apply_at: o.next_apply_at,
    last_check_at: o.last_check_at,
    last_result: last
      ? {
          job_id: last.job_id,
          kind: last.kind,
          outcome: last.outcome,
          finished_at: last.finished_at,
          to_version: last.to_version,
        }
      : null,
    rollback_available: o.rollback.available,
    os_reboot_required: o.os.reboot_required,
  };
}

export function maintenanceOf(state) {
  return { ...(state.u?.maintenance ?? OFF) };
}

// The care image and the care_fe sha of the running lock, for GET /status.
export function runningIdentity(state) {
  const l = state.u?.installed.lock ?? L0;
  return { care: `ghcr.io/ohcnetwork/care@${l.care}`, care_fe: l.care_fe };
}

export function agentVersion(state) {
  return state.u?.agentVersion ?? "v0.4.1";
}

// The maintenance page of a cutover: on at the step cutover-maintenance-on, the action of each step while it runs,
// off after maintenance-off. A failed rollback leaves it on.
function maintenanceHook(kind) {
  return (state, step, phase, job) => {
    const u = state.u;
    if (phase === "start") {
      if (step.action === "cutover-maintenance-on") {
        u.maintenance = { active: true, kind, job_id: job.id, step: step.action, started_at: now() };
      } else if (u.maintenance.active && u.maintenance.job_id === job.id) {
        u.maintenance.step = step.action;
      }
    }
    if (phase === "done" && step.action === "maintenance-off") u.maintenance = { ...OFF };
    // S62: the plug path restart shows the page during the step restart, and turns it off after it (also on a failure).
    if (step.action === "restart" && !(u.maintenance.active && u.maintenance.job_id !== job.id)) {
      if (phase === "start") u.maintenance = { active: true, kind, job_id: job.id, step: "restart", started_at: now() };
      else u.maintenance = { ...OFF };
    }
  };
}

function stageSteps(version) {
  const id = hex16(version);
  return [
    ["fetch-lock", `fetch-lock: care-clinic ${version}`],
    ["plan", `plan: target ${id}`],
    ["stage-pull", "stage-pull: 2 image(s)", { progress: "items" }],
    ["stage-bundles", `stage-bundles: fe/${id}, plugs/care_fasterappointments_fe/4cef80be6c13`],
    ["render", `render: state/staging/${id}`],
  ];
}

function cutoverSteps() {
  return [
    ["cutover-maintenance-on", "cutover-maintenance-on"],
    ["stop", `stop: ${UNITS.join(" ")}`],
    ["checkpoint", "checkpoint: incremental backup, restore point, archive push"],
    ["activate", "activate: install the target tree"],
    ["migrate", "migrate: start care-beat.service", { slow: 8 }],
    ["start", "start: care-worker.service care-api.service", { slow: 2 }],
    ["plug-config", "plug-config: 1 row(s)"],
    ["verify", "verify: /ping/, fe/current, plug_config"],
    ["write-lock", "write-lock"],
    ["maintenance-off", "maintenance-off"],
  ];
}

function assertPhase4(state) {
  if (!state.u.phase4)
    throw new Problem("not-implemented-in-this-version", "Updates and plugs come in Phase 4.", { phase: 4 });
}

function assertReady(state) {
  if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish the setup first.");
}

function assertFree(state) {
  const running = runningHostJob(state);
  if (running)
    throw new Problem("job-running", "Another job holds the apply lock. Wait until it ends.", { job_id: running.id });
}

function accepted(res, job) {
  sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
}

function startCheck(state) {
  const u = state.u;
  for (const id of state.jobOrder) {
    const j = state.jobs.get(id).job;
    if (j.kind === "update-check" && j.state === "running") return j;
  }
  return startJob(state, {
    kind: "update-check",
    holdsLock: false,
    steps: [
      ["fetch-index", `fetch-index: ${DIST}index/index.json`],
      ["fetch-lock", `fetch-lock: care-clinic ${u.remote[0].version}`],
      ...(cfg(state).solution.track === "custom"
        ? [["resolve-refs", "resolve-refs: care latest, care_fe develop"]]
        : []),
    ],
    onDone: (st, ok) => {
      if (!ok) return;
      st.u.cached = [...st.u.remote];
      st.u.lastCheckAt = now();
      st.u.lastCheckError = null;
    },
    result: (st) => {
      const o = overview(st);
      return {
        kind: "update-check",
        checked_at: now(),
        track: o.track,
        current_version: o.current?.version ?? null,
        latest_version: o.latest?.version ?? null,
        target_version: o.target?.version ?? null,
        target_id: o.target?.target_id ?? null,
        update_available: o.update_available,
        blocked_by: o.blocked_by,
        changed_components: o.components.filter((c) => c.changed).map((c) => c.id),
        stage_job_id: null,
      };
    },
  });
}

function requireTarget(state, version) {
  const o = overview(state);
  let lock = targetLock(state).lock;
  if (version) {
    lock = [...state.u.cached, ...state.u.remote].find((l) => l.version === version) ?? null;
    if (!lock)
      throw new Problem("lock-not-found", `care-clinic has no lock ${version} in the index, the cache, or the agent.`);
    if (o.track === "stable" && o.latest && version !== o.latest.version) {
      throw new Problem(
        "invalid-request",
        "On the stable track only the newest lock can be installed. Pin the track.",
        {
          errors: [{ pointer: "/version", message: "not the newest lock", code: "stable-newest" }],
        },
      );
    }
  }
  if (!lock || (lock.version === state.u.installed.lock.version && !version)) {
    throw new Problem(
      "no-update-available",
      `The device runs care-clinic ${state.u.installed.lock.version}, the newest lock.`,
    );
  }
  if (semverLess(state.u.agentVersion, lock.min_agent_version)) {
    throw new Problem(
      "agent-too-old",
      `care-clinic ${lock.version} needs agent ${lock.min_agent_version} or newer. This agent is ${state.u.agentVersion}. Run the installer again.`,
      { min_agent_version: lock.min_agent_version },
    );
  }
  return lock;
}

function startStage(state, lock) {
  const u = state.u;
  u.staging = { ...u.staging, state: "staging", version: lock.version, target_id: hex16(lock.version), error: null };
  const job = startJob(state, {
    kind: "update-stage",
    cancellable: true,
    steps: stageSteps(lock.version),
    onDone: (st, ok, j) => {
      st.u.staging = ok
        ? {
            state: "staged",
            version: lock.version,
            target_id: hex16(lock.version),
            job_id: j.id,
            staged_at: now(),
            download_bytes: 412316860,
            needs_build: false,
            error: null,
          }
        : { ...st.u.staging, state: j.state === "cancelled" ? "none" : "failed", error: j.error };
    },
    result: (_st, j) => ({
      kind: "update-stage",
      version: lock.version,
      target_id: hex16(lock.version),
      pulled_images: [`ghcr.io/rithviknishad/care-pi/care@${lock.care}`],
      downloaded: [`fe/${hex16(lock.version)}`, "plugs/care_fasterappointments_fe/4cef80be6c13"],
      built: [],
      download_bytes: 412316860,
      duration_s: Math.round((Date.parse(now()) - Date.parse(j.started_at)) * 10) / 10000,
    }),
  });
  u.staging.job_id = job.id;
  return job;
}

function windowClosed(state) {
  const w = windowState(state);
  const at = w.next_open_at;
  const local = new Date(Date.parse(at) + TZ_OFFSET_MIN * 60000);
  const day = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][local.getUTCDay()];
  return new Problem(
    "update-window-closed",
    `The window opens on ${day} at ${w.start} (${TZ}). Send now=true to update now.`,
    { next_window_at: at },
  );
}

const FAILURE_SLUG = {
  checkpoint: "checkpoint-failed",
  migrate: "migration-failed",
  verify: "verify-failed",
  start: "verify-failed",
  rollback: "rollback-failed",
};

function startApply(state, lock, body) {
  const u = state.u;
  const from = u.installed;
  const fail = u.failAt;
  u.failAt = null;
  let checkpoint = null;
  const job = startJob(state, {
    kind: "update-apply",
    reason: body.reason ?? null,
    steps: [
      ...(staged(state, lock) ? [] : stageSteps(lock.version)),
      ...cutoverSteps(),
      ["prune-images", "prune-images: 2 image(s)"],
    ],
    failAt: fail === "rollback" ? "migrate" : fail,
    // After a failure: the rollback to the previous lock, then the page goes off. A checkpoint failure needs no
    // rollback of the code (the units start again). A failed rollback leaves the page.
    recovery: (action) =>
      fail === "rollback"
        ? [["rollback", `rollback: previous lock ${from.id}`]]
        : action === "checkpoint"
          ? [
              ["start", `start: ${UNITS.join(" ")}`],
              ["maintenance-off", "maintenance-off"],
            ]
          : [
              ["rollback", `rollback: previous lock ${from.id}`],
              ["maintenance-off", "maintenance-off"],
            ],
    onStep: (st, step, phase, j) => {
      maintenanceHook("update-apply")(st, step, phase, j);
      if (step.action === "checkpoint" && phase === "done") checkpoint = checkpointOf(j.id, step.started_at);
      // The failed rollback of update_fail=rollback: the step rollback fails too.
      if (fail === "rollback" && step.action === "rollback" && phase === "done") {
        step.state = "failed";
        step.detail = "care-api.service did not become healthy in 5m0s after the rollback.";
      }
    },
    failure: (st, j, action) => {
      const slug = fail === "rollback" ? "rollback-failed" : (FAILURE_SLUG[action] ?? "internal");
      const offer = checkpoint && action !== "checkpoint" ? offerOf(checkpoint) : null;
      return problemOf(
        slug,
        slug === "checkpoint-failed"
          ? "The checkpoint before the migrations failed. Nothing changed. Care runs the previous lock."
          : slug === "rollback-failed"
            ? "care-beat.service did not become healthy. The rollback to the previous lock failed too. The maintenance page stays."
            : `${action} failed. The previous lock runs again.`,
        `/api/v1/jobs/${j.id}`,
        offer ? { pitr_offer: offer } : {},
      );
    },
    onDone: (st, ok, j) => {
      const downtime = st.u.maintenance.active ? null : 162.5;
      if (ok) {
        const id = lockId();
        st.u.previous = { lock: from.lock, id: from.id, pitr: checkpoint ? offerOf(checkpoint) : null };
        st.u.installed = { lock, at: now(), id };
        st.u.staging = {
          state: "none",
          version: null,
          target_id: null,
          job_id: null,
          staged_at: null,
          download_bytes: null,
          needs_build: false,
          error: null,
        };
        st.u.history.unshift(
          historyEntry(j, {
            kind: "update",
            outcome: "succeeded",
            from_version: from.lock.version,
            to_version: lock.version,
            from_lock: from.id,
            checkpoint,
            downtime_s: downtime,
          }),
        );
        return;
      }
      const rollbackFailed = j.error?.type?.endsWith("/rollback-failed");
      if (rollbackFailed) st.u.failedState = true;
      st.u.history.unshift(
        historyEntry(j, {
          kind: "update",
          outcome: rollbackFailed
            ? "rollback-failed"
            : j.error?.type?.endsWith("/checkpoint-failed")
              ? "failed"
              : "rolled-back",
          from_version: from.lock.version,
          to_version: from.lock.version,
          checkpoint,
          pitr_offer: j.error?.pitr_offer ?? null,
          problem: j.error,
          downtime_s: 120.4,
        }),
      );
    },
    result: (_st, j) => ({
      kind: "update-apply",
      from_version: from.lock.version,
      to_version: lock.version,
      target_id: hex16(lock.version),
      from_lock: from.id,
      checkpoint: checkpoint ?? checkpointOf(j.id, j.started_at),
      downtime_s: 162.5,
      pruned_images: 2,
      warnings: [],
    }),
  });
  return job;
}

function startRollback(state) {
  const u = state.u;
  const from = u.installed;
  const to = u.previous;
  let checkpoint = null;
  return startJob(state, {
    kind: "update-rollback",
    steps: [["fetch-lock", `fetch-lock: state/locks/${to.id}.yaml`], ...cutoverSteps()],
    onStep: (st, step, phase, j) => {
      maintenanceHook("update-rollback")(st, step, phase, j);
      if (step.action === "checkpoint" && phase === "done") checkpoint = checkpointOf(j.id, step.started_at);
    },
    onDone: (st, ok, j) => {
      if (!ok) return;
      st.u.installed = { lock: to.lock, at: now(), id: to.id };
      st.u.previous = { lock: from.lock, id: lockId(), pitr: null };
      st.u.failedState = false;
      st.u.history.unshift(
        historyEntry(j, {
          kind: "rollback",
          outcome: "succeeded",
          from_version: from.lock.version,
          to_version: to.lock.version,
          from_lock: from.id,
          to_lock: to.id,
          checkpoint,
          pitr_offer: to.pitr ?? null,
          downtime_s: 140,
        }),
      );
    },
    result: () => ({
      kind: "update-rollback",
      from_version: from.lock.version,
      to_version: to.lock.version,
      to_lock: to.id,
      checkpoint,
      pitr_offer: to.pitr ?? null,
      downtime_s: 140,
    }),
  });
}

// ---- Plugs ----

function getPath(obj, dotted) {
  return dotted.split(".").reduce((o, k) => (o && typeof o === "object" ? o[k] : undefined), obj);
}

function choiceOf(state, id) {
  return state.file.solution?.plugs?.[id] ?? {};
}

function enabledOf(state, def) {
  const choice = choiceOf(state, def.id).enabled;
  if (choice === true || choice === false) return { enabled: choice, source: "operator" };
  if (def.auto) return { enabled: state.u.hostnames > 1, source: "auto" };
  return { enabled: def.enabled, source: "solution" };
}

function operatorValue(state, def, s) {
  const v = choiceOf(state, def.id).settings?.[s.part]?.[s.key];
  return v === undefined ? undefined : v;
}

function defaultValue(def, s) {
  if (s.default !== undefined) return s.default;
  const v = s.part === "frontend" ? getPath(def.frontend?.meta, s.key) : def.backend?.configs?.[s.key];
  return v === undefined ? null : v;
}

function hasValue(v) {
  return !(v === null || v === undefined || v === "" || (Array.isArray(v) && v.length === 0));
}

function settingView(state, def, s) {
  const secret = s.type === "secret";
  const op = operatorValue(state, def, s);
  const secretSet = Boolean(state.u.secrets[def.id]?.[s.key]);
  const value = secret ? null : op !== undefined ? op : defaultValue(def, s);
  return {
    part: s.part,
    key: s.key,
    label: s.label,
    help: s.help ?? null,
    type: s.type,
    required: Boolean(s.required),
    placeholder: s.placeholder ?? null,
    options: s.options ?? null,
    default: s.default ?? null,
    min: s.min ?? null,
    max: s.max ?? null,
    pattern: s.pattern ?? null,
    advanced: Boolean(s.advanced),
    value,
    secret_set: secret ? secretSet : null,
    source: (secret ? secretSet : op !== undefined) ? "operator" : "default",
  };
}

function plugView(state, def) {
  const r = state.u.runtime[def.id];
  const { enabled, source } = enabledOf(state, def);
  const settings = def.settings.map((s) => settingView(state, def, s));
  const complete = settings.every((s) => !s.required || (s.type === "secret" ? s.secret_set : hasValue(s.value)));
  const fe = def.frontend;
  const sha12 = fe?.sha?.slice(0, 12);
  const metaUrl = fe
    ? fe.source === "external"
      ? fe.url
      : `https://care.local/plugs/${fe.slug}/${sha12}/assets/remoteEntry.js`
    : null;
  return {
    id: def.id,
    label: def.label,
    description: def.description ?? "",
    kind: def.backend && fe ? "both" : def.backend ? "backend" : "frontend",
    enabled,
    on: r.on,
    pending: enabled !== r.on || r.settingsPending,
    source,
    auto: def.auto ?? null,
    auto_active: Boolean(def.auto) && state.u.hostnames > 1,
    requires_internet: Boolean(def.requires_internet),
    custom: false,
    backend: def.backend
      ? {
          name: def.backend.name,
          package: def.backend.package,
          ref: def.backend.ref,
          tracked: true,
          sha: def.backend.sha,
          target_sha: null,
          installed: true,
          image: "solution-image",
        }
      : null,
    frontend: fe
      ? {
          slug: fe.slug,
          repo: fe.repo,
          ref: fe.ref,
          tracked: Boolean(fe.ref),
          sha: fe.sha,
          source: fe.source,
          meta_url: metaUrl,
          bundle:
            fe.source === "external"
              ? { state: "external", path: null, sha256: null, size_bytes: null }
              : r.bundle === "present"
                ? {
                    state: "present",
                    path: `/var/lib/care-pi/plugs/${fe.slug}/${sha12}`,
                    sha256: createHash("sha256").update(fe.slug).digest("hex"),
                    size_bytes: fe.size,
                  }
                : { state: "missing", path: null, sha256: null, size_bytes: null },
          plug_config: r.on
            ? { state: "synced", meta_url: metaUrl, checked_at: now() }
            : { state: "missing", meta_url: null, checked_at: now() },
        }
      : null,
    settings,
    settings_complete: complete,
    setup_steps: def.setup_steps ?? [],
  };
}

function plugList(state) {
  const items = state.u.defs.map((d) => plugView(state, d));
  return {
    solution: SOLUTION.name,
    version: state.u.installed.lock.version,
    track: cfg(state).solution.track,
    hostname_count: state.u.hostnames,
    pending: items.some((p) => p.pending),
    items,
  };
}

function escapePointer(key) {
  return key.replace(/~/g, "~0").replace(/\//g, "~1");
}

function matches(pattern, value) {
  return !pattern || new RegExp(pattern, "u").test(value);
}

// Validates the settings of the body against the settings form of the plug (docs/api/README.md section 14.6).
function validateSettings(def, settings) {
  const errors = [];
  for (const part of ["backend", "frontend"]) {
    for (const [key, value] of Object.entries(settings?.[part] ?? {})) {
      const pointer = `/settings/${part}/${escapePointer(key)}`;
      const s = def.settings.find((x) => x.part === part && x.key === key);
      if (!s) {
        errors.push({ pointer, message: "is not a setting of the plug", code: "additionalProperties" });
        continue;
      }
      if (value === null) continue;
      const fail = (message, code) => errors.push({ pointer, message, code });
      switch (s.type) {
        case "boolean":
          if (typeof value !== "boolean") fail("must be a boolean", "type");
          break;
        case "number":
          if (typeof value !== "number") fail("must be a number", "type");
          else if (s.min !== undefined && value < s.min) fail(`must be ${s.min} or more`, "minimum");
          else if (s.max !== undefined && value > s.max) fail(`must be ${s.max} or less`, "maximum");
          break;
        case "url":
          if (typeof value !== "string") fail("must be a string", "type");
          else if (value !== "" && !/^https?:\/\/\S+$/.test(value)) fail("must be an http or https URL", "format");
          break;
        case "list":
          if (!Array.isArray(value) || value.some((v) => typeof v !== "string" || v === ""))
            fail("must be a list of strings", "type");
          else if (value.some((v) => !matches(s.pattern, v))) fail(`each line must match ${s.pattern}`, "pattern");
          break;
        case "choices":
          if (!Array.isArray(value) || value.some((v) => !s.options.some((o) => o.value === v)))
            fail("must be values of the options", "enum");
          break;
        default:
          if (typeof value !== "string") fail("must be a string", "type");
          else if (value !== "" && !matches(s.pattern, value)) fail(`must match ${s.pattern}`, "pattern");
      }
    }
  }
  return errors;
}

function requiredErrors(state, def) {
  const errors = [];
  for (const s of def.settings) {
    if (!s.required) continue;
    const view = settingView(state, def, s);
    const ok = s.type === "secret" ? view.secret_set : hasValue(view.value);
    if (!ok) {
      errors.push({
        pointer: `/settings/${s.part}/${escapePointer(s.key)}`,
        message: "a value is required when the plug is on",
        code: "required",
      });
    }
  }
  return errors;
}

// Writes the change into state.file.solution.plugs and the secrets. Returns the settings that changed.
function writeChange(state, def, body) {
  const file = state.file;
  file.solution = { ...(file.solution ?? {}), plugs: { ...(file.solution?.plugs ?? {}) } };
  const choice = { ...(file.solution.plugs[def.id] ?? {}) };
  if ("enabled" in body) {
    if (body.enabled === null) delete choice.enabled;
    else choice.enabled = body.enabled;
  }
  let changed = false;
  const settings = {
    backend: { ...(choice.settings?.backend ?? {}) },
    frontend: { ...(choice.settings?.frontend ?? {}) },
  };
  for (const part of ["backend", "frontend"]) {
    for (const [key, value] of Object.entries(body.settings?.[part] ?? {})) {
      const s = def.settings.find((x) => x.part === part && x.key === key);
      changed = true;
      if (s.type === "secret") {
        state.u.secrets[def.id] = { ...(state.u.secrets[def.id] ?? {}) };
        if (value === null) delete state.u.secrets[def.id][key];
        else state.u.secrets[def.id][key] = value;
      } else if (value === null) delete settings[part][key];
      else settings[part][key] = value;
    }
  }
  for (const part of ["backend", "frontend"]) if (Object.keys(settings[part]).length === 0) delete settings[part];
  if (Object.keys(settings).length > 0) choice.settings = settings;
  else delete choice.settings;
  if (Object.keys(choice).length > 0) file.solution.plugs[def.id] = choice;
  else delete file.solution.plugs[def.id];
  if (Object.keys(file.solution.plugs).length === 0) delete file.solution.plugs;
  return changed;
}

function planOf(state, def, settingsChanged) {
  const r = state.u.runtime[def.id];
  const { enabled } = enabledOf(state, def);
  const onChange = enabled !== r.on;
  const frontendOnly = !def.backend || (!onChange && Object.keys(settingsChanged ?? {}).every((p) => p === "frontend"));
  let path = "none";
  if (onChange || settingsChanged || r.settingsPending) {
    if (frontendOnly && (!onChange || !def.backend)) path = "plug-config";
    else if (onChange && enabled) path = "cutover";
    else path = "restart";
  }
  const fe = def.frontend;
  const sha12 = fe?.sha?.slice(0, 12);
  const downloads =
    path === "plug-config" && enabled && fe?.source === "built" && r.bundle !== "present"
      ? [`plug-${fe.slug}-${sha12}/plug-${fe.slug}-${sha12}.tar.gz`]
      : [];
  const steps = {
    none: [],
    "plug-config": [
      ...(downloads.length ? ["stage-bundles"] : []),
      "plug-config",
      "verify",
      ...(onChange ? ["write-lock"] : []),
    ],
    restart: ["render", "restart", "verify", ...(onChange ? ["write-lock"] : [])],
    cutover: [
      "render",
      "cutover-maintenance-on",
      "stop",
      "checkpoint",
      "activate",
      "migrate",
      "start",
      "plug-config",
      "verify",
      "write-lock",
      "maintenance-off",
    ],
  }[path];
  return {
    changed: path !== "none",
    path,
    lock_changed: onChange,
    maintenance: path === "cutover" || path === "restart",
    checkpoint: path === "cutover",
    restarts: path === "restart" || path === "cutover" ? UNITS : [],
    downloads,
    builds: [],
    needs_internet: downloads.length > 0,
    estimated_downtime_s: { none: 0, "plug-config": 0, restart: 20, cutover: 120 }[path],
    steps,
    warnings: enabled && def.requires_internet ? [`${def.label} needs internet access at run time.`] : [],
  };
}

const STEP_NAMES = {
  "stage-bundles": (def) => `stage-bundles: plugs/${def.frontend?.slug}/${def.frontend?.sha?.slice(0, 12)}`,
  "plug-config": (def) => `plug-config: ${def.frontend?.slug ?? def.id}`,
  verify: () => "verify: /api/v1/plug_config/",
  "write-lock": () => "write-lock",
  render: () => "render: additional-plugs.json",
  restart: () => `restart: ${UNITS.join(" ")}`,
};

function startPlugJob(state, def, plan, reason) {
  const enabled = enabledOf(state, def).enabled;
  let checkpoint = null;
  const cutover = new Set(cutoverSteps().map(([a]) => a));
  const steps = plan.steps.map((a) => {
    if (STEP_NAMES[a] && !(plan.path === "cutover" && cutover.has(a) && a !== "plug-config"))
      return [a, STEP_NAMES[a](def)];
    return cutoverSteps().find(([x]) => x === a) ?? [a, a];
  });
  return startJob(state, {
    kind: "plug-change",
    reason,
    steps,
    onStep: (st, step, phase, j) => {
      maintenanceHook("plug-change")(st, step, phase, j);
      if (step.action === "checkpoint" && phase === "done") checkpoint = checkpointOf(j.id, step.started_at);
    },
    onDone: (st, ok, j) => {
      if (!ok) return;
      const r = st.u.runtime[def.id];
      const before = r.on;
      r.on = enabled;
      r.settingsPending = false;
      if (def.frontend?.source === "built" && enabled) r.bundle = "present";
      st.u.history.unshift(
        historyEntry(j, {
          kind: "plug-change",
          outcome: "succeeded",
          from_version: st.u.installed.lock.version,
          to_version: st.u.installed.lock.version,
          from_lock: before !== enabled ? st.u.installed.id : null,
          plug_id: def.id,
          checkpoint,
          downtime_s: plan.path === "plug-config" ? null : plan.path === "restart" ? 18.2 : 96.4,
        }),
      );
    },
    result: () => ({
      kind: "plug-change",
      plug_id: def.id,
      enabled,
      path: plan.path,
      lock_changed: plan.lock_changed,
      checkpoint,
      downtime_s: plan.path === "plug-config" ? null : plan.path === "restart" ? 18.2 : 96.4,
    }),
  });
}

function settingsSchema(def) {
  const part = (name) => {
    const props = {};
    const required = [];
    for (const s of def.settings.filter((x) => x.part === name)) {
      const base = {
        title: s.label,
        ...(s.help ? { description: s.help } : {}),
        ...(s.default !== undefined ? { default: s.default } : {}),
      };
      const pattern = s.pattern ? { pattern: s.pattern } : {};
      props[s.key] = {
        boolean: { ...base, type: "boolean" },
        string: { ...base, type: "string", ...pattern },
        number: {
          ...base,
          type: "number",
          ...(s.min !== undefined ? { minimum: s.min } : {}),
          ...(s.max !== undefined ? { maximum: s.max } : {}),
        },
        url: { ...base, type: "string", format: "uri", pattern: "^https?://" },
        secret: { ...base, type: ["string", "null"], writeOnly: true, ...pattern },
        list: { ...base, type: "array", items: { type: "string", minLength: 1, ...pattern } },
        choices: {
          ...base,
          type: "array",
          uniqueItems: true,
          items: { oneOf: (s.options ?? []).map((o) => ({ const: o.value, title: o.label })) },
        },
      }[s.type];
      if (s.required) required.push(s.key);
    }
    return { type: "object", additionalProperties: false, properties: props, ...(required.length ? { required } : {}) };
  };
  return {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    $id: `https://care-pi.rithviknishad.dev/schemas/plug-settings/${def.id}.json`,
    title: `${def.label} settings`,
    type: "object",
    additionalProperties: false,
    properties: { backend: part("backend"), frontend: part("frontend") },
  };
}

function plugDef(state, id) {
  const def = state.u.defs.find((d) => d.id === id);
  if (!def) {
    throw new Problem("plug-unknown", `care-clinic has no plug ${id}. Add a plug with solution.custom.plugs.`);
  }
  return def;
}

function putPlug({ req, res, state, params, query, body }) {
  assertPhase4(state);
  const def = plugDef(state, params.id);
  const ifMatch = req.headers["if-match"];
  const etag = etagOf(state.file);
  if (ifMatch && ifMatch !== etag)
    throw new Problem("config-changed", `The ETag is now ${etag}. Read the config again.`);
  const dryRun = query.get("dry_run") === "true";
  const apply = query.get("apply") === "true";
  if (dryRun && apply) {
    throw new Problem("invalid-request", "apply=true and dry_run=true together are not possible.", {
      errors: [
        { pointer: "", message: "apply and dry_run exclude each other", code: "dry-run-apply", parameter: "apply" },
      ],
    });
  }
  if ("enabled" in body && body.enabled !== null && typeof body.enabled !== "boolean") {
    throw new Problem("invalid-request", "enabled must be true, false, or null.", {
      errors: [{ pointer: "/enabled", message: "must be a boolean or null", code: "type" }],
    });
  }
  const shapeErrors = validateSettings(def, body.settings);
  if (shapeErrors.length > 0) {
    throw new Problem("plug-settings-invalid", `${shapeErrors.length} settings are not valid.`, {
      errors: shapeErrors,
    });
  }
  if (apply) {
    assertReady(state);
    assertFree(state);
  }
  // Write on a copy first: a dry run and a failed validation change nothing.
  const fileBefore = structuredClone(state.file);
  const secretsBefore = structuredClone(state.u.secrets);
  const settingsChanged = writeChange(state, def, body) ? body.settings : null;
  const { enabled } = enabledOf(state, def);
  const missing = enabled ? requiredErrors(state, def) : [];
  const plan = planOf(state, def, settingsChanged);
  const plug = plugView(state, def);
  if (missing.length > 0 || dryRun) {
    state.file = fileBefore;
    state.u.secrets = secretsBefore;
    if (missing.length > 0) {
      throw new Problem("plug-settings-invalid", `${missing.length} settings are not valid.`, { errors: missing });
    }
    sendJson(res, 200, { written: false, etag, plug, plan, job: null }, { ETag: etag });
    return;
  }
  if (!apply && settingsChanged) state.u.runtime[def.id].settingsPending = true;
  const job = apply && plan.changed ? startPlugJob(state, def, plan, body.reason ?? null) : null;
  const newTag = etagOf(state.file);
  sendJson(
    res,
    apply ? 202 : 200,
    { written: true, etag: newTag, plug: plugView(state, def), plan, job },
    { ETag: newTag, ...(job ? { Location: `/api/v1/jobs/${job.id}` } : {}) },
  );
}

// ---- Solutions ----

function solutionDocument(state) {
  return {
    apiVersion: "care-pi/v1",
    kind: "Solution",
    name: SOLUTION.name,
    title: SOLUTION.title,
    description: SOLUTION.description,
    care: { image: "ghcr.io/ohcnetwork/care", ref: "latest" },
    care_fe: { repo: "https://github.com/ohcnetwork/care_fe", ref: "develop" },
    plugs: state.u.defs.map((d) => ({
      id: d.id,
      label: d.label,
      enabled: d.enabled,
      ...(d.auto ? { auto: d.auto } : {}),
      ...(d.backend ? { backend: { name: d.backend.name, package: d.backend.package, ref: d.backend.ref } } : {}),
      ...(d.frontend
        ? {
            frontend: d.frontend.repo
              ? { slug: d.frontend.slug, repo: d.frontend.repo, ref: d.frontend.ref }
              : { slug: d.frontend.slug, url: d.frontend.url },
          }
        : {}),
    })),
  };
}

function knownLocks(state) {
  const seen = new Map();
  for (const l of [...state.u.cached, state.u.installed.lock])
    if (l.version && !seen.has(l.version)) seen.set(l.version, l);
  return [...seen.values()].sort((a, b) => (versionLess(a.version, b.version) ? 1 : -1));
}

// ---- OS ----

function osView(state) {
  const u = state.u;
  const c = cfg(state).updates;
  return {
    distro: { id: "debian", codename: "trixie", pretty_name: "Debian GNU/Linux 13 (trixie)" },
    security_auto: c.os_security !== false,
    reboot_on_window: c.os_reboot !== false,
    full_on_window: Boolean(c.os_full),
    unattended_last_run_at: u.os.unattended_last_run_at,
    reboot_required: u.os.reboot_required,
    reboot_required_packages: u.os.reboot_required_packages,
    reboot_scheduled_at: u.rebootScheduledAt,
    checked_at: u.os.checked_at,
    upgradable: u.os.upgradable,
    held: u.os.held,
    last_full_upgrade: u.os.last_full_upgrade,
  };
}

function startOsUpdate(state, body) {
  const u = state.u;
  const upgraded = u.os.upgradable.map((p) => ({ name: p.name, from: p.installed, to: p.candidate }));
  const heldChanged = u.os.held.filter((h) => h.action === "upgrade").map((h) => h.name);
  const cycle = heldChanged.length > 0;
  return startJob(state, {
    kind: "os-update",
    reason: body.reason ?? null,
    steps: [
      ["os-refresh", "os-refresh: apt-get update"],
      ["os-unhold", `os-unhold: ${heldChanged.join(" ") || "none"}`],
      ["os-upgrade", `os-upgrade: ${upgraded.length + heldChanged.length} package(s)`, { progress: "items" }],
      ["os-hold", "os-hold: podman crun netavark aardvark-dns conmon skopeo"],
      ...(cycle
        ? [
            ["cutover-maintenance-on", "cutover-maintenance-on"],
            ["stop", `stop: ${UNITS.join(" ")}`],
            ["start", `start: ${UNITS.join(" ")}`],
            ["verify", "verify: /ping/"],
            ["maintenance-off", "maintenance-off"],
          ]
        : []),
    ],
    onStep: maintenanceHook("os-update"),
    onDone: (st, ok, j) => {
      if (!ok) return;
      st.u.os.upgradable = [];
      st.u.os.held = st.u.os.held.map((h) =>
        h.action === "upgrade" ? { ...h, installed: h.tested, candidate: h.tested, action: "keep" } : h,
      );
      st.u.os.reboot_required = true;
      st.u.os.reboot_required_packages = ["raspi-firmware"];
      const entry = historyEntry(j, {
        kind: "os-update",
        outcome: "succeeded",
        from_version: st.u.installed.lock.version,
        to_version: st.u.installed.lock.version,
        downtime_s: cycle ? 48.3 : null,
      });
      st.u.os.last_full_upgrade = entry;
      st.u.history.unshift(entry);
    },
    result: () => ({
      kind: "os-update",
      upgraded,
      held_changed: heldChanged,
      care_restarted: cycle,
      reboot_required: true,
      rebooting: false,
      duration_s: 212.7,
    }),
  });
}

// ---- Routes ----

const route = (method, path, handler, extra = {}) => ({
  method,
  path,
  ...extra,
  handler: (ctx) => {
    assertPhase4(ctx.state);
    return handler(ctx);
  },
});

export const updatesRoutes = [
  route("GET", "/api/v1/updates", ({ res, state }) => sendJson(res, 200, overview(state))),
  route("POST", "/api/v1/updates/check", ({ res, state }) => {
    assertReady(state);
    accepted(res, startCheck(state));
  }),
  route("POST", "/api/v1/updates/stage", ({ res, state, body }) => {
    assertReady(state);
    assertFree(state);
    accepted(res, startStage(state, requireTarget(state, body.version ?? null)));
  }),
  route("POST", "/api/v1/updates/apply", ({ res, state, body }) => {
    assertReady(state);
    assertFree(state);
    const lock = requireTarget(state, body.version ?? null);
    if (!body.now && !windowState(state).open) throw windowClosed(state);
    accepted(res, startApply(state, lock, body));
  }),
  route(
    "POST",
    "/api/v1/updates/rollback",
    ({ res, state }) => {
      assertReady(state);
      assertFree(state);
      if (!state.u.previous) {
        throw new Problem("rollback-unavailable", "/var/lib/care-pi/state/locks has no lock before the running one.");
      }
      accepted(res, startRollback(state));
    },
    { reauth: true },
  ),
  route("GET", "/api/v1/updates/history", ({ res, state, query }) => {
    const limit = Math.min(100, Math.max(1, Number(query.get("limit") ?? 20) || 20));
    const before = query.get("before");
    let items = state.u.history;
    if (before) {
      const at = items.findIndex((e) => e.job_id === before);
      items = at >= 0 ? items.slice(at + 1) : [];
    }
    const page = items.slice(0, limit);
    sendJson(res, 200, { items: page, next_before: items.length > limit ? (page.at(-1)?.job_id ?? null) : null });
  }),
  route("GET", "/api/v1/updates/os", ({ res, state }) => sendJson(res, 200, osView(state))),
  route(
    "POST",
    "/api/v1/updates/os/apply",
    ({ res, state, body }) => {
      assertReady(state);
      assertFree(state);
      if (!body.now && !windowState(state).open) throw windowClosed(state);
      accepted(res, startOsUpdate(state, body));
    },
    { reauth: true },
  ),
  route(
    "POST",
    "/api/v1/updates/os/reboot",
    ({ res, state, body }) => {
      if (body.now) {
        assertFree(state);
        const at = addSeconds(now(), Number(body.delay_s ?? 5));
        sendJson(res, 202, { at, scheduled: false });
        return;
      }
      state.u.rebootScheduledAt = windowState(state).next_open_at;
      sendJson(res, 202, { at: state.u.rebootScheduledAt, scheduled: true });
    },
    { reauth: true },
  ),
  route("GET", "/api/v1/plugs", ({ res, state }) => sendJson(res, 200, plugList(state))),
  route("PUT", "/api/v1/plugs/:id", putPlug),
  route("GET", "/api/v1/plugs/:id/settings-schema", ({ res, state, params }) =>
    sendSchema(res, settingsSchema(plugDef(state, params.id))),
  ),
  route("GET", "/api/v1/solutions", ({ res, state }) =>
    sendJson(res, 200, {
      items: [
        {
          ...SOLUTION,
          source: "both",
          selected: true,
          latest_version: state.u.cached.find((l) => !l.withdrawn)?.version ?? null,
          lock_count: knownLocks(state).length,
          plug_count: state.u.defs.length,
        },
      ],
      index: overview(state).index,
    }),
  ),
  route("GET", "/api/v1/solutions/:name", ({ res, state, params }) => {
    if (params.name !== SOLUTION.name) throw new Problem("not-found", `No solution has the name ${params.name}.`);
    sendJson(res, 200, {
      ...SOLUTION,
      source: "both",
      selected: true,
      document: solutionDocument(state),
      document_version: state.u.cached[0]?.version ?? null,
      locks: knownLocks(state).map((l) => availableLock(state, l)),
      embedded_locks: ["2026.11.0"],
    });
  }),
  route("GET", "/api/v1/solutions/:name/locks/:version", ({ res, state, params }) => {
    const lock = knownLocks(state).find((l) => l.version === params.version);
    if (params.name !== SOLUTION.name || !lock) {
      throw new Problem(
        "lock-not-found",
        `${params.name} has no lock ${params.version} in the index, the cache, or the agent.`,
      );
    }
    sendJson(res, 200, {
      apiVersion: "care-pi/v1",
      kind: "SolutionLock",
      name: SOLUTION.name,
      version: lock.version,
      created_at: lock.released_at,
      created_by: "mock",
      notes: lock.summary,
      min_agent_version: lock.min_agent_version,
      solution: solutionDocument(state),
      care: { image: "ghcr.io/ohcnetwork/care", ref: "latest", digest: lock.care, git_sha: null },
      care_fe: { repo: "https://github.com/ohcnetwork/care_fe", ref: "develop", sha: lock.care_fe },
    });
  }),
];
