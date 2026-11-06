# The console UI (design note)

This note tells how the console (`console/`) looks and behaves, and why.
It is for the persons who change the console and for the persons who change the agent that serves it.
The run instructions are in `console/README.md`. The API contract is `docs/api/openapi.yaml` and `docs/api/README.md`.
The server side (`care-pi serve`, the dev listener) is in `docs/console-server.md`.

## 1. Scope

The console is a single-page app (SPA). The agent embeds the build and serves it at `https://<host>:8443/` (S48).
It has these pages:

| Route | Page | Session |
|---|---|---|
| `/claim` | Claim the device: claim code or Imager password, console password, 8 recovery codes. The page shows the field of the first item of `claim_methods`. When the operator types a claim code (`XXXX-XXXX-XXXX`, capital letters and digits) in the Imager password field, the page shows the claim code field with the code. | no |
| `/login`, `/recover` | Login (with the TOTP step), recovery with a recovery code | no |
| `/certificate` | The root CA: fingerprint, downloads, steps for each OS, QR code | optional |
| `/setup`, `/setup/<step>` | The setup wizard (`GET /setup` gives the steps) | yes |
| `/setup/progress`, `/setup/done` | The setup job (SSE) and the hand-off card | yes |
| `/setup/restore` | "Restore from backup": a new or replaced device from the recovery key and 1 destination (section 10.5) | yes, state `claimed` or `setup` |
| `/dashboard` | `GET /status` each 10 s, with the backup banner row (section 10.6) | yes, state `ready` |
| `/backups` | `GET /backups` and `GET /backups/health` each 30 s: the local backups, the destinations, the recovery key, the schedule, verify and the drill (section 10.3) | yes, state `ready` |
| `/restore` | `GET /restore/points`: undo a mistake (point in time), files only, new device (section 10.4). `/restore?time=<RFC 3339 time>` (the PITR offer of an update, section 11.4) opens "Undo a mistake" with that time | yes, state `ready` |
| `/logs` | The journal of a unit, Follow over SSE | yes |
| `/settings` | Identity, size, environment, backups (`PUT /config`), FE build, solution (the custom refs, section 11.3), power | yes, state `ready` |
| `/security` | Password, recovery codes, TOTP, sessions | yes |
| `/support` | Support bundle, versions | yes |
| `/updates` | Phase 4: the running version, the new version, check, download, install, the track, the window, rollback, OS updates, the history (section 11) | yes, state `ready` |
| `/plugs` | Phase 4: the solution and its plugs: on or off with a plan preview, the settings form of each plug (section 11) | yes, state `ready` |
| `/jobs` | S67: the jobs of the device, the running jobs first, then the recent ones (section 13.3) | yes |
| `/jobs/:id` | S67: one job: the steps (live), the log, "Copy the transcript", Cancel. Without a session: the public progress (steps only, section 13.3) | optional |

Phase 2 exit (`plans/06-roadmap.md`): a non-technical tester completes setup from a phone and a laptop.
Phase 4 exit: the console shows a visible maintenance flow, and a plug switch in the console gives a working Care.
Each decision below serves these goals.

## 2. Design decisions

1. **Geist for text, Geist Mono for data (v2, S66; it was "Monospace first").** Geist is the font of headings and body;
   Geist Mono is the font of ids, hashes, logs, metrics, keys, code, and times (`src/styles/typography.css`).
   The font files are in `src/styles/fonts/` (latin subset; the build hashes them into `/assets/`; the OFL licence is
   `public/fonts/OFL.txt`), so the page loads no font from another origin. Section 13.
2. **careui tokens and components, tuned (v2, S66).** The console uses the Care UI design tokens and components
   (section 3). It does not make its own version of a careui component. `src/styles/tokens.css` tunes the careui semantic
   tokens (surfaces, text, borders, radii) for v2; it does not edit `careui.css`. Section 13.
3. **Light and dark theme.** The default is the theme of the system. Section 4 tells more.
4. **Stable layout.** A page does not move when data arrives or refreshes. Section 5 gives the rules.
5. **Mobile and desktop.** The console works at a width of 360 px or more (phone) and 1024 px or more (laptop).
   The navigation is a rail from 1024 px (`lg`) and a bottom bar below 1024 px (v2: up to 5 places, "More" opens a sheet;
   before the state is `ready` a Menu button opens the navigation).
6. **Keyboard.** Each control is a careui (Base UI) control with a visible focus ring.
   The top bar has a "Go to the content" link. Dialogs keep the focus inside.
7. **No request leaves the origin.** The console loads each file from its own origin and calls only `/api/v1` and `/_care-pi`.
   It has no analytics, no CDN, and no font service. The e2e tests check this for each test.
   One exception: the trust check of the certificate page (`docs/api/README.md` section 2.7) requests
   `https://<other name of this device>:8443/api/v1/health` with `mode: "no-cors"`. It runs only when the operator presses the button.
8. **Text in Simplified Technical English.** Each text is in `src/lib/strings.ts` (ASD-STE100: active voice, short sentences,
   numerals). A component never has a text literal. A later version can add a language: copy the object and translate it.
9. **The API types come from the contract.** `npm run api:types` makes `src/api/types.gen.ts` from `docs/api/openapi.yaml`.
   `npm run api:check` fails when the file is old. The client is `openapi-fetch`, so each path, parameter, and body has a type.
10. **The CSRF token is in memory only.** The console reads it from `GET /session` or from the response of claim, login, or recover.
    `src/api/client.ts` adds `X-CSRF-Token` and `Content-Type: application/json` to each unsafe request
    and sends `credentials: "include"`.
11. **401.** Only the problem type `not-authenticated` sends the operator to `/login?next=<path>`. After the login, the console opens the path again.
    `bad-credentials` and `totp-required` (also 401) stay on the page.
12. **Problems.** `ProblemAlert` shows each `application/problem+json` error: the console text of the type, the `detail` of the agent,
    the field errors, `missing_steps`, `failed_checks`, and a count-down from `Retry-After`. The code branches on `type`, never on `status`.
13. **Later phases.** A page of a later phase calls its operation. The agent answers `501 not-implemented-in-this-version`
    with `phase`. The page shows "Not available in this version" and the phase.
14. **Code split.** Each page is a lazy chunk (`src/app/router.tsx`). The libraries are in their own chunks
    (`react`, `router`, `query`, `base-ui`, `vendor`), so a change of the console does not change their hashes.
15. **Operator text for job steps.** A job step shows a sentence for its `action` key, never the internal name
    (section 9). The raw name is the tooltip of the row and is in the output.
16. **One byte format.** `formatBytes` (`src/lib/format.ts`) formats each size: binary units (`KiB`, `MiB`, `GiB`),
    1 decimal, no `.0` (128 GB → `119.2 GiB`). The agent uses the same rule in its texts (`agent/internal/units`),
    so the hardware facts and the host checks show the same size (wave 4c showed `119 GiB` and `128 GB`).
17. **A failed setup tells what to do.** The progress page shows the failed step in plain words, the `detail` of the
    problem, "Retry" (`POST /setup/finish` again, then the new job), "Copy details", and a link to the Logs page of
    the unit in the error (`care-<unit>`, else the agent). The raw error is in a collapsed "Technical details" block.
18. **The hardware step shows the problems first.** The failed checks come first. The passed checks are one
    collapsed row ("All 43 checks passed"), and in it the directory checks are one row ("Directories: 24 OK").
19. **Each change shows its effect before it runs (Phase 4).** An update says that Care stops and for how long; a plug
    switch shows the plan (`PUT /plugs/{id}?dry_run=true`) before the apply. Section 11.
20. **The maintenance state is always in view (Phase 4).** The top bar has a slot for the banner
    "Maintenance in progress: <step>". The slot is always there, so the banner moves nothing (section 11.6).
21. **Times of the update window are in the time zone of the device**, not of the browser (`UpdateWindowState.timezone`).
    The Updates page shows each time in that zone and names the zone with its offset (section 11.2).

## 3. careui

careui (`https://careui.ohc.network`) is the component registry of Care. It uses the shadcn registry format with Base UI.

- `scripts/careui-add.mjs` downloads `https://careui.ohc.network/registry/care-ui/<name>/<name>.json`
  and writes the source into `src/components/ui/` (`src/hooks/` for hooks). The source is in the repository:
  the build and the app make no request to the registry.
- `src/components/ui/careui-lock.json` records each item: the source URL, the npm dependencies, and the sha256 of each file.
  `npm run careui:check` (also part of `npm run lint`) fails when a file is not the same as its lock entry.
- `src/styles/careui.css` holds the careui design tokens (`--primary-*`, `--background`, the `dark` variant, and others).
  `npm run careui:add -- --tokens <careui>/src/index.css` writes it again. Do not edit it.
- ESLint and Prettier do not check `src/components/ui/` and the careui hooks, so the files stay the same as the registry.
- careui components import `clsx` and `tailwind-merge`. `vite.config.ts` points both names to the package `cn`,
  which does the same work with a smaller build.

The items in the lock (36): alert, alert-dialog, badge, button, card, checkbox, collapsible, dialog, dropdown-menu,
empty, field, input, input-group, input-otp, item, kbd, label, native-select, progress, radio-group, scroll-area, separator,
sheet, skeleton, slider, sonner, spinner, switch, table, tabs, textarea, toggle, toggle-group, tooltip, use-auto-space,
use-mobile. Phase 3 added `slider` (the time of a restore). Wave 6b added `item` (the rows of the update history).

### 3.1 Add a component

1. Look for the component in careui first: `https://careui.ohc.network`.
2. If careui has it, run `npm run careui:add -- <name>`. Install the npm packages that the script prints.
3. Run `npm run careui:check` and commit the new files and the lock file.
4. If careui does not have it, make a console component in `src/components/app/`. Build it from careui components and tokens.
   Do not copy a careui component to change it. To change a careui component, change it in careui, then run
   `npm run careui:add -- --overwrite <name>`.
5. Put each text of the component in `src/lib/strings.ts`.

## 4. Theme

- The theme is a class on `<html>`: `light` or `dark`. careui defines `dark` as `&:is(.dark *)`.
- `next-themes` keeps the choice (`system`, `light`, or `dark`) in `localStorage` under `care-pi.theme`. The default is `system`.
- The top bar has the theme menu (System, Light, Dark).
- `public/theme.js` sets the class before the first paint. Thus a page load shows no flash of the other theme.
  It is a file and not an inline script, so a `Content-Security-Policy` with `script-src 'self'` allows it.
  When you change the storage key, change it in `src/app/app.tsx` and in `public/theme.js`.
- Use the careui tokens (`bg-background`, `text-soft-foreground`, `border`, `bg-primary-*`). Do not use a fixed color for a surface.
  A fixed color needs a `dark:` value.

## 5. Layout stability

The operator must not see the page move. A moving page causes a wrong click on a phone.
Each page obeys these rules:

1. **Reserve the space.** A value that comes later has its final size before it comes:
   a skeleton of the same height, a `min-h-*`, or a fixed width (`w-[19ch]` for a memory value, `tabular-nums` for numbers).
2. **Final-size skeletons.** The skeleton of a card has the same rows as the card. The skeleton of the units table has 10 rows, the units of `GET /status` in the ready state.
3. **Update in place.** A refresh changes the text of a cell, never the structure. The dashboard reads `GET /status` each 10 s
   and keeps each card.
4. **Never reorder a list on refresh.** `useStableOrder` (`src/hooks/use-stable-order.ts`) keeps the first order:
   a known item keeps its place, a new item goes to the end.
5. **Append only.** Log lines and job steps go to the end of the list. The log viewer keeps the newest line in view
   while it follows (sticky bottom). When the operator scrolls up, the view stays and "Go to the end" shows.
   When the operator pauses, new lines wait and a counter shows them.
6. **No layout from the scroll bar.** `html { scrollbar-gutter: stable }` keeps the width when a page becomes taller than the window.
7. **Errors in reserved space.** An error alert goes above the content or in place of a skeleton. It does not push a control
   that the operator is about to touch.
8. **Movement without a known end.** A job step that runs without a progress value shows an indeterminate bar at the
   bottom of its row (`value={null}`; `src/styles/console.css` moves it, and stops it for `prefers-reduced-motion`).
   The bar is absolute, so it does not change the row height.

### 5.1 Phones (below 640 px, `sm`)

The console works from 360 px. Below 640 px:

| Item | Rule |
|---|---|
| Key/value rows (`DataList`, `DataRow`) | The label is above the value. The value is not cut: it wraps. Each row has a fixed minimum height (label 26 px, value 28 px); the skeleton has the same height. From 640 px the label and the value are side by side, and a long value is cut with its full text in `title`. |
| Units of the dashboard | A list of compact cards (`data-testid="units-cards"`): the unit (a link to its log), one badge (the health, else the state), a memory bar with its text, and the restarts. Each card has a fixed height (4.25 rem); the skeleton has the 10 cards of `GET /status`. From 640 px the table shows. |
| Job steps | A title can use 2 lines (`line-clamp-2`); each row has the same fixed height (4.75 rem, 3.5 rem from 640 px). |
| Long wizard steps | The hardware step keeps its buttons in a fixed bar at the bottom of the window, with a spacer after the content. From 640 px the bar is sticky at the bottom of the card (the card does not clip: `overflow-visible`). |
| Side scroll | Nothing scrolls sideways: not the page, not a card. The e2e tests check `document.scrollingElement.scrollWidth <= clientWidth` and the same for the units card, at 390 px and 360 px. |

The e2e test `e2e/login-dashboard.spec.ts` checks rules 1 to 3 in 2 ways:

- It holds `GET /status`, records the box of each card of the dashboard (skeletons), releases the request,
  and checks that each box has the same position and size.
- In Chromium, it adds the `layout-shift` entries of a `PerformanceObserver` over the load and 1 refresh.
  The sum (CLS) must be less than 0.01. The last run gave 0.0000 at 1280 px and 360 px.

`e2e/updates.spec.ts` checks the same for the Updates page: it holds `GET /updates`, `/updates/history`, `/updates/os`,
`/status`, and `/solutions/{name}`, records the 7 cards, releases, and compares (1280 px, 390 px, 360 px; CLS 0.0000).
The rules that make it pass:

- Each card has fixed rows (`DataList`), and a row that can wrap on a phone reserves its lines (the summary of the new
  version: 3 lines below 640 px).
- A line that only some states have keeps its place: the release notes link, the PITR note of the rollback card,
  the note about daylight saving time. The action of a card header (a badge) has a skeleton of the same size.
- The history list reserves 3 rows. More rows make it taller; it is the last card of the page.
- The copy button of a digest (`ShortId`) is 24 px with a negative margin, so a row keeps its height.
- The job card and the PITR offer card show only after an action of the operator, below the cards that the operator used.

## 6. API usage

| Topic | Rule |
|---|---|
| Base URL | `/api/v1` for the API, `/_care-pi` for the public status (`src/api/client.ts`). |
| Session | `GET /session` at start. `null` means "no session" (401 `not-authenticated`). |
| Setup | `GET /setup` gives `current_step`. The wizard opens it. The easy path skips `env`. |
| Jobs | `GET /jobs/{id}/events` over `EventSource`. After a reload, `?after=0` gives each event again. When the stream closes before `done`, the console reads the JSON form each 2 s. A 4xx problem other than `429` stops these reads (for example `404 job-not-found`): the problem does not change, so the console shows it. This rule uses the status class, not the type. No connection, `429`, and `5xx` give one more read after 2 s. |
| Logs | `GET /logs/{unit}?n=N` for the last N lines. Follow uses `?follow=true` (SSE). |
| Config | `GET /config?view=file` gives the `ETag`. `PUT /config?dry_run=true` with `If-Match` gives the plan preview. `PUT /config?apply=true` with the same `If-Match` starts the apply job. `412 config-changed` asks the operator to read the config again. |
| Reauthentication | `403 reauth-required` opens the password dialog. Then the console sends the request again. |
| Rate limits | `429` with `Retry-After`: the submit button stays disabled and the alert counts down. |
| Backups | `GET /backups` and `GET /backups/health` each 30 s on the Backups page. The dashboard reads `Status.backups` (the same object): no second request. A job of the page (`POST /backups/run`, `/verify`, `/drill`, `/restore`, `/backups/disks/{id}/prepare`, `/backups/recovery-key/rotate`) shows in a dialog or in its panel, and its end reads the backup queries again. |
| Destinations | `PATCH /config` (`application/merge-patch+json`) with `If-Match` and the full list `backups.destinations` (RFC 7396 replaces an array): first `dry_run=true` (the plan), then `apply=true`. An S3 destination: `PATCH` without apply, `PUT /backups/destinations/{id}/secrets`, then `POST /apply`. The keys are never in `config.yaml`. |
| Reauthentication of a read | `GET /backups/recovery-key` and `GET /backups/recovery-key/download` can also give `403 reauth-required`: the same password dialog, then the console reads again. |
| Updates (Phase 4) | `GET /updates` gives the whole Updates page; the page reads it each 30 s and at each step of its job. `GET /updates/history` (pages with `before`), `GET /updates/os`. Check, stage, apply: `POST` without reauthentication; rollback, OS install, OS reboot: `x-reauth` (the password dialog). |
| Track (Phase 4) | `PATCH /config` (`application/merge-patch+json`) with `solution.track` and `solution.pin` and `If-Match`, then `POST /updates/check`. The custom refs use `PUT /config` with `If-Match` in Settings, tab "Solution (advanced)". |
| Window (Phase 4) | `PUT /config?dry_run=true` with `If-Match` (the timers in the plan), then `PUT /config?apply=true`. |
| Plugs (Phase 4) | `PUT /plugs/{id}?dry_run=true` gives the plan and the ETag of `config.yaml`; `PUT /plugs/{id}?apply=true` with the same body and `If-Match` on that ETag starts the `plug-change` job. `412 config-changed` asks the operator to read the plugs again. |
| Maintenance (Phase 4) | The top bar reads `GET /_care-pi/status` (`maintenance`, `job`) each 15 s, and each 3 s while a job holds the apply lock or the page shows (12 to 20 public reads each minute; the limit is 60). |

## 7. What the server must do for the SPA

`agent/internal/server/spa.go` does these things. A different server (for example a test server) must do them too.

| Request | Response |
|---|---|
| `/assets/*` (a file that exists) | The file, `Cache-Control: public, max-age=31536000, immutable`. The file names have a content hash. |
| `/assets/*` (no file) | `404`. Never `index.html`: a missing chunk must fail, not run HTML as JavaScript. |
| `/favicon.svg`, `/theme.js` | The file, `Cache-Control: no-cache`. |
| Another path with a file extension and no file | `404`. |
| Each other `GET` or `HEAD` (for example `/dashboard`, `/setup/identity`, `/certificate?os=ios`) | `index.html` (the SPA fallback), `Cache-Control: no-store`, `Content-Type: text/html; charset=utf-8`. |
| `/api/v1/*`, `/_care-pi/*` | The API. Never `index.html`. |

Why the fallback is necessary: the router is in the browser (`createBrowserRouter`, base `/`).
A deep link or a page reload asks the server for a path such as `/dashboard`. Without the fallback, the server answers `404`.
A check with a plain static server (`python3 -m http.server` in `console/dist`) gave:

| Path | Plain static server | `care-pi serve --insecure-dev` |
|---|---|---|
| `/` | 200 | 200, `no-store` |
| `/dashboard` | **404** | 200 (`index.html`), `no-store` |
| `/setup/hardware` | **404** | 200 (`index.html`), `no-store` |
| `/assets/index-<hash>.js` | 200 | 200, `immutable` |
| `/assets/missing.js` | 404 | 404 |

Why `no-store` for `index.html`: it names the hashed chunks of the current build. After an agent update,
the browser must get the new `index.html`, else it asks for chunks that do not exist.

Other rules for the server:

- Serve the SPA only on the console origin (port 8443). Port 443 sends `/console*` to port 8443 (S48).
- Send no `Access-Control-*` header.
- A `Content-Security-Policy` is not necessary for Phase 2. When the agent adds one, the console works with:
  `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self';
  connect-src 'self' https:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`.
  `style-src` needs `'unsafe-inline'` because Base UI sets inline styles (positions of menus and dialogs).
  `connect-src` needs `https:` only for the trust check of the certificate page (section 2, item 7).

## 8. Tests

| Level | Tool | Scope |
|---|---|---|
| Unit | Vitest, Testing Library, jsdom | The API client (CSRF, 401, problems), recovery codes, the wizard logic, the job events, the env editor, the stable order, the formats. |
| e2e | Playwright against the stateful mock and `vite preview` | Desktop (Chromium, 1280 px), iPhone 13 (WebKit, 390 px), and 360 px (Chromium). Claim to ready, login and dashboard, logs, certificate, settings, lockout, theme. Each test checks that each request stays on the origin. |
| e2e (wave 4d) | Playwright against the mock | `e2e/progress-hardware.spec.ts`: the step text and the indeterminate bar of the lock resolution, a failed setup (mock `fail_step`) and its retry, the grouped host checks and the button in the window. `e2e/login-dashboard.spec.ts`: no side scroll, unit cards, and no cut label or value below 640 px. |
| e2e (Phase 3) | Playwright against the mock | `e2e/backups-setup.spec.ts`: the key ceremony (a wrong word, then 4 letters), SFTP with the host key and the key of the device, a USB disk with the typed label and the password check. `e2e/backups.spec.ts`: the Backups page and its jobs, a destination that does not answer (here and on the dashboard), an S3 bucket from "Add a destination" (the keys never in `config.yaml`), the confirmation of a key from the page, the card boxes before and after the data, Settings > Backups. `e2e/restore.spec.ts`: a point in time with the password check, the 14 steps and their text, the undo; files into a side directory. `e2e/restore-dr.spec.ts`: claim, the entry of the wizard, the key as QR text and as the `.txt` file, inspect, the job, the login as the old device; a wrong key; SFTP host key; a failed job. |
| e2e on a device | Playwright against a real device (`e2e/real`, `playwright.real.config.ts`) | Desktop (Chromium, 1280 × 800) and iPhone 13 (WebKit). Claim to ready on a new install, the phone journey (dashboard, certificate, logs, TOTP), a resume after a reboot. The Phase 2 exit test on lumine used them. |
| Unit (wave 6b) | Vitest | `tests/updates-window.test.ts` (window text, length, time zone, offset, daylight saving time), `tests/settings-form.test.ts` and `tests/plug-settings-form.test.tsx` (form from the schema, validation, the body with only the changes, `errors[]` to fields), `tests/updates-text.test.ts` (track, version, result, state badges; PITR link), `tests/step-text.test.ts` (a sentence for each `JobStep.action` key and each `JobKind` of the contract). |
| e2e (wave 6b) | Playwright against the mock | `e2e/updates.spec.ts`: check → download → install (window closed → "Install anyway"), the banner shows the step and clears, the history row; rollback with the password and the PITR offer → `/restore?time=…`; a failed update that rolls back; `agent-too-old`; the dashboard summary and maintenance; the window editor; pin a version; the layout of the Updates page; a device without Phase 4; a withdrawn version. `e2e/plugs.spec.ts`: a frontend plug on (cancel, then plan → apply), Filly with the errors of the agent at the fields and the cutover with the banner, the external bundle, the automatic plug, the advanced settings, no plugs. |
| e2e on a device (Phase 4) | `e2e/real/updates.spec.ts` | Opt-in (`UPDATES_TEST=1`; `e2e/real/README.md`): the Updates page in place, a check, an update with the banner and the public status, a rollback with the PITR offer, a plug on and off. |

The mock implements the contract for the console (`console/README.md` section 3). When the contract changes,
change the mock, run `npm run api:types`, and run the tests.

## 9. Job steps

`src/lib/strings.ts` (`strings.steps.actions`) maps each `JobStep.action` key of the contract (`PlanActionKind` and the
other keys of `JobStep.action`) to an operator sentence. `src/features/jobs/step-text.ts` adds a short form of the
argument of the step name. A key that is not in the map shows the raw step name in a `code` element.
`tests/step-text.test.ts` fails when the contract has a key without a sentence.

| Key | Sentence | Second line (when the step does not run) |
|---|---|---|
| `validate` | Check the settings | `config.yaml` |
| `host-check` | Check the device | `required checks` |
| `resolve-lock` | Resolve the versions of the images (while it runs: "… (about 30 s)") | while it runs: the last `resolve …` line |
| `render` | Make the configuration files | the argument |
| `plan` | Find the changes | the argument |
| `ensure-dirs` | Make the directories | — |
| `set-secrets` | Store the secret keys | the argument |
| `write-files` | Write the configuration files | `15 files` |
| `delete-files` | Delete the old configuration files | `2 files` |
| `daemon-reload` | Load the new service files | — |
| `enable-target` | Start Care at each boot | `care-start` |
| `pull-images` | Download the container images | `caddy:2, care@sha256:77fe743e51a6` (`repo:tag` or `repo@sha256:<12 hex>`) |
| `full-cycle` | Stop and start all the services of Care | the reason, for example `pod changed` |
| `start-target` | Start all the services of Care | the reason |
| `restart` | Start the changed services again | `care-beat, care-worker, care-api` |
| `reload-caddy` | Load the new web server settings | — |
| `ensure-buckets` | Make the file storage | — |
| `ensure-stanza` | Prepare the database backups | — |
| `ensure-fe` | Build the Care web app | the argument |
| `write-lock` | Record the installed versions | — |
| `wait-healthy` | Wait until Care answers | `care-api` |
| `set-hostname` | Set the host name | `care → lumine` |
| `create-care-superuser` | Make the Care administrator | the user name (the detail) |
| `collect` | Collect the support data | the argument |
| `archive` | Make the support bundle file | the argument |
| `prune` | Delete the old builds of the web app (in `backup-verify`: "Remove the data of the old copies") | the argument |

Phase 3 keys (`docs/api/README.md` section 13.8). The second line is the argument, except where the table tells it.

| Key | Sentence | Key | Sentence |
|---|---|---|---|
| `ensure-mounts` | Prepare the backup disks | `ensure-repositories` | Prepare the backup destinations |
| `ensure-timers` | Set the backup schedule | `pause-backups` | Stop the scheduled backups |
| `resume-backups` | Start the scheduled backups again | `maintenance-on` | Show the maintenance page |
| `maintenance-off` | Remove the maintenance page | `stop-app` | Stop Care |
| `start-app` | Start Care | `stop-db` | Stop the database |
| `start-db` | Start the database | `wait-promote` | Wait until the database accepts changes |
| `checkpoint` (in `restore-pitr`) | Record an undo point | `backup-incr` | Make an incremental backup |
| `backup-full` | Make a full backup | `check-stanza` | Check the database backups |
| `snapshot` | Copy the backup to the destination (`usb: database`, `usb: files`) | `forget` | Remove the old copies |
| `verify-local` | Verify the backups on this device | `verify-destination` | Verify the copy at the destination |
| `check-space` | Check the free disk space | `check-key` | Check the recovery key |
| `check-destinations` | Check the backup destinations | `restore-db` | Restore the database (the local time of the target) |
| `restore-files` | Restore the files | `verify-files` | Verify the restored files |
| `stop-s3` | Stop the file storage | `start-s3` | Start the file storage |
| `fetch-config` | Get the settings from the backup | `fetch-db-repo` | Get the database backup from the destination |
| `fetch-files` | Get the files from the backup | `restore-account` | Restore the console account of the old device |
| `finish` | Complete the restore | `check-disk` | Check the disk |
| `wipe-disk` | Erase the disk | `partition` | Make the partition |
| `format` | Make the file system | `write-mount-unit` | Write the mount settings |
| `mount` | Mount the disk | `rekey-local` | Make the local backups with the new key |
| `init-destination` | Prepare a new copy at the destination | `activate-key` | Use the new recovery key |
| `remove-old-repositories` | Delete the old backups | `drill-restore` | Restore the latest backup into a test folder |
| `drill-start` | Start a test database | `drill-check` | Check the test database |
| `drill-cleanup` | Delete the test database | | |

`strings.jobs.kinds` has a sentence for each `JobKind` (for example `restore-pitr`: "Restore the database to a point in time").
`tests/step-text.test.ts` also fails when the contract has a kind without a sentence.

The expected times of the backup steps come from spike SP6 on a Pi 5 with a 46 MB database (`plans/08-spike-log.md`):
`checkpoint` and `backup-incr` 6 s, `backup-full` 4 s, `restore-db` 8 s, `wait-promote` 5 s, `verify-local` 1 s,
`drill-restore` 8 s, `drill-start` 5 s, `stop-app` and `start-app` 10 s, and `wait-healthy` 35 s after a restore
(a cold start of the stack took 44 s). They grow with the size of the database.

The keys of Phase 4 (`docs/solutions.md` sections 8 and 11). The maintenance page of Caddy shows the same sentence
for `maintenance.step` (`docs/api/README.md` section 14.3), and so does the banner of the top bar:

| Key | Sentence | Second line (when the step does not run) |
|---|---|---|
| `cutover` (plan action) | Install the new version behind the maintenance page | the argument |
| `plug-config` (plan action and step) | Send the plug settings to Care | `care_onboarding_fe` |
| `fetch-index` | Download the list of versions | the URL of `index.json` |
| `fetch-lock` | Download the description of the version | `care-clinic 2026.11.1` |
| `resolve-refs` | Resolve the custom versions | the argument |
| `stage-pull` | Download the new container images | `2 image(s)` |
| `stage-bundles` | Download the web app and the plug files | the argument |
| `stage-build` | Build the custom parts on this device | the argument |
| `cutover-maintenance-on` | Show the maintenance page | — |
| `stop` | Stop Care | the units |
| `checkpoint` | Make a backup point of the database (while it runs: "… (up to 1 min)"; in `restore-pitr`: "Record an undo point", `strings.steps.byKind`) | the backup label |
| `activate` | Install the new version | the argument |
| `migrate` | Update the database (while it runs: "… (this can take some minutes)") | the argument |
| `start` | Start Care | the units |
| `verify` | Make sure that Care works | the checks |
| `maintenance-off` | Remove the maintenance page | — |
| `rollback` | Go back to the previous version | the previous lock |
| `prune-images` | Delete the old container images | the argument |
| `os-refresh` | Read the list of OS packages | — |
| `os-unhold` | Release the tested container packages | the packages |
| `os-upgrade` | Install the OS updates | the packages |
| `os-hold` | Hold the container packages again | the packages |
| `reboot` | Restart the device | — |
| `ensure-ca` (plan action) | Make the root certificate of the device | — |
| `disable-units` (plan action) | Stop the DNS address timer | the units |
| `reload-firewall` (plan action) | Load the new firewall rules | — |
| `update-dns` (plan action) | Send the new address to the DNS service | — |
| `write-config` | Save the settings | the keys |
| `dns-register` | Register the device with the DNS service | the hint |
| `dns-address` | Send the address to the DNS service | — |
| `dns-delete` | Delete the public name | the id |
| `wait-certificate` | Wait for the certificate of the public name (while it runs: "… (about 1 min)") | the name |
| `tailscale-install` | Install Tailscale (while it runs: "… (about 1 min)") | — |
| `tailscale-up` | Connect to the tailnet | — |
| `tailscale-login` | Wait for the login to Tailscale (while it runs: "… (open the login link)") | — |
| `tailscale-down` | Disconnect from the tailnet | — |
| `tailscale-logout` | Remove the device from the tailnet | — |
| `wifi-radio` | Turn on the Wi-Fi radio | — |
| `wifi-profile` | Write the Wi-Fi settings | the SSID |
| `wifi-connect` | Connect to the Wi-Fi network | the SSID |
| `wifi-verify` | Check the network routes | — |
| `wifi-remove` | Remove the Wi-Fi network | — |

The second line of a step is, in this order: its `detail` (a result or an error); while it runs, the message of its
progress, the expected time ("Expected: about 3 min 40 s", `job-state.ts`), or its last output line; else the short
argument above.

Each `JobKind` has a name for the operator (`strings.jobs.kinds`; the dashboard and the job card of the Updates page use it):
`update-check` "Check for updates", `update-stage` "Download an update", `update-apply` "Install an update",
`update-rollback` "Go back to the previous version", `os-update` "Install the OS updates", `plug-change` "Change a plug",
`tls-public-enrol` "Change the public name", `tls-tailscale-enrol` "Change Tailscale", `network-wifi` "Change the Wi-Fi",
and the kinds of Phases 1 to 3. `tests/step-text.test.ts` fails when the contract has a kind without a name.

## 10. Backups and restore (Phase 3)

The contract is `docs/api/README.md` section 13. The plan is `plans/02-backups-and-restore.md` (section 7: what the operator sees).

### 10.1 The recovery key ceremony

`src/features/backups/recovery-key-ceremony.tsx`. The step backups of the wizard starts with it; the Backups page uses it for a key
that is new or not confirmed, and for the new key of a rotation (`which: "pending"`).

1. "Make the recovery key" (`POST /backups/recovery-key`). The response has the words one time.
2. The 24 words in a numbered grid, in monospace, down each column (2 columns below 640 px, 3 from 640 px: the same order
   as the `.txt` file of the agent), the QR code of `qr_payload` (`CARE-PI-RK1:` and the words in capital letters, made in the
   browser), Print (only the sheet: the class `print-area`), and Download (.txt) (`GET /backups/recovery-key/download`, recent
   authentication; the file name comes from `Content-Disposition`). The page has no copy button for the words.
3. "I saved the recovery key" (a check box), then Continue: the words hide.
4. The 3 words at `confirm_positions` (`POST /backups/recovery-key/confirm`). The console accepts a word of the list or its first
   4 letters, and sends the full word. A word that is not in the list gives a field error before the request; `422
   recovery-key-mismatch` puts "This is not word N of the key." on the field of `errors[].pointer`.
5. "Show the words again": from the memory of the page, or `GET /backups/recovery-key` (recent authentication) after a reload.

The words stay in the state of the component only. They go away when the operator leaves the page. A reminder ("The recovery key
is not confirmed …") stays on the step, on the key card, and as the banner `recovery-key-unconfirmed` until the confirmation.
The confirmation is a `div` with a button, not a `form`: the step is a form, and a form must not contain a form (a nested form
reloaded the page in Chromium).

### 10.2 Destinations: the 3 choices and the test

"Where should backups go?" has 3 big choices (USB disk, Network folder (SFTP), Cloud bucket (S3)) and "Add another destination".
Each choice opens `DestinationForm` (`src/features/backups/destination-form.tsx`): the name (the id), the fields, the test
(`POST /backups/destinations/test`), the interval and the retention with the defaults (15 min; 14 days, 14 daily, 8 weekly,
6 monthly) and the RPO that they give ("Data loss at most 20 min"). "Add this destination" needs a passed test
(`next_action: none`). A value that is the default stays out of `config.yaml`.

- **USB disk**: `GET /backups/disks`. The system disk shows with "This is the system disk. You cannot use it."; a disk with
  care-pi backups shows "Use this disk"; a mounted disk shows why it cannot be used; another disk shows "Prepare this disk". The
  prepare asks the operator to type the new label (for example `care-usb1`) in an alert dialog that tells the model and the size
  ("Preparing the disk deletes everything …"), then the password (recent authentication), then shows the job `disk-prepare`, then
  tests the disk. A folder (advanced) is a tab: the test warns when the folder is on the root disk (the check `root-disk`).
- **SFTP (TOFU, section 13.6)**: Test → the key type and the SHA-256 fingerprint, with the text to compare them with the server
  (`ssh-keygen -lf`) → "Trust this server" (the same test with `accept_fingerprint`) → when the server refuses the key of the
  device: the `authorized_keys` line of `GET /backups/ssh-key` with a copy button, a hint for a NAS and the command for a server
  → "Test again" → passed. `422 destination-untrusted-hostkey` (a changed key) shows the new key in a red panel with "Trust the new
  key only when you know why it changed". The destination gets `host_key` (the full public key of the test).
- **S3**: endpoint, region, bucket, prefix, access key id, secret access key. `next_action: create-bucket` shows "The bucket … does
  not exist. The device can make it." and "Make the bucket" (the same test with `create_bucket: true`). The keys go to the step
  (`credentials`) or to `PUT /backups/destinations/{id}/secrets`, never to `config.yaml`.

The step backups writes the destinations with `PUT /setup/steps/backups` (`recovery_key_words: null`: the ceremony confirmed the
key before). Continue needs a confirmed key and 1 destination or more. "Skip this step" shows only in a dev build.

### 10.3 The Backups page

`src/features/backups/backups-page.tsx`, from top to bottom: the banner row (section 10.6), the row of the running job ("Runs now:
Back up" and "Show the progress"), "This device" (state, restore window, last full and incremental backup, last change archived,
size, free space, key; Back up now = the checkpoint `scope: local-db, type: incr`, Full backup now), the recovery key (state, key
id, Show the recovery key with the password check, Make a new key = the rotation, Change the key of the local backups for a Phase 2
host), the destinations (one card each: type icon, state badge OK / Degraded / Unreachable / Waits, last database and file copy,
size, free space, next copy, data loss at most, a reserved line for the last error, Back up now, Verify, Change, Remove; "Add a
destination"), the schedule and the retention (the timers in plain words: "Each Sunday at 01:00"; the retention; "How much data
can you lose?": `archive_timeout` on this device, interval + `archive_timeout` off-site; "Change the schedule" opens
Settings > Backups), verify and the test restore (last and next; "Verify now", "Test restore (drill)"), and the SSH key of the device.

Layout: each card has a fixed set of rows; the skeleton of the destinations has the number of destinations of the last visit
(`localStorage` `care-pi.backups.destinations`, 2 the first time). The button rows and the RPO and retention texts render
invisible with the default values before the data, so they wrap the same way (monospace). A value below 640 px is short (the
relative time only, the window without the year), so it does not wrap. `e2e/backups.spec.ts` checks the boxes at 1280, 390, and
360 px. A job opens in a dialog, so the page does not move. The ages ("5 min ago", "for 1 d 2 h") use `generated_at` of the
report as "now" (the clock of the device), so a wrong clock of the browser does not change them.

Change and Remove of a destination: Change edits the interval, the retention, and new S3 keys; to change the place, add a destination
and remove the old one. Remove asks first; the data at the destination stays.

Settings > Backups (`src/features/settings/backups-tab.tsx`) edits `backups.*` with the flow of section 6 (plan preview, then
apply): the archive timeout (with the RPO that it gives), the 5 calendars (systemd calendar text), the test restore on or off, the
full backups to keep, the daily and weekly copies, the hours before a problem, and the pause.

### 10.4 The Restore page

`src/features/backups/restore-page.tsx`: 3 entry cards (`?kind=pitr|files|device`), then the panel of the kind.

- **Undo a mistake** (`kind: pitr`): the earliest time and "Now" (a restore job archives the current WAL first, so the window
  ends at the start of the job), a slider (careui `slider`, 1-minute steps) and an `<input type="datetime-local">` with `min` and
  `max` (local time of the browser; its time zone shows). The nearest files snapshot shows ("Nearest file backup: 02:30. The files
  can differ by up to 7 h 10 min."), then a red alert: "Records after HH:MM will be lost.", "Care stops for about 1 minute", the
  SP6 times, and the undo point. "Restore to HH:MM" → a confirm dialog → the password → the job in the panel with the 14 steps.
  `?time=<RFC 3339 time>` (the PITR offer of an update, section 11.4) selects this kind and fills in that time.
- **Undo the last restore**: the newest checkpoint of a `restore-pitr` job of the last 24 hours (`RestorePoints.checkpoints`)
  shows as a card above the panel, with "Undo this restore" (`checkpoint: <label>`).
- **Files only** (`kind: files`): a files snapshot of a destination (newest first), into a separate folder (the default) or into
  the storage of Care, optional paths below a bucket → confirm → the password → the job → the result with the side directory path
  and a copy button.
- **New or replaced device**: on a ready device, the text tells that it is for the wizard of a new device.

### 10.5 The wizard branch "Restore from backup"

`src/features/setup/restore-dr-page.tsx` at `/setup/restore`. The first page of the wizard (`recovery-codes`) shows the card "Do you
replace a device?" with "I have a backup of another device" when `SetupState.restore.available`.

1. The key: one text field for 24 words. It accepts words with spaces, commas, or lines; the QR text (`CARE-PI-RK1:…`); and the
   `.txt` file of the agent (the numbered words in 3 columns: the numbers give the order). It shows "N of 24 words", the words that
   are not in the BIP-39 list (`src/lib/bip39-english.ts`), and a wrong checksum (SHA-256 of Web Crypto; the agent checks again).
2. The destination: the same 3 choices. USB lists only the disks with care-pi backups (and a folder tab); SFTP has the same host
   key panel (from the problem `destination-untrusted-hostkey` of inspect); S3 has the keys.
3. "Read the backup" (`POST /restore/dr/inspect`): the clinic, the device name, the version, the last database and file backups,
   the database window, the sizes, the free space, and the warnings. Then the note on the console account, "Do not send backups
   from this device" (`pause_backups`), and "Restore this device" → confirm → `POST /restore/dr` → the job.
4. Success: the job ends each session. The console keeps the clinic name and `console_url` in `sessionStorage`
   (`src/features/setup/dr-handoff.ts`) and opens the login page: "This device now runs <name>. Log in with the console password
   of the old device." (and the new address when the host name of the old device is another name). Failure: the form again with
   the problem of the job; the account of the claim stays.

`/setup/progress` and the wizard send a `restore-dr` job (state `applying`) to `/setup/restore`, which follows it after a reload.

### 10.6 The banner row (dashboard and Backups page)

`BackupHealthRow` (`src/features/backups/backup-banners.tsx`) is one alert with a fixed height (4.75 rem, 3.5 rem from 640 px)
between the page title and the first card. Its skeleton has the same height. It shows the worst banner of `BackupsHealth.banners`
(error, then warning, then info) in the console text of its code, and "N more"; or "The backups are OK. Last copy off this device
5 min ago." The dashboard links to the Backups page; on the Backups page "N more" opens the list of each banner below the row (on a
click only), and each banner links to its fix (`/backups?add=1`, `#recovery-key`, `#destination-<id>`). An agent of Phase 2
(`Status.backups` is `null`) gives "This version has no backups." in the same space.

| Code | Text |
|---|---|
| `no-destination` | No backup destination. The backups stay on this device only. |
| `no-recovery-key` | This device has no recovery key. Make the recovery key. |
| `recovery-key-unconfirmed` | The recovery key is not confirmed. Without the key, you cannot restore the backups on a new device. |
| `destination-unreachable` | `<id>`: no successful backup since `<since>`. |
| `destination-low-space` | `<id>`: the free space is low. |
| `local-disk-low` | The disk of this device has little free space for the backups. |
| `local-backup-failed` | The last scheduled backup on this device failed. |
| `wal-archive-failing` | The archive of the database changes fails. New changes are not protected. |
| `verify-failed` | The last verify found damage in a backup. |
| `drill-failed` | The last test restore failed. |
| `backups-paused` | The scheduled backups are paused. |
| `local-repository-legacy-key` | The backups on this device use an old random key. Change it to the recovery key. |

A code of a later version shows the `message` of the agent.
## 11. Updates, solution, and plugs (Phase 4)

The contract is `docs/api/README.md` section 14; the model is `docs/solutions.md`. The code is in
`src/features/updates/` and `src/features/plugs/`. The PITR offer links to the Restore page of section 10.4.

### 11.1 The Updates page

One read, `GET /updates`, each 30 s and at each step of the job of the page; `GET /status` adds the care image,
the care_fe sha, and the agent (`GET /updates` has the components only with a target). The cards, in this order:

| Card | Content |
|---|---|
| Running version | Solution, version with the track badge (stable green, pinned blue, custom orange "Not supported"), installed time, care image and care_fe (short, monospace, copy), agent, release state. A withdrawn running lock gives a warning with the reason. |
| New version | Newest version, release date, summary, state ("Up to date", "Available. Not downloaded.", "Downloaded. Ready to install.", "Blocked: …", the running states), downtime, last check, the release notes link, "What changes" (components), the problem of the last check. `blocked_by: agent-too-old` gives the alert "Update the agent first" with the versions and "run the installer again". Buttons: Check now, Download the update (stage; "The downloads happen while Care keeps running."), Install now. |
| Job | The job that the page started, else `job` of `GET /updates` (a timer, the CLI, another tab): the steps over SSE, the error of a failed job, "Stop the job" for `update-stage`, "Close". |
| PITR offer | Section 11.4. |
| Update track | Section 11.3. |
| Update window | Section 11.2. |
| Previous version | `rollback.to_version` (or the time of the lock file), the images on disk, the note that a rollback does not undo the migrations, "Roll back" (confirmation, then the password dialog). |
| OS updates | `GET /updates/os`: the OS, security updates, the packages to install, the held packages (a table), reboot needed, scheduled reboot, last full upgrade. "Install now" (`now: true`) and "Reboot now" (both with the password dialog). The reboot confirmation names the window and offers "Reboot in the window" (`now: false`). |
| History | `GET /updates/history`, newest first: kind and versions (from → to, or the plug), the time, the length, the downtime, the source, the result badge, a rollback mark, "Restore offered". "Show older entries" adds the next page at the end. |

"Install now" opens a confirmation ("Care stops for a few minutes. Staff see a maintenance page. …") and sends
`now: false`. Outside the window the agent answers `409 update-window-closed` with `next_window_at`; the same dialog then
says when the window opens and offers "Install anyway" (`now: true`).

### 11.2 The window editor

- The card shows the window in words ("Sun, 02:00 to 05:00 (3 h)", "Sat, Sun, 23:00 to 02:00 the next day (3 h)",
  "Wed, from 01:00 for 24 h"), open or closed, the time zone of the device with its offset ("Asia/Kolkata (UTC+05:30)"),
  the next opening (or the closing time), the next check, the next automatic install, and automatic updates on or off.
- The note about daylight saving time comes from the zone: "Asia/Kolkata has no daylight saving time.", or the rule of
  `docs/api/README.md` section 14.4 (the window is 1 hour shorter or longer; a time in the skipped hour moves 1 hour later).
  The console finds it with `Intl` (the offset in January and in July).
- "Change the window" opens the editor: the days (a toggle group), the opening and the closing time (`type=time`),
  a preview of the new window in words, and "Install updates automatically". The change goes through the config flow
  of Settings (plan preview, then the apply job), because the timers change.
- `src/features/updates/window.ts` has the logic; `tests/updates-window.test.ts` tests it.

### 11.3 The track, and the custom versions

The card has the 3 tracks with a sentence each. "Pinned" shows the versions of `GET /solutions/{name}` (a withdrawn
version or a version for a newer agent is disabled). "Save and check for updates" sends `PATCH /config` with
`If-Match`, then `POST /updates/check`. "Custom" is not supported (03 section 3.2): the card does not edit the refs. It
links to Settings, tab "Solution (advanced)", which edits `solution.custom.care_ref` and `care_fe_ref` with the config
flow (`PUT /config` with `If-Match`, plan preview, apply) and sets the track to custom.

### 11.4 The PITR offer and the link to the Restore page

A rollback does not undo migrations (`docs/api/README.md` section 14.5). The page shows the card
"Restore the database to HH:MM (before the update)?" when the job that it followed has a `pitr_offer` (the result of
`update-rollback`, or `error.pitr_offer` of a failed update), else when `last_result.pitr_offer` of `GET /updates` has one.
The time is `target_time` in the time zone of the device. "Not now" hides the offer for the session (`sessionStorage`).

The link contract with the Restore page (section 10.4):

- The link is `/restore?time=<target_time>`: an RFC 3339 time in UTC, URL-encoded (`/restore?time=2026-11-22T02%3A05%3A40Z`).
- The Restore page opens "Undo a mistake" (`?kind=pitr`) with that time in the time field and the slider, shows the
  note "An update offered a restore of the database to … (in UTC: …)" (`data-testid="restore-offered"`) and a link back to
  the Updates page, and shows what is lost. The start asks for the password (reauthentication) and sends `POST /restore`
  with `{kind: pitr, time: <target_time>}` (the body `pitr_offer.restore`). The console never starts a restore by itself.
- A time outside the restore window shows the error of the field ("The time is outside the restore window") and no start.
- `restoreOffer()` of `src/features/backups/backups-format.ts` reads the parameter; `tests/backups-logic.test.ts` tests it.

### 11.5 The Solution and plugs page

`GET /plugs` and `GET /solutions/{name}`. The solution card: name, title, description, version, track, the number of plugs,
and "Some changes are not applied." Each plug: label and id, kind (Backend, Frontend, Backend and frontend), source
("Solution default", "Your choice", "Automatic", "Custom"), "Not applied", "Needs internet", the parts (package, ref with
"Tracked" or "Pinned", commit (short, copy), in the Care image; bundle state with its size, the `plug_config` row), and
for an external bundle "The browser loads the bundle from <url>. It is not pinned."

- **The switch.** It moves at once (optimistic) and sends `PUT /plugs/{id}?dry_run=true`. The plan dialog says what the
  change does: `plug-config` "This changes the frontend plug list. Care keeps running. …"; `restart` "This restarts the
  Care services (api, worker, beat) behind the maintenance page. Care stops for about 20 s." (S62); `cutover` "This restarts Care behind the maintenance
  page. The device makes a backup point of the database first. Care stops for about 2 min."; `build-and-cutover` the build
  first. It lists the downloads, the restarts, the steps (operator sentences), and the warnings. Cancel puts the switch
  back. Apply sends `?apply=true` with `If-Match` on the ETag of the dry run, then the card shows the job.
- **Automatic plugs.** `auto: when_multihost` shows "Automatic" and no switch, with the reason and the number of host names.
- **The settings form.** `src/features/plugs/settings-form.ts` makes the fields from `GET /plugs/{id}/settings-schema`
  and `Plug.settings` (the order, the values, `advanced`, the placeholders):

  | `type` | Control | Checks before the request |
  |---|---|---|
  | `boolean` | switch | — |
  | `string` | text field | `pattern` |
  | `number` | number field with `min` and `max` | a number, `minimum`, `maximum` |
  | `url` | text field, `type=url` | `http://` or `https://` |
  | `secret` | "A value is set" with "Change" and "Remove", else a password field. Never a value. | `pattern` |
  | `list` | text area, one entry for each line | `pattern` for each line |
  | `choices` | check boxes | — |

  A required field needs a value only when the plug is on. The advanced fields are under "Advanced settings". A field
  with an operator value has "Changed" and "Use the default" (sends `null`). "Show the effect" sends only the changed
  keys. `422 plug-settings-invalid` puts each `errors[]` item at its field (`/settings/<part>/<key>`, RFC 6901), with the
  text of the console for the `code`. A plug that is turned on without its required settings gets this `422`: the switch
  goes back, the form opens with the errors, and its button becomes "Turn on with these settings".

### 11.6 The maintenance banner

`maintenance` of `GET /_care-pi/status` (the member that the maintenance page of Caddy reads) or `job` of the same read
(S67, contract 0.9.0: each job that holds the apply lock, from any tab, device, timer, or the CLI): "Maintenance in progress
· step X of Y · <operator text of the step> · Open", a link to `/jobs/<id>` (to the Updates page when the status has no job
id). Y is `job.step_count`, the planned step count of the job; a job without a plan (`step_count: null`) shows "step X"
without a total. The banner reads the public status each 15 s, each 3 s while a job runs, so it shows on each open page of each device. From 640 px it is in a slot of the top bar between the
state and the buttons; the slot is always there. Below 640 px it takes the place of the device name and of the state badge,
on 2 lines ("Maintenance in progress" and the step). The top bar keeps its height (v2: 56 px, 64 px from 1024 px; the
banner slot is 40 px high, `--banner-height`). The dashboard shows the same
text first in its problems (`Status.maintenance`), and its state is "Maintenance".

### 11.6.1 The clock banner (S52)

`clock` of `GET /_care-pi/status` (`docs/api/README.md` section 16): while `clock.trusted` is `false`, the top bar shows
"Clock not trusted" and the reason of `clock.source` (`browser`: the clock came from the browser at the claim; `rtc`; `none`)
in the slot of the maintenance banner. The maintenance banner wins the slot while it shows. Below 640 px it takes the place
of the device name, on 2 lines. The operator cannot close it; it goes at the next status read (each 15 s) after NTP
synchronizes the clock. The claim page sends `client_time` (`new Date().toISOString()`). Tests: `tests/clock-banner.test.tsx`,
`tests/claim-page.test.tsx`, and `e2e/clock.spec.ts` (the mock option `clock: "untrusted"`).

### 11.7 Copy

The texts use the words of the operator: "Download the update" (stage), "Install now" (apply), "Roll back",
"Install anyway" (`now: true`), "Show the effect" (dry run), "Turn on with these settings". `index.json`, lock, ETag,
cutover, and checkpoint are not in the texts; "backup point" is the checkpoint, "list of versions" is the index.

## 12. Connectivity (Phase 6)

`docs/connectivity.md` is the specification. `src/features/connectivity/` holds the tab; `connectivity-logic.ts` has the
rules that the tests check (the hint pattern, the QR content, the mode of each name, the rebinding check).

### 12.1 Settings > Connectivity

The second tab of Settings (`/settings?tab=connectivity`). One job card at the top (the last job of the 3 kinds), then
3 cards. Each card has a fixed header (icon, title, state badge "On" or "Off") and keeps its place while a job runs.
While a job of the tab runs, the buttons of each card are disabled. The Tailscale card shows "A change runs now. Wait until
it ends." while its job waits.

- **Public name.** Off: the start of the name (`hostname_hint`, `a-z0-9-`, the identity host name by default), the switch
  "Use the test certificate of Let's Encrypt (staging)", and "Turn on". The note tells that the device and the devices that use
  the name need the internet. On: the name as a link, the certificate state (issued, waits, expired, error; "test
  certificate" for staging), the last DNS update, the advice "Bookmark this name", the HSTS note, and the result of the DNS
  rebinding check (a button: the browser loads `public.health_url` with `no-cors`; a failure tells the operator to check
  the router), and the limits of Let's Encrypt.
  "Turn off" asks for a confirmation: the name never comes back.
- **Tailscale.** Off: the optional auth key (`tskey-…`, a password field) and "Turn on" (a recent password check). While the
  job waits for the login: the login link as a button, a copy button, and a QR code (for a phone), with "Waiting for the
  login…". While a job of the tab runs, the console polls `GET /tls` each 2 s. On: the MagicDNS name as a link, the tailnet, the address, and a warning
  when the tailnet gives no HTTPS certificates. "Turn off" asks for a confirmation.
- **Wi-Fi.** "Find networks" (`GET /network/wifi/scan`): a radio list (SSID, signal bars, a lock for a secured network)
  and "Other network (type the name)". The passphrase (write-only), the country (2 letters), the switch "Use Wi-Fi only
  when ethernet is down" (on by default), and "Connect". The note tells that Care restarts the first time. On: the SSID,
  the signal, the address, which link carries the traffic, "Remove". "Other Wi-Fi profiles" lists the profiles that
  care-pi does not manage (the Imager settings). The DHCP reservation advice lists the MAC address and the address of
  each interface. A device without Wi-Fi shows "This device has no Wi-Fi" and no form.

### 12.2 The Certificate page

- "Names that the certificate allows": the DNS names and the address ranges of the name constraints, with the sentence
  "The root certificate of this device can only sign these names and addresses. It cannot sign the address of a bank or
  another site." A root without constraints (before the switch) says that the next apply replaces it.
- `local_ca.uncovered` not empty: a warning with the names.
- After the one-time switch (`ca_changed_at`): the warning "The certificate of this device changed" with the time, the
  start of the new fingerprint, and the old fingerprint, until the operator clicks "I installed it". `localStorage` key
  `care-pi.ca-changed.dismissed` keeps the fingerprint that was closed, so a later switch shows the warning again.
- The QR code has 3 modes ("The phone has"): "Internet" encodes `https://care-pi.rithviknishad.dev/cert#<root DER,
  base64url>` (the default); "No internet" encodes the fingerprint, to compare on the phone; "Only this network" encodes
  the download URL of the device (the behaviour of Phase 2).

### 12.3 The dashboard

The Network card lists each name that Care serves, in the order of `Status.tls` (public, local, Tailscale), with a badge of
the mode and the certificate below the name ("Let's Encrypt certificate", "Certificate of this device", "Tailscale
certificate"). Each name has 2 lines at each width, so the skeleton has the same height.

### 12.4 Mock and tests

Mock options (`POST /__mock/reset`): `connectivity` (`fresh`: a new device of Phase 6, the root with name constraints;
`migrated`: after the one-time switch, with the warning; `public`; `tailscale`), `wifi_device` (`false`: no Wi-Fi), `public_fail: "dns"` (the DNS service
does not answer), `tailscale_login: "manual"` (the login waits until `POST /__mock/tailscale-login`), `wifi_fail: "auth"`
(a wrong passphrase). Vitest: `tests/connectivity-logic.test.ts`, `tests/connectivity-tab.test.tsx`. Playwright:
`e2e/connectivity.spec.ts` (public name, Tailscale with the login link, Wi-Fi, the certificate page, the dashboard) and
`e2e/real/connectivity.spec.ts` (opt-in on a device).

## 13. Console v2 (S66)

The v2 design system is the look of the console for non-technical operators: calm green-tinted surfaces, a gradient on
heroes and on the setup rail only, 8 line illustrations, motion at key moments, Geist and Geist Mono. **`console/design.md`
is its specification** (principles, palette with measured contrast, type scale, space, motion, layouts, components,
illustrations, copy, states, do and don't, page recipes). The rules of this note stay in force; v2 adds to them:

| Topic | v2 |
| --- | --- |
| Tokens | `src/styles/tokens.css` (light `:root`, dark `.dark`): `--surface-*`, `--ink*`, `--line*`, the tones `--ok-*`, `--warn-*`, `--danger-*`, `--info-*`, `--gradient-hero`, scrims, `--shadow-soft`, layout sizes. Tailwind: `bg-surface-tint`, `text-ink-muted`, `border-danger-border`, `rounded-card`, `h-topbar`, … |
| Type | `type-display`, `type-h1`, `type-h2`, `type-h3`, `type-body`, `type-small`, `type-data` (not `text-*`: `cn()` takes an unknown `text-*` class for a colour) |
| Motion | `src/styles/motion.css`: 150/250/400 ms, ease-out, the variant `motion-ok:` (off with "reduce motion" and with careui `data-reduced-motion="true"`), view transitions for the Stage, the toasts |
| Components | `src/components/v2/`: AppShell, Stage, Focus, Hero, Panel, StatCard, MetricBar, DashboardGrid, KeyValueList, MonoBlock, CopyButton (S68 report header), ProblemCard, EmptyState, ChoiceCards, Explainer (backups retention), StepList, ProgressRing, StatusChip, Timeline, Illustration, Toaster, ThemeToggle |
| Shell | `src/app/layout/` uses the v2 AppShell: rail from 1024 px, bottom bar below (4 primary places and "More"), minimal mode before `ready`, a banner slot of 40 px; the public pages use the Focus backdrop |
| Gallery | `/__v2` on the Vite dev server only (`vite.config.ts`, `apply: "serve"`): each component with sample data, light and dark |
| Checks | `npm run test:v2`: axe (0 serious or critical), stability (boxes and CLS 0.0000 when the data comes), reduced motion (0 animations), font loading (swap, preloaded), token contrast |
| Texts | The default texts of the components are in `src/components/v2/strings.ts`; the texts of a page stay in `src/lib/strings.ts` |


### 13.1 The pages on v2

Each page keeps its data flow (queries, polling, the plan preview, the jobs) and its test ids; v2 changes the presentation.
The layout of each page comes from a recipe of `console/design.md` section 13.

| Page | Layout | What the operator sees |
| --- | --- | --- |
| Claim, Login, Recover | Focus (gradient backdrop, illustration) | One task for each screen. The password strength is a calm bar (amber for a weak password, never red). The recovery codes are a printable dashed card with Copy, Download, Print, and "I saved them". |
| Setup wizard | Stage (rail with progress; below 1024 px a progress header) | Each Continue has a "what happens next" line. The hardware step is a short summary with a "Details" disclosure. Identity shows the address big. The size step is section 13.4. The TLS step is one sentence and the switch "Keep Care working offline". The review is a calm summary. |
| Setup progress | Stage | The StepList with the expected length of each step ("Usually about 2 min") and a sentence for each phase. A reload or a second device resumes it (section 13.3). |
| Hand-off | Hero | A big "Open Care", "Install the certificate on this device", and a QR code for a phone. |
| Certificate | Focus | The OS of the browser first, one path at a time, the trust check with a green tick, the fingerprint and the name limits in "Check the fingerprint (for experts)", the QR code of the `/cert` page. |
| Dashboard | DashboardGrid of fixed StatCards | Health, backups ("Last copy 12 minutes ago"), updates, names with mode chips, memory bars, units; the banner row above. |
| Backups | Hero + Panels | "Back up now" (all places), destination cards in plain words, the Explainer of the retention (section 13.5), the recovery key card, verify and the test restore. |
| Restore | 3 ChoiceCards | Undo a mistake (a calm time picker and "You will lose the changes after HH:MM"), files only, a new device; the undo card after a restore. |
| Updates | Panels | The version card, the new version with "Install tonight" (the window) and "Install now", the window in words, the history (section 13.6). |
| Solution and plugs | Panels | A card for each plug with its settings form. |
| Settings | Tabs, one Panel each | One sentence of help for each tab and each setting; Settings > Backups uses the pickers of section 13.5. |
| Security | Panels | Password, recovery codes, two-step login (TOTP) with a QR card, sessions, the firewall policy and extra ports ("If you lose the connection, the firewall opens again in 90 s" with a countdown). |
| Logs | Panel | The service as chips, Follow, "Copy the log" (section 13.2). |
| Support | Panels | The health checks as a checklist (the failed ones first, a plain fix for each), "Copy the result", the support bundle with a message to copy, the CLI in a disclosure. |
| Connectivity | Settings tab, CardFrames | The public name big with "Bookmark this name on each device", Tailscale, Wi-Fi. |
| Jobs | Panel list / StepList | Section 13.3. |

A hash, a sha, or a path shows only in a "Technical details" disclosure. Each page has the empty, loading, and error states of
`console/design.md` section 12: the skeletons keep the boxes of the data (`e2e/a11y.spec.ts` and the stability checks of
`e2e/backups.spec.ts`, `e2e/updates.spec.ts`, `e2e/login-dashboard.spec.ts`, and `e2e/jobs.spec.ts`).

### 13.2 Copy to clipboard (S68)

`CopyButton` (`src/components/v2/copy-button.tsx`) copies a text with a header line: device name · agent version · time
(`CopyHeaderProvider` in the shell; the version comes from `GET /status`). A long text comes from the agent as plain text
(`src/api/exports.ts`, `parseAs: "text"`); the button starts the request at the click (a `ClipboardItem` with a promise, so
Safari keeps the user gesture).

| Where | What it copies |
| --- | --- |
| Each log view | The whole view: `GET /logs/{unit}/export` |
| Each job (job page, the job of a page, the setup progress) | The transcript: `GET /jobs/{id}/export` |
| Each ProblemCard | The title, the detail, the code, and the request id |
| Support > Health checks | `GET /doctor/export` |
| Support > Support bundle | The message for the support team |
| Certificate | The fingerprint |
| Ids (key id, job id, versions) | The value |

### 13.3 Jobs from any tab or device (S67)

- `/jobs`: `GET /jobs?state=active` (each 3 s) first, then `GET /jobs?state=recent` (each 15 s). Each row (title, state chip,
  step X of Y, source, time) opens `/jobs/<id>`. The skeleton has the number of rows of the last visit (`localStorage`
  `care-pi.jobs.recent`).
- `/jobs/<id>`: `GET /jobs/{id}` gives the steps so far and `log_tail`; the page then opens
  `GET /jobs/{id}/events?after=<last_event_id>`, so a reload or another device shows the same steps without a gap. The log
  is in a disclosure. "Copy the transcript" and Cancel (when the job allows it).
- The banner (section 11.6) links to the job on each page of each device.
- Setup `applying`: `setup_job_id` of `GET /_care-pi/status`. With a session, the wizard goes to `/setup/progress` (not a
  stale step). Without a session (a second device), `/` goes to `/jobs/<setup_job_id>`: a Focus page "Care is being set up"
  (or "updated") with "Step X of Y" and a bar above the step list of the public stream `GET /_care-pi/jobs/{id}/events`
  (steps only, no log), and a link to log in. Y is `job.step_count` of the public status; without it (`null`, a job
  without a plan) the page shows "Step X" and an indeterminate bar. The bar is full only when the job succeeded. On port 443 the maintenance page of Caddy covers the same case.

### 13.4 The size step: preset cards (S65)

Easy mode shows the 3 presets of `GET /config/presets` as ChoiceCards: Just me, Small clinic, Busy counter, each with a
plain fit text and the "Recommended" badge of the agent. On a device with less than 4 GB of memory the agent gives the
warning `busy-counter-low-ram`: the Busy counter card is off with the reason "This device has 2 GB of memory. Busy counter
needs 4 GB or more." The questions about the staff and the `presetFromAnswers` mapping are gone. Advanced mode keeps the
numbers (workers, concurrency, shared buffers) with the live hint "≈ <preset>" (`nearestPreset` in
`src/features/setup/size-logic.ts`).

### 13.5 The backup schedule in words (S66)

Each place that sets the retention (the wizard step backups, the Backups page, Settings > Backups) has 2 pickers and the
Explainer:

- "How often we save a copy": every 5, 15 (default), 30, or 60 minutes.
- "How far back you can go": Standard (2 days of every copy, 2 weeks of daily copies, 2 months of weekly copies, 6 months of
  monthly copies), Longer (7 days, 30 days, 24 weeks, 24 months), or Custom (the 4 numbers; the Explainer changes while the
  operator types). A destination with other numbers shows Custom.
- The sentence of the RPO: "If the device breaks, you lose at most N minutes of work."

### 13.6 An interrupted update (S69)

A history entry of an update that a power loss interrupted (`rolled-back` with a recovery reason, or the problem detail
"interrupted") says: "The update was interrupted. Care returned to the previous version." The PITR offer (section 11.4)
shows under it. The mock scenario `updates: "recovered"` gives this entry (`e2e/updates.spec.ts`).

### 13.7 Checks

`e2e/a11y.spec.ts` runs axe on each page in the light and the dark theme (0 serious or critical), checks reduced motion,
and the stability of the Jobs page. `npm run test:v2` checks the gallery. `playwright.shots.config.ts` takes a screenshot of
each page (desktop light and dark, phone light) for a design review. Each test checks that each request stays on the origin.
