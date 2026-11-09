#!/usr/bin/env bash
# Run the plug tests against a CARE checkout in Docker: Postgres 17, Redis 8, and the CARE dev image.
#
#   test-in-docker.sh <care checkout> [manage.py test arguments]
#   git clone -b ENG-737-backup-backup https://github.com/ohcnetwork/care.git ../care31
#   plugs/care_pi_onboarding/scripts/test-in-docker.sh ../care31
#
# The plug directory is mounted read-only and put on PYTHONPATH; ADDITIONAL_PLUGS adds the app to CARE.
set -euo pipefail

care="$(cd "${1:?usage: test-in-docker.sh <care checkout> [test args]}" && pwd)"
shift
plug="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
name="care-pi-onboarding-test"
image="${CARE_TEST_IMAGE:-${name}:latest}"

cleanup() {
  docker rm -f "${name}-db" "${name}-redis" > /dev/null 2>&1 || true
  docker network rm "${name}" > /dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

if [ -z "${CARE_TEST_IMAGE:-}" ]; then
  docker build -q -f "${care}/docker/dev.Dockerfile" -t "${image}" "${care}" > /dev/null
fi
docker network create "${name}" > /dev/null
docker run -d --name "${name}-db" --network "${name}" \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=care postgres:17-alpine > /dev/null
docker run -d --name "${name}-redis" --network "${name}" redis:8-alpine > /dev/null
for _ in $(seq 1 30); do
  docker exec "${name}-db" pg_isready -U postgres > /dev/null 2>&1 && break
  sleep 1
done

docker run --rm --network "${name}" \
  -v "${plug}:/plugs/care_pi_onboarding:ro" \
  -e DJANGO_SETTINGS_MODULE=config.settings.test \
  -e DATABASE_URL="postgres://postgres:postgres@${name}-db:5432/care" \
  -e REDIS_URL="redis://${name}-redis:6379" \
  -e CELERY_BROKER_URL="redis://${name}-redis:6379/0" \
  -e PYTHONPATH=/plugs/care_pi_onboarding \
  -e PYTHONDONTWRITEBYTECODE=1 \
  -e ADDITIONAL_PLUGS='[{"name": "care_pi_onboarding", "package_name": "/plugs/care_pi_onboarding", "version": ""}]' \
  "${image}" \
  bash -c 'python manage.py makemigrations --check --dry-run care_pi_onboarding && python manage.py test care_pi_onboarding --noinput "$@"' \
  _ "$@"
