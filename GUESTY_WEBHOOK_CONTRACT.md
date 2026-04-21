# Guesty Webhook Contract — `reservation.messageReceived`

Real contract for the inbound webhook your service must handle. This is what Guesty actually sends when a guest replies in a conversation (Airbnb, Booking, VRBO, or direct). Use this as ground truth — the simplified version in `CHALLENGE.md §5.1` is for orientation only.

---

## Endpoint

Your service exposes:

```
POST /webhooks/guesty/message-received
Content-Type: application/json
```

Return **`200` within a few hundred ms**, even if you haven't processed the message yet. Guesty retries on non-2xx. The production pattern is fast-path acknowledge → queue for async processing.

## Transport & Signing

Guesty delivers webhooks via **Svix**. Expect these headers:

| Header | Purpose |
|---|---|
| `svix-id` | Unique message id (stable across retries) — use for **idempotency**. |
| `svix-timestamp` | Unix seconds. Reject if drift > 5 min to block replay. |
| `svix-signature` | `v1,<base64-hmac-sha256>` (space-separated if multiple versions). |
| `user-agent` | Starts with `Svix-Webhooks/`. |

**Signature verification** (HMAC-SHA256):

```
signedPayload = `${svix-id}.${svix-timestamp}.${rawJsonBody}`
expected      = base64(HMAC_SHA256(webhookSecret, signedPayload))
valid         = constantTimeEqual(base64Decode(signatureFromHeader), base64Decode(expected))
```

Use the **raw request body bytes**, not a re-serialized version — re-serializing will change key order/whitespace and break the signature.

## Full Payload (real example, anonymized)

```json
{
  "event": "reservation.messageReceived",
  "reservationId": "68b9b28d58d5d60012e688ab",
  "message": {
    "postId": "69778d8f1ff4960010098e66",
    "body": "We opened the balcony door just to take a look at the view and it doesn't seem to close all the way, it still has the sound of air coming in. Any suggestions on how to get it fully closed? Thank you",
    "createdAt": "2026-01-26T15:51:27.000Z",
    "type": "fromGuest",
    "module": "airbnb2",
    "bcc": [],
    "cc": [],
    "to": [],
    "feedback": {}
  },
  "conversation": {
    "_id": "68b9b28d7c124a0011fbe0ca",
    "accountId": "63f77f4e7e251b0030c1464a",
    "guestId": "68b9b28c402adbc122f55ba6",
    "conversationWith": "Guest",
    "assignee": null,
    "firstReceptionist": "Marianne Lucas",
    "isRead": true,
    "language": "en",
    "status": "OPEN",
    "priority": 10,
    "snoozedUntil": null,
    "subject": "New guest reservation HM3M9S5Y3Q confirmed...",
    "createdAt": "2025-09-04T15:38:53.032Z",
    "lastModifiedAt": "2026-01-25T23:01:20.967Z",
    "lastUpdatedAt": "2026-01-25T23:01:20.967Z",
    "lastUpdatedFromGuest": "2026-01-20T17:34:41.000Z",
    "integration": {
      "_id": "640f227c3f5ec20034bc7958",
      "platform": "airbnb2",
      "airbnb2": { "guestId": 74388300, "id": "2284291023" }
    },
    "meta": {
      "guestName": "Chelsea Newkirk",
      "reservations": [
        {
          "_id": "68b9b28d58d5d60012e688ab",
          "checkIn":  "2026-01-25T22:00:00.000Z",
          "checkOut": "2026-01-27T17:00:00.000Z",
          "confirmationCode": "HM3M9S5Y3Q"
        }
      ]
    },
    "pendingTasks": [],
    "thread": [
      {
        "postId": "69778d8f1ff4960010098e66",
        "body": "We opened the balcony door ...",
        "createdAt": "2026-01-26T15:51:27.000Z",
        "type": "fromGuest",
        "module": "airbnb2",
        "bcc": [], "cc": [], "to": [],
        "feedback": {}
      }
    ]
  },
  "meta": {
    "eventId":   "de96967d-a117-4bc9-b352-41e720879b41",
    "messageId": "f4055f87-ec58-41fd-b219-81ffbf1ad883"
  }
}
```

## Field Reference

### Top level
| Field | Type | Notes |
|---|---|---|
| `event` | string | Always `reservation.messageReceived` for this webhook. Other events (e.g. `reservation.messageSent`) hit different routes. |
| `reservationId` | string | Guesty reservation id. **Not always present**; fall back to `conversation.meta.reservations[0]._id`. |
| `message` | object | The new message that triggered the webhook (see below). |
| `conversation` | object | Full conversation snapshot at webhook time — includes guest info and `thread` history. |
| `meta.eventId` | uuid | Guesty event id. |
| `meta.messageId` | uuid | **Distinct from `message.postId`.** Use `postId` for message dedup; `messageId` for event-level dedup. |

### `message` object
| Field | Type | Notes |
|---|---|---|
| `postId` | string (ObjectId) | Stable id for the message. **Use this for idempotency** — same `postId` = same message. |
| `body` | string | Raw message text. May contain newlines, emoji, non-Latin chars. Trim whitespace before use. |
| `createdAt` | ISO-8601 UTC | When the guest sent it (per OTA). Can be seconds to minutes before the webhook fires. |
| `type` | string | Direction. Map to sender role: `fromGuest`/`toHost` → **guest**; `fromHost`/`toGuest` → **host**; `system` → **system**. Unknown → treat as system (do **not** auto-process). |
| `module` | string | Channel: `airbnb2`, `booking`, `vrbo`, `direct`, etc. Useful for platform-specific behavior. |
| `bcc`/`cc`/`to` | string[] | Usually empty for OTA chat; populated for email-style channels. |
| `feedback` | object | Guesty internal — ignore. |

### `conversation.meta.reservations[]`
Array, but in practice the **first element** is the relevant reservation. Fields you'll actually use: `_id`, `checkIn`, `checkOut`, `confirmationCode`.

### `conversation.integration`
| Field | Notes |
|---|---|
| `platform` | `airbnb2` / `bookingCom` / `vrbo` / `manual` — drives tone and what data is available. |
| `<platform>` | Platform-specific ids (e.g. `airbnb2.guestId`). You usually don't need these. |

### `conversation.thread[]`
**Full message history** up to and including the current one. Order is **oldest → newest** but verify per request. Useful for:
- Reconstructing the guest turn (all messages since the last host/system reply).
- Detecting whether the host already responded (no need to auto-reply).
- Feeding conversation context into the LLM.

Each thread item has the same shape as the top-level `message` object.

## Important Behaviors

1. **Retries & idempotency.** Guesty/Svix will retry on 5xx, network errors, and timeouts. Dedupe on `message.postId` (message-level) or `svix-id` (delivery-level). Both are stable across retries.
2. **Burst messages.** Guests often send 2–4 short messages in a row (*"Hi"* → *"is it free this weekend?"* → *"for 4 people"*). Each fires a webhook. Production practice: **debounce ~15s** per conversation before classifying, so the LLM sees the full turn. Pair that sliding window with a **hard `maxWait` cap** (our default 60s, `DEBOUNCE_MAX_WAIT_MS`) measured from the first message of the turn, so a slow typist never stalls the pipeline indefinitely.
3. **Host already replied.** By the time your poll cycle runs, the host may have responded manually. Re-check `conversation.thread` for a later non-guest message before auto-sending.
4. **Missing reservation.** Pre-booking inquiries sometimes arrive before a Guesty reservation exists. Handle `reservationId` absent and `meta.reservations` empty.
5. **Body can be empty-ish.** Attachments, stickers, or system markers can produce `""` or whitespace-only bodies. Skip classification, don't crash.
6. **Timezones.** All timestamps are UTC ISO-8601. Display/bucketing in the real system is `America/Bogota` (UTC-5, no DST) — not relevant for the 90-min challenge but mention it if asked.

## Outbound Send (companion contract)

When you decide to reply, post to Guesty:

```
POST /conversations/{conversationId}/messages
Authorization: Bearer <guesty-oauth-token>
Content-Type: application/json

{ "body": "...your C.L.O.S.E.R. reply...", "type": "note" }
```

- `type: "note"` → **internal** note, never reaches the guest. **Use this for the challenge.**
- `type: "platform"` → sent to the guest via the originating OTA. Do not use.
- Guesty rate-limits: keep to ~2 req/sec with retry + jitter on 429/5xx.

## Minimum Test Payload

If you want a shorter fixture for a first pass, this is the smallest shape that exercises the happy path:

```json
{
  "event": "reservation.messageReceived",
  "reservationId": "res_test_001",
  "message": {
    "postId": "msg_test_001",
    "body": "Is the Soho 2BR available Fri-Sun for 4 adults? What's the total?",
    "createdAt": "2026-04-20T14:31:09Z",
    "type": "fromGuest",
    "module": "airbnb2"
  },
  "conversation": {
    "_id": "conv_test_001",
    "guestId": "guest_test_001",
    "language": "en",
    "status": "OPEN",
    "integration": { "platform": "airbnb2" },
    "meta": {
      "guestName": "Sarah",
      "reservations": [{
        "_id": "res_test_001",
        "checkIn":  "2026-04-24T22:00:00.000Z",
        "checkOut": "2026-04-26T16:00:00.000Z",
        "confirmationCode": "TESTCODE1"
      }]
    },
    "thread": []
  },
  "meta": { "eventId": "evt_test_001", "messageId": "msgid_test_001" }
}
```

## External Docs

- Guesty Open API: <https://open-api-docs.guesty.com>
- Guesty Webhooks (conversations/messages): <https://open-api-docs.guesty.com/docs/webhooks>
- Svix signature verification: <https://docs.svix.com/receiving/verifying-payloads/how>
