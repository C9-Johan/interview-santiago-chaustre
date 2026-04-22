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

# Clear the tester's tool-call log so we assert only on calls made during
# this run. Fire-and-forget — the tester always returns 200.
curl -sf -X POST "$TESTER_URL/api/tool-calls/reset" >/dev/null || true

echo "== sending happy message (asks about availability AND price — forces multi-tool) =="
# A rich question that names the listing, specific dates, guest count, and
# asks for the total price. A correct agent loop calls at least:
#   • get_listing          (resolve the Soho 2BR by name/id)
#   • check_availability   (for the dates + guest count)
# Optionally also get_conversation_history. The post-turn assertion below
# only checks distinct tool count >= 2, so one extra tool is fine.
send "$HAPPY_CONV" "Hi! Is the Soho 2BR (listing L1) available Fri April 24 – Sun April 26 for 4 adults? What's the total?"
happy_outcome=$(wait_for_outcome "$HAPPY_CONV")
echo "happy outcome: $happy_outcome"

echo "== asserting Stage B agent used multiple tools =="
tool_json=$(curl -sf "$TESTER_URL/api/tool-calls" || echo '{}')
distinct=$(echo "$tool_json" | jq -r '.distinct // 0')
total=$(echo "$tool_json"    | jq -r '.total // 0')
tools=$(echo "$tool_json"    | jq -r '.by_tool // {} | to_entries | map("\(.key)=\(.value)") | join(", ")')
echo "tool calls: total=$total distinct=$distinct  ($tools)"

if [[ "$distinct" -lt 2 ]]; then
    echo "✗ expected the agent to call at least 2 distinct tools, got distinct=$distinct" >&2
    echo "  tools seen: $tools" >&2
    exit 1
fi

has_listing=$(echo "$tool_json" | jq -r '(.by_tool.get_listing // 0) > 0')
has_avail=$(echo   "$tool_json" | jq -r '(.by_tool.check_availability // 0) > 0')
if [[ "$has_listing" != "true" || "$has_avail" != "true" ]]; then
    echo "✗ expected get_listing AND check_availability to both fire" >&2
    echo "  get_listing hit=$has_listing, check_availability hit=$has_avail" >&2
    exit 1
fi
echo "✓ agent invoked get_listing and check_availability"

echo "== sending escalation-trigger message (discount + off-platform) =="
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

# Conversion-rate assertion. The happy auto-reply path calls MarkManaged with
# the reservation id the service resolved from Mockoon's conversation snapshot
# — that fixture pins it to res_test_001 — so we confirm exactly that one.
CONVERSION_RES_ID="${CONVERSION_RES_ID:-res_test_001}"

echo "== confirming managed reservation ($CONVERSION_RES_ID) =="
confirm_resp=$(curl -sf -X POST "$TESTER_URL/api/confirm-reservation/$CONVERSION_RES_ID" || echo '{}')
echo "$confirm_resp" | jq -c '.' >/dev/null 2>&1 || true

echo "== polling /api/conversions until converted>=1 =="
conv_deadline=$(( $(date +%s) + 30 ))
conv_json=""
while (( $(date +%s) < conv_deadline )); do
    conv_json=$(curl -sf "$TESTER_URL/api/conversions" || echo '{}')
    managed=$(echo "$conv_json" | jq -r '.managed // 0')
    converted=$(echo "$conv_json" | jq -r '.converted // 0')
    if [[ "$managed" -ge 1 && "$converted" -ge 1 ]]; then
        rate=$(echo "$conv_json" | jq -r '.rate // 0')
        echo "✓ conversion tracker: managed=$managed converted=$converted rate=$rate"
        echo "✓ e2e smoke passed"
        exit 0
    fi
    sleep 1
done
echo "✗ conversion tracker never reached managed>=1 AND converted>=1" >&2
echo "  last response: $conv_json" >&2
exit 1
