import 'dotenv/config';
import { describe, it, expect } from 'vitest';
import { createOpenAiLlm } from '../../src/adapters/llm/openaiLlm.js';
import { createMockGuesty } from '../../src/adapters/guesty/mockGuesty.js';
import { Classification } from '../../src/domain/types.js';
import { LOW_RISK_CODES } from '../../src/domain/taxonomy.js';
import { playFor } from '../../src/domain/playbook.js';
import { CASES } from './dataset.js';
import {
  caseToMessage,
  hasRealKey,
  mapWithConcurrency,
  scoreCloser,
  type CloserScore,
} from './score.js';
import { createReplyJudge, type JudgeVerdict } from './judge.js';

/**
 * C.L.O.S.E.R. completion eval — drives the real reply generator over every auto-send-eligible case
 * (grounded on the deterministic mock Guesty facts), then grades each reply TWO ways:
 *   1. heuristics (score.ts): falsifiable failures — length, explicit ask, grounded, on-topic, and
 *      NO hallucinated price (every $ figure must be one the tools actually returned).
 *   2. LLM-as-judge (judge.ts): nuance the heuristics can't see — grounding, relevance, tone, overall —
 *      scored against the same listing facts at temperature 0.
 *
 * Both matter: heuristics are cheap/deterministic; the judge catches "technically passes but reads
 * badly". Thresholds are calibrated to observed performance with headroom; judge scores are asserted as
 * MEANS (never a single reply), since the judge is itself a model. Skipped without a real key.
 */
const MODEL = process.env.OPENAI_MODEL || 'gpt-4o-mini';

const REPLY_CASES = CASES.filter((c) => LOW_RISK_CODES.has(c.expectedPrimary) && !c.expectedRisk);

const mean = (xs: number[]) => (xs.length === 0 ? 0 : xs.reduce((a, b) => a + b, 0) / xs.length);

describe.skipIf(!hasRealKey())('C.L.O.S.E.R. completion eval (live OpenAI)', () => {
  const llm = createOpenAiLlm({ model: MODEL });
  const judge = createReplyJudge({ model: MODEL });

  it(
    'produces complete, grounded replies (heuristics) that a manager would send (LLM-judge)',
    async () => {
      const scored = await mapWithConcurrency(
        REPLY_CASES,
        async (c) => {
          // Pin the classification to the gold code so we grade the REPLY, not the classifier.
          const classification = Classification.parse({
            primary_code: c.expectedPrimary,
            confidence: 0.9,
            risk_flag: false,
            extracted_entities: { dates: ['Apr 24–26'], guestCount: 2, pets: null, vehicles: null },
          });
          const reply = await llm.generateReply(
            { message: caseToMessage(c, classification), classification },
            createMockGuesty(),
          );
          const play = playFor(c.expectedPrimary);
          const heuristic = scoreCloser(reply, play.replyMustMention);
          const verdict = await judge.judge({
            guestMessage: c.body,
            code: c.expectedPrimary,
            strategy: play.strategy,
            reply,
          });
          return { id: c.id, code: c.expectedPrimary, reply, heuristic, verdict };
        },
        4, // each case = generate + judge (2 calls); keep the pool modest
      );

      const hRate = (pick: (s: CloserScore) => boolean) =>
        mean(scored.map((r) => (pick(r.heuristic) ? 1 : 0)));
      const jMean = (pick: (v: JudgeVerdict) => number) => mean(scored.map((r) => pick(r.verdict)));

      // --- report ---
      console.log('\n=== C.L.O.S.E.R. completion eval ===');
      for (const r of scored) {
        const s = r.heuristic;
        const flags = [
          s.sentenceCountOk ? '' : `len=${s.sentenceCount}`,
          s.explicitAsk ? '' : 'no-ask',
          s.noHedging ? '' : 'hedging',
          s.noGenericIntro ? '' : 'generic-intro',
          s.grounded ? '' : 'ungrounded',
          s.mentionsCodeFacet ? '' : 'off-topic',
          s.noHallucinatedPrice ? '' : 'BAD-PRICE',
        ].filter(Boolean).join(',');
        console.log(
          `${r.id.padEnd(18)} [${r.code}] ${s.passAll ? 'PASS' : 'FAIL ' + flags}  ` +
            `judge: g${r.verdict.grounding} r${r.verdict.relevance} t${r.verdict.tone} =${r.verdict.overall}`,
        );
      }
      const passAllRate = hRate((s) => s.passAll);
      console.log(
        `\nheuristics — passAll ${(passAllRate * 100).toFixed(0)}%  ` +
          `ask ${(hRate((s) => s.explicitAsk) * 100).toFixed(0)}%  ` +
          `grounded ${(hRate((s) => s.grounded) * 100).toFixed(0)}%  ` +
          `priceOk ${(hRate((s) => s.noHallucinatedPrice) * 100).toFixed(0)}%`,
      );
      console.log(
        `judge means — grounding ${jMean((v) => v.grounding).toFixed(2)}  ` +
          `relevance ${jMean((v) => v.relevance).toFixed(2)}  ` +
          `tone ${jMean((v) => v.tone).toFixed(2)}  overall ${jMean((v) => v.overall).toFixed(2)}  ` +
          `ask ${(mean(scored.map((r) => (r.verdict.hasExplicitAsk ? 1 : 0))) * 100).toFixed(0)}%`,
      );

      // --- heuristic assertions (calibrated to observed, with headroom) ---
      // Observed: passAll 93%, ask 98%, grounded 100%, priceOk 100%, facet ~98%. The facet check is
      // English-only, so Spanish replies can trip it (e.g. "estacionamiento" ≠ "parking") — a known
      // heuristic limitation the LLM-judge compensates for; hence the modest facet bar.
      expect(hRate((s) => s.explicitAsk)).toBeGreaterThanOrEqual(0.85);
      expect(hRate((s) => s.grounded)).toBeGreaterThanOrEqual(0.85);
      expect(hRate((s) => s.noHallucinatedPrice), 'hallucinated prices').toBeGreaterThanOrEqual(0.95);
      expect(hRate((s) => s.mentionsCodeFacet)).toBeGreaterThanOrEqual(0.75);
      expect(passAllRate).toBeGreaterThanOrEqual(0.8);

      // --- LLM-judge assertions (means, with tolerance for a model grader) ---
      // Observed across runs: grounding 3.3–3.6, relevance 4.2–4.3, tone ~4.1, overall 3.9–4.1. The judge
      // is deliberately strict on grounding (it wants the reply to USE the specific facts, not just name
      // the listing) AND is itself non-deterministic — so the grounding bar carries the most headroom: a
      // drop below 3.0 means replies are genuinely getting vaguer, not just judge noise.
      expect(jMean((v) => v.grounding)).toBeGreaterThanOrEqual(3.0);
      expect(jMean((v) => v.relevance)).toBeGreaterThanOrEqual(3.9);
      expect(jMean((v) => v.overall)).toBeGreaterThanOrEqual(3.7);
    },
    300_000,
  );
});
