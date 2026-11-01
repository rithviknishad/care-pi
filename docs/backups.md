# Backups and restore

This document tells how care-pi makes backups and how it restores them (Phase 3, decision S40).
The API contract is `docs/api/README.md` section 13 and `docs/api/openapi.yaml` (tags `backups` and `restore`).
The config keys are in `docs/config.md`, section `backups`. The plan is `plans/02-backups-and-restore.md`.

## 1. The model

```text
care-db ── archive-push (WAL) ──► /var/lib/care-pi/backup/pg      local pgBackRest repository (encrypted)
                                   │  full (Sun 01:00), incr (daily 02:00), WAL each archive_timeout
                                   ▼
                   care-backup-db@<id>.timer (each interval_min) ──► restic repository of each destination
/var/lib/care-pi/s3 (files) ───── care-backup-files@<id>.timer (daily) ─┘   <location>/care-pi-<key id>
/etc/care-pi (config, secrets) ── in each snapshot (not recovery.key)
the local CA of Caddy ─────────── in each snapshot (/var/lib/care-pi/caddy/caddy/pki/authorities/local)
```

- pgBackRest has exactly one repository: `/var/lib/care-pi/backup/pg`. `archive_command` writes only there.
  A destination that does not answer never stops the WAL archive.
- Each destination is one restic repository. Each destination has its own timers, its own lock, and its own state.
  A failure of one destination has no effect on the other destinations.
- A `db` snapshot holds the whole local repository and `/etc/care-pi`. A `files` snapshot holds
  `/var/lib/care-pi/s3` (with the xattrs of versitygw) and `/etc/care-pi`. Each snapshot also holds the local CA of
  Caddy (root and intermediate, with their keys), so a restored device keeps the root CA that the clinic devices trust.
- The recovery key (24 words) opens everything. The agent keeps it on the device, so the timers and a
  restore on the same device never ask for it. A new device needs the key and one destination.

## 2. The recovery key

The agent makes 32 random bytes. It shows them as 24 words of the BIP-39 English list (256 bits and an 8-bit checksum).
The agent accepts the first 4 letters of each word. It also accepts the QR payload (`CARE-PI-RK1:` and the words in
uppercase letters) and the `.txt` file.

| File | Content | Mode |
|---|---|---|
| `/etc/care-pi/secrets/recovery.key` | the active key, 64 hex digits | root 0600 |
| `/etc/care-pi/secrets/recovery.key.pending` | the new key of a rotation | root 0600 |
| `/var/lib/care-pi/state/backups/key.json` | the state of each key: key id, dates, `unconfirmed` or `confirmed`, the 3 confirm positions. No secret. | root 0600 |

The key is never in `config.yaml`, in `lock.yaml`, in a log line, in a job journal, in a job event, in a snapshot, or in the
support bundle. Only `GET /backups/recovery-key` (recent authentication), its `.txt` download, and
`care-pi backup recovery-key show` (root) give the words.

### 2.1 Key derivation (v1)

The agent derives each other key with HKDF-SHA256 (RFC 5869). The input key is the 32 bytes. The salt is the ASCII
text `care-pi recovery key v1`.

| Output | HKDF `info` | Length | Form |
|---|---|---|---|
| key id | `key-id` | 4 bytes | 8 lowercase hex digits |
| pgBackRest cipher pass (`repo1-cipher-pass`) | `pgbackrest-repo-cipher-pass` | 32 bytes | base64url without padding (43 characters) |
| restic password | `restic-password` | 32 bytes | base64url without padding (43 characters) |
| SSH key of the agent | `ssh-ed25519-seed` | 32 bytes | the seed of an ed25519 key (RFC 8032) |

The SSH public key has the comment `care-pi@<hostname>-<key id>`.
A change of the derivation needs a new salt (`v2`). The agent must keep `v1` to restore an old backup.

### 2.2 Test vectors

A new implementation must give these values. `agent/internal/backup/key_test.go` checks them. A Python HKDF
(`hmac` and `hashlib`) made them independently, and `ssh-keygen -lf` gave the same fingerprint.

**Key `000102…1f`** (the bytes 0 to 31):

| Output | Value |
|---|---|
| words | `abandon amount liar amount expire adjust cage candy arch gather drum bullet absurd math era live bid rhythm alien crouch range attend journey unaware` |
| key id | `b7e88184` |
| cipher pass | `8ehnRmqnBwlAHh3wHaHbhT0WDfRXdQl9wL2Ml-WxTx8` |
| restic password | `TIH9b_g8wSQxca3ZUnSt9URb00TrkhURmbusKFYIRnA` |
| SSH seed | `a0e91ddc24e8bb314d27dd70924937f5c5c5451d58fc7eca25183648e8d19238` |
| SSH public key | `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKyYwDUS5/0JpEGJNwaAVmFIddIgQ30zNhNrwWN8t/9T care-pi@care-b7e88184` |
| SSH fingerprint | `SHA256:3uFNMWjcMszc+nqlp7ZyL4uK897yA66hAl+r4Qa300k` |

**Key of 32 zero bytes:**

| Output | Value |
|---|---|
| words | `abandon` 23 times, then `art` |
| key id | `9da6f546` |
| cipher pass | `5LYWK2aQES1l8SaZ3Fb_YV1unqmX6teWmWWoOFU97f0` |
| restic password | `RBb5bogfruDBWIq17yBBcELBlGmD1PX0PfVc4I3e0B8` |
| SSH seed | `66c06c14a59e398b32ea510289b8ace60133ebd6b86613e00442a3f93ab06d71` |

**Key of 32 bytes `ff`:**

| Output | Value |
|---|---|
| words | `zoo` 23 times, then `vote` |
| key id | `5ec9d612` |
| cipher pass | `OJ_BeB_RzCBsVZrGEy5OoLpYXe2jUgmCubde5CMaMrI` |
| restic password | `8D4je885KN2LzuP8KLWiimPnP0S_HBXjO5eVFYPAZgo` |
| SSH seed | `4ae31a05db820291944cf81850daa69ad184c765b87bd4fafe88890379912b04` |

The BIP-39 list is `agent/internal/backup/bip39-english.txt` (SHA-256 `2f5eed53…dbda`, the list of the BIP-39 repository).

### 2.3 Where each key goes

- The agent writes the cipher pass to `secrets/pgbackrest_repo_cipher_pass`. care-db reads it as a podman secret at its start.
- The agent derives the restic password and the SSH key for each run. It writes them to a directory of the run below
  `/run/care-pi/backup/` (tmpfs, 0700; the files 0600), and it deletes the directory after the run.
  restic gets only paths: `RESTIC_PASSWORD_FILE`, `AWS_SHARED_CREDENTIALS_FILE`, and `-o sftp.args=-F <ssh_config> -i <key>`.
  No secret is in an argument or in the environment.
- One SSH key serves each SFTP destination. A restored device derives the same key, so the servers still accept it.

### 2.4 The ceremony and the rotation

The setup step `backups` starts with the key (`docs/api/README.md` section 13.4):
make it, show the words and the QR code, offer the `.txt` file, hide the words, ask for 3 words.
The banner `recovery-key-unconfirmed` stays until the confirmation.

A rotation (`POST /backups/recovery-key/rotate`, job `recovery-key-rotate`) has two forms:

| Form | CLI | What it does |
|---|---|---|
| New key (`new_key: true`) | `care-pi backup recovery-key rotate` | Uses the confirmed pending key: a new local repository, a new repository `care-pi-<new key id>` at each destination, a full backup, and a first `db` and `files` snapshot. Then the new key is the active key, and only then the job deletes the old repositories. |
| Same key (`new_key: false`) | `care-pi backup recovery-key rotate --keep-key` | The re-key of a Phase 1 or Phase 2 host (a `legacy` local repository with a random pass). The job moves the repository to `/var/lib/care-pi/backup/pg.legacy`, writes the derived pass, restarts care-db, runs `stanza-create`, makes a full backup, and sends a snapshot to each destination. |

With `remove_old_repositories: true` (the default; the CLI flag `--keep-old-repositories` turns it off), the job deletes
the old repositories at the end. Care restarts one time in the job (the maintenance page, about 1 minute).

## 3. Destinations

| Type | Location | restic repository |
|---|---|---|
| `posix` with `path` | a directory | `<path>/care-pi-<key id>` |
| `posix` with `disk_label` | a USB disk that care-pi prepared | `/var/lib/care-pi/disks/<label>/care-pi-<key id>` |
| `sftp` | an SSH server | `sftp:<user>@<host>:<path>/care-pi-<key id>` (the port and the host key are in the SSH config of the agent) |
| `s3` | an S3 service | `s3:<endpoint>/<bucket>[/<prefix>]/care-pi-<key id>` (`-o s3.bucket-lookup=path`) |

Add a destination in this order: test, add to `config.yaml`, set the secret (S3), apply, first snapshot
(`docs/api/README.md` section 13.5). `apply` runs `ensure-mounts`, `ensure-repositories` (`restic init` when
`restic cat config` finds no repository), and `ensure-timers`. A destination that does not answer does not fail the apply.

### 3.1 A directory (`posix` with `path`)

The test warns (level `advised`) when the directory is on the root disk. A disk failure then loses both copies.
Use it for a NAS mount or for a test.

### 3.2 A USB disk (`posix` with `disk_label`)

1. Connect the disk. `care-pi backup disk list` (or `GET /backups/disks`) shows it with `usage: empty` or `usage: other`.
2. Prepare it: `care-pi backup disk prepare sdb --wipe [--label care-usb1]`. The job `disk-prepare` erases the disk
   (`wipefs -a`), writes a GPT with one Linux partition (`sfdisk`), makes ext4 with the label (`mkfs.ext4 -L`), writes the
   mount unit and the automount unit, and mounts the disk at `/var/lib/care-pi/disks/<label>`.
   The console sends the label that it chose (the next free `care-usbN`). Without a label, the agent chooses it.
3. Add the destination `{id: usb, type: posix, disk_label: care-usb1}`, then apply.

The agent refuses the disk of the system, a mounted disk, a label of a destination of another disk, and a label of another
connected backup disk (`409 disk-in-use`). The operation needs a recent authentication and `wipe: true`.

The mount units are `var-lib-care\x2dpi-disks-<label>.mount` (`nofail`) and `.automount` (idle unmount after 10 minutes).
`care-backup.target` wants the automount unit. Before each run, the agent checks `/dev/disk/by-label/<label>`.
When the disk is not connected, the run fails at once with "the backup disk … is not connected", and the destination goes
`degraded`, then `error` after `backups.alert_after_hours`. Connect the disk again. The next run catches up.

### 3.3 SFTP

1. Test the destination. The first test returns the host key of the server (type and SHA-256 fingerprint).
2. Compare the fingerprint with `ssh-keygen -lf /etc/ssh/ssh_host_<type>_key.pub` on the server. Accept it only when it is the same.
3. Add the public key of the agent (`care-pi backup ssh-key`, or `GET /backups/ssh-key`) to `~/.ssh/authorized_keys` of the user on the server.
   The line starts with `restrict`. You can limit it more, for example:
   `restrict,from="<IP of the device>",command="internal-sftp" ssh-ed25519 AAAA… care-pi@care-1a2b3c4d`.
   restic uses only the SFTP subsystem, so `command="internal-sftp"` is sufficient.
4. Test again. `config.yaml` gets `host_key` (the full key). `apply` writes `/etc/care-pi/rendered/backup/ssh_config` and
   `known_hosts` with `StrictHostKeyChecking yes`, `HostKeyAlgorithms <type>`, `IdentitiesOnly yes`, and `BatchMode yes`.

When the server changes its host key, each run fails with `destination-untrusted-hostkey`. Test the destination again, and
accept the new key only when you know why it changed.

### 3.4 S3

The agent keeps the keys in `/etc/care-pi/secrets/backup/<id>.json` (0600). Set them with
`care-pi backup destination secrets <id> --access-key-id KEY --secret-stdin` (the secret on the standard input) or
`PUT /backups/destinations/{id}/secrets`. The test creates the bucket only with `create_bucket: true`.
An S3 destination without its secret has the state `error` with the code `missing-credentials`.

## 4. Units and timers

`apply` renders these units (templates `quadlets/templates/care-backup*.tmpl`). The services run the agent CLI, not the
daemon: each run has its own unit, its own memory cap (`MemoryMax=512M`, `MemorySwapMax=128M`), `Nice=10`,
`IOSchedulingClass=idle`, and `GOMAXPROCS=2`.

| Unit | When (default) | Command |
|---|---|---|
| `care-backup-db-full.timer` | `Sun 01:00` (`backups.schedule.db_full`) | `care-pi backup run --scope local-db --type full --from-timer` |
| `care-backup-db-incr.timer` | `daily 02:00` (`backups.schedule.db_incr`) | `care-pi backup run --scope local-db --type incr --from-timer` |
| `care-backup-verify.timer` | `Sat 03:00` (`backups.schedule.verify`) | `care-pi backup verify --scope all --prune --from-timer` |
| `care-backup-drill.timer` | `*-*-01 04:00` (`backups.schedule.drill`; only with `drill_enabled`) | `care-pi backup drill --from-timer` |
| `care-backup-db@<id>.timer` | 1 minute after the start, then each `interval_min` (15) | `care-pi backup run --scope destination --destination <id> --snapshot db --from-timer` |
| `care-backup-files@<id>.timer` | `daily 02:30` (`backups.schedule.files`) | `care-pi backup run --scope destination --destination <id> --snapshot files --from-timer` |
| `care-backup.target` | at boot (`WantedBy=multi-user.target`) | wants each timer and each automount unit |

- The calendar timers have `Persistent=true`: a run that the device missed while it was off runs after the boot.
- systemd does not accept `daily HH:MM`. `apply` writes `*-*-* HH:MM` for it.
- Each timer is `PartOf=care-backup.target`. A stop of the target stops each timer. The restore jobs and `backups.paused` use this.
  With `backups.paused: true`, the target wants no timer.
- A destination timer writes no job journal: 96 runs each day for each destination would push the other jobs out of the journal.
  The agent keeps the last 10 runs of each destination in `/var/lib/care-pi/state/backups/destinations/<id>.json` (`recent_runs`).
  The other timers write a job with `source: timer`.
- `GET /logs/backup` (and `care-pi logs backup`) gives the journal of each `care-backup-*` unit. Each line has its unit.

## 5. Retention

| Where | Rule |
|---|---|
| Local repository | `repo1-retention-full` = `backups.retention.db_full` (default 2). pgBackRest expires after each backup and keeps the WAL of each kept full backup. The restore window is about `db_full` weeks. |
| `db` snapshots | `restic forget --tag db --group-by host,tags --keep-last 1 --keep-within 48h --keep-daily N --keep-weekly N --keep-monthly N` |
| `files` snapshots | `restic forget --tag files --group-by host,tags --keep-last 1 --keep-within <keep_within_days>d --keep-daily N --keep-weekly N --keep-monthly N` |

- **`db` snapshots keep 48 hours** (owner decision, wave 5a). Each `db` snapshot holds the whole local repository of its time.
  So 2 days of snapshots each 15 minutes are sufficient, and the daily, weekly, and monthly snapshots keep the older points.
  This keeps the snapshot list short (about 200 snapshots, not 1 300 with 14 days).
- The agent runs `forget` after each snapshot run (it is fast). The weekly verify runs `restic prune` (it rewrites data).
- `--keep-last 1` keeps the newest snapshot of each tag in all cases.

## 6. Locks

| Lock | Holder | Effect |
|---|---|---|
| `/run/care-pi/apply.lock` | apply, setup, the restore jobs, `disk-prepare`, `recovery-key-rotate` | One change of the host at a time. |
| `/run/care-pi/backup-local.lock` | each local pgBackRest backup | One local backup at a time. |
| `/run/care-pi/backup-<id>.lock` | each restic run of the destination `<id>` | One run for each destination at a time. |
| `/run/care-pi/backup-state.lock` | each write of a state file in `/var/lib/care-pi/state/backups/` | Short. |

- A backup does not take the apply lock: a snapshot each 15 minutes must not block an apply.
- A job of the console waits in the state `queued` for a run of the same target.
- The restore jobs stop `care-backup.target` (step `pause-backups`) and wait 10 minutes at most for each backup lock.
  While a restore job runs, a backup job gives `409 job-running`. The step `resume-backups` starts the target again.

## 7. The sandbox

`care-pi.service` keeps its sandbox (`ProtectSystem=strict` with `ReadWritePaths`, `MemoryMax=128M`). The backups add no
path and no capability to it:

- restic, the probe of a `posix` destination, and the commands of `disk-prepare` (`wipefs`, `sfdisk`, `partprobe`,
  `mkfs.ext4`) run in transient units: `systemd-run --wait --pipe --collect`. They see the mount namespace of the host
  (the backup disks, `/srv`), and they get their own memory cap. The agent gives the environment with `--setenv`.
- systemd mounts the backup disks from the mount units that `apply` writes to `/etc/systemd/system` (a writable path already).
  The agent starts the units with `systemctl`. The mounts propagate into the namespace of `care-pi.service`.
- The restore and the drill run `podman run` of the care-db image in a transient unit.
- The timers run `care-pi` as root in their own units, without the sandbox of the daemon.

So `care-pi.service` needs no `CAP_SYS_ADMIN` for `mount` or `mkfs`. This is the same model as the image pulls of Phase 2.

## 8. Restore

### 8.1 Point in time on this device

`care-pi restore pitr --time 2026-10-30T09:40:00Z`, or `POST /restore` with `{kind: pitr, time}`.
The time must be from the start of the restore window to the start of the job. The window ends after the last archived WAL,
because the checkpoint of the job archives the WAL up to its start.

The steps: `pause-backups`, `maintenance-on`, `stop-app`, `checkpoint` (an incremental backup with the archive check),
`stop-db`, `restore-db` (`pgbackrest restore --delta --type=time --target=<time> --target-action=promote` in a
`podman run` of the care-db image, with the data directory at the same path), `start-db`, `wait-promote`, `start-app`,
`wait-healthy`, `maintenance-off`, `backup-full` (a new timeline), `check-stanza`, `resume-backups`.

- **Undo.** The result has the checkpoint. `care-pi restore pitr --checkpoint <label>` (`{kind: pitr, checkpoint}`) gives the
  database of the time before the restore. The checkpoint stays as long as its backup stays in the local repository
  (`RestorePoints.checkpoints`). Each restore makes a full backup, so 2 more restores can expire it (`db_full: 2`).
- **Rollback.** When `restore-db`, `start-db`, or `wait-promote` fails, the job restores its checkpoint, starts the stack, and fails
  with the first error. The database is then the same as before the job.
- The files in the buckets do not change.

### 8.2 Files

`care-pi restore files --destination usb --snapshot 1a2b3c4d [--path care-media/x.pdf] [--side-dir]`.

- `--side-dir` writes into `/var/lib/care-pi/restore/<job id>/` and changes nothing else. The agent deletes the directory after 7 days
  (at the next files restore or the next daily incremental backup of the timer). It also deletes a drill directory after 1 day.
- Without `--side-dir`, the job stops care-s3 (the maintenance page) and writes the files in place. A newer file stays.
- restic restores the xattrs of versitygw (the content type, the ETag). `--verify` reads each file back.

### 8.3 Disaster recovery: a new or replaced device

You need the 24 words (or the `.txt` file, or the QR code) and access to one destination. The console password of the old device
is useful, but the recovery codes of the old device or `sudo care-pi auth reset` also work.

1. Turn off the old device, if it still runs. Else both devices write to the same repositories.
2. Install care-pi on the new device in setup mode (`installer/install.sh`). Claim it with the claim code.
3. For a USB disk, connect it. The disk list shows it with `usage: care-backup`.
4. In the setup wizard, choose "Restore from backup". Or, as root:

   ```sh
   cat > dest.json <<'EOF'
   {"type": "sftp", "host": "nas.lan", "user": "care", "path": "/volume1/care"}
   EOF
   sudo care-pi restore dr --inspect --destination-json dest.json --recovery-key-stdin < words.txt
   ```

   For SFTP, the first inspect gives the host key. Compare the fingerprint, then add `"host_key": "<type> <base64>"` to the file.
5. Read the result of the inspect: the clinic, the times of the newest snapshots, the restore window, the space, the warnings.
   The warning `source-may-be-active` tells that the newest snapshot is younger than 2 intervals. Make sure that the old device is off.
6. Start the restore: `sudo care-pi restore dr --destination-json dest.json --recovery-key-stdin < words.txt`
   (`--time T` for a point in time, `--no-files` without the files, `--pause-backups` for a test on a second device).
7. The job restores `/etc/care-pi`, applies with the restored lock (the images, the FE build), restores the pgBackRest repository,
   runs `pgbackrest restore`, restores the files, starts Care, makes a full backup, turns on the timers, sets the host name,
   and restores the console account of the old device. The setup state is then `ready`.
8. Log in with the console password of the old device. The password of the claim is not valid any more.

After the event `done`, the agent ends each session. The result has the clinic name (`display_name`) and the URLs.
Then the agent puts back the local CA of Caddy of the old device and restarts care-caddy (a few seconds).
The clinic devices that trust the old root CA need no change. A snapshot of an agent before wave 5a has no CA: then the
device keeps its new CA, and each clinic device must install the new root CA (`docs/install.md`).
The destinations continue in the same repository `care-pi-<key id>`. When the job fails, the setup state is `setup`, the account
of the claim stays, and you can start the restore again.

On lumine (Phase 3 exit test), the full procedure took 8 min 46 s: 6 s for the install, 355 s for the restore job (with the FE build).

### 8.4 The restore drill

The drill (`care-pi backup drill`, monthly timer) restores the newest local backup into `/var/lib/care-pi/drill/<job id>/`,
starts PostgreSQL there in a throwaway container (`archive_mode=off`, no listen address, `max_connections=40` as care-db),
runs `pg_controldata` and `select 1`, and deletes everything. It never touches the live database or the repository.

- It needs free space of 2 times the database size. Else a timer run records `skipped`, and a manual run gives `409 insufficient-space`.
- The environment variable `CARE_PI_DRILL_FREE_BYTES` replaces the free space (a test only).
- A failed drill gives the banner `drill-failed`.

## 9. Health

`GET /backups/health` (and `care-pi backup health`) gives the banners of `docs/api/README.md` section 13.10.
`GET /status` has the same object in `backups`. A destination is:

| State | Rule |
|---|---|
| `pending` | No run yet (a new destination). |
| `ok` | The last run succeeded. |
| `degraded` | The last run failed, and a successful snapshot is younger than `backups.alert_after_hours`. No banner. |
| `error` | No successful snapshot for `backups.alert_after_hours` (default 24), a changed host key, or no secret. Banner `destination-unreachable`. |
| `unknown` | The agent cannot read the state of the destination. |

The local repository gives `local-backup-failed` (the last scheduled backup failed), `local-disk-low`, and `wal-archive-failing`
(`archive-push` fails for longer than 2 times `archive_timeout`).

## 10. Commands

| Command | Operation |
|---|---|
| `care-pi backup status` | `GET /backups` |
| `care-pi backup health` | `GET /backups/health` |
| `care-pi backup run [--scope all\|local-db\|destination] [--type incr\|full] [--destination ID]` | `POST /backups/run` |
| `care-pi backup verify [--scope …] [--read-data-subset PCT] [--prune]` | `POST /backups/verify` |
| `care-pi backup drill` | `POST /backups/drill` |
| `care-pi backup recovery-key create\|show\|confirm\|status\|rotate [--keep-key]` | `/backups/recovery-key*` |
| `care-pi backup ssh-key [--pending]` | `GET /backups/ssh-key` |
| `care-pi backup destination list\|test\|secrets` | `/backups/destinations*` |
| `care-pi backup disk list\|prepare` | `/backups/disks*` |
| `care-pi restore points` | `GET /restore/points` |
| `care-pi restore pitr --time T \| --checkpoint LABEL` | `POST /restore` (`pitr`) |
| `care-pi restore files --destination ID --snapshot ID [--path P]... [--side-dir]` | `POST /restore` (`files`) |
| `care-pi restore dr [--inspect] --destination-json FILE --recovery-key-stdin` | `POST /restore/dr/inspect`, `POST /restore/dr` |

The CLI as root needs no session and no recent authentication. `--json` gives the JSON object of the operation.

## 11. Troubleshooting

**A destination is `degraded` or `error`.** Run `sudo care-pi backup destination list` and `sudo care-pi logs backup`.
Then run `sudo care-pi backup destination test <id>`. The test tells the next action.

**"the backup disk care-usb1 is not connected".** Connect the disk. Run `sudo care-pi backup run --scope destination --destination <id>`, or wait for the next timer run.

**`destination-untrusted-hostkey`.** The SFTP server shows another host key. Ask the administrator of the server why.
Then test the destination with `--accept-fingerprint` and apply.

**`recovery-key-mismatch` in a restore.** The words do not open `care-pi-<key id>` at this location. Check the words.
An older key opens only the repository of that key. The destination test lists the other key ids at the location
(`repository.other_key_ids`).

**`insufficient-space`.** A local full backup needs 1.2 times the last full backup. Remove old files, or set a lower `backups.retention.db_full`.

**`wal-archive-failing`.** Run `sudo care-pi logs db`. Look for `archive-push` errors. A full disk is the usual cause.
Then run `sudo podman exec --user postgres care-db pgbackrest --stanza=main check`.

**A restore job failed.** Read the job: `sudo care-pi jobs show <id>`. A PITR that failed restored its checkpoint.
A restore from backup that failed leaves the restored config as the draft; start it again.

**A stuck lock.** A lock is a `flock` in `/run/care-pi`. It ends with its process. Run `systemctl list-units 'care-backup-*'` to find the run.
