# docs

This directory holds the operator documentation and the developer documentation.
The planning documents are in `plans/`. Start with `plans/09-handoff.md`.

| File | Content |
|---|---|
| `install.md` | Install Care on a host with `installer/install.sh`. The setup mode and the claim code. |
| `config.md` | Each key of `/etc/care-pi/config.yaml`. |
| `apply.md` | How `care-pi apply` works: the lock, the steps, the rendered files, the plan rules, setup mode. |
| `fe.md` | The FE providers and the FE build. |
| `console-server.md` | `care-pi serve`: the console API server, auth and claim, setup mode, jobs and SSE, the dev listener. |
| `api/` | The contract between the agent and the console (OpenAPI 3.1 and its design note). |
