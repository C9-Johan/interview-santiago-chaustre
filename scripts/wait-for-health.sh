#!/usr/bin/env bash
# wait-for-health.sh — block until the dev stack is ready.
#
# Polls the service /healthz, the tester /api/health, and Mockoon. Exits 0
# as soon as all three respond 2xx, or non-zero after WAIT_TIMEOUT_SECS.
# Call after `make dev-up` but before posting traffic through the tester.

set -euo pipefail

SERVICE_URL="${INQUIRYIQ_URL:-http://localhost:8080}"
TESTER_URL="${TESTER_URL:-http://localhost:4000}"
MOCKOON_URL="${MOCKOON_URL:-http://localhost:3001}"
WAIT_TIMEOUT_SECS="${WAIT_TIMEOUT_SECS:-60}"
SLEEP_INTERVAL="${SLEEP_INTERVAL:-1}"

wait_for() {
    local name="$1"
    local url="$2"
    local deadline=$(( $(date +%s) + WAIT_TIMEOUT_SECS ))
    while (( $(date +%s) < deadline )); do
        if curl -sf -o /dev/null -m 2 "$url"; then
            echo "✓ $name ready"
            return 0
        fi
        sleep "$SLEEP_INTERVAL"
    done
    echo "✗ $name did not become healthy at $url within ${WAIT_TIMEOUT_SECS}s" >&2
    return 1
}

# Mockoon does not expose a health endpoint, but any known route returns
# fast once the server has bound the port. listings/L1 is seeded by the
# fixture so it's a safe readiness signal.
wait_for "service"   "$SERVICE_URL/healthz"
wait_for "tester"    "$TESTER_URL/api/health"
wait_for "mockoon"   "$MOCKOON_URL/listings/L1"

echo "dev stack ready."
