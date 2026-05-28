import type { TrafficLightCode } from '../../src/domain/types.js';

/**
 * Labeled eval set for the classifier (the "agent"). One realistic guest message per case with the
 * code a human would assign per CHALLENGE.md §6. This is the ground truth the precision eval scores
 * against — kept hand-checked rather than large and noisy, since each case must be a code a reasonable
 * annotator would agree on.
 *
 * `alsoAccept` covers genuine ambiguity (a message two codes could defensibly carry); the grader counts
 * a prediction correct if it matches `expectedPrimary` OR any `alsoAccept`. Use it sparingly — it's for
 * real ambiguity, not for excusing the model.
 *
 * The set is deliberately adversarial, not just clean single-intent messages (those score ~100% and
 * tell you nothing). The hard groups probe the failure modes that actually bite in production:
 *  - multi_intent — several concerns at once; the §6 PRIORITY rule decides the primary, the other is secondary.
 *  - decoy        — a trigger word is present but means something else ("no rush to *book*, are pets ok?").
 *  - negation     — the guest rules a concern OUT ("I *don't* need parking, but late checkout?").
 *  - noisy        — typos, slang, no punctuation — real OTA chat.
 *  - multilingual — Spanish (the real system runs in Bogotá); intent must survive translation.
 *  - risk         — restricted content (off-platform, address leak, guarantees) that MUST set risk_flag.
 *  - ambiguous    — genuinely sparse; the model should NOT be over-confident (see maxConfidence).
 */
export interface EvalCase {
  id: string;
  body: string;
  language?: string;
  /** The single code a human annotator assigns. */
  expectedPrimary: TrafficLightCode;
  /** Defensible alternatives that should also count as correct for the PRIMARY. */
  alsoAccept?: TrafficLightCode[];
  /**
   * For multi-intent messages: the second concern the §6 priority rule demotes. Scored as "captured"
   * if the model surfaces it as either primary or secondary_code — i.e. did the agent SEE both blockers.
   */
  expectedSecondary?: TrafficLightCode;
  /** When set, the case additionally checks risk_flag matches (restricted content). */
  expectedRisk?: boolean;
  /**
   * Entities a correct read should extract. Only the provided keys are checked. `dates: true` means
   * "at least one date phrase"; guestCount/pets/vehicles are checked for the exact value.
   */
  expectedEntities?: {
    dates?: boolean;
    guestCount?: number;
    pets?: boolean;
    vehicles?: number;
  };
  /**
   * Calibration cap for genuinely sparse/ambiguous messages: the model's confidence should not exceed
   * this. A classifier that's 0.97-sure about "weekend for a few of us?" is miscalibrated, even if the
   * code is right. Checked as a rate, not all-or-nothing.
   */
  maxConfidence?: number;
  /**
   * 'easy' = phrased with the §6 trigger words (measures the model can read the taxonomy);
   * 'hard' = paraphrased/adversarial (measures generalization, not keyword echo). The easy↔hard
   * accuracy gap is the overfitting signal.
   */
  tier: 'easy' | 'hard';
  /** Reporting bucket so a regression can be localized to a failure mode (see groups above). */
  group:
    | 'core'
    | 'multi_intent'
    | 'decoy'
    | 'negation'
    | 'noisy'
    | 'multilingual'
    | 'risk'
    | 'ambiguous'
    | 'priority';
}

export const CASES: EvalCase[] = [
  // ============================================================================
  // EASY / CORE — phrased with §6 trigger words. The baseline; should score high.
  // ============================================================================
  { id: 'g1-book', body: "I'd like to book the apartment for next weekend.", expectedPrimary: 'G1', tier: 'easy', group: 'core' },
  { id: 'g1-confirm', body: "Ready to confirm and pay — how do I reserve?", expectedPrimary: 'G1', tier: 'easy', group: 'core' },
  { id: 'g2-wedding', body: "We're looking for a place for our wedding weekend in June for 6 guests.", expectedPrimary: 'G2', alsoAccept: ['Y6'], expectedEntities: { guestCount: 6 }, tier: 'easy', group: 'core' },
  { id: 'g2-work', body: "I need somewhere for a 3-week work stay starting next month.", expectedPrimary: 'G2', alsoAccept: ['G1'], tier: 'easy', group: 'core' },
  { id: 'y1-parking', body: "Is there free parking at the building?", expectedPrimary: 'Y1', tier: 'easy', group: 'core' },
  { id: 'y1-access', body: "How do I get into the apartment when I arrive?", expectedPrimary: 'Y1', tier: 'easy', group: 'core' },
  { id: 'y2-deposit', body: "Is there a security deposit, and when do I get it back?", expectedPrimary: 'Y2', tier: 'easy', group: 'core' },
  { id: 'y2-cancel', body: "What's your cancellation policy if my plans change?", expectedPrimary: 'Y2', tier: 'easy', group: 'core' },
  { id: 'y3-beds', body: "How many beds are there? Is it ok for a family of four?", expectedPrimary: 'Y3', alsoAccept: ['Y6'], expectedEntities: { guestCount: 4 }, tier: 'easy', group: 'core' },
  { id: 'y4-checkin', body: "Can I check in at 10pm? My flight lands late.", expectedPrimary: 'Y4', tier: 'easy', group: 'core' },
  { id: 'y4-luggage', body: "Could I drop my luggage off before check-in?", expectedPrimary: 'Y4', tier: 'easy', group: 'core' },
  { id: 'y5-pets', body: "Can I bring my small dog along?", expectedPrimary: 'Y5', expectedEntities: { pets: true }, tier: 'easy', group: 'core' },
  { id: 'y5-party', body: "Is it ok to have a small party with about 15 friends?", expectedPrimary: 'Y5', tier: 'easy', group: 'core' },
  { id: 'y6-dates', body: "Are the dates April 24–26 still available for 2?", expectedPrimary: 'Y6', expectedEntities: { dates: true, guestCount: 2 }, tier: 'easy', group: 'core' },
  { id: 'y6-weekend', body: "Do you have availability this weekend?", expectedPrimary: 'Y6', tier: 'easy', group: 'core' },
  { id: 'y7-total', body: "What's the total price including all fees and cleaning?", expectedPrimary: 'Y7', tier: 'easy', group: 'core' },
  { id: 'y7-fees', body: "Are there extra cleaning or service fees on top of the nightly rate?", expectedPrimary: 'Y7', tier: 'easy', group: 'core' },
  { id: 'r1-discount', body: "Any chance of a discount for a 5-night stay?", expectedPrimary: 'R1', tier: 'easy', group: 'core' },
  { id: 'r1-bestprice', body: "Can you do better on the price? What's your best deal?", expectedPrimary: 'R1', tier: 'easy', group: 'core' },
  { id: 'r2-expensive', body: "This is a bit expensive for us — anything cheaper available?", expectedPrimary: 'R2', tier: 'easy', group: 'core' },
  { id: 'x1-hi', body: "Hi!", expectedPrimary: 'X1', tier: 'easy', group: 'core' },
  { id: 'x1-interested', body: "Interested 🙂", expectedPrimary: 'X1', tier: 'easy', group: 'core' },

  // ============================================================================
  // PRIORITY — §6 chain: RED > Y5 > Y2 > Y4 > Y1 > Y3 > Y6 > Y7 > GREEN > GRAY.
  // Multi-signal; the demoted concern is the expectedSecondary.
  // ============================================================================
  { id: 'prio-r1-over-y1', body: "Any discount? Also, is parking included?", expectedPrimary: 'R1', expectedSecondary: 'Y1', tier: 'easy', group: 'priority' },
  { id: 'prio-g1-over-y6', body: "I want to book it for next weekend.", expectedPrimary: 'G1', alsoAccept: ['Y6'], tier: 'easy', group: 'priority' },
  { id: 'prio-r1-over-g1', body: "I'm ready to book — but can you do anything on the price first?", expectedPrimary: 'R1', expectedSecondary: 'G1', tier: 'hard', group: 'priority' },
  { id: 'prio-y5-over-y6', body: "Is it free next weekend, and are dogs allowed?", expectedPrimary: 'Y5', expectedSecondary: 'Y6', expectedEntities: { pets: true }, tier: 'hard', group: 'priority' },
  { id: 'prio-y2-over-y4', body: "Do you take a deposit, and could I check in late?", expectedPrimary: 'Y2', expectedSecondary: 'Y4', tier: 'hard', group: 'priority' },
  { id: 'prio-y4-over-y1', body: "Arriving around midnight — where would I park once I'm there?", expectedPrimary: 'Y4', expectedSecondary: 'Y1', alsoAccept: ['Y1'], tier: 'hard', group: 'priority' },
  { id: 'prio-y3-over-y7', body: "Would 4 of us fit, and what's the all-in for two nights?", expectedPrimary: 'Y3', expectedSecondary: 'Y7', alsoAccept: ['Y7'], expectedEntities: { guestCount: 4 }, tier: 'hard', group: 'priority' },
  { id: 'prio-r2-over-y6', body: "Bit out of our budget honestly — is it even open next weekend?", expectedPrimary: 'R2', expectedSecondary: 'Y6', tier: 'hard', group: 'priority' },

  // ============================================================================
  // MULTI-INTENT — two real questions, no explicit priority keyword collision.
  // ============================================================================
  { id: 'mi-avail-fees', body: "Is it open this weekend, and what are the cleaning fees?", expectedPrimary: 'Y6', expectedSecondary: 'Y7', alsoAccept: ['Y7'], tier: 'hard', group: 'multi_intent' },
  { id: 'mi-beds-total', body: "How many beds, and what would the total come to for the weekend?", expectedPrimary: 'Y3', expectedSecondary: 'Y7', alsoAccept: ['Y7'], tier: 'hard', group: 'multi_intent' },
  { id: 'mi-checkin-parking', body: "What time can we check in, and is there parking nearby?", expectedPrimary: 'Y4', expectedSecondary: 'Y1', alsoAccept: ['Y1'], tier: 'hard', group: 'multi_intent' },

  // ============================================================================
  // DECOY — a §6 trigger word appears but the real intent is something else.
  // Pure keyword matchers fail these; a model that reads intent should not.
  // ============================================================================
  { id: 'decoy-book-pets', body: "No rush to book yet — just checking first, are pets allowed?", expectedPrimary: 'Y5', expectedEntities: { pets: true }, tier: 'hard', group: 'decoy' },
  { id: 'decoy-cancel-parking', body: "A friend had to cancel her own trip, lucky us! Anyway — is parking free nearby?", expectedPrimary: 'Y1', tier: 'hard', group: 'decoy' },
  { id: 'decoy-cheap-compliment', body: "What a steal, looks lovely and not pricey at all! Can I grab it for Friday?", expectedPrimary: 'G1', alsoAccept: ['Y6'], tier: 'hard', group: 'decoy' },
  { id: 'decoy-party-quiet', body: "We're a quiet couple, definitely not party people — is a late checkout possible?", expectedPrimary: 'Y4', tier: 'hard', group: 'decoy' },
  { id: 'decoy-discount-noprice', body: "Your place is a great discount on hotels for the space — how many bedrooms is it?", expectedPrimary: 'Y3', tier: 'hard', group: 'decoy' },

  // ============================================================================
  // NEGATION — the guest rules a concern OUT; the real ask is the other clause.
  // ============================================================================
  { id: 'neg-noparking-late', body: "I don't need parking, but is a late checkout possible?", expectedPrimary: 'Y4', tier: 'hard', group: 'negation' },
  { id: 'neg-nopets-beds', body: "No pets or anything like that — just wondering how many beds there are?", expectedPrimary: 'Y3', tier: 'hard', group: 'negation' },
  { id: 'neg-nodiscount-total', body: "I'm not after a discount, just want to confirm the total with all fees included.", expectedPrimary: 'Y7', tier: 'hard', group: 'negation' },

  // ============================================================================
  // NOISY — typos, slang, missing punctuation. Real OTA chat.
  // ============================================================================
  { id: 'noisy-avail', body: "yo this place still open fri-sun?? for 4 ppl", expectedPrimary: 'Y6', expectedEntities: { guestCount: 4 }, tier: 'hard', group: 'noisy' },
  { id: 'noisy-park', body: "heyy is ther somwhere 2 park the car nearby", expectedPrimary: 'Y1', tier: 'hard', group: 'noisy' },
  { id: 'noisy-book', body: "ok im in how do i pay n lock the dates", expectedPrimary: 'G1', tier: 'hard', group: 'noisy' },

  // ============================================================================
  // MULTILINGUAL — Spanish (system runs in Bogotá). Intent must survive language.
  // ============================================================================
  { id: 'es-avail', body: "Hola, ¿tienen disponibilidad para el próximo fin de semana para 3 personas?", language: 'es', expectedPrimary: 'Y6', expectedEntities: { guestCount: 3 }, tier: 'hard', group: 'multilingual' },
  { id: 'es-parking', body: "¿Hay estacionamiento gratis cerca del apartamento?", language: 'es', expectedPrimary: 'Y1', tier: 'hard', group: 'multilingual' },
  { id: 'es-discount', body: "¿Me pueden hacer un descuento si reservo varias noches?", language: 'es', expectedPrimary: 'R1', tier: 'hard', group: 'multilingual' },
  { id: 'es-checkin', body: "¿Puedo hacer el check-in tarde, sobre las 11 de la noche?", language: 'es', expectedPrimary: 'Y4', tier: 'hard', group: 'multilingual' },
  { id: 'es-book', body: "Perfecto, quiero reservarlo para este finde. ¿Cómo pago?", language: 'es', expectedPrimary: 'G1', tier: 'hard', group: 'multilingual' },

  // ============================================================================
  // RISK — restricted content. risk_flag MUST be set (safety-critical), regardless
  // of code. These should never auto-send.
  // ============================================================================
  { id: 'risk-venmo', body: "Can I just pay you directly over Venmo instead of through Airbnb?", expectedPrimary: 'Y2', alsoAccept: ['G1', 'R1', 'X1'], expectedRisk: true, tier: 'easy', group: 'risk' },
  { id: 'risk-offplatform', body: "Let's take this off the platform — text me at my number and I'll send cash.", expectedPrimary: 'X1', alsoAccept: ['Y2', 'G1', 'R1'], expectedRisk: true, tier: 'easy', group: 'risk' },
  { id: 'risk-address', body: "Can you send me the exact street address so I can drive past it tonight?", expectedPrimary: 'Y1', alsoAccept: ['X1', 'Y2'], expectedRisk: true, tier: 'hard', group: 'risk' },
  { id: 'risk-guarantee', body: "Can you guarantee it will be completely silent the whole stay, no exceptions?", expectedPrimary: 'Y3', alsoAccept: ['X1', 'Y2'], expectedRisk: true, tier: 'hard', group: 'risk' },
  { id: 'risk-whatsapp', body: "Easier to chat on WhatsApp — here's my number, 300-555-1234, message me there.", expectedPrimary: 'X1', alsoAccept: ['Y2'], expectedRisk: true, tier: 'hard', group: 'risk' },
  { id: 'risk-cash-discount', body: "If I pay in cash outside the app, can you knock 20% off the price?", expectedPrimary: 'R1', alsoAccept: ['Y2'], expectedRisk: true, tier: 'hard', group: 'risk' },

  // ============================================================================
  // AMBIGUOUS — genuinely sparse. Code may be right, but confidence should be modest
  // (maxConfidence). Catches an over-confident classifier.
  // ============================================================================
  { id: 'amb-weekend-few', body: "weekend for a few of us?", expectedPrimary: 'Y6', alsoAccept: ['X1'], maxConfidence: 0.85, tier: 'hard', group: 'ambiguous' },
  { id: 'amb-comfortable', body: "will we be ok in there?", expectedPrimary: 'Y3', alsoAccept: ['X1'], maxConfidence: 0.85, tier: 'hard', group: 'ambiguous' },
  { id: 'amb-soon', body: "thinking about it for soon, is that doable?", expectedPrimary: 'X1', alsoAccept: ['Y6'], maxConfidence: 0.9, tier: 'hard', group: 'ambiguous' },
  { id: 'amb-nice', body: "looks nice 👀", expectedPrimary: 'X1', maxConfidence: 0.95, tier: 'hard', group: 'ambiguous' },

  // ============================================================================
  // HARD CORE — paraphrased single-intent (kept from the original hard tier).
  // ============================================================================
  { id: 'h-g1-lockin', body: "Great, let's lock it in — what's the next step on my end?", expectedPrimary: 'G1', tier: 'hard', group: 'core' },
  { id: 'h-y1-car', body: "Where would I leave the car overnight near the place?", expectedPrimary: 'Y1', tier: 'hard', group: 'core' },
  { id: 'h-y2-money', body: "If something comes up and I have to back out, what happens to my money?", expectedPrimary: 'Y2', tier: 'hard', group: 'core' },
  { id: 'h-y3-comfort', body: "Would three adults actually be comfortable in there?", expectedPrimary: 'Y3', expectedEntities: { guestCount: 3 }, tier: 'hard', group: 'core' },
  { id: 'h-y4-midnight', body: "My train gets in close to midnight — is that workable for getting the keys?", expectedPrimary: 'Y4', alsoAccept: ['Y1'], tier: 'hard', group: 'core' },
  { id: 'h-y6-open', body: "Is it still open for the two nights at the end of April?", expectedPrimary: 'Y6', tier: 'hard', group: 'core' },
  { id: 'h-y7-allin', body: "What's the all-in number once everything's added up?", expectedPrimary: 'Y7', tier: 'hard', group: 'core' },
  { id: 'h-r1-meet', body: "Could you meet me partway on the nightly rate?", expectedPrimary: 'R1', tier: 'hard', group: 'core' },
  { id: 'h-r2-stretch', body: "Honestly that's a bit more than we can stretch to right now.", expectedPrimary: 'R2', tier: 'hard', group: 'core' },
  { id: 'h-x1-wave', body: "👋", expectedPrimary: 'X1', maxConfidence: 0.95, tier: 'hard', group: 'core' },
];
