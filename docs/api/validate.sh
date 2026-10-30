#!/bin/sh
# Validate the console API contract (docs/api/openapi.yaml).
# 1. Redocly CLI lints the document with docs/api/.redocly.yaml.
# 2. The Go test loads the document with kin-openapi, validates it,
#    checks the contract rules, and validates every example.
# Needs: node (npx) and go. Run from any directory.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

echo "==> redocly lint"
npx --yes @redocly/cli@latest lint --config docs/api/.redocly.yaml docs/api/openapi.yaml

echo "==> go test ./docs/api"
go test ./docs/api/
