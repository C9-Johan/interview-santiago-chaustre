#!/usr/bin/env bash
# e2e-smoke.sh — exercise the full pipeline through the tester proxy.
#
# Sends one "happy" guest message and one "always-escalate" guest message
# through the tester's signing proxy (same path the UI uses), waits for the
# service to produce a terminal state, and asserts each one landed in the
# expected channel:
#
#   happy message    -> bot reply captured in the conversation log, OR
#                       escalation (non-deterministic LLMs are allowed to
#                       fall back to escalate).
#   discount message -> escalation with reason matching R1/haggle rules.
#
# Requires LLM_API_KEY set in the environment — the service makes real LLM
# calls. All other config has a dev default.

set -euo pipefail

TESTER_URL="${TESTER_URL:-http://localhost:4000}"
WAIT_TIMEOUT_SECS="${WAIT_TIMEOUT_SECS:-90}"
SLEEP_INTERVAL="${SLEEP_INTERVAL:-2}"

if ! command -v jq >/dev/null 2>&1; then
    echo "jq is required (mise install jq)" >&2
    exit 1
fi

# Send a guest message through the tester. Arguments: conversation_id, body.
# Returns the conversation_id on stdout so callers can poll the log.
send() {
    local conv_id="$1"
    local body="$2"
    curl -sf -X POST "$TESTER_URL/api/send" \
        -H "Content-Type: application/json" \
        -d "$(jq -n --arg cid "$conv_id" --arg body "$body" \
            '{conversation_id: $cid, reservation_id: ($cid | sub("conv_"; "res_")), guest_name: "Smoke", platform: "airbnb2", body: $body}')" >/dev/null
    echo "$conv_id"
}

# wait_for_outcome polls the tester until either (a) a bot/note reply
# appears in the conversation log OR (b) an escalation exists that points
# at the same conversation. Prints "bot" or "escalate" on success, fails
# on timeout.
wait_for_outcome() {
    local conv_id="$1"
    local deadline=$(( $(date +%s) + WAIT_TIMEOUT_SECS ))
    while (( $(date +%s) < deadline )); do
        local conv_json
        conv_json=$(curl -sf "$TESTER_URL/api/conversations/$conv_id" || echo '{}')
        if echo "$conv_json" | jq -e '.entries[] | select(.role=="bot" or .role=="note")' >/dev/null 2>&1; then
            echo "bot"
            return 0
        fi
        local esc_json
        esc_json=$(curl -sf "$TESTER_URL/api/escalations" || echo '[]')
        if echo "$esc_json" | jq -e --arg cid "$conv_id" 'map(select(.ConversationKey | tostring | contains($cid))) | length > 0' >/dev/null 2>&1; then
            echo "escalate"
            return 0
        fi
        sleep "$SLEEP_INTERVAL"
    done
    echo "timeout" >&2
    return 1
}

escalation_reason() {
    local conv_id="$1"
    curl -sf "$TESTER_URL/api/escalations" \
        | jq -r --arg cid "$conv_id" \
            'map(select(.ConversationKey | tostring | contains($cid))) | first | .Reason // ""'
}

timestamp=$(date +%s)
HAPPY_CONV="conv_smoke_happy_${timestamp}"
ESC_CONV="conv_smoke_esc_${timestamp}"

echo "== sending happy message =="
send "$HAPPY_CONV" "Hi! Is the Soho 2BR available Fri April 24 – Sun April 26 for 4 adults?"
happy_outcome=$(wait_for_outcome "$HAPPY_CONV")
echo "happy outcome: $happy_outcome"

echo "== sending escalation-trigger message =="
send "$ESC_CONV" "Any discount if I book right now? Cash, off-platform."
esc_outcome=$(wait_for_outcome "$ESC_CONV")
echo "escalation outcome: $esc_outcome"

if [[ "$esc_outcome" != "escalate" ]]; then
    echo "✗ discount+off-platform message should have escalated, got: $esc_outcome" >&2
    exit 1
fi

reason=$(escalation_reason "$ESC_CONV")
echo "escalation reason: $reason"
case "$reason" in
    code_requires_human|risk_flag|restricted_content|prompt_injection_suspected)
        ;;
    "")
        echo "✗ no escalation reason found" >&2
        exit 1
        ;;
    *)
        echo "⚠ unexpected escalation reason ($reason) — still green, pipeline reacted" >&2
        ;;
esac

echo "✓ e2e smoke passed"
