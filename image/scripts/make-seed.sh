#!/usr/bin/env bash
# make-seed.sh: the pre-migrated database of the Raspberry Pi image (S72, docs/image.md section 6.5).
#
# It runs PostgreSQL (the postgres-pgbackrest image of the lock), Redis, and the CARE image of the lock with docker on
# the build host (a Mac with Apple silicon, a GitHub ubuntu-24.04-arm runner), runs the steps of celery_beat.sh that
# write the database (migrate, sync_permissions_roles, sync_valueset) against an empty database, stops PostgreSQL
# cleanly, and writes the data directory as OUT/pg-<sha12>.tar.zst with OUT/pg-<sha12>.json.
#
# The database does not depend on the secrets of a device: the password of the user postgres and DJANGO_SECRET_KEY
# are thrown away; the first start on the device sets the password of the device (the agent, docs/image.md 6.5).
# No CARE user is created: the setup makes the superuser.
#
# Usage: image/scripts/make-seed.sh --care IMAGE --postgres IMAGE --redis IMAGE --sha SHA --out DIR [--platform P]
# Needs: docker (or DOCKER=podman), zstd, jq.
set -euo pipefail

usage() {
	echo "Usage: make-seed.sh --care IMAGE --postgres IMAGE --redis IMAGE --sha SHA --out DIR [--platform linux/arm64]"
}

CARE="" POSTGRES="" REDIS="" SHA="" OUT="" PLATFORM="linux/arm64"
while [ $# -gt 0 ]; do
	case "$1" in
	--care) CARE="$2"; shift 2 ;;
	--postgres) POSTGRES="$2"; shift 2 ;;
	--redis) REDIS="$2"; shift 2 ;;
	--sha) SHA="$2"; shift 2 ;;
	--out) OUT="$2"; shift 2 ;;
	--platform) PLATFORM="$2"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) echo "make-seed.sh: unknown option $1" >&2; usage >&2; exit 2 ;;
	esac
done
die() { echo "make-seed.sh: $*" >&2; exit 1; }
if [ -z "$CARE" ] || [ -z "$POSTGRES" ] || [ -z "$REDIS" ] || [ -z "$OUT" ]; then
	usage >&2
	exit 2
fi
[[ "$SHA" =~ ^[0-9a-f]{40}$ ]] || die "--sha must be the 40 hex digits of the CARE commit"
command -v zstd > /dev/null || die "zstd is not installed"
DOCKER="${DOCKER:-docker}"
SHA12="${SHA:0:12}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

id="care-seed-$$"
log() { printf '[seed] %s\n' "$*"; }
cleanup() {
	"$DOCKER" rm -f "$id-db" "$id-redis" "$id-care" > /dev/null 2>&1 || true
	"$DOCKER" network rm "$id" > /dev/null 2>&1 || true
	"$DOCKER" volume rm "$id-pg" > /dev/null 2>&1 || true
}
trap cleanup EXIT

# Throwaway values: the seed must not depend on them.
pw="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
key="$(od -An -N48 -tx1 /dev/urandom | tr -d ' \n')"

"$DOCKER" network create "$id" > /dev/null
"$DOCKER" volume create "$id-pg" > /dev/null
log "postgres: $POSTGRES"
"$DOCKER" run -d --name "$id-db" --network "$id" --platform "$PLATFORM" \
	-e POSTGRES_DB=care -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD="$pw" \
	-v "$id-pg:/var/lib/postgresql/data" "$POSTGRES" postgres -c shared_buffers=128MB -c max_connections=40 > /dev/null
"$DOCKER" run -d --name "$id-redis" --network "$id" --platform "$PLATFORM" "$REDIS" > /dev/null
for _ in $(seq 120); do
	# The entrypoint initializes the directory with a server on the socket only; TCP answers after the restart.
	if "$DOCKER" exec "$id-db" pg_isready -q -h 127.0.0.1 -U postgres -d care; then break; fi
	sleep 1
done
"$DOCKER" exec "$id-db" pg_isready -q -h 127.0.0.1 -U postgres -d care || die "PostgreSQL did not start"

log "care: $CARE (CARE $SHA12)"
t0=$(date +%s)
"$DOCKER" run --rm --name "$id-care" --network "$id" --platform "$PLATFORM" \
	-e DJANGO_SETTINGS_MODULE=config.settings.production -e DJANGO_SECRET_KEY="$key" \
	-e DATABASE_URL="postgres://postgres:$pw@$id-db:5432/care" \
	-e POSTGRES_USER=postgres -e POSTGRES_PASSWORD="$pw" -e POSTGRES_HOST="$id-db" -e POSTGRES_PORT=5432 -e POSTGRES_DB=care \
	-e REDIS_URL="redis://$id-redis:6379/0" -e CELERY_BROKER_URL="redis://$id-redis:6379/0" \
	-e BUCKET_PROVIDER=MINIO -e BUCKET_ENDPOINT=http://localhost:7070 -e BUCKET_REGION=ap-south-1 \
	-e FILE_UPLOAD_BUCKET=patient-bucket -e FACILITY_S3_BUCKET=facility-bucket -e USE_SMS=False \
	-e DJANGO_SECURE_SSL_REDIRECT=False \
	"$CARE" bash -c './wait_for_db.sh && python manage.py migrate --noinput && python manage.py sync_permissions_roles && python manage.py sync_valueset'
t1=$(date +%s)
log "migrate and sync: $((t1 - t0)) s"

"$DOCKER" exec "$id-db" psql -q -U postgres -d care -c CHECKPOINT
tables="$("$DOCKER" exec "$id-db" psql -At -U postgres -d care -c "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'")"
migrations="$("$DOCKER" exec "$id-db" psql -At -U postgres -d care -c "SELECT count(*) FROM django_migrations")"
pgversion="$("$DOCKER" exec "$id-db" cat /var/lib/postgresql/data/PG_VERSION)"
# SIGINT of the postgres image: a fast shutdown with a checkpoint, so the directory needs no recovery.
"$DOCKER" stop -t 120 "$id-db" > /dev/null
dblog="$("$DOCKER" logs "$id-db" 2>&1)"
case "$dblog" in
*"database system is shut down"*) ;;
*) die "PostgreSQL did not shut down cleanly" ;;
esac

file="$OUT/pg-$SHA12.tar.zst"
"$DOCKER" run --rm --platform "$PLATFORM" -v "$id-pg:/data:ro" --entrypoint tar "$POSTGRES" --numeric-owner -C /data -cf - . \
	| zstd -q -19 -T0 -o "$file" -f
size="$(wc -c < "$file" | tr -d ' ')"
jq -n --arg sha "$SHA" --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg care "$CARE" --arg postgres "$POSTGRES" \
	--arg pg "$pgversion" --argjson size "$size" --argjson migrate "$((t1 - t0))" --argjson tables "$tables" \
	--argjson migrations "$migrations" \
	'{care_git_sha: $sha, created_at: $created, care_image: $care, postgres_image: $postgres, pg_version: $pg,
	  size_bytes: $size, migrate_s: $migrate, tables: $tables, migrations: $migrations}' > "$OUT/pg-$SHA12.json"
log "wrote $file ($size bytes, $tables tables, $migrations migrations)"
