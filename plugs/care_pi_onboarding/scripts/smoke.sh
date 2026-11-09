#!/usr/bin/env bash
# Apply each onboarding step twice against a running CARE and check that the second run creates nothing.
#
#   smoke.sh <care api url> <username> <password>
#   smoke.sh http://127.0.0.1:9000 admin 'secret'
#
# Needs curl and jq. It changes the CARE database: use a test instance.
set -euo pipefail

api="${1:?usage: smoke.sh <care api url> <username> <password>}"
user="${2:?username}"
password="${3:?password}"
base="${api%/}/api/care_pi_onboarding"

token="$(curl -fsS -H 'Content-Type: application/json' \
  -d "$(jq -n --arg u "$user" --arg p "$password" '{username: $u, password: $p}')" \
  "${api%/}/api/v1/auth/login/" | jq -r .access)"
auth=(-H "Authorization: Bearer ${token}" -H 'Content-Type: application/json')

apply() {
  local step="$1" body="$2" response code
  response="$(curl -sS -w '\n%{http_code}' "${auth[@]}" -d "$body" "${base}/steps/${step}/apply/")"
  code="${response##*$'\n'}"
  response="${response%$'\n'*}"
  if [ "$code" != 200 ]; then
    echo "FAIL ${step}: HTTP ${code}: ${response}" >&2
    exit 1
  fi
  printf '%s\n' "$response"
}

facility=""
input() {
  case "$1" in
    organization) echo '{"state": "Kerala", "district": "Ernakulam"}' ;;
    facility) echo '{"template": "clinic", "name": "Smoke Test Clinic", "address": "MG Road, Kochi", "pincode": 682011, "phone_number": "+914842123456"}' ;;
    roles | valuesets) echo '{}' ;;
    staff) jq -n --arg f "$facility" '{facility: $f, members: [
      {username: "smoke-doctor", first_name: "Smoke", last_name: "Doctor", phone_number: "+919446100001", gender: "female", role: "doctor"},
      {username: "smoke-front", first_name: "Smoke", last_name: "Front", phone_number: "+919446100002", gender: "male", role: "receptionist"}]}' ;;
    *) jq -n --arg f "$facility" '{facility: $f}' ;;
  esac
}

steps=(organization facility roles staff valuesets questionnaires catalog demo_data)
for run in 1 2; do
  echo "== run ${run}"
  for step in "${steps[@]}"; do
    body="$(apply "$step" "$(input "$step")")"
    if [ "$step" = facility ]; then
      facility="$(jq -r .result.facility.id <<< "$body")"
    fi
    created="$(jq '[.created[]] | add // 0' <<< "$body")"
    existing="$(jq '[.existing[]] | add // 0' <<< "$body")"
    printf '%-15s %-6s created=%-4s existing=%s\n' "$step" "$(jq -r .status <<< "$body")" "$created" "$existing"
    if [ "$run" = 2 ] && [ "$created" != 0 ]; then
      echo "FAIL ${step}: the second run created ${created} objects" >&2
      exit 1
    fi
  done
done
echo "== status"
curl -fsS "${auth[@]}" "${base}/status/?facility=${facility}" | jq -c '{next_step, wizard, steps: [.steps[] | {step, state, runs}]}'
echo "OK: the second run created nothing."
