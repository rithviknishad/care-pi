# agent

This directory holds the Go code of the `care-pi` CLI.
The CLI renders `/etc/care-pi/config.yaml` into quadlet units, env files, a Caddyfile,
`pgbackrest.conf`, status pages, and a resolved `lock.yaml`. Then it applies them with the
smallest set of restarts.
Since Phase 2, `care-pi serve` (`care-pi.service`) is the daemon: the console API of `docs/api/openapi.yaml`
and the console SPA on the unix socket `/run/care-pi/api.sock`. The HTTP handlers and the CLI commands call
the same service layer (`internal/service`), so each operation has a CLI command with the same effect.

- How apply works: `docs/apply.md`.
- The console server, the auth, the setup mode, the jobs, and the dev listener: `docs/console-server.md`.
- The API contract: `docs/api/openapi.yaml` and `docs/api/README.md`.
- Every config key: `docs/config.md`.
- The FE providers and the build id: `docs/fe.md`.
- The local CA, the public name, Tailscale, and Wi-Fi (Phase 6): `docs/connectivity.md`.
- The CLI, paths, and units contract: the Phase 1 contract of the orchestrator.

## Commands

| Command | File |
|---|---|
| `version` | `cmd/care-pi/version.go` |
| `serve` | `cmd/care-pi/serve.go` |
| `bootstrap` | `cmd/care-pi/bootstrap.go` |
| `apply` | `cmd/care-pi/apply.go` |
| `render` | `cmd/care-pi/render.go` |
| `config validate`, `config schema`, `config show`, `config set`, `config presets` | `cmd/care-pi/config.go` |
| `status` | `cmd/care-pi/status.go` |
| `logs` | `cmd/care-pi/logs.go` |
| `care create-superuser`, `care info` | `cmd/care-pi/care.go`, `cmd/care-pi/ops.go` |
| `auth claim-code\|set-password\|reset\|recovery-codes\|totp\|sessions\|unlock\|status` | `cmd/care-pi/auth.go` |
| `setup status\|skip\|finish` | `cmd/care-pi/setup.go` |
| `jobs list\|show\|follow\|cancel`, `support-bundle`, `tls show\|root-ca`, `power reboot\|shutdown` | `cmd/care-pi/ops.go` |
| `clock wait` | `cmd/care-pi/clock.go` |
| `host check` | `cmd/care-pi/host.go` |
| `fe build`, `fe status`, `fe prune` (and the hidden `fe build-id`, `fe env`, `fe script` for CI) | `cmd/care-pi/fe.go` |
| `backup status\|health\|run\|verify\|drill\|ssh-key`, `backup recovery-key create\|show\|confirm\|status\|rotate`, `backup destination list\|test\|secrets`, `backup disk list\|prepare` (and the hidden `backup destination probe` of the transient unit) | `cmd/care-pi/backup.go` |
| `restore points\|pitr\|files\|dr` | `cmd/care-pi/restore.go` |
| `update status\|check\|stage\|apply\|rollback\|history`, `update --os [--check\|--now\|--reboot\|--reboot-if-required]` (Phase 4) | `cmd/care-pi/update.go` |
| `plugs list\|set\|schema` (Phase 4) | `cmd/care-pi/plugs.go` |
| `solution list\|show\|validate` (and `solution lock`, `solution index` for CI and `make-lock.sh`) (Phase 4) | `cmd/care-pi/solution.go` |
| `tls public enable\|disable\|status\|update-address\|rotate`, `tls tailscale enable\|disable\|status`, `network wifi scan\|set\|remove\|status` (and the hidden `network wait-address` of the pod unit) (Phase 6) | `cmd/care-pi/connectivity.go` |

Global flags: `--config` (default `/etc/care-pi/config.yaml`), `--root` (default `/`), `-v`.
`apply`, `bootstrap`, `fe build`, `fe prune`, `restore pitr|files|dr`, `backup disk prepare`, and
`backup recovery-key rotate` hold the apply lock `/run/care-pi/apply.lock` for their full run.
`backup run|verify|drill` take only the lock of their target (`docs/backups.md`, section 6). The backup timers run
these commands with `--from-timer`.
Their flag `--lock-timeout` (default `30m`) limits the wait for the lock (`docs/apply.md`, "The apply lock").
`bootstrap --fe-provider` (default `local`) sets `fe.provider` of a new `config.yaml`.
`--root` puts every absolute path below another directory. The tests use it.
To add a subcommand, add one file in `cmd/care-pi/`. Register the command on `rootCmd` in `init()`.

## Packages

| Package | Job |
|---|---|
| `internal/paths` | Every host path, and the directory layout with owners and modes. `Paths.Abs` adds the `--root` prefix. |
| `internal/applock` | The apply lock: an exclusive `flock` on `/run/care-pi/apply.lock`, with a timeout. |
| `internal/config` | The `config.yaml` types, defaults, presets, memory caps, and the JSON Schema (`schema/config.schema.json`). |
| `internal/solution` | Load `solutions/<name>/solution.yaml` (embedded by `solutions/embed.go`), apply the custom plug overrides and the operator choices (`solution.plugs`), merge the env. Phase 4: the schemas (`solution`, `lock`, `index`), solution locks and `index.json` (parse, assemble for CI), the embedded locks and the lock cache (`LockStore`), lock versions and `min_agent_version`, the settings schema of a plug. |
| `internal/secrets` | Generate and read the secret files. The JWKS generator. The map from secret files to podman secrets. |
| `internal/lock` | Read and write `lock.yaml` (version 2; a version 1 file is migrated in memory), keep the history (`history.previous`), resolve refs with skopeo and git. Print one `resolve <ref> … <id>` line for each network resolution. |
| `internal/netinfo` | The LAN IPv4 addresses from the interfaces. Phase 6: the address of `tailscale0` (a separate source), and the wait for an address (`network wait-address`). |
| `internal/render` | The template model and the renderer. The templates are in `quadlets/templates/`. |
| `internal/plan` | Compare the rendered tree with the disk. Decide the actions (buckets and stanza before the FE build). Unified diff for `--dry-run`. |
| `internal/apply` | Run the plan through the host clients. Write the job journal. |
| `internal/host` | The host clients: `Systemd`, `Podman`, `Skopeo`, `Journal`, `Clock`, `Git`. One `Runner` executes each command. `Fake` records the commands for tests. `Dev` answers with plausible output for the dev listener (`serve --fake-host`), also for the backup commands. `Transient` runs `podman pull`, restic, the disk tools, and the probe of a destination in a transient unit (`care-pi serve`; `docs/backups.md`, "The sandbox"). |
| `internal/s3admin` | Create the buckets and the facility read policy with minio-go. |
| `internal/backup` | Phase 3. The recovery key (BIP-39, HKDF-SHA256, the key store), the destinations, the restic runner (one session for each run in `/run/care-pi-backup/`), pgBackRest (`info`, the backup arguments, the encrypted `backup.info`), the state files, the locks, the health and the banners, the destination tests (SSH TOFU, S3, the posix probe), `ensure-repositories`. `docs/backups.md`. |
| `internal/restore` | Phase 3. The restore jobs: point in time (with the checkpoint and the rollback), files, the restore drill, the re-key and the rotation, the restore from backup of a new device. |
| `internal/disks` | Phase 3. The block devices (`lsblk --json`), their usage, and the commands of a disk preparation (`wipefs`, `sfdisk`, `mkfs.ext4`). |
| `internal/fe` | The FE providers: `none`, `artifact` (download and extract a bundle), `local` (scale down, start `care-fe-build.service`, restore; also after SIGINT, SIGTERM, or SIGHUP). The build id, `fe/current`, the list of builds, and prune. `RemoveScaleDown` removes a leftover scale-down drop-in for apply. |
| `internal/status` | Collect the unit states, health, memory, and lock summary. |
| `internal/hostcheck` | Check the host prerequisites. It never changes the host. |
| `internal/server` | `care-pi serve`: the routes of the contract, the middleware (session, CSRF, `Origin`, rate limits, body rules), SSE, the SPA, the unix socket and the dev listener, sd_notify. `server/contracttest` validates each test exchange against `docs/api/openapi.yaml` (kin-openapi). |
| `internal/service` | The operations of the API and of the CLI: status, setup, config (ETag, merge patch, plan preview), apply, jobs, logs, support bundle, network, TLS, FE, Care, power, backups and restore (`backups.go`, `backupjobs.go`). |
| `internal/auth` | The console account (`secrets/agent.json`): argon2id, recovery codes, TOTP, the claim code and the Imager password, sessions (`state/sessions.json`), rate limits and locks. |
| `internal/setup` | The setup record (`state/setup.json`), the state machine, and the apply phase (`bootstrap` or `full`). |
| `internal/jobs` | The job journals and their events (`state/jobs/`), the apply lock for jobs, cancel, retention. |
| `internal/problem` | The RFC 9457 problem types of `docs/api/README.md` section 7. |
| `internal/exitcode` | The exit codes of the contract. |
| `internal/buildinfo` | Version, commit, and build date. |
| `internal/update` (Phase 4) | The update machine (`docs/solutions.md` sections 7 to 12): the index client, the tracks, the jobs `update-check`, `update-stage`, `update-apply`, `update-rollback` with the cutover and the automatic rollback, the recovery after an interruption, the `Checkpointer` interface and its pgBackRest stub, the verifier, OS updates, plug changes, image retention. `update/updatetest` makes a `file://` dist with two locks for tests. |
| `internal/window` (Phase 4) | The update window in the time zone of the host: open or closed, the next opening, the timer calendars, daylight saving time. |
| `internal/plugcfg` (Phase 4) | The `plug_config` rows of Care: the desired rows, the sync through `manage.py shell` in `care-api`, and the managed slugs (`state/plug-config.json`). |
| `internal/ca` (Phase 6) | The name-constrained root CA (ECDSA P-256, 10 years): the constraint set, make, load, write, the one-time switch from the root of Caddy (`state/ca.json`), the adoption after a restore from backup, and the read of an issued ACME certificate in the data of Caddy. |
| `internal/dnsclient` (Phase 6) | The client of the DNS/ACME service (`services/dns/API.md`): the Ed25519 signatures, register, address, status, rotate, delete, retries. `Fake` is an `httptest` server of the same protocol. |
| `internal/tailscale` (Phase 6) | `tailscale status --json`, the install in the transient unit `care-pi-tailscale-install`, `up` with an auth key file or the login link (`care-pi-tailscale-up`), `down`, `logout`. |
| `internal/wifi` (Phase 6) | The NetworkManager connection `care-pi-wifi`: the keyfile (through `care-pi-wifi-keyfile`), the scan, the country, the link state, the route check, remove. |
| `internal/clockgate` (S52) | The clock without internet: the time of the browser at the claim (plausible, one time), the record `state/clock-trusted.json`, the source and the trust of the clock, and the Caddy reload after a jump. |

The agent runs `systemctl`, `systemd-run`, `podman`, `skopeo`, `journalctl`, `timedatectl`, and `hostnamectl` as commands.
Phase 4 adds `dpkg-query`, `apt-cache`, and, in the transient unit `care-pi-apt`, `apt-get` and `apt-mark`.
Phase 6 adds `nmcli`, `ip route`, `tailscale`, and, in transient units, `apt-get install tailscale`, `raspi-config` (or `iw`),
and the write of the Wi-Fi keyfile. S52 adds `date -s` and `fake-hwclock save` in the transient unit `care-pi-clock-set`.
It reads git refs over HTTPS, so the host needs no git.

## Tests

Run every test on any OS. No test needs root, podman, or network:

```sh
make test
```

The apply tests run against `host.Fake` and a temporary `--root`.
They check the exact command list, for example
stop `care.target` → wait until inactive → `reset-failed` → start `care.target`.
Most apply tests set `fe.provider: none`, because the default `local` adds an FE build to each apply.
The restore tests (`internal/restore`) check the ordered commands of each job the same way: the point-in-time restore
and its rollback, the files restore, the drill, the rotation, and the restore from backup. `internal/backup` checks
the key vectors of `docs/backups.md`, the restic arguments, the retention, and the health with a fake clock.
`internal/disks` checks that a disk preparation refuses the disk of the system.

The update tests (`internal/update/*_test.go`) run the update jobs end to end against `host.Fake` and a `file://`
index with the locks `2026.11.0` and `2026.11.1` (`update/updatetest`). They check the steps and the commands of
each job: an update from N to N+1, the rollback with the PITR offer, a migration failure with the automatic rollback,
a checkpoint failure, a withdrawn lock, `agent-too-old`, a bad lock hash, the index over HTTPS (ETag, 404, size limit),
OS updates, plug plans and a plug restart, the window run, and the recovery after an interruption.

## Clock gate

`care-clock.service` runs `care-pi clock wait` before `care.target`. `care-start.service` starts
`care.target` at boot with `--no-block`, so the boot does not wait for the clock gate.
When the clock passes, `clock wait` deletes the flag `state/mode/clock-wait`. Then it reloads Caddy:
`podman exec care-caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile --force`.
When the reload fails, it runs `systemctl try-restart care-caddy.service`. Errors do not stop the gate.
Caddy starts before the gate, so with a stale clock it can load an expired certificate as valid.
The reload makes Caddy check the certificates with the correct clock (`docs/apply.md`).

## Golden files

`internal/render/testdata/golden/<case>/` holds the rendered tree of each case:
`solo`, `small-clinic`, `busy-counter`, `custom`, `small-clinic-bootstrap` (setup mode: `apply --phase bootstrap`),
`backups` (Phase 3: a USB disk, an SFTP, and an S3 destination; the timers, the mount units, the SSH files),
`custom-build` (Phase 4: a local backend build and a local FE plug build), `public` (Phase 6: the public name), and
`public-tailscale` (Phase 6: the public name, Tailscale, and 2 LAN addresses).
The configs are `internal/render/testdata/config-<case>.yaml`.
The cases use a test solution in `internal/render/testdata/solutions/`, the addresses
`192.168.1.10` and `10.0.0.5`, and a lock from a fake resolver (digest = SHA-256 of the reference).
After a template change, update the goldens and review the diff:

```sh
go test ./agent/internal/render -update
git diff agent/internal/render/testdata/golden
```

## Render without a Pi

`render` needs no root and no network:

```sh
go run ./agent/cmd/care-pi render \
  --config agent/internal/render/testdata/config-small-clinic.yaml \
  --out ./out --root ./root --ip 192.168.1.10
```

It reads the lock below `--root` when the lock exists. Without a lock, each unit uses the image tag.
Check the output with the quadlet generator and Caddy:

```sh
docker run --rm -v "$PWD/out/etc/containers/systemd:/etc/containers/systemd:ro" \
  quay.io/podman/stable /usr/libexec/podman/quadlet -dryrun
docker run --rm -v "$PWD/out/etc/care-pi/rendered/caddy:/etc/caddy:ro" \
  docker.io/library/caddy:2 caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
```

From Phase 6, the Caddyfile names the root of the agent (`pki`), and the public and Tailscale sites need the modules
of `images/caddy` (`acmedns`, `tailscale`). Build the image, and mount a test root and a credentials file:

```sh
docker build -f images/caddy/Containerfile -t care-pi-caddy:dev .
mkdir -p ca && openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 -subj /CN=test \
  -keyout ca/root.key -out ca/root.crt
echo '{"username":"u","password":"p","subdomain":"s","server_url":"https://care-pi.rithviknishad.dev"}' > acme.json
docker run --rm -v "$PWD/out/etc/care-pi/rendered/caddy:/etc/caddy:ro" -v "$PWD/ca:/etc/care-pi-ca:ro" \
  -v "$PWD/acme.json:/run/secrets/care-pi-acme-dns:ro" \
  care-pi-caddy:dev caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
```
