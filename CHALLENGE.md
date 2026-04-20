# Engineering Challenge — InquiryIQ Vertical Slice

**Duration:** 90 minutes, hands-on. **AI tools:** any (Claude Code, Cursor, Copilot, ChatGPT…). **Repo:** empty — you choose stack, structure, libraries, and deployment target. Commit early and often; the interviewer pulls your repo in real time to test.

---

## 1. The Business

Cloud9 manages short-term rental listings on Airbnb/Booking/VRBO via **Guesty** (PMS). Every day guests send inquiries ("Is the apartment available next weekend for 4?", "Can I check in at 10pm?", "Is parking free?"). Hosts are slow, inconsistent, and lose bookings. We want an AI agent that reads each inbound message and either **auto-replies** (when confident) or **flags for human review**.

## 2. What You're Building

A service that handles the **inquiry → reply loop** end to end:

1. **Ingest** a Guesty webhook when a guest sends a message.
2. **Classify** the message (intent, urgency, missing info) using an LLM.
3. **Generate** a reply using the **C.L.O.S.E.R.** sales framework (§6).
4. **Decide**: auto-send vs. escalate to a human.
5. **Send** the reply back to Guesty as an internal note (so nothing reaches real guests during the exercise).

You will **not** have live API credentials. Assume Guesty and OpenAI exist; build against the contracts in §5. The interviewer will supply fixtures/sample payloads on request.

### Flow at a glance

The webhook acknowledges fast, then everything else happens async. Classification gates generation, generation uses real listing facts, and the final auto-send decision is a hard rules check — **not** something the LLM decides.

## 3. What We Evaluate

| Area | What we look for |
|---|---|
| **Project thinking** | Do you design before you code? Do you identify what's missing from this brief and ask? |
| **Architecture** | Sensible boundaries between transport / domain / integrations. Not over-engineered, not a single 400-line file. |
| **Software quality** | Types, error handling, idempotency, testability. Code a teammate could extend on Monday. |
| **Agency** | You notice gaps the brief doesn't spell out and propose solutions. See §4. |
| **AI fluency** | Smart delegation to AI. You review what it outputs instead of accepting it. You iterate. |
| **Pragmatism** | 90 min is short. You ship a working slice, not a half-finished platform. |

We are **not** grading: UI polish, deployment, test coverage %, or framework choice. Pick what gets you to a working slice fastest.

## 4. Explicitly Under-Specified

The core loop above is the minimum. A production system needs more. **We want to see what you surface without being asked** — talk through your choices, build what you can, leave TODOs for the rest. Think about things like: observability, operator controls, team notifications, safety nets, replayability. Pick 1–2 and actually build them if time allows.

## 5. API Contracts (simplified)

### 5.1 Inbound webhook — `POST /webhooks/guesty/message-received`
See **`GUESTY_WEBHOOK_CONTRACT.md`** for the full real contract: Svix signing, headers, full payload, field-by-field reference, idempotency keys, edge cases (bursts, host-already-replied, missing reservation), and a minimal test fixture. Read it before you start — several design decisions (debounce, dedup, signature verification, async vs sync) fall out of that contract, not this brief.

### 5.2 Guesty API (what you'd call)
- `GET /listings/{id}` → listing facts (title, bedrooms, amenities, houseRules, basePrice).
- `GET /availability?listingId=…&from=…&to=…` → `{ available: bool, nights: n, total: usd }`.
- `POST /conversations/{id}/messages` → `{ body, type: "note" | "platform" }`. **Use `type: "note"` for this exercise.**
- Docs: <https://open-api-docs.guesty.com>

### 5.3 OpenAI
- Chat Completions / Responses API with **tool calling** for the Guesty lookups above.
- Docs: <https://platform.openai.com/docs>

## 6. Domain Reference

### Classification taxonomy — "Traffic Light" system

Guest messages are tagged with **one primary code** (plus an optional secondary for analytics). Four top-level colors, each with sub-codes that drive the reply strategy. The point isn't to label — it's to identify **the one thing blocking the booking** and remove it.

**GREEN — Lay-downs (high intent, ready to book)**
| Code | Trigger examples | Strategy |
|---|---|---|
| G1 | *"book"*, *"reserve"*, *"confirm"*, *"pay"* | Fast Track: confirm + booking link, minimal friction |
| G2 | *"wedding"*, *"family trip"*, *"work stay"* | Personal Touch: validate context, then close |

**YELLOW — Hurdles (interested but blocked by one concern)**
| Code | Category | Trigger examples | Strategy |
|---|---|---|---|
| Y1 | Logistics | parking, directions, access | Problem Solver |
| Y2 | Trust / Admin | deposit, refund, cancel, ID | Authority — calm, standard-process |
| Y3 | Product fit | beds, layout, stairs, size | Visualizer |
| Y4 | Timing | check-in/out, early, late, luggage | Accommodator |
| Y5 | Permissions | pets, party, visitors, rules | Boundaries — firm policy + alternative |
| Y6 | Availability | dates, calendar, vacancy | Confirm & Close |
| Y7 | Price clarity | total, fees, cleaning, taxes | Transparent Anchor |

**RED — Anchors (price sensitivity)**
| Code | Category | Triggers | Strategy |
|---|---|---|---|
| R1 | Haggle | discount, deal, best price | Value Stack — **never drop price first** |
| R2 | Budget | expensive, can't afford, cheaper | Takeaway — polite refusal + options |

**GRAY — Low signal (not enough info)**
| Code | Triggers | Strategy |
|---|---|---|
| X1 | *"Hi"*, emoji-only, *"interested"*, vague | Qualify — ask ≤ 2 questions to create a bookable path |

### Decision rules

- **Priority (when multiple signals present):** `RED > Y5 > Y2 > Y4 > Y1 > Y3 > Y6 > Y7 > GREEN > GRAY`. Red price language dominates everything. Example: *"Any discount? Also is parking included?"* → primary `R1`, secondary `Y1`.
- **Default bias:** unclear → Gray or Yellow, **not** Green. Don't promote intent you can't prove.
- **Auto-send gate (ALL must hold):**
  1. Primary code ∈ **{G1, G2, Y1, Y3, Y4, Y6, Y7}** (low-risk set).
  2. Confidence ≥ **0.65**.
  3. System toggle `auto_response_enabled` is on.
- **Always escalate to human:** `Y2`, `Y5`, `R1`, `R2`, any confidence < 0.65, or restricted content detected (off-platform payment requests, address leakage, guarantee language).
- **Classifier should also emit:** `secondary_code` (optional), `confidence` (0–1), `extracted_entities` (dates, guest count, pets, vehicles), `risk_flag` (boolean).

### C.L.O.S.E.R. reply structure

A sales-conversion framework (Hormozi). Every auto-reply is **one short paragraph** (3–5 sentences) in the guest's language, covering the six beats in order. Don't label them in the output — they're the internal scaffolding, not headings.

| Beat | Purpose | Mini-phrase |
|---|---|---|
| **C**larify | Restate what the guest is actually asking so they feel heard | *"You're looking at Fri–Sun for 4 adults."* |
| **L**abel | Name the underlying need/concern | *"Short city break, want to lock it in fast."* |
| **O**verview | Fit the property to that need using real facts (beds, amenities, location) | *"Our Soho 2BR sleeps 4, self-check-in, right on Spring St."* |
| **S**ell certainty | State availability and total price from the tool calls — no hedging | *"Those dates are open, $480 for 2 nights all-in."* |
| **E**xplain | One differentiator that matters for *this* inquiry (not a brochure dump) | *"Guests love the quiet bedroom off the courtyard — real sleep in Manhattan."* |
| **R**equest | Ask for the next step explicitly | *"Want me to hold it while you decide?"* |

**Full example — webhook body:** `"Hi! Is the Soho 2BR available Fri April 24 – Sun April 26 for 4 adults? What's the total?"`

**Reply:**
> Hi Sarah — you're looking at Fri–Sun (Apr 24–26) for 4, a quick city weekend. Our Soho 2BR sleeps 4 with self-check-in and is right on Spring St. Those dates are open and the total is **$480 for 2 nights**, taxes included. Most guests tell us the second bedroom off the courtyard is the quietest sleep they've had in Manhattan. Want me to hold the dates for you while you decide?

**What to avoid:** generic intros ("Thanks for reaching out!"), hedging ("I think it might be available"), dumping every amenity, skipping the ask at the end. If a beat has no real data (e.g., availability check failed), **escalate instead of faking it**.

## 7. Deliverables

- A running service exposing `POST /webhooks/guesty/message-received`.
- `README.md`: how to run it, architectural choices, what you'd build next, where you used AI and how you verified it.
- Git history with meaningful commits.

## 8. Rules of Engagement

- Ask questions at any time. "What does X mean?" is free; "build Y for me" is not.
- Narrate your thinking. Silent 90 min reads as uncertainty.
- When AI gives you code, read it before pasting. We will ask why a line is there.
- If you finish the core loop early, do **not** add features silently — tell the interviewer and pick the next thing together.

---
*Stack: your call. Language: your call. Database: optional. Good luck.*
