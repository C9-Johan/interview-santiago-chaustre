import type { Classification, GuestMessage, TrafficLightCode } from '../../src/domain/types.js';
import { TRAFFIC_LIGHT_CODES } from '../../src/domain/types.js';
import type { EvalCase } from './dataset.js';
import { mentionsAny } from './match.js';

/**
 * Pure scorers for the eval suites — no I/O, no model calls. Kept separate from the test files so the
 * metric definitions are reviewable on their own and reused by both the classification and completion
 * evals. The model calls live in the *.eval.test.ts files; everything here is deterministic.
 */

/** True when a real OpenAI key is configured (placeholder keys in .env.example don't count). */
export function hasRealKey(): boolean {
  const key = process.env.OPENAI_API_KEY;
  return Boolean(key) && !key!.includes('REPLACE');
}

// ---------------------------------------------------------------------------
// Classification precision
// ---------------------------------------------------------------------------

/** A prediction counts as correct if it matches the gold code or any accepted alternative. */
export function isCorrect(c: EvalCase, predicted: TrafficLightCode): boolean {
  return predicted === c.expectedPrimary || (c.alsoAccept?.includes(predicted) ?? false);
}

/**
 * Run an async fn over items with bounded concurrency, preserving input order. The dataset is ~70
 * cases; firing all of them at the model at once invites 429s, and an unbounded Promise.all also lets a
 * single rate-limit blip fail the whole run. A small pool keeps it fast but polite.
 */
export async function mapWithConcurrency<T, R>(
  items: readonly T[],
  fn: (item: T, index: number) => Promise<R>,
  limit = 6,
): Promise<R[]> {
  const results = new Array<R>(items.length);
  let next = 0;
  async function worker(): Promise<void> {
    while (next < items.length) {
      const i = next++;
      results[i] = await fn(items[i]!, i);
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}

// ---------------------------------------------------------------------------
// Secondary intent · entities · confidence calibration
// ---------------------------------------------------------------------------

/**
 * Did the agent SEE the demoted second concern of a multi-intent message? Captured if the model put
 * `expectedSecondary` in EITHER slot (a borderline read may legitimately flip them; what matters is the
 * blocker wasn't dropped). Returns null when the case has no secondary label.
 */
export function capturedSecondary(
  c: EvalCase,
  primary: TrafficLightCode,
  secondary: TrafficLightCode | null,
): boolean | null {
  if (!c.expectedSecondary) return null;
  return primary === c.expectedSecondary || secondary === c.expectedSecondary;
}

/** Entity-extraction score for a case: how many declared entity expectations the model got right. */
export function scoreEntities(
  c: EvalCase,
  entities: Classification['extracted_entities'],
): { checked: number; correct: number } {
  const exp = c.expectedEntities;
  if (!exp) return { checked: 0, correct: 0 };
  let checked = 0;
  let correct = 0;
  if (exp.dates !== undefined) {
    checked++;
    if (entities.dates.length > 0 === exp.dates) correct++;
  }
  if (exp.guestCount !== undefined) {
    checked++;
    if (entities.guestCount === exp.guestCount) correct++;
  }
  if (exp.pets !== undefined) {
    checked++;
    if (entities.pets === exp.pets) correct++;
  }
  if (exp.vehicles !== undefined) {
    checked++;
    if (entities.vehicles === exp.vehicles) correct++;
  }
  return { checked, correct };
}

/**
 * Calibration check for sparse messages: confidence must not exceed the case's cap. Returns null when
 * the case sets no cap (most don't). A tiny tolerance absorbs rounding wobble.
 */
export function respectsConfidenceCap(c: EvalCase, confidence: number): boolean | null {
  if (c.maxConfidence === undefined) return null;
  return confidence <= c.maxConfidence + 1e-9;
}

export interface CodeMetric {
  code: TrafficLightCode;
  support: number; // gold cases with this code as expectedPrimary
  predicted: number; // times the model predicted this code
  tp: number; // predicted this code AND it was correct for that case
  precision: number; // tp / predicted
  recall: number; // (cases with this gold code the model got right) / support
}

export interface ClassificationReport {
  total: number;
  correct: number;
  accuracy: number;
  perCode: CodeMetric[];
  /** confusion[gold][predicted] = count */
  confusion: Record<string, Record<string, number>>;
}

/**
 * Compute accuracy + per-code precision/recall + a confusion matrix from gold cases and the model's
 * predictions (index-aligned). Precision is over the predicted label; recall is over the gold label.
 */
export function scoreClassification(
  cases: EvalCase[],
  predictions: TrafficLightCode[],
): ClassificationReport {
  const tpByPred = new Map<TrafficLightCode, number>();
  const predictedCount = new Map<TrafficLightCode, number>();
  const supportByGold = new Map<TrafficLightCode, number>();
  const recallHitByGold = new Map<TrafficLightCode, number>();
  const confusion: Record<string, Record<string, number>> = {};

  let correct = 0;
  cases.forEach((c, i) => {
    const pred = predictions[i]!;
    const ok = isCorrect(c, pred);
    if (ok) correct++;

    predictedCount.set(pred, (predictedCount.get(pred) ?? 0) + 1);
    if (ok) tpByPred.set(pred, (tpByPred.get(pred) ?? 0) + 1);

    supportByGold.set(c.expectedPrimary, (supportByGold.get(c.expectedPrimary) ?? 0) + 1);
    if (ok) recallHitByGold.set(c.expectedPrimary, (recallHitByGold.get(c.expectedPrimary) ?? 0) + 1);

    (confusion[c.expectedPrimary] ??= {})[pred] = ((confusion[c.expectedPrimary] ??= {})[pred] ?? 0) + 1;
  });

  const perCode: CodeMetric[] = TRAFFIC_LIGHT_CODES.map((code) => {
    const predicted = predictedCount.get(code) ?? 0;
    const tp = tpByPred.get(code) ?? 0;
    const support = supportByGold.get(code) ?? 0;
    const recallHit = recallHitByGold.get(code) ?? 0;
    return {
      code,
      support,
      predicted,
      tp,
      precision: predicted === 0 ? 1 : tp / predicted,
      recall: support === 0 ? 1 : recallHit / support,
    };
  });

  return {
    total: cases.length,
    correct,
    accuracy: cases.length === 0 ? 1 : correct / cases.length,
    perCode,
    confusion,
  };
}

/** Render a compact report for the test log so a failing threshold is debuggable at a glance. */
export function formatClassificationReport(r: ClassificationReport): string {
  const lines: string[] = [];
  lines.push(`accuracy ${r.correct}/${r.total} = ${(r.accuracy * 100).toFixed(1)}%`);
  lines.push('code  support  predicted  precision  recall');
  for (const m of r.perCode) {
    if (m.support === 0 && m.predicted === 0) continue; // skip codes absent from this run
    lines.push(
      `${m.code.padEnd(5)} ${String(m.support).padStart(7)} ${String(m.predicted).padStart(10)} ` +
        `${(m.precision * 100).toFixed(0).padStart(9)}% ${(m.recall * 100).toFixed(0).padStart(6)}%`,
    );
  }
  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// C.L.O.S.E.R. reply completion (heuristic)
// ---------------------------------------------------------------------------

const HEDGING = /\b(i think|i believe|might be|maybe|probably|possibly|not sure|should be)\b/i;
const GENERIC_INTRO = /(thanks for reaching out|thank you for (your message|reaching out)|thank you for getting in touch)/i;
// The Request beat = "ask for the next step EXPLICITLY" (§6). That's satisfied by a closing question
// OR an imperative call-to-action ("lock it in now", "follow the booking link", "let me know") — the
// real model often closes a G1 lay-down with a CTA, not a "?". We check the FINAL sentence so a CTA
// buried mid-reply doesn't count as the closing ask.
const CTA =
  /\b(book|reserv|lock (it|in)|secure your|follow the|click here|tap (here|the)|let me know|just (say|reply|let)|reply (back|to|here)|proceed|hold (it|the|your)|grab (it|the)|complete your|get you (booked|in))\b/i;
// Distinctive Soho-2BR facts from the mock listing — proves the reply was grounded, not free-floating.
// Matched with stemming (see match.ts), so "bedrooms"/"bedroom" etc. all count.
const GROUNDED_TERMS = ['soho', 'courtyard', 'spring', 'wifi', 'kitchen', 'bedroom', 'self-check'];
// Dollar figures the mock Guesty can justify: $200/night, $75 cleaning, $400 subtotal, $475 all-in
// (2 nights × 200 + 75). ANY other $ amount in a reply is a hallucinated price — the worst failure
// mode here, since a wrong total auto-sent to a guest is a real booking error.
const ALLOWED_PRICES = new Set([75, 200, 400, 475]);

export interface CloserScore {
  sentenceCount: number;
  sentenceCountOk: boolean; // 3–5 per §6 (allow 6 as slack for the real model)
  explicitAsk: boolean; // the Request beat — closing question OR an imperative next-step CTA
  noHedging: boolean;
  noGenericIntro: boolean;
  grounded: boolean; // mentions a real listing fact
  mentionsCodeFacet: boolean; // surfaces the facet the code is about (playbook.replyMustMention)
  noHallucinatedPrice: boolean; // every $ figure is one the tools actually returned
  passAll: boolean;
}

/** True if every `$<number>` in the text is a price the mock Guesty could have produced. */
function pricesAreGrounded(text: string): boolean {
  const matches = text.match(/\$\s?(\d[\d,]*)/g) ?? [];
  return matches.every((m) => ALLOWED_PRICES.has(Number(m.replace(/[^0-9]/g, ''))));
}

/**
 * Heuristic C.L.O.S.E.R. completion check. We can't cheaply assert all six beats are present, but we
 * can assert the load-bearing, falsifiable ones: short paragraph, explicit ask, grounded in real
 * facts, on-topic for the code, and free of the failure modes the brief calls out (hedging, generic
 * intros). `mustMention` is the code's `replyMustMention` term set from the playbook (matched with
 * stemming; omit ⇒ that check passes).
 */
export function scoreCloser(reply: string, mustMention?: readonly string[]): CloserScore {
  const text = reply.trim();
  const sentences = text.split(/[.!?]+/).map((s) => s.trim()).filter(Boolean);
  const sentenceCount = sentences.length;

  const sentenceCountOk = sentenceCount >= 3 && sentenceCount <= 6;
  const lastSentence = sentences[sentences.length - 1] ?? '';
  const explicitAsk = text.endsWith('?') || CTA.test(lastSentence);
  const noHedging = !HEDGING.test(text);
  const noGenericIntro = !GENERIC_INTRO.test(text);
  const grounded = mentionsAny(text, GROUNDED_TERMS);
  const mentionsCodeFacet = mustMention ? mentionsAny(text, mustMention) : true;
  const noHallucinatedPrice = pricesAreGrounded(text);

  return {
    sentenceCount,
    sentenceCountOk,
    explicitAsk,
    noHedging,
    noGenericIntro,
    grounded,
    mentionsCodeFacet,
    noHallucinatedPrice,
    passAll:
      sentenceCountOk &&
      explicitAsk &&
      noHedging &&
      noGenericIntro &&
      grounded &&
      mentionsCodeFacet &&
      noHallucinatedPrice,
  };
}

/** Build the minimal GuestMessage the reply generator needs from an eval case + its classification. */
export function caseToMessage(c: EvalCase, classification: Classification): GuestMessage {
  return {
    postId: `eval_${c.id}`,
    conversationId: `conv_${c.id}`,
    body: c.body,
    sender: 'guest',
    createdAt: '2026-04-20T12:00:00.000Z',
    platform: 'airbnb',
    language: c.language ?? 'en',
    guestName: 'Sam',
    listingId: 'soho-2br',
    // Give the generator concrete dates so the "Sell certainty" beat can call check_availability.
    reservation: { id: 'res_eval', checkIn: '2026-04-24', checkOut: '2026-04-26' },
    hostAlreadyReplied: false,
    thread: [{ body: c.body, sender: 'guest', createdAt: '2026-04-20T12:00:00.000Z' }],
  };
}
