# Solutions

A solution is a curated set of plugs with default values.
The operator picks one solution in the setup wizard.
Version 1 of care-pi has one solution: `care-clinic`.

## Layout

```text
solutions/
  README.md                      This file.
  schema/solution.schema.json    JSON Schema (draft 2020-12) of solution.yaml.
  care-clinic/solution.yaml      The CARE Clinic solution.
```

One solution is one directory with one file, `solution.yaml`.
The directory name must equal the `name` field in the file.

## The format of solution.yaml

The schema describes each field.
The schema is strict.
It rejects unknown fields at every level.

| Field | Meaning |
|---|---|
| `apiVersion`, `kind` | Always `care-pi/v1` and `Solution`. |
| `name`, `title`, `description` | The identifier, the name for the operator, and a short text. |
| `care` | The upstream backend image: `image` and `ref`. The ref is a tag to track or a `sha256:` digest to pin. |
| `care_fe` | The frontend source: `repo` and `ref`. The ref is a branch or tag to track, or a git sha to pin. |
| `plugs` | The list of plugs. |
| `env` | Default environment variables. `backend` applies to the backend containers. `frontend` applies to the frontend build (`REACT_*`). |
| `defaults` | Defaults for `config.yaml`: `size_preset` and `clock_policy`. |

A plug has these fields:

| Field | Meaning |
|---|---|
| `id`, `label`, `description` | The identifier and the text for the operator. The `id` is unique in the solution. |
| `enabled` | The base state. CI builds the solution image from the enabled plugs that have a `backend` part. |
| `auto` | A rule that lets the agent enable the plug. The only rule is `when_multihost`. |
| `requires_internet` | True when the plug needs internet access at run time. |
| `backend` | A Python package: `name`, `package`, `ref`, and `configs`. It becomes one entry of `ADDITIONAL_PLUGS`. |
| `frontend` | A module federation bundle: `slug`, and `repo` with `ref`, or `url`, or both. `meta` goes to the `plug_config` row of CARE. |
| `env` | Variables that the plug needs. The renderer merges them into `env` when the plug is enabled. |
| `settings` | The form fields that the console shows for this plug. |
| `setup_steps` | Steps that the operator does by hand after the agent applies the plug. |

Rules to remember:

- A plug that overrides a frontend component must list the component in
  `env.frontend.REACT_MFE_REGISTERED_COMPONENTS`.
  The override has no effect when the frontend build does not register the component.
  The renderer joins the values of all enabled plugs with commas.
- A frontend plug with a `url` is loaded from that URL. CI does not build it.
- A plug with `auto` is not in the prebuilt solution image.
  The agent builds a local image when it enables the plug.
- Do not write secrets in `backend.configs`.
  Add an entry to `settings` with the type `secret`.
  The agent stores the value in `/etc/care-pi/secrets/`.
- Keep the text for the operator short and in plain words.

## Add a solution

1. Create the directory `solutions/<name>/`. Use lowercase letters, digits, and single hyphens.
2. Copy `solutions/care-clinic/solution.yaml` to the new directory.
3. Change `name`, `title`, `description`, the refs, the plugs, `env`, and `defaults`.
4. Validate the file against the schema (see the next section).
5. Open a pull request.
6. After the merge, run the workflow `solution-image` with the new name.

## Validate a file

Use any JSON Schema validator that supports draft 2020-12.
These commands work:

```sh
pipx run check-jsonschema --schemafile solutions/schema/solution.schema.json \
  solutions/care-clinic/solution.yaml
```

The command `uvx check-jsonschema` with the same arguments does the same.
The next command uses a Node.js validator:

```sh
npx --yes ajv-cli@5 validate --spec=draft2020 --validate-formats=false \
  -s solutions/schema/solution.schema.json -d solutions/care-clinic/solution.yaml
```

The agent will provide the same check as `care-pi solution validate` (planned).
A schema cannot check that plug ids are unique. The agent and CI check this rule in code.

## What CI does with a solution

Phase 1 (now): the workflow `.github/workflows/solution-image.yml`.

1. It validates `solutions/<name>/solution.yaml` against the schema.
2. It resolves `care.image` and `care.ref` to a digest with `skopeo`.
3. It builds the `ADDITIONAL_PLUGS` JSON from the enabled plugs that have a `backend` part.
4. It builds `images/care` for `linux/arm64` and `linux/amd64`.
   The base is `<care.image>@<digest>`.
5. It pushes `ghcr.io/rithviknishad/care-pi/care:<name>-<version>`.
   On `main`, it also pushes `ghcr.io/rithviknishad/care-pi/care:<name>-latest`.
6. It writes the digest and the plug list to the job summary.

The workflow runs on each push to `main` that changes `solutions/` or `images/care/`.
A push build uses the version `0.0.0-dev` and the default solution `care-clinic`.
A manual run takes a `solution` and a `version`.
The tag `<name>-latest` means the newest build on `main`.

Phase 4 (planned): CI finds all `solutions/*/solution.yaml` files.
CI resolves every ref to a digest or a sha and writes a lock file.
CI builds the frontend plug bundles and the default frontend bundle.
CI publishes an `index.json` that the devices read.

## Calendar versions of locks

A lock pins every component of a solution to a digest or a sha.
The lock is the unit that devices install and roll back.
A lock has a calendar version in the form `YYYY.MM.N`.

- `YYYY.MM` is the year and the month of the release.
- `N` is the number of the release in that month. The first release has `N` = 0.
- Example: `2026.11.0` is the first lock of November 2026.
- The lock file name is `<name>-<version>.lock.yaml`, for example `care-clinic-2026.11.0.lock.yaml`.
- The git tag of a release is `<name>/<version>`, for example `care-clinic/2026.11.0`.
- A build that is not a release uses the version `0.0.0-dev`.

The agent and the Pi image use semantic versions.
They do not use calendar versions.
