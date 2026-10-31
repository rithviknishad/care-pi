// The state of the mock agent and the fixtures that it derives from the state.

import { createHash, randomBytes } from "node:crypto";

export const CLAIM_CODE = "4F7K-Q2MX-9PDA";
export const OS_PASSWORD = "imager-password";
export const READY_PASSWORD = "correct horse battery staple";
export const TOTP_CODE = "000000";
export const FINGERPRINT = "3f1d6c0e5a2b9e4d7c8f1a0b2c3d4e5f60718293a4b5c6d7e8f9011223344556";
export const FINGERPRINT_COLON = FINGERPRINT.toUpperCase().match(/../g).join(":");
export const STEP_IDS = [
  "account",
  "recovery-codes",
  "hardware",
  "identity",
  "tls",
  "size",
  "env",
  "backups",
  "care-admin",
];
const GIB = 1024 ** 3;

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

export function randomCode(groups, size) {
  const bytes = randomBytes(groups * size);
  const chars = [...bytes].map((b) => CROCKFORD[b % 32]);
  const out = [];
  for (let g = 0; g < groups; g++) out.push(chars.slice(g * size, g * size + size).join(""));
  return out.join("-");
}

export function normalizeCode(text) {
  return String(text).toUpperCase().replace(/[\s-]/g, "").replace(/[IL]/g, "1").replace(/O/g, "0");
}

export function etagOf(doc) {
  return `"${createHash("sha256").update(JSON.stringify(doc)).digest("hex").slice(0, 16)}"`;
}

export function now() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

export const PRESETS = {
  solo: { gunicorn_workers: 1, celery_concurrency: 1, pg_shared_buffers_mb: 128, api: 320, worker: 384, db: 384 },
  "small-clinic": {
    gunicorn_workers: 2,
    celery_concurrency: 1,
    pg_shared_buffers_mb: 128,
    api: 512,
    worker: 384,
    db: 384,
  },
  "busy-counter": {
    gunicorn_workers: 3,
    celery_concurrency: 2,
    pg_shared_buffers_mb: 256,
    api: 768,
    worker: 512,
    db: 512,
  },
};

export function presetMemory(name) {
  const p = PRESETS[name] ?? PRESETS["small-clinic"];
  return {
    "care-api": { max_mb: p.api, swap_max_mb: p.api === 320 ? 64 : 128 },
    "care-worker": { max_mb: p.worker, swap_max_mb: 128 },
    "care-beat": { max_mb: 512, swap_max_mb: 128 },
    "care-db": { max_mb: p.db, swap_max_mb: 0 },
    "care-redis": { max_mb: 96, swap_max_mb: 0 },
    "care-s3": { max_mb: 256, swap_max_mb: 0 },
    "care-caddy": { max_mb: 96, swap_max_mb: 0 },
  };
}

function freshSteps() {
  return Object.fromEntries(
    STEP_IDS.map((id) => [id, { status: "pending", updated_at: null, errors: [], warnings: [] }]),
  );
}

// scenario: unclaimed | setup | review | ready
export function freshState(options = {}) {
  const scenario = options.scenario ?? "unclaimed";
  const state = {
    scenario,
    stepMs: Number(options.step_ms ?? process.env.MOCK_STEP_MS ?? 1000),
    failStep: options.fail_step ?? null,
    ramBytes: Math.round(Number(options.ram_gib ?? 2) * GIB) - 25 * 1024 * 1024,
    diskKind: options.disk ?? "usb",
    devBuild: options.dev_build ?? true,
    claimed: false,
    claimCode: CLAIM_CODE,
    claimMethods: ["os-password", "claim-code"],
    claimRecord: null,
    password: null,
    passwordSetAt: null,
    recoveryCodes: [],
    recoveryGeneratedAt: null,
    totp: { enabled: false, enrolledAt: null, pending: null },
    failures: new Map(),
    sessions: new Map(),
    setup: { state: "unclaimed", steps: freshSteps(), jobId: null, claimedAt: null, readyAt: null },
    careAdmin: { username: null, email: null, password: null },
    file: {
      version: 1,
      identity: { display_name: "CARE", hostname: "care" },
      size: { preset: "small-clinic" },
      tls: { local_ca: { enabled: true }, public: { enabled: false, clinic_id: null }, tailscale: { enabled: false } },
      env: { backend: {}, frontend: {} },
      fe: { provider: "local", artifact_url: null, artifact_sha256: null },
    },
    jobs: new Map(),
    jobOrder: [],
    feBuilds: [],
    appliedHostname: "care",
    startedAt: now(),
  };
  if (scenario !== "unclaimed") {
    state.claimed = true;
    state.claimMethods = [];
    state.password = READY_PASSWORD;
    state.passwordSetAt = "2026-10-30T10:00:00Z";
    state.recoveryCodes = Array.from({ length: 8 }, () => randomCode(2, 5));
    state.recoveryGeneratedAt = "2026-10-30T10:00:00Z";
    state.claimRecord = {
      claimed_at: "2026-10-30T10:00:00Z",
      method: "claim-code",
      address: "192.168.1.23",
      user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/150.0 Safari/537.36",
    };
    state.setup.claimedAt = "2026-10-30T10:00:00Z";
    state.setup.steps.account = { status: "done", updated_at: "2026-10-30T10:00:00Z", errors: [], warnings: [] };
  }
  if (scenario === "setup") {
    state.setup.state = "setup";
    for (const id of ["recovery-codes", "hardware"]) {
      state.setup.steps[id] = { status: "done", updated_at: "2026-10-30T10:02:00Z", errors: [], warnings: [] };
    }
  }
  // review: each step is done (env and backups skipped), and the agent has the Care password in memory.
  // The operator can start the setup on /setup/review.
  if (scenario === "review") {
    state.setup.state = "setup";
    for (const id of STEP_IDS) {
      state.setup.steps[id] = {
        status: id === "env" || id === "backups" ? "skipped" : "done",
        updated_at: "2026-10-30T10:10:00Z",
        errors: [],
        warnings: [],
      };
    }
    state.file.identity = { display_name: "Sunrise Clinic", hostname: "care" };
    state.careAdmin = { username: "admin", email: "admin@sunrise.example", password: "another long care password" };
  }
  if (scenario === "ready") {
    state.setup.state = "ready";
    state.setup.readyAt = "2026-10-30T10:20:00Z";
    for (const id of STEP_IDS) {
      state.setup.steps[id] = {
        status: id === "env" || id === "backups" ? "skipped" : "done",
        updated_at: "2026-10-30T10:10:00Z",
        errors: [],
        warnings: [],
      };
    }
    state.file.identity = { display_name: "Sunrise Clinic", hostname: "care" };
    state.careAdmin = { username: "admin", email: "admin@sunrise.example", password: null };
    state.feBuilds = [{ id: "8d2f0c1a9b7e", bytes: 13_421_772, modified_at: "2026-10-30T10:19:00Z", current: true }];
  }
  return state;
}

export function effectiveConfig(state) {
  const f = state.file;
  return {
    version: 1,
    identity: { display_name: "CARE", hostname: "care", ...f.identity },
    solution: {
      name: "care-clinic",
      track: "stable",
      pin: null,
      custom: { care_ref: null, care_fe_ref: null, plugs: [] },
    },
    size: { preset: "small-clinic", ...f.size },
    tls: f.tls,
    clock: { policy: "ntp" },
    backups: {
      wal_archive_timeout_min: 5,
      schedule: { db_full: "Sun 01:00", db_incr: "daily 02:00", files: "daily 02:30", verify: "Sat 03:00" },
      retention: { db_full: 2, archive_days: 14, files_keep_daily: 14, files_keep_weekly: 8 },
      destinations: [],
    },
    updates: { auto: false, window: { days: ["Sun"], start: "02:00", end: "05:00" }, os_security: true },
    env: { backend: { ...(f.env?.backend ?? {}) }, frontend: { ...(f.env?.frontend ?? {}) } },
    fe: { provider: "local", artifact_url: null, artifact_sha256: null, ...f.fe },
    images: {},
    uploads: { max_body_mb: 100 },
    network: { wifi: null },
    telemetry: { enabled: false, remote_write_url: null },
  };
}

export function hostnameOf(state) {
  return state.file.identity?.hostname ?? "care";
}

export const ADDRESS = "192.168.1.10";

export function network(state) {
  const label = state.appliedHostname;
  const collision = label === "care-collide";
  const advertised = collision ? `${label}-2.local` : `${label}.local`;
  return {
    hostname: label,
    os_hostname: label,
    mdns: { configured: `${label}.local`, advertised, collision },
    addresses: [ADDRESS],
    published: [ADDRESS],
    address_changed: false,
    interfaces: [
      {
        name: "eth0",
        kind: "ethernet",
        mac: "d8:3a:dd:00:11:22",
        link: true,
        speed_mbps: 1000,
        addresses: [
          { address: ADDRESS, prefix_length: 24, family: "ipv4", dynamic: true },
          { address: "fd00::1a2b:3c4d", prefix_length: 64, family: "ipv6", dynamic: true },
        ],
      },
    ],
    gateway: "192.168.1.1",
    dns_servers: ["192.168.1.1"],
    internet: { reachable: true, checked_at: now() },
    dhcp_hint: { interface: "eth0", mac: "d8:3a:dd:00:11:22", address: ADDRESS },
    urls: [`https://${advertised}/`, `https://${ADDRESS}/`],
    wifi: null,
  };
}

// The directories of the host check of the agent (agent/internal/hostcheck).
const DIRS = [
  "/etc/care-pi",
  "/etc/care-pi/secrets",
  "/etc/care-pi/rendered",
  "/etc/care-pi/rendered/caddy",
  "/etc/care-pi/rendered/care",
  "/etc/care-pi/rendered/db",
  "/etc/care-pi/rendered/fe",
  "/etc/containers/systemd",
  "/etc/systemd/system",
  "/var/lib/care-pi",
  "/var/lib/care-pi/pg",
  "/var/lib/care-pi/s3",
  "/var/lib/care-pi/redis",
  "/var/lib/care-pi/caddy",
  "/var/lib/care-pi/backup",
  "/var/lib/care-pi/backup/pg",
  "/var/lib/care-pi/fe",
  "/var/lib/care-pi/plugs",
  "/var/lib/care-pi/build",
  "/var/lib/care-pi/build/care_fe",
  "/var/lib/care-pi/state",
  "/var/lib/care-pi/state/jobs",
  "/var/lib/care-pi/state/locks",
  "/var/lib/care-pi/state/mode",
];

// The byte format of the agent and the console: binary units, 1 decimal, no ".0" (agent/internal/units).
function formatBytes(n) {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  if (n < 1024) return `${n} B`;
  let v = n;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${(Math.round(v * 10) / 10).toFixed(1).replace(/\.0$/, "")} ${units[u]}`;
}

export function hostCheck(state, probe) {
  const ram = state.ramBytes;
  const sd = state.diskKind === "sd";
  const diskProbe =
    probe || state.diskProbe
      ? { write_mb_s: sd ? 14.2 : 312.4, read_mb_s: sd ? 41.0 : 401.9, measured_at: now() }
      : null;
  if (diskProbe) state.diskProbe = diskProbe;
  const disk = sd
    ? { device: "/dev/mmcblk0", kind: "sd", size_bytes: 63_864_569_856 }
    : { device: "/dev/sda", kind: "usb", size_bytes: 256_060_514_304 };
  // The checks of the agent on lumine (43, 24 of them directories; wave 4d), in the same order.
  const checks = [
    { name: "podman", level: "required", ok: true, detail: "podman 5.4.2" },
    { name: "quadlet", level: "required", ok: true, detail: "/usr/libexec/podman/quadlet" },
    { name: "skopeo", level: "required", ok: true, detail: "/usr/bin/skopeo" },
    { name: "systemctl", level: "required", ok: true, detail: "/usr/bin/systemctl" },
    { name: "timedatectl", level: "required", ok: true, detail: "/usr/bin/timedatectl" },
    { name: "restic", level: "advised", ok: true, detail: "/usr/bin/restic" },
    { name: "nft", level: "advised", ok: true, detail: "/usr/sbin/nft" },
    { name: "avahi-daemon", level: "advised", ok: true, detail: "/usr/sbin/avahi-daemon" },
    { name: "cgroup memory controller", level: "required", ok: true, detail: "memory controller active" },
    {
      name: "zram swap",
      level: "required",
      ok: true,
      detail: `zram swap ${Math.round(ram / 2 ** 20)} MiB, RAM ${Math.round(ram / 2 ** 20)} MiB`,
    },
    { name: "avahi allow-interfaces", level: "advised", ok: true, detail: "allow-interfaces=eth0,wlan0" },
    ...DIRS.map((d) => ({ name: `dir ${d}`, level: "advised", ok: true, detail: "present" })),
    {
      name: "ram",
      level: "advised",
      ok: ram >= 1800 * 2 ** 20,
      detail: `RAM ${formatBytes(ram)}`,
      ...(ram < 1800 * 2 ** 20 ? { fix: "use a host with 2 GB RAM or more" } : {}),
    },
    {
      name: "root disk",
      level: "advised",
      ok: !sd,
      detail: sd ? "the root disk is an SD card" : `${disk.device} (${disk.kind}, ${formatBytes(disk.size_bytes)})`,
      ...(sd ? { fix: "use a USB SSD or an NVMe drive of 64 GB or more" } : {}),
    },
    {
      name: "disk speed",
      level: "advised",
      ok: diskProbe ? diskProbe.write_mb_s >= 20 : true,
      detail: diskProbe
        ? `write ${diskProbe.write_mb_s} MB/s, read ${diskProbe.read_mb_s} MB/s`
        : "not measured (use the probe)",
      ...(diskProbe && diskProbe.write_mb_s < 20 ? { fix: "use a faster disk" } : {}),
    },
    { name: "page size", level: "advised", ok: true, detail: "page size 16384" },
    { name: "rtc", level: "advised", ok: true, detail: "/dev/rtc0 exists" },
    { name: "ethernet", level: "advised", ok: true, detail: `eth0 ${ADDRESS}` },
    { name: "internet", level: "advised", ok: true, detail: "the internet is reachable" },
    { name: "clock", level: "advised", ok: true, detail: "the clock is synchronized" },
  ];
  return {
    ok: checks.every((c) => c.level !== "required" || c.ok),
    checks,
    hardware: {
      model: "Raspberry Pi 5 Model B Rev 1.0",
      arch: "arm64",
      ram_bytes: ram,
      page_size_bytes: 16384,
      cpu_count: 4,
      root_disk: disk,
      rtc_present: true,
      ethernet: { interface: "eth0", link: true, speed_mbps: 1000, address: ADDRESS },
      internet_reachable: true,
      clock_synchronized: true,
      disk_probe: diskProbe,
    },
  };
}
