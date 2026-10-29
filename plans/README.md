# care-pi — plans

Self-hosted [CARE](https://github.com/ohcnetwork/care) for small clinics and pharmacies on a
Raspberry Pi: a flashable Pi OS Lite image, a `curl | sh` installer, and a Go agent with an
on-Pi web console that renders one declarative `config.yaml` into rootful podman/quadlet
units, Caddy, pgBackRest/restic backups, and solution-based updates.

Status: **planning complete, Phase 0 (hardware spikes) in progress** on the dev Pi `lumine`.
Nothing is implemented yet beyond throwaway spike scripts (kept out of the repo, S39).
Start with `09-handoff.md` if you are picking this up.

How to read the labels used across these documents:

| Label | Meaning |
|---|---|
| **SETTLED** / `S<n>` | Decided with the owner. Not re-litigated. |
| **PROPOSED** / `P<n>` | Design calls made by the planning agent; all of P1–P20 are now accepted (S37, S40). |
| **OPEN** / `O<n>` | A question that still changes the plan in some phase. Listed in `00-decisions.md` §C. |
| **SPIKE** / `SP<n>` | Something measured (or still to measure) on hardware before relying on it. Results in `08-spike-log.md`. |
| *(inference)* | A claim not yet verified on hardware or in docs. |

Files, in suggested reading order:

1. `09-handoff.md` — **start here**: consolidated state, settled decisions, measured numbers, design consequences, what is left, how to resume on lumine.
2. `00-decisions.md` — the authoritative decision ledger (S1–S40, P1–P20, O1–O17).
3. `01-architecture.md` — what runs where, the declarative config model, routing, TLS/naming modes, memory budget and presets, agent/console.
4. `02-backups-and-restore.md` — Postgres PITR without downtime, files, destinations (one local pgBackRest repo + restic per destination), recovery key, restore flows.
5. `03-solutions-releases-updates.md` — the "solution" format, refs vs pins, CI artefacts, lockfile, update/rollback orchestration.
6. `04-security.md` — scoped threat model, agent auth, secrets, network exposure.
7. `05-image-and-installer.md` — Pi image pipeline, first boot, Raspberry Pi Imager, generic-Linux installer.
8. `06-roadmap.md` — phases, exit criteria, spike list with results, risk register.
9. `07-first-contact-trust.md` — the first-contact trust problem in depth (browser constraints, every option, cross-cutting controls, the decision).
10. `08-spike-log.md` — Phase 0 raw results from lumine, SP1–SP6, with the per-spike findings that changed the design.

Source material consulted (local checkouts, not assumed): `~/ohc.network/care`
(compose files, `docker/prod.Dockerfile`, `plugs/`, `scripts/*.sh`,
`config/settings/*.py`, `.github/workflows/deploy.yml`), `~/ohc.network/care_fe`
(`Dockerfile`, `care.config.ts`, `src/PluginEngine.tsx`, routes),
`~/ohc.network/care_clinic` (deployment kit and docs, for inspiration only),
`~/ohc.network/careui`, and lumine itself for every number in `08`.
