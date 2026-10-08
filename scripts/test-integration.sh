#!/usr/bin/env bash

set -euo pipefail

require() {
    local name="$1"
    if [[ -z "${!name:-}" ]]; then
        echo "error: ${name} is required for the real integration suite" >&2
        exit 1
    fi
}

require POSTGRES_TEST_DATABASE_URL
require KEYCLOAK_TEST_ISSUER_URL
require SQS_TEST_ENDPOINT_URL

command -v curl >/dev/null 2>&1 || {
    echo "error: curl is required for integration preflight checks" >&2
    exit 1
}

curl --fail --silent --show-error \
    "${KEYCLOAK_TEST_ISSUER_URL}/.well-known/openid-configuration" >/dev/null

# Any HTTP response proves the local AWS-compatible endpoint is reachable; the
# Go readiness/integration tests subsequently validate the required queues.
curl --silent --show-error "${SQS_TEST_ENDPOINT_URL}/" >/dev/null

result_file="$(mktemp)"
trap 'rm -f "${result_file}"' EXIT

if ! go test -count=1 -json ./... >"${result_file}"; then
    cat "${result_file}"
    exit 1
fi

if grep -Eq 'POSTGRES_TEST_DATABASE_URL is not set|POSTGRES_TEST_DATABASE_URL and KEYCLOAK_TEST_ISSUER_URL are required|POSTGRES_TEST_DATABASE_URL and SQS_TEST_ENDPOINT_URL are required|POSTGRES_TEST_DATABASE_URL, KEYCLOAK_TEST_ISSUER_URL, and SQS_TEST_ENDPOINT_URL are required|SQS_TEST_ENDPOINT_URL is required' "${result_file}"; then
    echo "error: a real integration suite was skipped because infrastructure was unavailable" >&2
    exit 1
fi

echo "real PostgreSQL, Keycloak, MiniStack, migration, recovery, and multi-process integration tests passed"
