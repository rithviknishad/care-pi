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

Global flags: `--config` (default `/etc/care-pi/config.yaml`), `--root` (default `/`), `-v`.
`apply`, `bootstrap`, `fe build`, and `fe prune` hold the apply lock `/run/care-pi/apply.lock` for their full run.
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
| `internal/solution` | Load `solutions/<name>/solution.yaml` (embedded by `solutions/embed.go`), apply the custom plug overrides, merge the env. |
| `internal/secrets` | Generate and read the secret files. The JWKS generator. The map from secret files to podman secrets. |
| `internal/lock` | Read and write `lock.yaml`, keep the history, resolve refs with skopeo and git. Print one `resolve <ref> … <id>` line for each network resolution. |
| `internal/netinfo` | The LAN IPv4 addresses from the interfaces. |
| `internal/render` | The template model and the renderer. The templates are in `quadlets/templates/`. |
| `internal/plan` | Compare the rendered tree with the disk. Decide the actions (buckets and stanza before the FE build). Unified diff for `--dry-run`. |
| `internal/apply` | Run the plan through the host clients. Write the job journal. |
| `internal/host` | The host clients: `Systemd`, `Podman`, `Skopeo`, `Journal`, `Clock`, `Git`. One `Runner` executes each command. `Fake` records the commands for tests. `Dev` answers with plausible output for the dev listener (`serve --fake-host`). `Transient` runs `podman pull` in a transient unit (`care-pi serve`). |
| `internal/s3admin` | Create the buckets and the facility read policy with minio-go. |
| `internal/fe` | The FE providers: `none`, `artifact` (download and extract a bundle), `local` (scale down, start `care-fe-build.service`, restore; also after SIGINT, SIGTERM, or SIGHUP). The build id, `fe/current`, the list of builds, and prune. `RemoveScaleDown` removes a leftover scale-down drop-in for apply. |
| `internal/status` | Collect the unit states, health, memory, and lock summary. |
| `internal/hostcheck` | Check the host prerequisites. It never changes the host. |
| `internal/server` | `care-pi serve`: the routes of the contract, the middleware (session, CSRF, `Origin`, rate limits, body rules), SSE, the SPA, the unix socket and the dev listener, sd_notify. `server/contracttest` validates each test exchange against `docs/api/openapi.yaml` (kin-openapi). |
| `internal/service` | The operations of the API and of the CLI: status, setup, config (ETag, merge patch, plan preview), apply, jobs, logs, support bundle, network, TLS, FE, Care, power. |
| `internal/auth` | The console account (`secrets/agent.json`): argon2id, recovery codes, TOTP, the claim code and the Imager password, sessions (`state/sessions.json`), rate limits and locks. |
| `internal/setup` | The setup record (`state/setup.json`), the state machine, and the apply phase (`bootstrap` or `full`). |
| `internal/jobs` | The job journals and their events (`state/jobs/`), the apply lock for jobs, cancel, retention. |
| `internal/problem` | The RFC 9457 problem types of `docs/api/README.md` section 7. |
| `internal/exitcode` | The exit codes of the contract. |
| `internal/buildinfo` | Version, commit, and build date. |

The agent runs `systemctl`, `systemd-run`, `podman`, `skopeo`, `journalctl`, `timedatectl`, and `hostnamectl` as commands.
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
`solo`, `small-clinic`, `busy-counter`, `custom`, and `small-clinic-bootstrap` (setup mode: `apply --phase bootstrap`).
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
