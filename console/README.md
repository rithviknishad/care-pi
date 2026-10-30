# console

The care-pi console: the setup wizard and the control panel of the device.
The agent embeds the build (`dist/`) and serves it at `https://<host>:8443/` (decision S48).
The API is on the same origin at `/api/v1`. The contract is `docs/api/openapi.yaml` and `docs/api/README.md`.
The design rules are in `docs/console-ui.md`.

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
| `npm run api:types`               | Makes `src/api/types.gen.ts` from `docs/api/openapi.yaml`.                                                                                              |
| `npm run api:check`               | Exits with code 1 when `src/api/types.gen.ts` is not the same as a new file.                                                                            |
| `npm run careui:add -- <name>...` | Adds careui components from the registry (section 6).                                                                                                   |
| `npm run careui:check`            | Exits with code 1 when a careui file is not the same as the registry file of the lock.                                                                  |

Do the full check before a commit:

```sh
npm run api:check && npm run typecheck && npm run lint && npm run test && npm run test:e2e
```

## 3. Work with the mock

The mock (`mock/`) is a small Node HTTP server. It implements the contract for the console:

- the claim (claim code `4F7K-Q2MX-9PDA`, Imager password `imager-password`), the login, the recovery, and TOTP (code `000000`);
- the session cookie, the CSRF token (`X-CSRF-Token`), and the `Origin` check;
- the rate limits and the lockout with `Retry-After`;
- the setup state machine, the setup job with SSE events, and the apply job;
- logs with SSE follow, `If-Match` on `/config`, and `501` for each `x-phase` operation.

It sends each other operation to Prism when Prism runs (`npm run mock`).

The mock serves plain HTTP, the same as `care-pi serve --insecure-dev`.
Thus it uses the cookie `care_pi_session` without `Secure`. The agent on a device uses `__Host-care_pi_session`.

Start the mock and the dev server in 2 terminals:

```sh
npm run mock
npm run dev
```

Open `http://localhost:5173`. The mock starts in the scenario `unclaimed`.
To select a scenario, set `MOCK_SCENARIO` (`unclaimed`, `setup`, or `ready`), or reset the mock:

```sh
curl -X POST http://127.0.0.1:4011/__mock/reset -d '{"scenario":"ready","step_ms":200}'
```

The console password of the scenarios `setup` and `ready` is `correct horse battery staple`.
Other reset options: `fail_step` (the index of a setup step that fails), `ram_gib`, `disk`, `dev_build`.

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

| Path                     | Content                                                                                                                                                    |
| ------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `src/api/`               | The typed API client (`client.ts`), the problem types (`problem.ts`), the queries (`queries.ts`), and the generated types (`types.gen.ts`).                |
| `src/app/`               | The router, the session guards, and the shell (top bar, navigation, theme toggle).                                                                         |
| `src/features/<page>/`   | 1 directory for each page: claim, login, setup, dashboard, certificate, logs, settings, security, support, and the pages of later phases (`unavailable/`). |
| `src/components/ui/`     | careui components. Do not edit these files (section 6).                                                                                                    |
| `src/components/app/`    | Console components that use careui components.                                                                                                             |
| `src/lib/strings.ts`     | Each text of the console, in ASD-STE100 Simplified Technical English.                                                                                      |
| `src/styles/careui.css`  | The careui design tokens. Do not edit this file.                                                                                                           |
| `src/styles/console.css` | The few rules that the console adds.                                                                                                                       |
| `public/`                | `favicon.svg` and `theme.js` (the theme before the first paint).                                                                                           |
| `mock/`                  | The stateful mock of the agent.                                                                                                                            |
| `tests/`                 | Unit tests.                                                                                                                                                |
| `e2e/`                   | Playwright tests: desktop (Chromium, 1280 px), iPhone 13 (WebKit, 390 px), and 360 px (Chromium).                                                          |
| `scripts/`               | `api-types.mjs`, `careui-add.mjs`, `clean-dist.mjs`.                                                                                                       |

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
The tests run one after the other, because each test resets the mock.

| File                          | What it checks                                                                                        |
| ----------------------------- | ----------------------------------------------------------------------------------------------------- |
| `e2e/setup.spec.ts`           | Claim, recovery codes, each step of the easy path, finish, SSE progress, hand-off, dashboard.         |
| `e2e/login-dashboard.spec.ts` | Login with a deep link, the dashboard, layout stability (CLS and card boxes), theme, logout, lockout. |
| `e2e/logs.spec.ts`            | Follow over SSE, the sticky bottom, pause, and resume.                                                |
| `e2e/certificate.spec.ts`     | The public certificate page: fingerprint, OS detection, tabs, downloads, QR code.                     |
| `e2e/settings.spec.ts`        | A config change with `If-Match`, the plan preview, the apply job, and a `501` page.                   |

Each test also checks that each request of the page goes to the origin of the console.

`e2e/real` holds the journeys against a real device: claim and setup on a laptop or a phone, the phone journey on a
ready device, a resume after a reboot, a revoked session, and a missing job. `npm run test:e2e` ignores them.
`npm run test:e2e:real` runs them with `playwright.real.config.ts`. `e2e/real/README.md` lists the variables.
