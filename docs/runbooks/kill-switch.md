# Runbook — auto-response kill-switch

Operator procedure for disabling and re-enabling the auto-reply path during an
incident. Flipping the switch takes effect on the next inbound turn; in-flight
turns already past GATE 1 continue to completion.

## When to use

- **LLM provider outage / timeouts spiking.** Turns will escalate anyway once
  the classifier times out, but flipping the switch avoids burning API budget
  on already-failing turns.
- **Hallucination / restricted-content incident.** A bad-reply pattern is
  reaching guests (or, for our internal-note setup, reaching hosts) and we
  need the human-in-the-loop path until we ship a fix.
- **Prompt or model change rollout.** Flip off, ship the change, flip on —
  cleaner than relying on escalation queues to absorb mispredictions.
- **Planned maintenance** on Guesty credentials, Mongo schema changes, etc.

## Pre-flight

You need the `ADMIN_TOKEN` value in the environment the service is running
against. It's per-environment — staging and prod tokens differ. Check the
same secret source the service reads `ADMIN_TOKEN` from.

If `ADMIN_TOKEN` was never set on the deployment, admin endpoints return 503
and there is no way to flip the switch at runtime. You must set the var and
restart before you can use this runbook. Prefer setting it up front.

## Commands

Current state:

```sh
curl -sS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$SERVICE_URL/admin/auto-response" | jq .
# {"auto_response_enabled": true}
```

Flip **off** (all turns escalate from GATE 1 onward):

```sh
curl -sS -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"auto_response_enabled": false, "actor": "oncall:<your-name>"}' \
     "$SERVICE_URL/admin/auto-response" | jq .
# {"previous": true, "auto_response_enabled": false, "actor": "oncall:<your-name>"}
```

Flip **on** (resume normal auto-send):

```sh
curl -sS -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"auto_response_enabled": true, "actor": "oncall:<your-name>"}' \
     "$SERVICE_URL/admin/auto-response" | jq .
```

## Verification

1. `inquiryiq.admin.toggle_flips` counter in Grafana should tick on every flip,
   labeled `{field="auto_response", enabled=true|false}`.
2. Service logs emit `toggle_flip` records carrying `prev`, `now`, `actor`.
3. After flipping off, every subsequent escalation should carry
   `Reason="auto_disabled"`. A handful of in-flight turns may still reach
   GATE 2 and be decided there — that's expected; only new turns hit the new
   state.
4. After flipping on, the next qualifying turn should produce a
   `send-message` call against Guesty (check the conversions dashboard /
   `inquiryiq.conversations.managed` counter).

## What the switch does NOT do

- It does not drain the debouncer. Messages already buffered at flip time
  will flush normally; most will then escalate at GATE 1 with
  `Reason="auto_disabled"`.
- It does not stop LLM spend from in-flight turns (classifier and generator
  calls already started will run to completion).
- It does not affect the reservation-updated webhook or conversion counters —
  those remain reachable.

## Rolling back the switch

If you flipped the switch by mistake, just flip it back. The audit log and
counter show both events; `previous` in the response body confirms the state
you changed.

## Escalation paths

- Can't reach the service's admin port? Roll the config: set
  `AUTO_RESPONSE_ENABLED=false` in the deployment env and restart. The startup
  state seeds the togglesource.
- Admin token lost / leaked? Rotate `ADMIN_TOKEN`, restart, notify ops. Old
  requests will start returning 401 immediately after restart.
- Service crashes during an incident? State is in-memory — on restart the
  togglesource re-seeds from `AUTO_RESPONSE_ENABLED`. If you need durable
  state across restarts, move to a backed store (out of scope for this
  iteration).
