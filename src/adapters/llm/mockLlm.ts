import type { GuestyPort } from '../../ports/GuestyPort.js';
import type {
  ClassifyInput,
  GenerateReplyInput,
  LlmPort,
} from '../../ports/LlmPort.js';
import { Classification, type TrafficLightCode } from '../../domain/types.js';
import { highestPriority } from '../../domain/taxonomy.js';

/**
 * Deterministic, offline LlmPort. Used by tests and whenever OPENAI_API_KEY is absent, so the
 * vertical slice runs end-to-end with no network. The classifier is keyword-based against the
 * CHALLENGE.md §6 triggers; the reply generator templates a C.L.O.S.E.R. paragraph from real mock
 * facts. No model calls — same input always yields the same output.
 */

/** Trigger words per code (CHALLENGE.md §6). Order here is irrelevant — priority is resolved later. */
const TRIGGERS: Array<{ code: TrafficLightCode; words: string[] }> = [
  { code: 'G1', words: ['book', 'reserve', 'confirm', 'pay'] },
  { code: 'G2', words: ['wedding', 'family trip', 'work stay', 'honeymoon'] },
  { code: 'Y1', words: ['parking', 'directions', 'access', 'how do i get'] },
  { code: 'Y2', words: ['deposit', 'refund', 'cancel', 'id ', 'identification'] },
  { code: 'Y3', words: ['beds', 'bed', 'layout', 'stairs', 'size', 'sleep'] },
  { code: 'Y4', words: ['check-in', 'check in', 'checkout', 'check-out', 'early', 'late', 'luggage'] },
  { code: 'Y5', words: ['pet', 'pets', 'party', 'visitors', 'rules', 'smoke'] },
  { code: 'Y6', words: ['dates', 'calendar', 'availability', 'available', 'weekend', 'vacancy'] },
  { code: 'Y7', words: ['total', 'fees', 'fee', 'cleaning', 'taxes', 'price', 'cost'] },
  { code: 'R1', words: ['discount', 'deal', 'best price', 'lower', 'negotiate'] },
  { code: 'R2', words: ['expensive', 'cheaper', 'afford', 'budget'] },
];

/** Restricted-content cues that force escalation (off-platform payment, address leak, guarantees). */
const RISK_CUES = [
  'venmo', 'zelle', 'cash app', 'cashapp', 'paypal', 'wire',
  'off airbnb', 'off-airbnb', 'my address', 'guarantee',
];

/** Vague openers / low-signal bodies → X1. */
const VAGUE = ['hi', 'hello', 'hey', 'interested', 'info', 'question'];

const EMOJI_ONLY = /^[\p{Emoji}\s]+$/u;

function detectCodes(text: string): TrafficLightCode[] {
  const found = new Set<TrafficLightCode>();
  for (const { code, words } of TRIGGERS) {
    if (words.some((w) => text.includes(w))) found.add(code);
  }
  return [...found];
}

export function createMockLlm(): LlmPort {
  return {
    async classify(input: ClassifyInput): Promise<Classification> {
      const text = input.body.toLowerCase();
      const trimmed = input.body.trim();

      const codes = detectCodes(text);
      const risk_flag = RISK_CUES.some((cue) => text.includes(cue));

      // Default bias: unclear → Gray, not Green. X1 stands in when nothing concrete matched, the
      // body is just a greeting, or it's emoji/whitespace only.
      const vague =
        codes.length === 0 ||
        EMOJI_ONLY.test(trimmed) ||
        (trimmed.split(/\s+/).length <= 2 && VAGUE.some((v) => text.includes(v)));

      let detected = vague ? [] : codes;

      // Explicit booking intent (G1) dominates incidental soft signals like a "weekend" (Y6) or a
      // "total" (Y7) mention in the same breath — e.g. "book for next weekend" is a lay-down, not an
      // availability question. The taxonomy priority order ranks Y6/Y7 above GREEN for genuine
      // hurdles, so we only drop those two when a hard GREEN verb is also present. RED and the
      // higher-priority yellows are left intact so the priority rule still holds (haggle → R1).
      if (detected.includes('G1')) {
        detected = detected.filter((c) => c !== 'Y6' && c !== 'Y7');
      }

      const primary_code = highestPriority(detected);

      // Secondary = a second distinct detected code (the next-highest priority one).
      const ordered = [...detected].sort(
        (a, b) => detected.indexOf(a) - detected.indexOf(b),
      );
      const secondary_code =
        detected.length >= 2
          ? highestPriority(detected.filter((c) => c !== primary_code))
          : null;

      // Confidence: one clear match is high, ambiguity (≥2 or none) is lower.
      const confidence =
        detected.length === 1 ? 0.9 : detected.length >= 2 ? 0.7 : 0.5;

      const rationale = vague
        ? 'No concrete intent matched; treating as low-signal (X1).'
        : `Matched ${detected.join(', ')}; primary ${primary_code} by priority rule.`;

      // Parse through the Zod schema so the returned object is guaranteed contract-valid.
      return Classification.parse({
        primary_code,
        secondary_code,
        confidence,
        extracted_entities: extractEntities(input.body),
        risk_flag,
        rationale,
      });
    },

    async generateReply(
      input: GenerateReplyInput,
      guesty: GuestyPort,
    ): Promise<string> {
      const { message, classification } = input;

      const listing = await guesty.getListing(message.listingId ?? 'default');

      const res = message.reservation;
      const availability =
        res?.checkIn && res?.checkOut
          ? await guesty.checkAvailability(listing.id, res.checkIn, res.checkOut)
          : null;

      const name = message.guestName ?? 'there';
      const dates = classification.extracted_entities.dates;
      const guests = classification.extracted_entities.guestCount;

      // C.L.O.S.E.R. beats in order, unlabeled (CHALLENGE.md §6). Templated from real mock facts.
      const clarify = dates.length
        ? `Hi ${name} — you're asking about ${dates.join(' to ')}${guests ? ` for ${guests}` : ''}.`
        : `Hi ${name} — happy to help with your stay${guests ? ` for ${guests}` : ''}.`;
      const label = `Sounds like you want to lock in the right place without any surprises.`;
      const overview = `Our ${listing.title} sleeps ${listing.bedrooms * 2}, has ${listing.amenities[0]}, and sits right on ${listing.address ?? 'a great block'}.`;
      const sell = availability
        ? `${availability.available ? 'Those dates are open' : 'Let me double-check those dates'} and the total comes to $${availability.total} for ${availability.nights} night${availability.nights === 1 ? '' : 's'}, all-in.`
        : `Send me your dates and I'll confirm availability and the all-in total right away.`;
      const explain = `Guests consistently mention the quiet courtyard bedroom — a real night's sleep in the middle of the city.`;
      const request = `Want me to hold it for you while you decide?`;

      return [clarify, label, overview, sell, explain, request].join(' ');
    },
  };
}

/** Best-effort entity extraction from the raw body — kept tiny; the real model does better. */
function extractEntities(body: string): {
  dates: string[];
  guestCount: number | null;
  pets: boolean | null;
  vehicles: number | null;
} {
  const text = body.toLowerCase();

  const dates: string[] = [];
  if (/weekend/.test(text)) dates.push('this weekend');
  const monthDay = body.match(
    /\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s*\d{1,2}/gi,
  );
  if (monthDay) dates.push(...monthDay);

  const guestMatch = text.match(/(\d+)\s*(?:adults?|guests?|people|pax)/);
  const guestCount = guestMatch ? Number(guestMatch[1]) : null;

  const pets = /\bpets?\b|\bdog\b|\bcat\b/.test(text) ? true : null;

  const vehicleMatch = text.match(/(\d+)\s*(?:cars?|vehicles?)/);
  const vehicles = vehicleMatch ? Number(vehicleMatch[1]) : null;

  return { dates, guestCount, pets, vehicles };
}
