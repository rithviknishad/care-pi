# Clinic onboarding (care_pi_onboarding)

care-pi sets up a new clinic in CARE with its own onboarding plugs (decision S73):

- `plugs/care_pi_onboarding`: a CARE backend plug. It holds the fixture data and gives one API for each onboarding
  step. CARE does the work of a step on the server, in one request and one transaction.
- `plugs/care_pi_onboarding_fe`: an MFE plug. It is a wizard inside CARE at `/onboarding`. Each action of the
  operator is one request.

The plugs replace `care_onboarding_fe`. The target is CARE 3.1 (decision S71): `ohcnetwork/care` branch
`ENG-737-backup-backup` and `ohcnetwork/care_fe` branch `3.1/base`.

## 1. Why

`care_onboarding_fe` did the setup in the browser with the public CARE API, one request for each object, in
loops of 2 or 3 parallel requests (`src/lib/batch.ts` of that plug). Some examples:

- The first step read each page of the organisation lists, then created each state and union territory of India
  and each district, one POST each: 36 and 726 objects (`src/care/bootstrap.ts`).
- The staff step read the members of a department before each new membership, and again after an error.
- Each step read its lists again when it opened.

So one setup sent many hundreds of requests to CARE in a few minutes. On a Raspberry Pi the clinic notices this
load, and a failure in the middle left a half-made clinic.

The new plugs send one request for each step. The server finds what exists and makes what is missing, with a
bulk insert where CARE allows it. A step that fails changes nothing.

## 2. The steps

The wizard shows the steps in this order. Each step is `POST /api/care_pi_onboarding/steps/<step>/apply/`.

| Step | Who | What the step makes | How it finds an existing object |
|---|---|---|---|
| `organization` | superuser | The government organisations of the clinic area: state, district, and an optional local body. With `all_districts`, each district of the state, in one insert. With `parent`, a district under an existing organisation. | The name (without case) under the same parent. |
| `facility` | superuser, or a user with `can_create_facility`; a facility admin to apply again | The facility from a template (`clinic` or `pharmacy`): the facility, its departments, its rooms and beds, and the links between rooms and departments. CARE makes the root organisation Administration and makes the caller its Facility Admin. | The facility of the last run, else the facility name (as CARE compares names). Departments and rooms: the name under the same parent. |
| `roles` | superuser | The custom role Receptionist with its permission list, and one role organisation for each job. The jobs Doctor, Nurse, Pharmacist, and Clinic administrator use the CARE system roles. | The role name; the permission slug; the name of the role organisation. |
| `staff` | facility admin with `can_create_user` | The user accounts, one membership in a department for each person, and a membership of the role organisation of the job. The response gives each new initial password once. With `invite`, CARE sends a link to set the password instead; then `email` is required. | The username; the membership of the user in the department. |
| `valuesets` | superuser | The CARE system value sets that are missing (as `sync_valueset` without `--overwrite`), and the short coded lists of the clinic. | The value set slug. |
| `questionnaires` | facility admin | The clinic forms (vitals, consultation note, triage, pharmacy intake) as facility questionnaires, each linked to its departments. | The slug in the facility. |
| `catalog` (optional) | facility admin | Eight starter medicines in the facility product catalog. CARE checks their codes on its terminology server, so this step needs internet access. | The slug in the facility. |
| `demo_data` (optional) | facility admin | Five demo patients, each with an open visit. `DELETE /api/care_pi_onboarding/demo_data/?facility=<id>` removes them. | The record of the plug for each demo patient and visit. |

The response of each apply:

```json
{"step": "staff", "status": "done",
 "created": {"user": 4, "membership": 4, "role_organization_membership": 4},
 "existing": {"user": 0, "membership": 0, "role_organization_membership": 0},
 "detail": "The facility has 4 staff members from this list. 4 accounts are new. ...",
 "result": {"staff": [...]},
 "secrets": {"credentials": [{"username": "dr-anil", "password": "..."}]}}
```

`status` is `done`, `skipped`, or `failed`. A second apply with the same input gives `created` of 0 for each kind
and the same total in `existing`. The plug does not store the passwords; the second apply has no `secrets`.

Other calls:

| Call | What it does |
|---|---|
| `GET status/?facility=<id>` | The state of each step (`pending`, `done`, `skipped`, `failed`), what exists now, the next step, and whether the dashboard must open the wizard. A user who is not an administrator gets `can_manage: false` and no data. |
| `GET steps/<step>/preview/?facility=<id>` | The options and defaults of the form, and what exists. It changes nothing. |
| `POST steps/<step>/skip/` | Marks an optional step `skipped`. |
| `POST wizard/close/` with `{"reason": "finished"}` or `"later"` | The dashboard does not open the wizard again. |

The full table of the inputs is in `plugs/care_pi_onboarding/README.md`.

### 2.1 Idempotency and safety

- Each apply runs in one database transaction. A failure rolls back each change of the call, and the step row
  records `failed` with the reason.
- Before the work, the call locks the row of its step (`select_for_update`). A second apply of the same step waits,
  then finds the objects of the first.
- Each record goes through the CARE resource spec (the pydantic model of the CARE API) before the save, so the
  validation is the same as for the CARE API. The plug then does the side effects of the CARE viewset (for example
  the questionnaire organisation cache, or the patient identifiers).
- The demo data takes the CARE create locks of patients and visits once for the whole batch, and frees them when
  the transaction ends.
- The plug uses no raw SQL. It writes one log line for each call:
  `care_pi_onboarding step=staff scope=<facility> status=done created=12 existing=0 user=admin ms=44`.

## 3. Permissions and limits

- A **superuser** can apply each step.
- A **facility admin** holds `can_create_facility_organization` in the root organisation (Administration) of the
  facility. In CARE 3.1 that is the role Facility Admin. A facility admin can apply the facility steps of that
  facility: `facility` (again), `staff`, `questionnaires`, `catalog`, and `demo_data`. The plug does not use
  `can_update_facility`, because the role Staff has it too.
- Other users get HTTP 403 from each step call, and `can_manage: false` from `status`. The wizard shows that only
  an administrator can do the setup. The dashboard redirect never runs for them.
- Rate limits (DRF `ScopedRateThrottle`, for each user): 30 write calls a minute (`ONBOARDING_APPLY_RATE`) and 120
  read calls a minute (`ONBOARDING_READ_RATE`). The wizard shows one sentence on HTTP 429.
- One staff call takes at most 50 people (`ONBOARDING_MAX_STAFF`).

## 4. The fixtures

The fixture files are in `plugs/care_pi_onboarding/care_pi_onboarding/fixtures/`. The plug ships them in its Python
package. `fixtures/README.md` gives the format of each file, the rules, and the sources and licences.

| File | Content |
|---|---|
| `locales/in.json` | India: the country, the pincode pattern, the government levels, 36 states and union territories, and 726 districts. |
| `facility_templates.json` | The templates `clinic` and `pharmacy`. |
| `roles.json` | The jobs and their CARE roles; the permission list of Receptionist. |
| `valuesets.json` | Two SNOMED CT lists: common complaints and common conditions. |
| `questionnaires.json` | Vitals, consultation note (SOAP), triage, pharmacy intake. |
| `catalog.json` | Eight starter medicines. |
| `demo.json` | Five demo patients. |

### 4.1 Add a locale

1. Copy `locales/in.json` to `locales/<code>.json` (letters and digits only).
2. Set `country`, `phone_example`, and `pincode_pattern` (an empty pattern turns the check off).
3. Set `levels` and the list of `states`, each with its `districts`.
4. Set `ONBOARDING_LOCALE` to `<code>` in the plug configs (solution setting **Locale of the area list**).
5. Run the tests (section 7).

## 5. The hand-off from the care-pi setup

1. The console setup wizard finishes with the job step `create-care-superuser`
   (`care-pi care create-superuser`, `docs/console-server.md`).
2. The operator opens CARE and signs in as that superuser.
3. The plug overrides the CARE dashboard (`UserDashboard`). For a superuser, it asks `status` once. While
   the facility step is not done and nobody closed the wizard, it opens `/onboarding`. It does this once in each
   browser session.
4. The operator does the steps. **Open CARE** on the last screen closes the wizard and opens
   `/facility/<id>/overview`. **Clinic setup** in the admin menu opens the wizard again at any time.

The redirect needs `REACT_MFE_REGISTERED_COMPONENTS=UserDashboard` in the care_fe build. The solution sets it for the
plug (`env.frontend`). The plug setting **Open the clinic setup after sign-in** (`config.redirect_after_login`) turns
the redirect off; the route and the menu item stay.

## 6. The plugs in the solution

`solutions/care-clinic/solution.yaml` has one plug `care_pi_onboarding` with both parts:

```yaml
backend:
  name: care_pi_onboarding
  package: git+https://github.com/rithviknishad/care-pi.git
  subdirectory: plugs/care_pi_onboarding
  ref: main
frontend:
  slug: care_pi_onboarding_fe
  repo: "."
  path: plugs/care_pi_onboarding_fe
  ref: main
```

- **Backend.** pip needs the subdirectory fragment after the ref, so the install list entry is
  `{"package_name": "git+https://github.com/rithviknishad/care-pi.git", "version": "@<sha>#subdirectory=plugs/care_pi_onboarding"}`.
  A `package` with the fragment and a version `@<sha>` after it fails: pip reads `plugs/care_pi_onboarding@<sha>` as
  the subdirectory. `care-beat` runs `migrate` at start (`docs/apply.md`), which adds the three tables of the plug.
- **Frontend.** `repo: "."` is this repository. CI builds `path` with `plug-bundle.yml` (in-repo mode): the checks
  of the plug, then `npm run build -- --base /plugs/care_pi_onboarding_fe/<sha12>/`. The lock pins the last commit of
  `ref` that changed `path`, so a release without a change to the plug reuses the bundle. Caddy serves
  `/plugs/*` with `Access-Control-Allow-Origin: *`.
- The plug works without internet, except the `catalog` step.

## 7. Develop

Backend (Docker; `scripts/test-in-docker.sh` builds the CARE dev image and starts Postgres 17 and Redis 8):

```bash
git clone -b ENG-737-backup-backup https://github.com/ohcnetwork/care.git ../care31
plugs/care_pi_onboarding/scripts/test-in-docker.sh ../care31
cd plugs/care_pi_onboarding && uvx ruff check . && uvx ruff format --check .
# Against a running CARE with the plug: apply each step twice; the second run must create nothing.
plugs/care_pi_onboarding/scripts/smoke.sh http://127.0.0.1:9000 <superuser> '<password>'
```

To run CARE with the plug from the source, mount the plug directory, put it on `PYTHONPATH`, and set
`ADDITIONAL_PLUGS='[{"name": "care_pi_onboarding", "package_name": "/plugs/care_pi_onboarding", "version": ""}]'`.
After a model change: `python manage.py makemigrations care_pi_onboarding`.

Frontend:

```bash
cd plugs/care_pi_onboarding_fe
npm ci
npm run dev                      # the wizard in the dev harness, port 10142
npm run typecheck && npm run lint && npm run format:check
npm test                         # Vitest: the wizard reducer
npm run test:e2e                 # build; Playwright: one full run on a mocked backend, and a cross-origin load of remoteEntry.js
npm run test:locale && npm run test:single-react
```

To try the wizard in a care_fe dev server, add a plug_config row with the slug `care_pi_onboarding_fe` and
`meta.url` `http://localhost:10142/assets/remoteEntry.js` (after `npm run build && npm run preview`).

CI: `.github/workflows/plug-backend.yml` (lint, the tests against CARE 3.1, and the install from git with the
subdirectory) and `.github/workflows/plug-bundle.yml` (the bundle).
