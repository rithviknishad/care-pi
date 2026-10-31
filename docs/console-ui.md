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
| `/dashboard` | `GET /status` each 10 s | yes, state `ready` |
| `/logs` | The journal of a unit, Follow over SSE | yes |
| `/settings` | Identity, size, environment (`PUT /config`), FE build, power | yes, state `ready` |
| `/security` | Password, recovery codes, TOTP, sessions | yes |
| `/support` | Support bundle, versions | yes |
| `/backups`, `/updates`, `/plugs` | Features of later phases: "Not available in this version" | yes, state `ready` |

Phase 2 exit (`plans/06-roadmap.md`): a non-technical tester completes setup from a phone and a laptop.
Each decision below serves this goal.

## 2. Design decisions

1. **Monospace first.** Geist Mono is the body font (`src/styles/console.css` sets `--font-sans`).
   The font files come from `@fontsource-variable/geist-mono`, so they are in the build. The page loads no font from another origin.
2. **careui tokens and components, without change.** The console uses the Care UI design tokens and components
   (section 3). It does not make its own version of a careui component.
3. **Light and dark theme.** The default is the theme of the system. Section 4 tells more.
4. **Stable layout.** A page does not move when data arrives or refreshes. Section 5 gives the rules.
5. **Mobile and desktop.** The console works at a width of 360 px or more (phone) and 1024 px or more (laptop).
   The navigation is a side bar from 1024 px (`lg`) and a sheet below 1024 px.
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

The items in the lock (34): alert, alert-dialog, badge, button, card, checkbox, collapsible, dialog, dropdown-menu,
empty, field, input, input-group, input-otp, kbd, label, native-select, progress, radio-group, scroll-area, separator,
sheet, skeleton, sonner, spinner, switch, table, tabs, textarea, toggle, toggle-group, tooltip, use-auto-space, use-mobile.

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
| e2e on a device | Playwright against a real device (`e2e/real`, `playwright.real.config.ts`) | Desktop (Chromium, 1280 × 800) and iPhone 13 (WebKit). Claim to ready on a new install, the phone journey (dashboard, certificate, logs, TOTP), a resume after a reboot. The Phase 2 exit test on lumine used them. |

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
| `create-care-superuser` | Make the Care administrator | the user name |
| `collect` | Collect the support data | the argument |
| `archive` | Make the support bundle file | the argument |
| `prune` | Delete the old builds of the web app | the argument |

The second line of a step is, in this order: its `detail` (a result or an error); while it runs, the message of its
progress, the expected time ("Expected: about 3 min 40 s", `job-state.ts`), or its last output line; else the short
argument above.
