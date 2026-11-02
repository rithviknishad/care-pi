#!/bin/sh
# Build the docs site into site/ (docs/releasing.md, "The docs site").
# Needs: mkdocs-material 9 on PATH, and node (npx) for the API reference.
# Run from any directory.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

src=.docs-site
rm -rf "$src" site
mkdir -p "$src/developer"
cp -R docs/. "$src/"
rm -rf "$src/site"
cp plans/01-architecture.md "$src/developer/architecture.md"
cp plans/README.md "$src/developer/plans.md"
# A README.md below the top directory is not a page that the nav can name.
mv "$src/api/README.md" "$src/api/contract.md"

echo "==> API reference (Redoc)"
npx --yes @redocly/cli@2.72.1 build-docs docs/api/openapi.yaml --output "$src/api/redoc.html" >/dev/null 2>&1
cat >"$src/api/reference.md" <<'MD'
# API reference

The reference of the console API, made with Redoc from `docs/api/openapi.yaml`.
[Open it on its own page](redoc.html).

<iframe src="redoc.html" style="width: 100%; height: 80vh; border: 0;" title="API reference"></iframe>
MD

echo "==> mkdocs build"
NO_MKDOCS_2_WARNING=1 mkdocs build --quiet --config-file docs/site/mkdocs.yml
rm -rf "$src"
echo "The site is in site/ ($(find site -type f | wc -l | tr -d ' ') files). Open site/index.html."
