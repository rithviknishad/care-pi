# care_pi_onboarding

A CARE backend plug for the first setup of a clinic. Each onboarding step is one API call. The server does the
work in one transaction, from fixture data in the plug. A step is idempotent: a second call finds the objects of
the first call and creates nothing. The MFE wizard is `plugs/care_pi_onboarding_fe`. The overview is
`docs/onboarding.md`.

Target: CARE 3.1 (`ohcnetwork/care` branch `ENG-737-backup-backup`, decision S71). Python 3.12 or later.

## Install

CARE installs the plug from git. pip needs the sha before the subdirectory fragment:

```json
{"name": "care_pi_onboarding",
 "package_name": "git+https://github.com/rithviknishad/care-pi.git",
 "version": "@<sha>#subdirectory=plugs/care_pi_onboarding",
 "configs": {"ONBOARDING_LOCALE": "in", "ONBOARDING_DEMO_ALLOWED": true}}
```

`install_plugins.py` runs `pip install <package_name><version>`. Then run `python manage.py migrate`. CARE mounts
the URLs at `/api/care_pi_onboarding/`.

## Settings

Each setting comes from `configs` of the `ADDITIONAL_PLUGS` entry, then from the environment, then from the default.

| Key | Default | What it does |
|---|---|---|
| `ONBOARDING_LOCALE` | `in` | The fixture `fixtures/locales/<code>.json` (country, pincode pattern, states, districts). |
| `ONBOARDING_DEMO_ALLOWED` | `true` | `false` turns off the step `demo_data` (403). Skip still works. |
| `ONBOARDING_APPLY_RATE` | `30/min` | DRF rate for each user: apply, skip, delete, and close. |
| `ONBOARDING_READ_RATE` | `120/min` | DRF rate for each user: status and preview. |
| `ONBOARDING_MAX_STAFF` | `50` | The largest staff list of one call. |

## API

Base: `/api/care_pi_onboarding/`. The trailing slash is optional. Authentication: the CARE JWT
(`Authorization: Bearer <access>`).

| Method and path | Who | What |
|---|---|---|
| `GET status/?facility=<id>` | any user (data only for admins) | The state of each step, what exists, the next step, and `wizard.redirect`. |
| `GET steps/<step>/preview/?facility=<id>` | as apply | The options, the defaults, and what exists. Creates nothing. |
| `POST steps/<step>/apply/` | see the table below | Runs the step in one transaction. |
| `POST steps/<step>/skip/` | as apply | Marks an optional step `skipped`. |
| `DELETE demo_data/?facility=<id>` | facility admin | Soft-deletes the demo patients and visits. |
| `POST wizard/close/` `{"reason": "finished"\|"later"}` | an admin | The dashboard does not open the wizard again. |

The response of apply and skip:

```json
{"step": "facility", "status": "done", "created": {"department": 5}, "existing": {"department": 0},
 "detail": "...", "result": {"facility": {"id": "...", "name": "..."}}}
```

`status` is `done`, `skipped`, or `failed`. A failed call answers HTTP 400 with `detail` and `errors`, and the
database has no change. The step `staff` adds `secrets.credentials` (the initial passwords) once; the plug does not
store them.

| Step | Input | Who | Creates |
|---|---|---|---|
| `organization` | `state`, `district`, `local_body`, `all_districts`, or `parent` (a govt organisation id) | superuser | govt organisations (bulk insert for all districts) |
| `facility` | `template` (`clinic`, `pharmacy`), `name`, `address`, `pincode`, `phone_number`, optional `facility_type`, `latitude`, `longitude`, `features`, `is_public`, `geo_organization`, `departments`, `locations`; or `facility` to re-apply | superuser or `can_create_facility`; a facility admin to re-apply | the facility, departments, rooms and beds, room links |
| `roles` | `role_organizations` | superuser | the custom role Receptionist, role organisations |
| `staff` | `facility`, `members[]` (`username`, `first_name`, `last_name`, `phone_number`, `gender`, `role`, `department`, `email`, `password`, `invite`; `invite` needs `email`) | facility admin with `can_create_user` | users, department memberships, and (when a superuser applies) role organisation memberships with the role Member |
| `valuesets` | `system`, `selected[]` | superuser | missing CARE system value sets, clinic value sets |
| `questionnaires` | `facility`, `selected[]` | facility admin | facility questionnaires, department links |
| `catalog` (optional) | `facility`, `selected[]` | facility admin | facility medicines (needs the CARE terminology server) |
| `demo_data` (optional) | `facility` | facility admin | 5 demo patients, 5 open visits |

A facility admin holds `can_create_facility_organization` in the root organisation (Administration) of the
facility. In CARE 3.1 that is the role Facility Admin.

## Develop

```bash
git clone -b ENG-737-backup-backup https://github.com/ohcnetwork/care.git ../care31
plugs/care_pi_onboarding/scripts/test-in-docker.sh ../care31        # build the CARE dev image, run the tests
uvx ruff check plugs/care_pi_onboarding && uvx ruff format --check plugs/care_pi_onboarding
plugs/care_pi_onboarding/scripts/smoke.sh http://127.0.0.1:9000 <superuser> '<password>'   # each step twice
```

`test-in-docker.sh` starts Postgres 17 and Redis 8, mounts this directory, puts it on `PYTHONPATH`, sets
`ADDITIONAL_PLUGS`, checks for missing migrations, and runs `python manage.py test care_pi_onboarding`. Set
`CARE_TEST_IMAGE` to reuse an image you built before.
