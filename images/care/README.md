# care (solution image)

This image is the upstream CARE image plus the backend plugs of one solution.
The `care-api`, `care-worker`, and `care-beat` containers of care-pi run it.

Image name: `ghcr.io/rithviknishad/care-pi/care`

## Inputs

| Build argument | Required | Meaning |
|---|---|---|
| `BASE` | yes (CI and the agent always set it) | The upstream image. Use a digest reference, for example `ghcr.io/ohcnetwork/care@sha256:...`. The default is `ghcr.io/ohcnetwork/care:latest`. |
| `ADDITIONAL_PLUGS` | no | A JSON list of backend plugs. The default is `[]`. An empty list installs nothing. |

Each entry of `ADDITIONAL_PLUGS` has the fields of the upstream `Plug` class (`plugs/plug.py`):

```json
[
  {
    "name": "care_multihost_files_be",
    "package_name": "git+https://github.com/ohcnetwork/care_multihost_files_be.git",
    "version": "@<sha or ref>",
    "configs": {}
  }
]
```

- `name` is the Django app name of the plug.
- `package_name` is a pip requirement without a version.
- `version` is appended to `package_name`. It starts with `@` for a git ref or a git sha.
- `configs` is a JSON object. Care passes it to the plug.

## What the Containerfile does

1. The builder stage starts from `BASE` and switches to `root`.
   It installs `build-essential`, `libpq-dev`, `libffi-dev`, and `git`.
   It runs `python install_plugins.py`.
   The script reads `ADDITIONAL_PLUGS` and runs `pip install` into `/app/.venv`.
2. The runtime stage starts from `BASE`.
   It copies `/app/.venv` from the builder with `--chown=django:django`.
   It sets `ENV ADDITIONAL_PLUGS`.

The runtime stage has no compiler.
It keeps the user, the entrypoint, the command, and the health check of the base image.

## Upstream paths

We checked these paths in `docker/prod.Dockerfile` and `install_plugins.py` of `ohcnetwork/care`.
We checked them again in the `ghcr.io/ohcnetwork/care:latest` image for `linux/arm64`.

| Item | Value |
|---|---|
| Working directory | `/app` |
| Virtual environment | `/app/.venv` (first on `PATH`) |
| Runtime user | `django` (uid 100) |
| Plug installer | `/app/install_plugins.py`, which calls `plug_config.manager.install()` |
| Plug variable | `ADDITIONAL_PLUGS`, a JSON list. Upstream sets it at build time only. |

The paths match `plans/01-architecture.md` section 10.
The Containerfile needed no change for them.
We added a default for `BASE`, a default for `ADDITIONAL_PLUGS`, and the OCI labels.

## Notes

- Care reads `ADDITIONAL_PLUGS` at run time too.
  The Care configuration adds the plugs to `INSTALLED_APPS`.
  The agent writes the same JSON into `backend.env`.
  A value in `backend.env` replaces the value in the image.
- Do not put secrets in `configs` of a solution.
  The image stores the JSON in an environment variable.
  The agent adds secret values at run time.
- A plug that needs a system library at run time is not supported.
  The runtime stage does not install packages.
  No plug in the Clinic solution needs one.
- With `[]`, the build adds no files to the virtual environment.
  The layer that holds `/app/.venv` stays almost equal to the layer of the base image.
  On 2026-10-30, Docker reported an image size 660 bytes larger than the base.
- The label `org.opencontainers.image.base.name` holds the `BASE` value.

## How CI builds it

The workflow `.github/workflows/solution-image.yml` builds the image.

1. It reads `solutions/<solution>/solution.yaml`.
2. It resolves `care.image:care.ref` to a digest with `skopeo`.
3. It builds the `ADDITIONAL_PLUGS` JSON from the plugs that have `enabled: true` and a `backend`.
4. It builds the image for `linux/arm64` and `linux/amd64`.
   It pushes the tag `<solution>-<version>`.
   On `main`, it also pushes `<solution>-latest`.

For Phase 1, `ADDITIONAL_PLUGS` is `[]` for `care-clinic`.
The only backend plug in the default set has `auto: when_multihost`.

## How the agent builds it locally

The agent uses a quadlet `.build` unit when the operator changes a backend plug or a Care ref.
The unit builds from this Containerfile.
The agent passes `BASE` and `ADDITIONAL_PLUGS` as build arguments.
The agent never changes this file.
When you change this file, you also change the local build.

Run the same build by hand from the repository root.
Podman finds the `Containerfile` by itself:

```sh
podman build images/care \
  --build-arg BASE=ghcr.io/ohcnetwork/care:latest \
  --build-arg ADDITIONAL_PLUGS='[]' \
  -t care-pi/care:dev
```

Docker looks for a file with the name `Dockerfile`. Name the file with `-f`:

```sh
docker build -f images/care/Containerfile images/care \
  --build-arg BASE=ghcr.io/ohcnetwork/care:latest \
  --build-arg ADDITIONAL_PLUGS='[]' \
  -t care-pi/care:dev
```

Check the result:

```sh
docker run --rm --entrypoint python care-pi/care:dev -c 'import django; print(django.__version__)'
docker run --rm --entrypoint printenv care-pi/care:dev ADDITIONAL_PLUGS
```
