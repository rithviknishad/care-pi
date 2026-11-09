# Fixtures of care_pi_onboarding

The onboarding steps read these JSON files. The plug ships them in the Python package. A step validates each record
with the CARE resource spec (the same validation as the CARE API) before it saves the record.

| File | Step | Content |
|---|---|---|
| `locales/<code>.json` | `organization`, `facility` | The country, the pincode pattern, the government levels, and the states with their districts. |
| `facility_templates.json` | `facility`, `staff` | One template for each kind of facility (`clinic`, `pharmacy`): the CARE facility type, the departments, the rooms, and the default department of each staff role. |
| `roles.json` | `roles`, `staff` | The staff roles. Each role maps to a CARE system role (`care_role`) or defines a custom role (`custom`) with a list of CARE permission slugs. |
| `valuesets.json` | `valuesets` | The short coded lists of the clinic (SNOMED CT concepts). |
| `questionnaires.json` | `questionnaires` | The clinic forms in the CARE questionnaire format. `departments` is `"*"` (each department) or a list of department names. |
| `catalog.json` | `catalog` | The starter medicines in the CARE product knowledge format. |
| `demo.json` | `demo_data` | The synthetic patients of the try-it mode. |

## Rules

- A record has a stable natural key: the name of an organisation, department, or room; the `slug` of a value set or
  questionnaire; the `slug_value` of a medicine; the `key` of a role or demo patient. The steps find existing objects
  by this key. Do not rename a key after a release: the step then makes a second object.
- Question ids in `questionnaires.json` are fixed UUID4 values. Make new ones with `python -c "import uuid; print(uuid.uuid4())"`.
- Do not put a coded field that CARE checks on its terminology server (`code` or `unit` of a question) in a
  questionnaire. Such a field needs internet at apply time. Only the `catalog` step needs the terminology server.
- A permission slug in `roles.json` must exist in CARE (`python manage.py sync_permissions_roles` makes them).
  The `roles` step fails with the list of unknown slugs.

## Add a locale

1. Copy `locales/in.json` to `locales/<code>.json`. `<code>` is letters and digits only, for example `ke` or `lk`.
2. Set `code`, `country`, `phone_example`, and `pincode_pattern` (a regular expression for the whole pincode; an empty
   string turns the check off).
3. Set `levels`. Each level has a `label` and the name of its child level (`children`). The `organization` step
   writes the level names to `metadata.govt_org_type` and `metadata.govt_org_children_type` of each organisation.
4. Replace `states`. Each state has a `name`, a `kind` (`state` or `union_territory`), and `districts` (a list of
   names). Names must be unique within their parent, without regard to case. A state can also have `aliases`: other
   names of the same root organisation in CARE. The `organization` step uses a root with an alias name and does not
   make a second root.
5. Set `ONBOARDING_LOCALE` to `<code>` in the plug configs (`ADDITIONAL_PLUGS[].configs`) or in the environment.
6. Run the tests: `python manage.py test care_pi_onboarding`.

## Sources and licences

- `locales/in.json`: the states and districts come from `data_source/states-and-districts.json` of
  [ohcnetwork/care_onboarding_fe](https://github.com/ohcnetwork/care_onboarding_fe) (MIT licence,
  Copyright (c) 2026 Open Healthcare Network), which took them from `ohcnetwork/care_desktop`. The suffixes `(UT)` and
  `(NCT)` became `"kind": "union_territory"`, and the names with the suffix are the `aliases`, because a CARE that
  care_onboarding_fe set up has roots with these names. The licence text is `locales/LICENSE-care_onboarding_fe.txt`.
- `catalog.json`: the units, dose form, route, and the three product codes come from `care/fixtures/constants.py` of
  [ohcnetwork/care](https://github.com/ohcnetwork/care) (MIT licence).
- The other files are original to care-pi.
