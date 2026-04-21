# Runbook — LLM budget watcher

Operator procedure for the daily LLM spend cap. The watcher rides inside the
LLM client: every `Chat` call reports prompt/completion tokens to a
per-UTC-day tally; when the tally breaches `LLM_BUDGET_DAILY_USD` it flips
`auto_response_enabled=false` through the same source the manual kill-switch
uses and publishes `budget.exceeded` on the event bus. The tally self-heals
at UTC midnight — a new bucket starts and the tripped flag resets.

## Configuration

| Env var | Default | Meaning |
|---|---|---|
| `LLM_BUDGET_DAILY_USD` | `0` (disabled) | Daily cap. `<=0` turns accounting on but never trips the kill-switch. |
| `LLM_PRICE_PROMPT_PER_1K` | `0.00014` | DeepSeek chat prompt pricing. |
| `LLM_PRICE_COMPLETION_PER_1K` | `0.00028` | DeepSeek chat completion pricing. |

Pricing is applied to **every** model the service sees. If you switch
providers mid-incident, update the envs and restart; the watcher re-seeds.

## Check current spend

```sh
curl -sS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$SERVICE_URL/admin/budget" | jq .
# {"day":"2026-04-21","spent_usd":0.3124,"cap_usd":5.00,"tripped":false}
```

`tripped=true` means the watcher already flipped the kill-switch for this
bucket. The flip stays in place until either an operator re-enables via the
kill-switch runbook **or** UTC midnight rolls the bucket.

## When the watcher trips

1. The service logs `budget_exceeded` at WARN with
   `day/spent_usd/cap_usd/model/actor`.
2. `inquiryiq.budget.flips` counter increments (Grafana: LLM cost dashboard).
3. `budget.exceeded` fires on the event bus; the default log subscriber
   echoes it. In production, chain a Slack or PagerDuty subscriber here.
4. `auto_response_enabled` goes to `false` with actor=`budget_watcher`;
   `inquiryiq.admin.toggle_flips{field=auto_response,enabled=false}` ticks.

## Recovery options

- **Raise the cap and re-enable.** Set `LLM_BUDGET_DAILY_USD` higher,
  restart, then flip the kill-switch back on via the kill-switch runbook.
  The watcher's tally restarts at zero; if real spend is already past the
  new cap it'll trip again immediately.
- **Wait it out.** Escalations absorb every inbound turn until UTC midnight.
  The bucket rolls, tripped clears, and the kill-switch stays off until you
  flip it on manually. Tally continues clean for the new day.
- **Ignore the cap for now.** Flip the kill-switch back on manually. The
  watcher keeps accounting but will not re-trip until the NEXT day's cap is
  reached — current-day tripped flag stays true. Use sparingly; you are
  silencing a finance guardrail.

## What the watcher does NOT do

- It does not rate-limit individual calls — a single oversized request can
  still complete and be billed. Guard that with request-level token caps
  upstream.
- It does not persist the tally. Restart resets the day's bucket to zero and
  clears tripped; if spend is already past the cap on-disk, the watcher will
  not know until enough new calls accumulate again. For durability, move the
  tally to Redis (out of scope).
- It does not price per-model. Set prompt/completion rates for whichever
  provider you run against; per-model pricing is a follow-up.
