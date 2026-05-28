import type { Classification, GuestMessage } from './types.js';
import { playFor } from './playbook.js';

/**
 * Prompt builders for the C.L.O.S.E.R. reply (consumed by the OpenAI adapter, which exposes
 * get_listing / check_availability as tools). The system prompt enforces the structure and the
 * no-fabrication rule; the user prompt supplies the grounded context for THIS inquiry.
 */

export const CLOSER_SYSTEM_PROMPT = `You write the reply to a short-term-rental guest using the C.L.O.S.E.R. sales-conversion framework (Hormozi). Output ONE short paragraph, 3–5 sentences, in the guest's language. Cover all six beats IN ORDER but DO NOT label them — they are internal scaffolding, not headings:

  Clarify       — restate what the guest is actually asking so they feel heard.
  Label         — name the underlying need or concern.
  Overview      — fit the property to that need using REAL facts (beds, amenities, location).
  Sell certainty— state availability and total price from the tool calls, with no hedging.
  Explain       — one differentiator that matters for THIS inquiry, not a brochure dump.
  Request       — ask for the next step explicitly.

Ground the reply in real data. You have two tools — call them before writing:
  get_listing(listingId)                       → title, bedrooms, amenities, houseRules, basePrice
  check_availability(listingId, from, to)       → { available, nights, total }

Hard rules:
- Use ONLY facts returned by the tools or given in the prompt. Never invent prices, amenities, dates, or availability.
- If a beat lacks real data (e.g. an availability check failed or returned nothing), do NOT fabricate — say plainly what you can't confirm rather than guessing.
- No generic intros ("Thanks for reaching out!"). No hedging ("I think it might be available").
- Do not dump every amenity. End with an explicit ask.`;

/** Builds the user message: guest/classification context, reservation dates, and the message body. */
export function buildCloserPrompt(input: {
  message: GuestMessage;
  classification: Classification;
}): string {
  const { message, classification } = input;
  const e = classification.extracted_entities;

  const entityLines: string[] = [];
  if (e.dates.length > 0) entityLines.push(`dates: ${e.dates.join(', ')}`);
  if (e.guestCount != null) entityLines.push(`guests: ${e.guestCount}`);
  if (e.pets != null) entityLines.push(`pets: ${e.pets}`);
  if (e.vehicles != null) entityLines.push(`vehicles: ${e.vehicles}`);
  const entities = entityLines.length > 0 ? entityLines.join('; ') : '(none extracted)';

  // Per-code playbook: shapes the reply by the winning code rather than a generic six-beat dump.
  const play = playFor(classification.primary_code);

  const res = message.reservation;
  const reservationLine = res
    ? `Reservation: ${res.confirmationCode ?? res.id}` +
      (res.checkIn ? `, check-in ${res.checkIn}` : '') +
      (res.checkOut ? `, check-out ${res.checkOut}` : '')
    : 'Reservation: none (pre-booking inquiry)';

  return `Guest name: ${message.guestName ?? 'unknown'}
Language: ${message.language}
Listing id: ${message.listingId ?? 'unknown'}
Primary code: ${classification.primary_code} — ${play.strategy}
Strategy for this code: ${play.guidance}
Extracted entities: ${entities}
${reservationLine}

Guest's message:
"""
${message.body}
"""`;
}
