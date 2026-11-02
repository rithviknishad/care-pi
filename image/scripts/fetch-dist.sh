#!/usr/bin/env bash
# fetch-dist.sh: download one solution lock and its bundles from the GitHub releases into a dist
# directory with the release layout <tag>/<asset> (docs/solutions.md, section 2.2). image/build.sh
# --dist reads that directory. The workflow pi-image.yml uses it.
#
#   lock-<solution>-<version>/<solution>-<version>.lock.yaml (+ .sha256)
#   fe-<solution>-<sha12>-<build id>/care_fe-<...>.tar.gz (+ .sha256)     the default FE bundle
#   plug-<slug>-<sha12>/plug-<slug>-<sha12>.tar.gz (+ .sha256)            each built FE plug bundle
#
# Each bundle must have the SHA-256 of the lock. Needs: gh (authenticated), jq, sha256sum, and the
# care-pi binary of this host (--care-pi) to read the lock.
set -euo pipefail

usage() {
	echo "Usage: image/scripts/fetch-dist.sh --solution NAME --lock-version YYYY.MM.N --out DIR --care-pi BIN [--repo OWNER/REPO]"
}

SOLUTION="" LOCK_VERSION="" OUT="" CARE_PI="" REPO="${GITHUB_REPOSITORY:-rithviknishad/care-pi}"
while [ $# -gt 0 ]; do
	case "$1" in
	--solution) SOLUTION="$2"; shift 2 ;;
	--lock-version) LOCK_VERSION="$2"; shift 2 ;;
	--out) OUT="$2"; shift 2 ;;
	--care-pi) CARE_PI="$2"; shift 2 ;;
	--repo) REPO="$2"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) echo "fetch-dist.sh: unknown option $1" >&2; usage >&2; exit 2 ;;
	esac
done
die() { echo "fetch-dist.sh: $*" >&2; exit 1; }
if [ -z "${SOLUTION}" ] || [ -z "${LOCK_VERSION}" ] || [ -z "${OUT}" ] || [ ! -x "${CARE_PI}" ]; then usage >&2; exit 2; fi

# fetch REL [SHA256]: download the asset <tag>/<file> and its .sha256; check the SHA-256.
fetch() {
	local rel="$1" want="${2:-}" tag file got
	tag="${rel%%/*}"
	file="${rel#*/}"
	mkdir -p "${OUT}/${tag}"
	gh release download "${tag}" --repo "${REPO}" --pattern "${file}" --dir "${OUT}/${tag}" --clobber
	gh release download "${tag}" --repo "${REPO}" --pattern "${file}.sha256" --dir "${OUT}/${tag}" --clobber || true
	# Phase 7: the minisign signature of a lock, when CI signed it (docs/solutions.md section 18).
	gh release download "${tag}" --repo "${REPO}" --pattern "${file}.minisig" --dir "${OUT}/${tag}" --clobber 2>/dev/null || true
	got="$(sha256sum "${OUT}/${rel}" | cut -d' ' -f1)"
	if [ -z "${want}" ] && [ -f "${OUT}/${rel}.sha256" ]; then
		want="$(cut -d' ' -f1 "${OUT}/${rel}.sha256")"
	fi
	[ -z "${want}" ] || [ "${got}" = "${want}" ] || die "the SHA-256 of ${rel} is ${got}, not ${want}"
	[ -f "${OUT}/${rel}.sha256" ] || echo "${got}  ${file}" > "${OUT}/${rel}.sha256"
	echo "fetched ${rel} (${got})"
}

lock_rel="lock-${SOLUTION}-${LOCK_VERSION}/${SOLUTION}-${LOCK_VERSION}.lock.yaml"
fetch "${lock_rel}"
"${CARE_PI}" solution validate --json "${OUT}/${lock_rel}" > "${OUT}/.lock.json"
fetch "$(jq -r .fe.url "${OUT}/.lock.json")" "$(jq -r .fe.sha256 "${OUT}/.lock.json")"
while IFS=$'\t' read -r rel sum; do
	fetch "${rel}" "${sum}"
done < <(jq -r '.plugs // {} | to_entries[] | .value.frontend // empty | select(.source == "built") | [.url, .sha256] | @tsv' "${OUT}/.lock.json")
rm -f "${OUT}/.lock.json"
