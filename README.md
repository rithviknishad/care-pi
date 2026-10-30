# care-pi

care-pi is a self-hosted CARE for a small clinic or pharmacy.
It runs on a Raspberry Pi with as little as 2 GB of RAM.
CARE consists of the `ohcnetwork/care` backend and the `ohcnetwork/care_fe` frontend.
A Go agent reads one file, `/etc/care-pi/config.yaml`.
The agent renders podman quadlet units, env files, and a Caddyfile from this file.
Then the agent applies them.
A second `apply` with the same file changes nothing.
A web console, a `curl | sh` installer, and a Pi image are planned.
care-pi is an independent project.

Status: Phase 1 in progress. Only the `care-pi version` command works today.

## Layout

| Directory | Holds | Phase |
|---|---|---|
| `agent/` | Go code of the `care-pi` CLI and agent | 1 |
| `console/` | Web console (React, Vite, careui) | 2 |
| `quadlets/` | Templates for quadlet units, env files, and the Caddyfile | 1 |
| `images/` | Containerfiles that we publish | 1 |
| `solutions/` | One directory for each solution (plug set and env defaults) | 1, 4 |
| `installer/` | `install.sh` for Debian-family hosts | 1 |
| `image/` | Raspberry Pi image build, first-boot units, Imager `os_list.json` | 5 |
| `docs/` | Operator and developer documentation | all |
| `.github/` | CI workflows | all |

The Go module is at the repository root. Its path is `github.com/rithviknishad/care-pi`.

## Quick start for developers

Install Go 1.26 or later. Then run these commands:

```sh
make build
make test
bin/care-pi version
```

To cross-build the Linux binaries, run `make build-linux`.
The output is `bin/care-pi_linux_arm64` and `bin/care-pi_linux_amd64`.
The other targets are `vet`, `lint`, `fmt`, `tidy`, and `clean`.

## Plans

Read `plans/README.md` first.
Then read `plans/09-handoff.md`. It states the decisions, the measured facts, and the next steps.
