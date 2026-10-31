// The backup state of the mock (docs/api/README.md section 13): the recovery key, the local repository,
// the destinations, the disks, and the backup of an "old device" for the restore of a new device.

import { createHash, randomBytes, randomInt } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
// The console has the BIP-39 list (src/lib/bip39-english.ts). The mock reads the same file.
export const WORDS = readFileSync(path.join(ROOT, "src/lib/bip39-english.ts"), "utf8")
  .split("\n")
  .filter((l) => l.trim().startsWith('"'))
  .flatMap((l) => JSON.parse(l.trim().replace(/ \+$/, "")).trim().split(" "));
if (WORDS.length !== 2048) throw new Error(`the BIP-39 list has ${WORDS.length} words, not 2048`);
const INDEX = new Map(WORDS.map((w, i) => [w, i]));
const BY_PREFIX = new Map(WORDS.map((w) => [w.slice(0, 4), w]));

export const GIB = 1024 ** 3;
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

export function iso(ms) {
  return new Date(ms).toISOString().replace(/\.\d{3}Z$/, "Z");
}

function sha256(data) {
  return createHash("sha256").update(data).digest();
}

// 32 bytes of entropy -> 24 words with the 8-bit checksum (BIP-39).
export function wordsOf(entropy) {
  const bits =
    [...entropy].map((b) => b.toString(2).padStart(8, "0")).join("") + sha256(entropy)[0].toString(2).padStart(8, "0");
  return Array.from({ length: 24 }, (_, i) => WORDS[parseInt(bits.slice(i * 11, i * 11 + 11), 2)]);
}

// The words -> the entropy, or null (an unknown word or a wrong checksum). A word can be its first 4 letters.
export function entropyOf(words) {
  if (!Array.isArray(words) || words.length !== 24) return null;
  let bits = "";
  for (const w of words) {
    const full = INDEX.has(w) ? w : BY_PREFIX.get(String(w).slice(0, 4));
    if (!full) return null;
    bits += INDEX.get(full).toString(2).padStart(11, "0");
  }
  const entropy = Buffer.from(Array.from({ length: 32 }, (_, i) => parseInt(bits.slice(i * 8, i * 8 + 8), 2)));
  return sha256(entropy)[0] === parseInt(bits.slice(256), 2) ? entropy : null;
}

export function keyIdOf(entropy) {
  return sha256(Buffer.concat([entropy, Buffer.from("key-id")]))
    .toString("hex")
    .slice(0, 8);
}

export function makeKey(entropy = randomBytes(32), at = Date.now()) {
  const positions = new Set();
  while (positions.size < 3) positions.add(randomInt(1, 25));
  return {
    entropy,
    words: wordsOf(entropy),
    key_id: keyIdOf(entropy),
    created_at: iso(at),
    confirmed_at: null,
    positions: [...positions].sort((a, b) => a - b),
  };
}

// The key of the "old device" whose backup the restore of a new device finds (GET /__mock/state: dr_words).
export const DR_KEY = makeKey(sha256(Buffer.from("care-pi mock old device")), Date.parse("2026-10-01T09:00:00Z"));
DR_KEY.confirmed_at = "2026-10-01T09:05:00Z";

export function sshPublicKey(key, which = "active", hostname = "care") {
  const b64 = Buffer.concat([Buffer.from("\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20"), sha256(key.entropy)])
    .toString("base64")
    .replace(/=+$/, "");
  const pub = `ssh-ed25519 ${b64} care-pi@${hostname}-${key.key_id}`;
  return {
    key: which,
    key_id: key.key_id,
    type: "ssh-ed25519",
    public_key: pub,
    authorized_keys_line: `restrict ${pub}`,
    fingerprint_sha256: `SHA256:${sha256(pub).toString("base64").slice(0, 43)}`,
  };
}

// The host key that an SFTP server presents. A host "changed-*" presents another key after the first test.
export function hostKeyOf(host, generation = 0) {
  const type = host.startsWith("nas") ? "ecdsa-sha2-nistp256" : "ssh-ed25519";
  const blob = sha256(`${host}:${generation}`).toString("base64").replace(/=+$/, "");
  const publicKey = `${type} AAAA${blob}`;
  return {
    type,
    fingerprint_sha256: `SHA256:${sha256(publicKey).toString("base64").slice(0, 43)}`,
    public_key: publicKey,
  };
}

export function label(ms, type, full) {
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, "0");
  const stamp = `${d.getUTCFullYear()}${p(d.getUTCMonth() + 1)}${p(d.getUTCDate())}-${p(d.getUTCHours())}${p(d.getUTCMinutes())}${p(d.getUTCSeconds())}`;
  return type === "full" ? `${stamp}F` : `${full}_${stamp}I`;
}

function snapId() {
  return randomBytes(4).toString("hex");
}

const SYSTEM_DISK = {
  id: "usb-Samsung_Portable_SSD_T7_S5SXNS0R700123-0:0",
  device: "/dev/sda",
  model: "Samsung Portable SSD T7",
  serial: "S5SXNS0R700123",
  size_bytes: 256_060_514_304,
  transport: "usb",
  removable: false,
  usage: "system",
  mounted: true,
  partitions: [
    { device: "/dev/sda1", size_bytes: 536_870_912, fs_type: "vfat", label: "bootfs", mount: "/boot/firmware" },
    { device: "/dev/sda2", size_bytes: 255_523_643_392, fs_type: "ext4", label: "rootfs", mount: "/" },
  ],
  care_label: null,
  destination_id: null,
};

function usbDisk(careLabel, destinationId = null) {
  return {
    id: "usb-SanDisk_Extreme_55AE_323130364E-0:0",
    device: "/dev/sdb",
    model: "SanDisk Extreme 55AE",
    serial: "323130364E",
    size_bytes: 500_107_862_016,
    transport: "usb",
    removable: false,
    usage: careLabel ? "care-backup" : "other-data",
    mounted: Boolean(destinationId),
    partitions: [
      careLabel
        ? {
            device: "/dev/sdb1",
            size_bytes: 500_105_764_864,
            fs_type: "ext4",
            label: careLabel,
            mount: destinationId ? `/var/lib/care-pi/disks/${careLabel}` : null,
          }
        : { device: "/dev/sdb1", size_bytes: 500_105_764_864, fs_type: "exfat", label: "SANDISK", mount: null },
    ],
    care_label: careLabel,
    destination_id: destinationId,
  };
}

// The runtime state of one destination (the state files of the agent).
export function freshRuntime(state = "pending", at = Date.now()) {
  const ok = state === "ok";
  const lastOk = state === "error" ? at - 26 * HOUR : state === "degraded" ? at - 50 * MIN : at - 5 * MIN;
  const has = state !== "pending";
  return {
    state,
    last_db_snapshot_at: has ? iso(lastOk) : null,
    last_files_snapshot_at: has ? iso(at - 8 * HOUR) : null,
    last_attempt_at: has ? iso(at - 5 * MIN) : null,
    last_error: ok || !has ? null : "dial tcp: lookup s3.eu-central-003.backblazeb2.com: no such host",
    last_error_code: ok || !has ? null : "destination-unreachable",
    failing_since: ok || !has ? null : iso(state === "error" ? at - 26 * HOUR : at - 35 * MIN),
    size_bytes: has ? 61_234_567 : null,
    snapshot_count: has ? 412 : null,
    last_verify: has ? { job_id: null, finished_at: iso(at - 2 * DAY), ok: true, detail: null } : null,
    recent_runs: has
      ? [
          {
            started_at: iso(at - 5 * MIN),
            finished_at: iso(at - 5 * MIN + 4000),
            kind: "db",
            source: "timer",
            ok,
            snapshot_id: ok ? snapId() : null,
            added_bytes: ok ? 1_048_576 : null,
            error_code: ok ? null : "destination-unreachable",
            error: ok ? null : "dial tcp: no such host",
            job_id: null,
          },
        ]
      : [],
    running: false,
  };
}

// The local repository with 2 full backups and a daily incremental backup.
function localHistory(at) {
  const backups = [];
  const fullA = at - 11 * DAY - 3 * HOUR;
  const fullB = at - 4 * DAY - 3 * HOUR;
  const la = label(fullA, "full");
  const lb = label(fullB, "full");
  backups.push({ ms: fullA, label: la, type: "full", size: 48_234_496, repo: 4_700_000 });
  for (let d = 1; d < 7; d++)
    backups.push({
      ms: fullA + d * DAY,
      label: label(fullA + d * DAY, "incr", la),
      type: "incr",
      size: 48_300_000,
      repo: 26_000,
    });
  backups.push({ ms: fullB, label: lb, type: "full", size: 48_400_000, repo: 4_710_000 });
  for (let d = 1; d <= 4; d++) {
    const ms = fullB + d * DAY;
    if (ms < at) backups.push({ ms, label: label(ms, "incr", lb), type: "incr", size: 48_500_000, repo: 26_000 });
  }
  return backups.reverse().map((b) => ({
    label: b.label,
    type: b.type,
    started_at: iso(b.ms),
    finished_at: iso(b.ms + 5000),
    size_bytes: b.size,
    repo_size_bytes: b.repo,
    reason: null,
  }));
}

// scenario: the option `backups` of /__mock/reset (MOCK_BACKUPS): ok, degraded, unconfirmed, no-destination, legacy.
// In the setup scenarios the device has no key and no destination.
export function freshBackups(setupScenario, scenario = "ok") {
  const at = Date.now();
  const ready = setupScenario === "ready";
  const b = {
    scenario: ready ? scenario : "none",
    key: null,
    pending: null,
    localRepository: "none",
    rotationJobId: null,
    runtime: {},
    secrets: {},
    backups: [],
    walAt: null,
    timeline: 1,
    checkpoints: [],
    verifyLast: null,
    drills: [],
    disks: [SYSTEM_DISK, usbDisk(null)],
    servers: {},
    buckets: new Set(["sunrise-care", "clinic-backups"]),
    tested: new Set(),
    lastDr: null,
    dirty: false,
  };
  if (setupScenario === "restore") b.disks = [SYSTEM_DISK, usbDisk("care-usb1")];
  if (!ready) return { backups: b, destinations: [] };

  b.backups = localHistory(at);
  b.walAt = at - 2 * MIN;
  b.verifyLast = { job_id: null, finished_at: iso(at - 2 * DAY), ok: true, detail: null };
  b.drills = [{ job_id: null, finished_at: iso(at - 9 * DAY), outcome: "passed", duration_s: 21.4, detail: null }];
  if (scenario === "legacy") {
    b.localRepository = "legacy";
    return { backups: b, destinations: [] };
  }
  b.key = makeKey(randomBytes(32), at - 12 * DAY);
  b.localRepository = "derived";
  if (scenario === "unconfirmed") b.key.confirmed_at = null;
  else b.key.confirmed_at = iso(at - 12 * DAY + 2 * MIN);
  if (scenario === "no-destination") return { backups: b, destinations: [] };

  const destinations = [
    { id: "usb", type: "posix", disk_label: "care-usb1" },
    {
      id: "b2",
      type: "s3",
      endpoint: "https://s3.eu-central-003.backblazeb2.com",
      region: "eu-central-003",
      bucket: "sunrise-care",
      prefix: "care-pi",
    },
  ];
  b.disks = [SYSTEM_DISK, usbDisk("care-usb1", "usb")];
  b.secrets.b2 = { access_key_id: "AKIAEXAMPLEB2KEY0001", updated_at: iso(at - 12 * DAY) };
  b.runtime.usb = freshRuntime("ok", at);
  b.runtime.usb.free_bytes = 480_000_000_000;
  b.runtime.b2 = freshRuntime(scenario === "degraded" ? "degraded" : "ok", at);
  if (scenario === "degraded") {
    const nas = { id: "nas", type: "sftp", host: "nas.lan", user: "care", path: "/volume1/care" };
    nas.host_key = hostKeyOf(nas.host).public_key;
    destinations.push(nas);
    b.runtime.nas = freshRuntime("error", at);
    b.runtime.nas.last_error = "ssh: connect to host nas.lan port 22: No route to host";
    b.servers["nas.lan"] = { generation: 0, authorized: true };
  }
  return { backups: b, destinations };
}

export { snapId, HOUR, MIN, DAY };
