# Releasing

This page lists the artefacts of care-pi and the workflow that makes each one.
Each workflow is in `.github/workflows/`. A workflow that publishes a GitHub release uses the release name as the
identity of the artefact: a second run with the same inputs does nothing.

| Artefact | Workflow | Trigger | Where it goes |
|---|---|---|---|
| The agent binaries `care-pi_linux_arm64`, `care-pi_linux_amd64` (with the console) | `ci.yml` | each push and pull request | workflow artifacts (a dev build) |
| An agent release: the two binaries, `SHA256SUMS`, `install.sh`, `uninstall.sh` | `release.yml` | a push of a tag `vX.Y.Z` | release `vX.Y.Z`, marked as the latest; the installer downloads from it (`docs/install.md`, "Agent download") |
| The Caddy image (with the Coraza WAF module) and the Postgres image with pgBackRest | `images.yml` | a push to `images/**`, each Monday, by hand | `ghcr.io/rithviknishad/care-pi/*` |
| A solution image (the backend plugs of a solution) | `solution-image.yml` (`solution-lock.yml` builds it in its own job) | by hand | GHCR |
| The default care_fe bundle of a solution | `fe-bundle.yml` | called by `solution-lock.yml`, by hand | release `fe-<solution>-<sha12>-<build id>` |
| One FE plug bundle | `plug-bundle.yml` | called by `solution-lock.yml`, by hand | release `plug-<slug>-<sha12>` |
| A solution lock | `solution-lock.yml` | by hand (`solution`, `version`, `notes`) | release `lock-<solution>-<version>` (the lock, `.sha256`, and `.minisig` when the signing key is set) |
| `index.json` | `solution-index.yml` | after each lock, a push that changes a `withdrawn.yaml`, by hand | release `index` (`index.json`, `.minisig`) |
| The Raspberry Pi image of a lock | `pi-image.yml` | a published lock release, by hand | release `image-<version>`, and `os_list.json` of the release `imager` |
| The DNS/ACME service | `dns-service.yml` | a push to `services/dns/**` on `main`, by hand | Cloudflare Workers (`docs/dns-service.md`) |
| The docs site | `docs-site.yml` | a push to `docs/**`, by hand | workflow artifact `docs-site` (GitHub Pages when the repository is public) |

## An agent release

An agent release has a tag `vX.Y.Z`. Only such a version is a release build: `care-pi version` prints it, setup cannot
skip the backups, and the agent compares it with `min_agent_version` of a lock (`agent-too-old`). Each other version
(`dev`, a commit, `v0.1.0-3-g9ff6634`, `-dirty`) is a dev build, and a dev build counts as the newest agent
(`docs/solutions.md` section 7.4).

1. Check that `ci.yml` passed on the commit of `main`.
2. Make and push an annotated tag: `git tag -s v0.1.0 -m "care-pi v0.1.0" && git push origin v0.1.0`.
3. The workflow `release` runs on the tag:
   - it stops when the tag is not `vX.Y.Z`;
   - `go vet` and `go test`;
   - `make build-linux VERSION=<tag>` builds the console and `care-pi_linux_arm64`, `care-pi_linux_amd64` with the
     version of the tag (`-ldflags -X …/buildinfo.Version`), and checks that `care-pi version` prints it;
   - `SHA256SUMS` of the two binaries, `install.sh`, and `uninstall.sh` (the format of `sha256sum`);
   - the release `vX.Y.Z` with the 5 files, marked as the latest (`--latest`). The notes have the install command
     and the commits since the previous tag `vX.Y.Z`. From the second release, the notes of GitHub (`--generate-notes`
     from the previous tag) follow; the first release has only the commits, because GitHub would start at the newest
     release of any kind (a bundle).
4. Check the release: `curl -fsSL https://github.com/rithviknishad/care-pi/releases/latest/download/SHA256SUMS`.

The installer downloads `releases/download/<tag>/` with `--version <tag>`, else `releases/latest/download/`
(`docs/install.md`, "Agent download"). Every other workflow makes its releases with `--latest=false`, so "latest" is
always an agent release. `installer/test-download.sh` (a step of `ci.yml`) checks that the URLs and the file names of
the installer match this workflow. A second run of the same tag uploads the files again (`--clobber`).

A lock made after the tag gets `min_agent_version` of the newest tag (`solution-lock.yml`), so an older agent refuses
it with `agent-too-old`. A lock of `make-lock.sh` without `--min-agent` gets `v0.1.0`.

Do not move or delete a published tag: a device and the installer use the files of the release by their tag.

## A new solution release

1. Change `solutions/<solution>/solution.yaml` (the refs of Care, care_fe, and the plugs) on `main`.
2. Run `solution-lock.yml` with the solution and the next version. It builds the images and bundles, assembles and
   validates the lock, signs it when the secret `MINISIGN_KEY` is set, publishes the lock release, and writes
   `index.json` again.
3. Commit the lock from the job summary to `solutions/<solution>/locks/`.
4. Publish the lock release by hand (a release that a workflow makes with `GITHUB_TOKEN` starts no other workflow), or
   run `pi-image.yml`, to build the image of the lock.

To withdraw a lock, add it to `solutions/<solution>/locks/withdrawn.yaml` on `main`. `solution-index.yml` writes
`index.json` again without it (`docs/solutions.md`, section 5.4).

## Signing

The signing of `index.json` and the locks is optional in this version (S55). `docs/solutions.md`, section 18,
describes the key ceremony, the secret `MINISIGN_KEY`, the public key `solutions/keys/care-pi.pub`, and the rotation.
A device checks the signatures when `updates.require_signed` is `true`.

## The docs site

`docs-site.yml` builds this site with MkDocs Material (`docs/site/build.sh`): the pages of `docs/`, the architecture
and the plan index of `plans/`, and the API reference (Redoc, from `docs/api/openapi.yaml`). Without the repository
variable `DOCS_PAGES`, the workflow uploads the site as the artifact `docs-site` only. Download it from the run page and open
`index.html`.

Build it on a computer:

```sh
python3 -m venv .venv && .venv/bin/pip install mkdocs-material==9.8.0
PATH="$PWD/.venv/bin:$PATH" sh docs/site/build.sh     # writes site/
python3 -m http.server -d site 8000
```

To publish the site on GitHub Pages (the repository is public):

1. In Settings > Pages, set the source to **GitHub Actions**.
2. In Settings > Secrets and variables > Actions > Variables, add the repository variable `DOCS_PAGES` with the value
   `true`. On `main`, the job `build` then also uploads the site with `actions/upload-pages-artifact`, and the job
   `deploy` runs `actions/deploy-pages` (it has the `pages: write` and `id-token: write` permissions). No change of
   the workflow is needed.
