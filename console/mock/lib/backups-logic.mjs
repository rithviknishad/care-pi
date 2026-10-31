// The backup logic of the mock (docs/api/README.md section 13): the recovery key, the destination test, and the
// guards of the jobs. backups-api.mjs and restore-api.mjs have the routes.

import { Problem } from "./http.mjs";
import { runningHostJob, summary } from "./jobs.mjs";
import { now } from "./state.mjs";
import { GIB, MIN, entropyOf, hostKeyOf, iso, label, makeKey, snapId, sshPublicKey } from "./backups-state.mjs";
import { backupsConfig, destinationState, health, keyStatus, location, overview } from "./backups-view.mjs";

const ID = /^[a-z0-9][a-z0-9-]{0,30}$/;
const LABEL = /^care-[a-z0-9][a-z0-9-]{0,10}$/;
const RESTORE_KINDS = new Set(["restore-pitr", "restore-files", "restore-dr", "recovery-key-rotate"]);
const BACKUP_KINDS = new Set(["backup-run", "backup-verify", "restore-drill", ...RESTORE_KINDS, "disk-prepare"]);

function runningOf(state, kinds) {
  for (const id of state.jobOrder) {
    const j = state.jobs.get(id).job;
    if ((j.state === "running" || j.state === "queued") && kinds.has(j.kind)) return j;
  }
  return null;
}

export function backupsOverview(state) {
  const running = runningOf(state, BACKUP_KINDS);
  return overview(state, running ? summary(running) : null);
}

function requireReady(state) {
  if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish the setup first.");
  const restore = runningOf(state, RESTORE_KINDS);
  if (restore) {
    throw new Problem("job-running", "A restore runs now. Backups start again when it ends.", { job_id: restore.id });
  }
}

function requireHostLock(state) {
  const running = runningHostJob(state);
  if (running) throw new Problem("job-running", "Another job holds the apply lock.", { job_id: running.id });
}

function invalid(pointer, message, code) {
  return new Problem("invalid-request", message, { errors: [{ pointer, message, code }] });
}

function keyOf(state, which) {
  const b = state.backups;
  if (!b.key) throw new Problem("no-recovery-key", "Make the recovery key first with POST /backups/recovery-key.");
  if (which === "pending") {
    if (!b.pending) throw new Problem("not-found", "No pending recovery key exists.");
    return b.pending;
  }
  return b.key;
}

function recoveryKey(key, which, state) {
  return {
    words: key.words,
    created_at: key.created_at,
    key: which,
    key_id: key.key_id,
    qr_payload: `CARE-PI-RK1:${key.words.map((w) => w.toUpperCase()).join(" ")}`,
    file_name: `care-recovery-${
      state.file.identity.display_name
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-|-$/g, "") || "care"
    }.txt`,
  };
}

function keyText(key, state) {
  const rows = [];
  for (let r = 0; r < 8; r++) {
    rows.push(
      [0, 1, 2]
        .map((c) => {
          const n = c * 8 + r + 1;
          return `${String(n).padStart(2)} ${key.words[n - 1].padEnd(9)}`;
        })
        .join(" ")
        .trimEnd(),
    );
  }
  return [
    "care-pi recovery key",
    `Clinic: ${state.file.identity.display_name}`,
    `Key id: ${key.key_id}`,
    `Made: ${key.created_at.slice(0, 10)}`,
    "",
    ...rows,
    "",
    "Keep this sheet in a safe place, away from the device.",
    "You need it to restore the backups on a new device.",
    "",
  ].join("\n");
}

function confirmKey(state, which, words) {
  const key = keyOf(state, which);
  if (key.confirmed_at) return;
  if (!Array.isArray(words) || words.length !== 3) throw invalid("/words", "send 3 words", "minItems");
  const errors = [];
  words.forEach((w, i) => {
    if (w?.index !== key.positions[i]) {
      throw invalid(`/words/${i}/index`, `use the position ${key.positions[i]}`, "confirm-position");
    }
    const typed = String(w.word ?? "").toLowerCase();
    const want = key.words[w.index - 1];
    if (
      typed.length < 3 ||
      !(typed === want || (typed.length >= 4 && want.startsWith(typed.slice(0, 4)) && want.startsWith(typed)))
    ) {
      errors.push({ pointer: `/words/${i}/word`, message: `not the word at position ${w.index}`, code: "wrong-word" });
    }
  });
  if (errors.length) {
    throw new Problem(
      "recovery-key-mismatch",
      `The word at position ${words[Number(errors[0].pointer.split("/")[2])].index} is not the word of the recovery key.`,
      { errors },
    );
  }
  key.confirmed_at = now();
}

// The destination test (POST /backups/destinations/test). The mock knows these places:
// posix: a care-pi disk label of GET /backups/disks, or a path (a path with "missing" fails; a path below /mnt or
// /media is not on the root disk); sftp: each host ("down" in the name: no answer); s3: each endpoint (access key
// "bad": refused), the buckets sunrise-care and clinic-backups exist.
export function testDestination(state, body, { createBucket = false } = {}) {
  const b = state.backups;
  const d = body.destination ?? {};
  if (!ID.test(d.id ?? "")) throw invalid("/destination/id", "must match ^[a-z0-9][a-z0-9-]{0,30}$", "pattern");
  if (d.type === "sftp" && !b.key) throw new Problem("no-recovery-key", "An SFTP test needs the recovery key.");
  const checks = [];
  const check = (name, ok, detail, extra = {}) => {
    checks.push({ name, level: extra.level ?? "required", ok, detail, ...(extra.fix ? { fix: extra.fix } : {}) });
    return ok;
  };
  const result = (next, extra = {}) => ({
    ok: next === "none",
    checks,
    host_key_fingerprint: extra.host_key?.fingerprint_sha256 ?? null,
    public_key: extra.public_key ?? null,
    host_key: extra.host_key ?? null,
    next_action: next,
    repository: next === "none" ? { exists: Boolean(extra.exists), other_key_ids: extra.others ?? [] } : null,
    free_bytes: extra.free ?? null,
    location: b.key ? location(d, b.key.key_id) : null,
    tested_at: now(),
  });
  const writeChecks = () => {
    check("write", true, "wrote and read 1 MiB");
    check("read", true, "read 1 MiB back");
    check("delete", true, "deleted the test object");
  };
  if (d.type === "posix") {
    if (d.disk_label) {
      const disk = b.disks.find((x) => x.care_label === d.disk_label);
      if (
        !check(
          "mount",
          Boolean(disk),
          disk
            ? `LABEL=${d.disk_label} at /var/lib/care-pi/disks/${d.disk_label}`
            : `no disk has the label ${d.disk_label}`,
          { fix: "connect the disk" },
        )
      )
        return result("fix");
      writeChecks();
      check("free-space", true, "480 GB free");
      check("root-disk", true, "the path is not on the root disk", { level: "advised" });
      check("repository", true, "no care-pi repository of this key");
      return result("none", {
        free: 480_000_000_000,
        others: disk.partitions[0].label === "care-usb1" && !disk.destination_id ? ["7e3f9a10"] : [],
      });
    }
    const p = String(d.path ?? "");
    if (
      !check(
        "write",
        !p.includes("missing"),
        p.includes("missing") ? `${p}: no such directory` : `wrote 1 MiB in ${p}`,
        { fix: "make the directory, or use another path" },
      )
    )
      return result("fix");
    check("read", true, "read 1 MiB back");
    check("delete", true, "deleted the test file");
    check("free-space", true, "180 GB free");
    const offRoot = /^\/(mnt|media)\//.test(p);
    check(
      "root-disk",
      offRoot,
      offRoot ? "the path is not on the root disk" : "the path is on the root disk /dev/sda2",
      {
        level: "advised",
        fix: "use a folder on another disk",
      },
    );
    check("repository", true, "no care-pi repository of this key");
    return result("none", { free: 180_000_000_000 });
  }
  if (d.type === "sftp") {
    const host = String(d.host ?? "");
    if (
      !check(
        "connect",
        !host.includes("down"),
        host.includes("down") ? `${host}:${d.port ?? 22}: no route to host` : `${host}:${d.port ?? 22} answers`,
        { fix: "check the host name and the port" },
      )
    )
      return result("fix");
    const server = (b.servers[host] ??= { generation: 0, authorized: false, tests: 0 });
    // A host "changed-*" presents another key after the first test (a changed key).
    if (host.startsWith("changed") && server.tests++ > 0) server.generation = 1;
    const presented = { ...hostKeyOf(host, server.generation), trusted: false };
    const pinned = d.host_key ?? null;
    const accept = body.accept_fingerprint ?? null;
    if (!pinned && !accept) {
      check("host-key", false, "first contact: compare the fingerprint with the server", {
        fix: "accept the fingerprint",
      });
      return result("accept-host-key", { host_key: presented });
    }
    if (accept && accept !== presented.fingerprint_sha256) {
      throw new Problem(
        "destination-untrusted-hostkey",
        `${host} presents a host key that is not pinned. Compare the fingerprint with the server.`,
        { host_key: presented, ...(d.id ? { destination_id: d.id } : {}) },
      );
    }
    if (pinned && !accept && pinned !== presented.public_key) {
      check("host-key", false, "the server presents another host key now", {
        fix: "accept the new key only when you know why it changed",
      });
      return result("accept-host-key", { host_key: presented });
    }
    const trusted = { ...presented, trusted: true };
    check("host-key", true, `${presented.type} ${presented.fingerprint_sha256}`);
    if (!server.authorized) {
      check("authenticate", false, `${d.user}@${host}: Permission denied (publickey)`, {
        fix: "add the public key to ~/.ssh/authorized_keys",
      });
      // The operator adds the key before the next test.
      server.authorized = true;
      return result("install-ssh-key", {
        host_key: trusted,
        public_key: sshPublicKey(b.key, "active", state.file.identity.hostname).public_key,
      });
    }
    check("authenticate", true, `${d.user}@${host} accepted the key of the device`);
    writeChecks();
    check("free-space", true, "1.2 TB free", { level: "advised" });
    check("repository", true, "no care-pi repository of this key");
    return result("none", {
      host_key: trusted,
      free: 1_200_000_000_000,
      others: host === "nas.lan" ? ["7e3f9a10"] : [],
    });
  }
  if (d.type === "s3") {
    const creds = body.credentials ?? b.secrets[d.id] ?? null;
    if (!check("connect", true, `${d.endpoint} answers`)) return result("fix");
    if (!creds) {
      check("authenticate", false, "no access keys", { fix: "enter the access keys" });
      return result("fix");
    }
    if (
      !check(
        "authenticate",
        creds.access_key_id !== "bad",
        creds.access_key_id === "bad" ? "403 InvalidAccessKeyId" : "the keys are valid",
        { fix: "check the access keys" },
      )
    )
      return result("fix");
    if (!b.buckets.has(d.bucket)) {
      if (!createBucket) {
        check("bucket", false, `the bucket ${d.bucket} does not exist`, { fix: "create the bucket" });
        return result("create-bucket");
      }
      b.buckets.add(d.bucket);
      check("bucket", true, `created the bucket ${d.bucket}`);
    } else check("bucket", true, `the bucket ${d.bucket} exists`);
    writeChecks();
    check("object-lock", false, "object lock is off", { level: "advised" });
    check("repository", true, "no care-pi repository of this key");
    return result("none", { others: d.bucket === "sunrise-care" && !b.runtime[d.id] ? ["7e3f9a10"] : [] });
  }
  throw invalid("/destination/type", "must be posix, sftp, or s3", "enum");
}

export { requireReady, requireHostLock, invalid, runningOf, RESTORE_KINDS, keyOf, confirmKey, recoveryKey, keyText };
export { GIB, MIN, entropyOf, iso, label, makeKey, snapId, health, keyStatus, destinationState, backupsConfig, LABEL };
