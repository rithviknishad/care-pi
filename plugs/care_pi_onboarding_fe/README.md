# care_pi_onboarding_fe

The clinic setup wizard of care-pi: a CARE MFE plug (Vite and module federation) that care_fe loads at run time.
It calls the backend plug `plugs/care_pi_onboarding`. The overview is `docs/onboarding.md`.

- One GET of `status` when the page opens, one GET of `preview` for each screen, and one POST for each action
  (apply, skip, remove the demo data, close). Nothing polls.
- Only an administrator sees the steps: a CARE superuser, or a facility admin for the facility steps.
- Target: care_fe branch `3.1/base` (decision S71).

## Contract with care_fe

`src/manifest.tsx` (types: `care_fe/src/pluginTypes.ts`, `PluginManifest`):

| Field | Value |
|---|---|
| `plugin` | `care_pi_onboarding_fe` (also the federation name and the i18n namespace) |
| `routes` | `/onboarding` and `/admin/onboarding` |
| `adminNavItems` | Clinic setup → `/admin/onboarding` |
| `overrides` | `UserDashboard` → opens the wizard once for a superuser of a new CARE |

The `UserDashboard` override needs `REACT_MFE_REGISTERED_COMPONENTS=UserDashboard` in the care_fe build.
The plug_config row:

```json
{"slug": "care_pi_onboarding_fe",
 "meta": {"url": "https://<host>/plugs/care_pi_onboarding_fe/<sha12>/assets/remoteEntry.js",
          "config": {"redirect_after_login": true}}}
```

`config.redirect_after_login: false` turns the redirect off; the routes and the admin link stay.

Host globals that the plug reads: `window.CARE_API_URL`, `window.AuthUserContext` (the signed-in user, no request),
`window.__CARE_PLUGIN_RUNTIME__.meta` (the config above), and `localStorage.care_access_token`. The plug never
refreshes the token: a 401 asks the user to sign in again.

Federation `shared`: `react`, `react-dom`, `react-i18next`, `raviger`, `sonner` (the libraries of care_fe that the
plug imports).

## Strings

`src/locale/en.json`, under the key `cpo`. The plug adds them to the i18n instance of the host
(`src/lib/i18n.ts`): care_fe fetches a plug locale from the origin of `meta.url`, and on care-pi that origin is CARE.
The build also writes `dist/locale/en.json`. `npm run test:locale` checks that each key exists, each entry is used,
and the keys are sorted.

## Develop

```bash
npm ci
npm run dev            # the dev harness (index.html, src/main.tsx) on port 10142
npm run typecheck && npm run lint && npm run format:check
npm test               # Vitest: the wizard reducer (src/lib/wizard.ts)
npm run build          # dist/assets/remoteEntry.js and the assets
npm run test:e2e       # build, then Playwright: one full run on a mocked backend, and a cross-origin load of the remote
npm run test:single-react
npm run test:locale
npm run preview        # serves dist with Access-Control-Allow-Origin: *
```

care-pi builds the bundle with `npm run build -- --base /plugs/care_pi_onboarding_fe/<sha12>/`
(`.github/workflows/plug-bundle.yml`, in-repo mode).

## Chassis

Forked from `care_token_display_fe` (the Care UI fork of the `care_teleicu_devices_fe` chassis): Care UI components
(`src/components/ui/*`, vendored from careui.ohc.network), the PostCSS pass that scopes the styles to
`.care-pi-onboarding-fe-container` (with the dark-mode selectors on the ancestor side), and the
`use-sync-external-store` shims that keep one React.
