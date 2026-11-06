# console

The care-pi console: the setup wizard and the control panel of the device.
The agent embeds the build (`dist/`) and serves it at `https://<host>:8443/` (decision S48).
The API is on the same origin at `/api/v1`. The contract is `docs/api/openapi.yaml` and `docs/api/README.md`.
The design rules are in `docs/console-ui.md`; the look (the v2 design system, S66) is in `console/design.md`.

Stack: Vite, React 19, TypeScript (strict), Tailwind CSS 4, Care UI (careui) components, react-router,
TanStack Query, openapi-fetch with types from `openapi-typescript`, Vitest, Playwright.

## 1. Requirements

- Node.js 22 or later and npm.
- For the e2e tests: the Playwright browsers. Install them one time:

```sh
npx playwright install chromium webkit
```

## 2. Scripts

Run each script in `console/`.

| Script                            | What it does                                                                                                                                            |
| --------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `npm ci`                          | Installs the exact packages of `package-lock.json`.                                                                                                     |
| `npm run dev`                     | Starts the Vite dev server at `http://localhost:5173`. It sends `/api` and `/_care-pi` to `CARE_PI_API` (default: the mock at `http://127.0.0.1:4011`). |
| `npm run mock`                    | Starts the stateful mock on port 4011 and Prism (the examples of the contract) on port 4010.                                                            |
| `npm run mock:state`              | Starts only the stateful mock (port 4011, `MOCK_PORT`).                                                                                                 |
| `npm run build`                   | Removes the old build and writes `dist/` (`dist/.gitkeep` stays).                                                                                       |
| `npm run preview`                 | Serves `dist/` with the SPA fallback and the same proxy as `npm run dev`.                                                                               |
| `npm run typecheck`               | Checks the types of the app, the tests, and the config files.                                                                                           |
| `npm run lint`                    | ESLint, Prettier (check only), and the check of the careui files against the lock file.                                                                 |
| `npm run format`                  | Formats each file with Prettier.                                                                                                                        |
| `npm run test`                    | Unit tests (Vitest, Testing Library, jsdom).                                                                                                            |
| `npm run test:e2e`                | Builds, then starts the mock and `vite preview`, then runs the Playwright tests.                                                                        |
| `npm run test:e2e:real`           | Runs the Playwright journeys of `e2e/real` against a real device (`playwright.real.config.ts`, `e2e/real/README.md`).                                   |
| `npm run test:v2`                 | The checks of the v2 design system on the `/__v2` gallery (axe, stability, reduced motion, fonts, contrast; `console/design.md` section 14).            |
| `npm run api:types`               | Makes `src/api/types.gen.ts` from `docs/api/openapi.yaml`.                                                                                              |
| `npm run api:check`               | Exits with code 1 when `src/api/types.gen.ts` is not the same as a new file.                                                                            |
| `npm run careui:add -- <name>...` | Adds careui components from the registry (section 6).                                                                                                   |
| `npm run careui:check`            | Exits with code 1 when a careui file is not the same as the registry file of the lock.                                                                  |

Do the full check before a commit:

```sh
npm run api:check && npm run typecheck && npm run lint && npm run test && npm run test:e2e && npm run test:v2
```

Screenshots of each page (desktop light and dark, phone light) for a design review, after `npm run build`:

```sh
SHOTS_DIR=/path/to/dir npx playwright test -c playwright.shots.config.ts   # SHOTS_ONLY=setup,jobs for some pages
```

## 3. Work with the mock

The mock (`mock/`) is a small Node HTTP server. It implements the contract for the console:

- the claim (claim code `4F7K-Q2MX-9PDA`, Imager password `imager-password`), the login, the recovery, and TOTP (code `000000`);
- the session cookie, the CSRF token (`X-CSRF-Token`), and the `Origin` check;
- the rate limits and the lockout with `Retry-After`;
- the setup state machine, the setup job with SSE events, and the apply job;
- the backups and the restore (contract section 13): the recovery key (make, show, `.txt`, confirm, rotate), the destinations
  through `PATCH /config` and the secrets, the destination test with the SFTP host key (TOFU) and the key of the device, the
  disks and their prepare, the health and the banners, the restore points, and the jobs `backup-run`, `backup-verify`,
  `restore-drill`, `disk-prepare`, `recovery-key-rotate`, `restore-pitr`, `restore-files`, and `restore-dr` with their steps;
- logs with SSE follow, `If-Match` on `/config`, and `501` for each operation of Phase 6 and 7;
- contract 0.9.0 (S67, S68): `job` (with `id`, `state`, `title`), `jobs_recent`, and `setup_job_id` of
  `GET /_care-pi/status`; the public job stream `GET /_care-pi/jobs/{id}/events` (steps only); `GET /jobs?state=active|recent`;
  `log_tail` of `GET /jobs/{id}`; the plain-text exports `GET /jobs/{id}/export`, `GET /logs/{unit}/export`, and
  `GET /doctor/export`;
- Phase 4 (`mock/lib/updates.mjs`): the update machine (check, stage, apply with the cutover steps and the maintenance
  flag of `GET /status` and `GET /_care-pi/status`, rollback with the PITR offer), the window, the history, OS updates,
  the plugs (plan preview, `If-Match`, the settings with their checks, the `plug-change` job), and the solutions.

It sends each other operation to Prism when Prism runs (`npm run mock`).

The mock serves plain HTTP, the same as `care-pi serve --insecure-dev`.
Thus it uses the cookie `care_pi_session` without `Secure`. The agent on a device uses `__Host-care_pi_session`.

Start the mock and the dev server in 2 terminals:

```sh
npm run mock
npm run dev
```

Open `http://localhost:5173`. The mock starts in the scenario `unclaimed`.
To select a scenario, set `MOCK_SCENARIO` (`unclaimed`, `setup`, `review`, `ready`, or `restore`), or reset the mock.
`review` has each setup step done and the Care password in memory: the operator can start the setup on `/setup/review`.
`restore` is a claimed device with the USB backup disk of an old device (`care-usb1`): the wizard offers "Restore from backup".

The backups of the scenario `ready` have a switch: `MOCK_BACKUPS` (or the reset option `backups`).

| `backups`        | The device                                                                                    |
| ---------------- | --------------------------------------------------------------------------------------------- |
| `ok` (default)   | A confirmed recovery key, a USB disk (`usb`) and an S3 bucket (`b2`), both OK. No banner.     |
| `degraded`       | `b2` is degraded (no banner), `nas` (SFTP) does not answer for 26 hours: the banner of `nas`. |
| `unconfirmed`    | The recovery key is not confirmed: the banner `recovery-key-unconfirmed`.                     |
| `no-destination` | No destination: the banner `no-destination`.                                                  |
| `legacy`         | A Phase 2 host after the upgrade: no key, no destination, the local backups with the old key. |

```sh
MOCK_SCENARIO=ready MOCK_BACKUPS=degraded npm run mock:state
curl -X POST http://127.0.0.1:4011/__mock/reset -d '{"scenario":"ready","backups":"unconfirmed"}'
```

The backup of the old device (for "Restore from backup") is at the folder `/mnt/old-backup`, at the SFTP folder
`nas.lan:/volume1/care` (user `care`), in the S3 bucket `sunrise-care` (any keys), and on the disk `care-usb1` of the scenario
`restore`. `GET /__mock/state` gives its 24 words in `dr_words` (and the key of the device in `key_words`). After the restore, the
console password is `correct horse battery staple`. Other places of the destination test: an SFTP host with `down` in its name
does not answer; an S3 access key `bad` is refused; the buckets `sunrise-care` and `clinic-backups` exist (another bucket needs
"Make the bucket"); a folder below `/mnt/` or `/media/` is not on the root disk.

```sh
curl -X POST http://127.0.0.1:4011/__mock/reset -d '{"scenario":"ready","step_ms":200}'
```

A job of another device or of the command line (S67): `POST /__mock/start-job` starts an apply that holds the apply lock
(`slow` makes each step longer, `fail` the action of a step that fails, `kind` and `source` change the job). The banner of
each open page names it, and `/jobs` lists it.

```sh
curl -X POST http://127.0.0.1:4011/__mock/start-job -d '{"slow":40}'
```

The console password of the scenarios `setup`, `review`, and `ready` is `correct horse battery staple`.
Other reset options: `fail_step` (the action key of a step that fails one time, for example `ensure-stanza` or `fetch-db-repo`),
`ram_gib`, `disk`, `dev_build`, `force_reauth` (the next operation with a recent authentication asks for the password), and
`stale_auth` (the login is 10 minutes old, so each operation with `x-reauth` asks for the password). The setup job of the mock
has the steps of the agent on lumine (wave 4d).

The Phase 4 options (also `MOCK_UPDATES=<updates>` for `npm run mock:state`):

| Option        | Values                                                                                        | What it gives                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| ------------- | --------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `updates`     | `idle` (default), `available`, `staged`, `updated`, `recovered`, `agent-too-old`, `withdrawn` | `idle`: the device runs 2026.11.0 and a check finds 2026.11.1. `available`: the check is done. `staged`: 2026.11.1 is downloaded. `updated`: the device runs 2026.11.1, and a rollback to 2026.11.0 offers the PITR. `recovered` (S69): a power loss interrupted the update to 2026.11.1; the recovery rolled back, with the PITR offer. `agent-too-old`: 2026.11.1 needs agent v0.5.0 (the agent is v0.4.1). `withdrawn`: the running lock is withdrawn. |
| `window_open` | `false` (default), `true`, `"real"`                                                           | The update window: closed (`POST /updates/apply` without `now` gives `409 update-window-closed`), open, or computed from the clock (Sun 02:00 to 05:00, Asia/Kolkata).                                                                                                                                                                                                                                                                                    |
| `update_fail` | `checkpoint`, `migrate`, `verify`, `rollback`                                                 | The next update fails at this step and rolls back (`migration-failed` and the others, with `pitr_offer`). `rollback`: the rollback fails too; the maintenance page stays (`state: failed`).                                                                                                                                                                                                                                                               |
| `phase4`      | `true` (default), `false`                                                                     | `false`: each Phase 4 operation answers `501`, as an agent of Phase 2 or 3.                                                                                                                                                                                                                                                                                                                                                                               |
| `no_plugs`    | `true`                                                                                        | A solution without plugs (the empty state of the Plugs page).                                                                                                                                                                                                                                                                                                                                                                                             |

```sh
curl -X POST http://127.0.0.1:4011/__mock/reset -d '{"scenario":"ready","updates":"staged","window_open":true,"step_ms":400}'
```

The plugs are the plugs of `solutions/care-clinic/solution.yaml` and one external FE plug of the mock
(`care_token_display_fe`: a number with limits, a string with a pattern, and 2 advanced settings).
The cutover steps take longer (`migrate` 8 times `step_ms`), so the maintenance banner stays in view.

The Phase 6 options (`docs/console-ui.md` section 12.4):

| Option            | Values                                               | What it gives                                                                                                                                                                                                                                   |
| ----------------- | ---------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `connectivity`    | `fresh` (default), `migrated`, `public`, `tailscale` | `fresh`: a new device of Phase 6 (the root with name constraints, no switch). `migrated`: the root with name constraints and the one-time warning. `public`: the public name is on (issued). `tailscale`: the public name and Tailscale are on. |
| `wifi_device`     | `true` (default), `false`                            | `false`: the device has no Wi-Fi (`GET /network/wifi/scan` gives `409 wifi-no-device`).                                                                                                                                                         |
| `public_fail`     | `dns`                                                | The DNS service does not answer: the enrol job fails with `dns-service-unreachable`.                                                                                                                                                            |
| `tailscale_login` | `auto` (default), `manual`                           | `manual`: the login waits until `POST /__mock/tailscale-login`.                                                                                                                                                                                 |
| `wifi_fail`       | `auth`                                               | The next Wi-Fi job fails with `wifi-auth-failed`.                                                                                                                                                                                               |

```sh
curl -X POST http://127.0.0.1:4011/__mock/reset -d '{"scenario":"ready","connectivity":"migrated","tailscale_login":"manual"}'
```

The clock option (S52, also `MOCK_CLOCK=<clock>`): `clock` `ok` (default: NTP) or `untrusted` (no NTP). With `untrusted`,
the claim records `client_time` (`GET /__mock/state` has it in `clock`) and the clock comes from the browser; a claimed
scenario starts with the clock of the browser. The top bar then shows "Clock not trusted" until `POST /__mock/ntp-sync`.

```sh
curl -X POST http://127.0.0.1:4011/__mock/reset -d '{"scenario":"unclaimed","clock":"untrusted"}'
curl -X POST http://127.0.0.1:4011/__mock/ntp-sync
```

## 4. Work with the Go dev server

The agent has a dev listener for console work (`docs/console-server.md` section 9).
It serves plain HTTP on a loopback address. Never use it on a clinic device.

1. In the repository root, build the agent and start it with a dev root:

   ```sh
   go build -o bin/care-pi ./agent/cmd/care-pi
   bin/care-pi serve --root ./.dev-root --listen tcp://127.0.0.1:8443 --insecure-dev --fake-host
   ```

   The agent prints the claim code of the dev root. `--fake-host` gives plausible answers for `systemctl` and `podman`.

2. In `console/`, start the dev server with the agent as the API:

   ```sh
   CARE_PI_API=http://127.0.0.1:8443 npm run dev
   ```

3. Open `http://localhost:5173` and claim the device with the claim code.

The Vite proxy sets `Origin` to the origin of the agent, so the `Origin` check of the agent accepts the requests.
The agent also serves the console that it embeds at `http://127.0.0.1:8443/`. Run `make console-build`
and build the agent again to change that copy.

## 5. Layout of the source

| Path                     | Content                                                                                                                                                                                                                              |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `src/api/`               | The typed API client (`client.ts`), the problem types (`problem.ts`), the queries (`queries.ts`), and the generated types (`types.gen.ts`).                                                                                          |
| `src/app/`               | The router, the session guards, and the shell (top bar with the job banner, navigation, theme toggle).                                                                                                                               |
| `src/features/<page>/`   | 1 directory for each page: claim, login, setup, dashboard, certificate, logs, settings, security, support, backups (Backups and Restore), updates, plugs, jobs (S67), and the not-available state of a later phase (`unavailable/`). |
| `src/components/v2/`     | The v2 design system (S66): layouts, panels, the step list, the explainer, copy for support. `console/design.md` is its specification; `/__v2` (dev server) shows each component.                                                    |
| `src/components/ui/`     | careui components. Do not edit these files (section 6).                                                                                                                                                                              |
| `src/components/app/`    | Console components that use careui components.                                                                                                                                                                                       |
| `src/lib/strings.ts`     | Each text of the console, in ASD-STE100 Simplified Technical English.                                                                                                                                                                |
| `src/styles/careui.css`  | The careui design tokens. Do not edit this file.                                                                                                                                                                                     |
| `src/styles/console.css` | The few rules that the console adds.                                                                                                                                                                                                 |
| `public/`                | `favicon.svg` and `theme.js` (the theme before the first paint).                                                                                                                                                                     |
| `mock/`                  | The stateful mock of the agent.                                                                                                                                                                                                      |
| `tests/`                 | Unit tests.                                                                                                                                                                                                                          |
| `e2e/`                   | Playwright tests: desktop (Chromium, 1280 px), iPhone 13 (WebKit, 390 px), and 360 px (Chromium).                                                                                                                                    |
| `scripts/`               | `api-types.mjs`, `careui-add.mjs`, `clean-dist.mjs`.                                                                                                                                                                                 |

## 6. Add a careui component

```sh
npm run careui:add -- accordion
npm run careui:check
```

The script downloads `https://careui.ohc.network/registry/care-ui/<name>/<name>.json`, writes the files into
`src/components/ui/` (`src/hooks/` for hooks), and records each file with its sha256 in
`src/components/ui/careui-lock.json`. It prints the npm packages that the component needs. Install them with `npm install`.
The build has no request to the registry: the source is in the repository.
`docs/console-ui.md` section 3 tells more.

## 7. The e2e tests

`npm run test:e2e` builds the console and starts 2 servers: the mock (port 4711) and `vite preview` (port 4773).
`E2E_MOCK_PORT` and `E2E_PREVIEW_PORT` change the ports (for example when another worktree runs its tests).
The tests run one after the other, because each test resets the mock.

| File                          | What it checks                                                                                                                                                                                                  |
| ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `e2e/setup.spec.ts`           | Claim, recovery codes, each step of the easy path (the preset cards of S65), finish, SSE progress, hand-off, dashboard.                                                                                         |
| `e2e/jobs.spec.ts`            | S67: a job of another device in the banner, the Jobs page, the job page after a reload (log tail, then the stream after it), the transcript copy, the public progress of a second device.                       |
| `e2e/a11y.spec.ts`            | axe on each page in the light and the dark theme (no serious or critical violation), reduced motion, and the stability of the Jobs page.                                                                        |
| `e2e/login-dashboard.spec.ts` | Login with a deep link, the dashboard, layout stability (CLS and card boxes), theme, logout, lockout.                                                                                                           |
| `e2e/logs.spec.ts`            | Follow over SSE, the sticky bottom, pause, resume, the service chips, and "Copy the log" (S68).                                                                                                                 |
| `e2e/doctor.spec.ts`          | The health checks of the Support page, the failed ones first, and "Copy the result" (S68).                                                                                                                      |
| `e2e/certificate.spec.ts`     | The public certificate page: fingerprint, OS detection, tabs, downloads, QR code.                                                                                                                               |
| `e2e/settings.spec.ts`        | A config change with `If-Match`, the plan preview, and the apply job.                                                                                                                                           |
| `e2e/backups-setup.spec.ts`   | The step backups: the key ceremony, SFTP with the host key (TOFU), a USB disk with the typed label.                                                                                                             |
| `e2e/backups.spec.ts`         | The Backups page, its jobs, the banners (also on the dashboard), add S3, confirm a key, the layout.                                                                                                             |
| `e2e/restore.spec.ts`         | A point in time with the password check and its undo, files into a side directory.                                                                                                                              |
| `e2e/restore-dr.spec.ts`      | "Restore from backup" on a new device: key, destination, inspect, job, login as the old device.                                                                                                                 |
| `e2e/updates.spec.ts`         | Check, download, install (window closed: "Install anyway"), the job banner, the history, rollback and the PITR offer, a failed update, a power loss (S69), `agent-too-old`, the window editor, pin, the layout. |
| `e2e/plugs.spec.ts`           | A plug on with the plan preview and the job, the settings form with the errors of the agent, a cutover with the banner, the external bundle, the automatic plug, no plugs.                                      |

Each test also checks that each request of the page goes to the origin of the console.

`e2e/real` holds the journeys against a real device: claim and setup on a laptop or a phone, the phone journey on a
ready device, a resume after a reboot, a revoked session, and a missing job. `npm run test:e2e` ignores them.
`npm run test:e2e:real` runs them with `playwright.real.config.ts`. `e2e/real/README.md` lists the variables.
