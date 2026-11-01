# docs

This directory holds the operator documentation and the developer documentation.
The planning documents are in `plans/`. Start with `plans/09-handoff.md`.

| File | Content |
|---|---|
| `install.md` | Install Care on a host with `installer/install.sh`. The setup mode and the claim code. |
| `config.md` | Each key of `/etc/care-pi/config.yaml`. |
| `apply.md` | How `care-pi apply` works: the lock, the steps, the rendered files, the plan rules, setup mode. |
| `fe.md` | The FE providers and the FE build. |
| `backups.md` | Backups and restore (Phase 3): the recovery key and its test vectors, the destinations, the timers, PITR, files, the restore drill, a restore of a new device. |
| `solutions.md` | Phase 4: the solution format, solution locks, `index.json`, the CI artefacts, tracks, updates, rollback, OS updates, and plugs. |
| `image.md` | Phase 5: the Raspberry Pi image: the build (pi-gen), the check, Raspberry Pi Imager, the first boot, the `user-data` pre-seed, the hardware test. |
| `console-server.md` | `care-pi serve`: the console API server, auth and claim, setup mode, jobs and SSE, the dev listener. |
| `console-ui.md` | The console SPA: design decisions, layout rules, the job step texts, the pages of each phase. |
| `connectivity.md` | Phase 6: the name-constrained local CA, the public name, Tailscale, the names that Care serves, Wi-Fi and the failover. |
| `dns-service.md` | Phase 6: the DNS/ACME service (`services/dns/`). |
| `api/` | The contract between the agent and the console (OpenAPI 3.1 and its design note). |
