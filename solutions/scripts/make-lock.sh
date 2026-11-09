#!/usr/bin/env bash
# make-lock.sh: make a solution lock (docs/solutions.md, section 3) and a local index.json on a dev
# machine. It writes the same lock as the workflow solution-lock: both call "care-pi solution lock".
#
# The output is a dist directory with the layout of the release assets (<tag>/<asset>):
#   OUT/lock-<solution>-<version>/<solution>-<version>.lock.yaml (+ .sha256)
#   OUT/fe-<solution>-<sha12>-<build id>/care_fe-<solution>-<sha12>-<build id>.tar.gz (+ .sha256)
#   OUT/plug-<slug>-<sha12>/plug-<slug>-<sha12>.tar.gz (+ .sha256): each --plug-bundle
#   OUT/index/index.json (+ .sha256): each lock of OUT, with dist_base = --dist-base (default file://OUT/)
# Copy OUT to a device (for example to /var/lib/care-pi-dist) and set updates.index_url to
# file:///var/lib/care-pi-dist/index/index.json; make the locks with --dist-base file:///var/lib/care-pi-dist/.
#
# Each identity that a flag does not give is resolved: images with skopeo, git refs with git ls-remote.
# --offline resolves nothing: each identity must come from a flag. An FE plug uses its url (external),
# or a bundle of --plug-bundle (built, as plug-bundle.yml makes it); the workflow solution-lock builds the plug bundles.
# Needs: bash, python3, go (or --care-pi BIN), and (online) skopeo and git.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: solutions/scripts/make-lock.sh --solution NAME --version YYYY.MM.N [options]

Options:
  --solution-file FILE      solution.yaml (default solutions/NAME/solution.yaml)
  --notes TEXT              release notes (the first line is the summary of the index)
  --out DIR                 the dist directory (default ./dist-local)
  --dist-base URL           dist_base of index.json (default file://<absolute OUT>/)
  --care-digest DIGEST      the care image digest (default: resolve care.image:care.ref; with
                            care.source git: resolve --care-base-image:<sha12>)
  --care-git-sha SHA        the git sha of that image (default: the label org.opencontainers.image.revision;
                            with care.source git: resolve care.ref in care.repo)
  --care-base-image REPO    care.source git: the repository of the image that CI built from the source
                            (default: care.image, else ghcr.io/rithviknishad/care-pi/care-base)
  --solution-image REF      the solution image with a digest, REPO@sha256:... (default: resolve
                            ghcr.io/rithviknishad/care-pi/care:NAME-latest)
  --care-fe-sha SHA         the care_fe sha (default: resolve care_fe.ref)
  --fe-bundle FILE          the default FE bundle (tar.gz) for the FE build id of the solution
  --fe-url REF              use a published FE bundle: <tag>/<asset> (needs --fe-sha256 and --fe-size)
  --fe-sha256 HEX           the SHA-256 of --fe-url
  --fe-size BYTES           the size of --fe-url
  --plug ID=SHA             the backend sha of a plug (repeat; default: resolve the ref)
  --plug-bundle ID=FILE     a built FE plug bundle (tar.gz of plug-bundle.yml, with care-pi-plug.json) (repeat)
  --image NAME=DIGEST       the digest of a base image: postgres, caddy, redis, s3, node (repeat; default: resolve)
  --min-agent vX.Y.Z        min_agent_version (default: solution.yaml, else v0.1.0, the first agent release)
  --offline                 resolve nothing
  --care-pi BIN             the care-pi binary (default: go run ./agent/cmd/care-pi)
EOF
}

SOLUTION="" VERSION="" SOLUTION_FILE="" NOTES="" OUT="./dist-local" DIST_BASE=""
CARE_DIGEST="" CARE_GIT_SHA="" CARE_BASE_IMAGE="" SOLUTION_IMAGE="" CARE_FE_SHA="" FE_BUNDLE="" FE_URL="" FE_SHA256="" FE_SIZE=""
MIN_AGENT="" OFFLINE=0 CARE_PI="" PLUG_ARGS="" PLUG_BUNDLES="" IMAGE_ARGS=""

while [ $# -gt 0 ]; do
  case "$1" in
    --solution) SOLUTION="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --solution-file) SOLUTION_FILE="$2"; shift 2 ;;
    --notes) NOTES="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --dist-base) DIST_BASE="$2"; shift 2 ;;
    --care-digest) CARE_DIGEST="$2"; shift 2 ;;
    --care-git-sha) CARE_GIT_SHA="$2"; shift 2 ;;
    --care-base-image) CARE_BASE_IMAGE="$2"; shift 2 ;;
    --solution-image) SOLUTION_IMAGE="$2"; shift 2 ;;
    --care-fe-sha) CARE_FE_SHA="$2"; shift 2 ;;
    --fe-bundle) FE_BUNDLE="$2"; shift 2 ;;
    --fe-url) FE_URL="$2"; shift 2 ;;
    --fe-sha256) FE_SHA256="$2"; shift 2 ;;
    --fe-size) FE_SIZE="$2"; shift 2 ;;
    --plug) PLUG_ARGS="$PLUG_ARGS $2"; shift 2 ;;
    --plug-bundle) PLUG_BUNDLES="$PLUG_BUNDLES $2"; shift 2 ;;
    --image) IMAGE_ARGS="$IMAGE_ARGS $2"; shift 2 ;;
    --min-agent) MIN_AGENT="$2"; shift 2 ;;
    --offline) OFFLINE=1; shift ;;
    --care-pi) CARE_PI="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "make-lock.sh: unknown option $1" >&2; usage >&2; exit 2 ;;
  esac
done

die() { echo "make-lock.sh: $*" >&2; exit 1; }
[ -n "$SOLUTION" ] || die "--solution is required"
[[ "$VERSION" =~ ^[0-9]{4}\.[0-9]{1,2}\.[0-9]+$ ]] || die "--version must be YYYY.MM.N"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SOLUTION_FILE="${SOLUTION_FILE:-$ROOT/solutions/$SOLUTION/solution.yaml}"
[ -f "$SOLUTION_FILE" ] || die "no solution file $SOLUTION_FILE"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
DIST_BASE="${DIST_BASE:-file://$OUT/}"
WORK="$OUT/.work-$VERSION"
rm -rf "$WORK"
mkdir -p "$WORK/solutions/$SOLUTION"
trap 'rm -rf "$WORK"' EXIT
cp "$SOLUTION_FILE" "$WORK/solutions/$SOLUTION/solution.yaml"

carepi() {
  if [ -n "$CARE_PI" ]; then "$CARE_PI" "$@"; else (cd "$ROOT" && go run ./agent/cmd/care-pi "$@"); fi
}
# lookup KEY "k1=v1 k2=v2": the value of KEY, or "".
lookup() {
  local kv
  for kv in $2; do
    if [ "${kv%%=*}" = "$1" ]; then echo "${kv#*=}"; return; fi
  done
}
# field EXPR: a field of solution.yaml ("care.image"), or "plugs" (one line per plug: id, backend repo, backend ref).
# care-pi validates the file and prints it as JSON.
field() {
  [ -f "$WORK/solution.json" ] || carepi solution validate --json "$SOLUTION_FILE" > "$WORK/solution.json"
  python3 - "$WORK/solution.json" "$1" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
if sys.argv[2] == "plugs":
    for p in doc.get("plugs") or []:
        b = p.get("backend") or {}
        print("\t".join([p["id"], b.get("package", "").removeprefix("git+") or "-", b.get("ref") or "-"]))
else:
    v = doc
    for k in sys.argv[2].split("."):
        v = (v or {}).get(k)
    print(v if v is not None else "")
PY
}
sha256_of() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
size_of() { wc -c < "$1" | tr -d ' '; }
need_online() { [ "$OFFLINE" = 0 ] || die "--offline: $1 is required"; }
# digest_of REF: the manifest list digest of an image reference.
digest_of() {
  need_online "the digest of $1"
  skopeo inspect --raw "docker://$1" > "$WORK/manifest.json"
  skopeo manifest-digest "$WORK/manifest.json"
}
# git_sha_of REPO REF
git_sha_of() {
  if [[ "$2" =~ ^[0-9a-f]{40}$ ]]; then echo "$2"; return; fi
  need_online "the sha of $1@$2"
  git ls-remote "$1" "refs/heads/$2" "refs/tags/$2^{}" "refs/tags/$2" | head -1 | cut -f1
}

CARE_SOURCE="$(field care.source)"; CARE_SOURCE="${CARE_SOURCE:-image}"
CARE_IMAGE="$(field care.image)"
CARE_REF="$(field care.ref)"; CARE_REF="${CARE_REF:-latest}"
CARE_FE_REPO="$(field care_fe.repo)"; CARE_FE_REF="$(field care_fe.ref)"
MIN_AGENT="${MIN_AGENT:-$(field min_agent_version)}"; MIN_AGENT="${MIN_AGENT:-v0.1.0}"

if [ "$CARE_SOURCE" = git ]; then
  # S71: CI builds docker/prod.Dockerfile of the resolved sha into <base image>:<sha12> (solution-lock.yml).
  CARE_IMAGE="${CARE_BASE_IMAGE:-${CARE_IMAGE:-ghcr.io/rithviknishad/care-pi/care-base}}"
  [ -n "$CARE_GIT_SHA" ] || CARE_GIT_SHA="$(git_sha_of "$(field care.repo)" "$CARE_REF")"
  [[ "$CARE_GIT_SHA" =~ ^[0-9a-f]{40}$ ]] || die "cannot resolve care $CARE_REF in $(field care.repo)"
  [ -n "$CARE_DIGEST" ] || CARE_DIGEST="$(digest_of "$CARE_IMAGE:${CARE_GIT_SHA:0:12}")"
else
  [ -z "$CARE_BASE_IMAGE" ] || die "--care-base-image needs care.source git"
  CARE_IMAGE="${CARE_IMAGE:-ghcr.io/ohcnetwork/care}"
  [ -n "$CARE_DIGEST" ] || CARE_DIGEST="$(digest_of "$CARE_IMAGE:$CARE_REF")"
  if [ -z "$CARE_GIT_SHA" ] && [ "$OFFLINE" = 0 ]; then
    CARE_GIT_SHA="$(skopeo inspect --override-os linux --override-arch arm64 --format '{{index .Labels "org.opencontainers.image.revision"}}' "docker://$CARE_IMAGE@$CARE_DIGEST" || true)"
  fi
  [[ "$CARE_GIT_SHA" =~ ^[0-9a-f]{40}$ ]] || CARE_GIT_SHA=""
fi
[ -n "$SOLUTION_IMAGE" ] || SOLUTION_IMAGE="ghcr.io/rithviknishad/care-pi/care@$(digest_of "ghcr.io/rithviknishad/care-pi/care:$SOLUTION-latest")"
SI_REPO="${SOLUTION_IMAGE%@*}"
SI_DIGEST="${SOLUTION_IMAGE#*@}"
[[ "$SI_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]] || die "--solution-image must be REPO@sha256:<64 hex>"
[ -n "$CARE_FE_SHA" ] || CARE_FE_SHA="$(git_sha_of "$CARE_FE_REPO" "$CARE_FE_REF")"
[[ "$CARE_FE_SHA" =~ ^[0-9a-f]{40}$ ]] || die "cannot resolve care_fe $CARE_FE_REF"

: > "$WORK/images.tsv"
for pair in postgres=ghcr.io/rithviknishad/care-pi/postgres-pgbackrest:17 caddy=ghcr.io/rithviknishad/care-pi/caddy:2 \
  redis=docker.io/library/redis:8-alpine s3=docker.io/versity/versitygw:latest node=docker.io/library/node:22-bookworm-slim; do
  name="${pair%%=*}"
  ref="${pair#*=}"
  d="$(lookup "$name" "$IMAGE_ARGS")"
  [ -n "$d" ] || d="$(digest_of "$ref")"
  printf '%s\t%s\t%s\n' "$name" "$ref" "$d" >> "$WORK/images.tsv"
done

: > "$WORK/plugs.tsv"
while IFS=$'\t' read -r id repo ref; do
  [ "$repo" != "-" ] || { printf '%s\t\n' "$id" >> "$WORK/plugs.tsv"; continue; }
  s="$(lookup "$id" "$PLUG_ARGS")"
  [ -n "$s" ] || s="$(git_sha_of "$repo" "$ref")"
  [[ "$s" =~ ^[0-9a-f]{40}$ ]] || die "cannot resolve the plug $id ($repo@$ref)"
  printf '%s\t%s\n' "$id" "$s" >> "$WORK/plugs.tsv"
done < <(field plugs)

# The FE build id of the default env of the solution (the superset rule, docs/solutions.md section 1.5).
carepi fe env --solution "$SOLUTION" --solutions "$WORK/solutions" > "$WORK/frontend.env"
BUILD_ID="$(carepi fe build-id --care-fe-sha "$CARE_FE_SHA" --env-file "$WORK/frontend.env")"
SHA12="${CARE_FE_SHA:0:12}"
if [ -n "$FE_BUNDLE" ]; then
  tag="fe-$SOLUTION-$SHA12-$BUILD_ID"
  asset="care_fe-$SOLUTION-$SHA12-$BUILD_ID.tar.gz"
  mkdir -p "$OUT/$tag"
  cp "$FE_BUNDLE" "$OUT/$tag/$asset"
  FE_SHA256="$(sha256_of "$OUT/$tag/$asset")"
  FE_SIZE="$(size_of "$OUT/$tag/$asset")"
  echo "$FE_SHA256  $asset" > "$OUT/$tag/$asset.sha256"
  FE_URL="$tag/$asset"
fi
if [ -z "$FE_URL" ] || [ -z "$FE_SHA256" ] || [ -z "$FE_SIZE" ]; then
  die "give --fe-bundle FILE, or --fe-url, --fe-sha256, and --fe-size (FE build id $BUILD_ID)"
fi

# Built FE plug bundles: the slug and the sha come from care-pi-plug.json in the bundle.
: > "$WORK/plugfe.tsv"
for kv in $PLUG_BUNDLES; do
  id="${kv%%=*}"
  file="${kv#*=}"
  [ -f "$file" ] || die "--plug-bundle $id: no file $file"
  meta="$(python3 - "$file" <<'PY'
import json, sys, tarfile
with tarfile.open(sys.argv[1]) as t:
    for name in ("care-pi-plug.json", "./care-pi-plug.json"):
        try:
            m = json.load(t.extractfile(name))
            break
        except KeyError:
            continue
    else:
        sys.exit("the bundle has no care-pi-plug.json")
print(m["slug"], m["sha"])
PY
)" || die "--plug-bundle $id: $file is not a plug bundle"
  slug="${meta% *}"
  psha="${meta#* }"
  [[ "$psha" =~ ^[0-9a-f]{40}$ ]] || die "--plug-bundle $id: the sha in care-pi-plug.json is not 40 hex digits"
  tag="plug-$slug-${psha:0:12}"
  mkdir -p "$OUT/$tag"
  cp "$file" "$OUT/$tag/$tag.tar.gz"
  psum="$(sha256_of "$OUT/$tag/$tag.tar.gz")"
  echo "$psum  $tag.tar.gz" > "$OUT/$tag/$tag.tar.gz.sha256"
  printf '%s\t%s\t%s\t%s\t%s\n' "$id" "$psha" "$tag/$tag.tar.gz" "$psum" "$(size_of "$OUT/$tag/$tag.tar.gz")" >> "$WORK/plugfe.tsv"
done

export SOLUTION VERSION NOTES MIN_AGENT CARE_DIGEST CARE_GIT_SHA CARE_IMAGE CARE_SOURCE SI_REPO SI_DIGEST CARE_FE_SHA BUILD_ID FE_URL FE_SHA256 FE_SIZE WORK
COMMIT="$(cd "$ROOT" && git rev-parse HEAD)"
export COMMIT
python3 - <<'PY'
import json, os
e = os.environ
w = e["WORK"]
images = {}
for line in open(w + "/images.tsv"):
    name, ref, digest = line.rstrip("\n").split("\t")
    images[name] = {"ref": ref, "digest": digest}
plugs = {}
for line in open(w + "/plugs.tsv"):
    pid, sha = (line.rstrip("\n").split("\t") + [""])[:2]
    plugs[pid] = {"backend_sha": sha} if sha else {}
for line in open(w + "/plugfe.tsv"):
    pid, sha, url, sum256, size = line.rstrip("\n").split("\t")
    plugs.setdefault(pid, {}).update({"frontend_sha": sha, "frontend_url": url, "frontend_sha256": sum256,
                                      "frontend_size_bytes": int(size)})
keys = [l.split("=", 1)[0] for l in open(w + "/frontend.env") if l.strip() and not l.startswith("#")]
data = {
    "solution_file": w + "/solutions/" + e["SOLUTION"] + "/solution.yaml",
    "version": e["VERSION"],
    "created_by": e.get("USER", "dev") + " (make-lock.sh)",
    "notes": e["NOTES"],
    "min_agent_version": e["MIN_AGENT"],
    "commit": e["COMMIT"],
    "care": {"digest": e["CARE_DIGEST"], "git_sha": e["CARE_GIT_SHA"]} | ({"image": e["CARE_IMAGE"]} if e["CARE_SOURCE"] == "git" else {}),
    "solution_image": {"image": e["SI_REPO"], "tag": e["SOLUTION"] + "-" + e["VERSION"], "digest": e["SI_DIGEST"]},
    "care_fe_sha": e["CARE_FE_SHA"],
    "fe": {"build_id": e["BUILD_ID"], "url": e["FE_URL"], "sha256": e["FE_SHA256"], "size_bytes": int(e["FE_SIZE"]), "env_keys": keys},
    "plugs": plugs,
    "images": images,
}
json.dump(data, open(w + "/input.json", "w"), indent=2)
PY

tag="lock-$SOLUTION-$VERSION"
asset="$SOLUTION-$VERSION.lock.yaml"
mkdir -p "$OUT/$tag"
carepi solution lock --input "$WORK/input.json" > "$OUT/$tag/$asset"
echo "$(sha256_of "$OUT/$tag/$asset")  $asset" > "$OUT/$tag/$asset.sha256"

# index.json of each lock in OUT.
args=()
for f in "$OUT"/lock-*/*.lock.yaml; do args+=(--lock "$f"); done
mkdir -p "$OUT/index"
carepi solution index --dist-base "$DIST_BASE" "${args[@]}" > "$OUT/index/index.json"
echo "$(sha256_of "$OUT/index/index.json")  index.json" > "$OUT/index/index.json.sha256"
echo "lock:  $OUT/$tag/$asset"
echo "index: $OUT/index/index.json (dist_base $DIST_BASE)"
