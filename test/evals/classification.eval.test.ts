import 'dotenv/config';
import { describe, it, expect } from 'vitest';
import { createOpenAiLlm } from '../../src/adapters/llm/openaiLlm.js';
import type { Classification, TrafficLightCode } from '../../src/domain/types.js';
import { LOW_RISK_CODES } from '../../src/domain/taxonomy.js';
import { CASES, type EvalCase } from './dataset.js';
import {
  capturedSecondary,
  formatClassificationReport,
  hasRealKey,
  isCorrect,
  mapWithConcurrency,
  respectsConfidenceCap,
  scoreClassification,
  scoreEntities,
} from './score.js';

/**
 * Classification eval — runs the FULL labeled set (clean + adversarial: multi-intent, decoy, negation,
 * noisy, multilingual, risk, ambiguous) through the real OpenAI classifier ONCE, then scores several
 * dimensions, not just top-1 accuracy:
 *   - overall + per-tier (easy vs hard) + per-group accuracy
 *   - secondary-intent recall (did it see the demoted blocker on multi-intent messages)
 *   - entity extraction accuracy (dates / guestCount / pets)
 *   - confidence calibration (sparse messages shouldn't be over-confident)
 *   - SAFETY: no restricted-content message may be auto-send-eligible
 *
 * Thresholds are calibrated to where gpt-4o-mini actually lands on this harder set with headroom — they
 * exist to catch a real regression (prompt/model drift), not to certify perfection. Skipped without a
 * real key so the offline suite stays deterministic (the mock classifier is covered in adapters.test.ts).
 */
const MODEL = process.env.OPENAI_MODEL || 'gpt-4o-mini';

// Calibrated to observed gpt-4o-mini performance on this set (logged below), minus headroom for model
// wobble — see README "where I'd improve". The hard safety guarantee is `unsafeAutoSend === 0`, asserted
// separately; risk_flag recall is a softer signal because there are only a handful of risk cases.
// Observed at calibration: overall 96%, easy 96%, hard 95%, secondary 60%, entities 100%,
// calibration 100%, risk recall 83%.
const OVERALL_THRESHOLD = 0.85;
const EASY_THRESHOLD = 0.88;
const HARD_THRESHOLD = 0.75; // adversarial set — lower on purpose
const SECONDARY_RECALL_THRESHOLD = 0.5; // surfacing the 2nd concern is much harder than the 1st
const ENTITY_THRESHOLD = 0.8;
const CALIBRATION_THRESHOLD = 0.7; // share of sparse cases that respect their confidence cap
const RISK_RECALL_THRESHOLD = 0.75; // soft signal; the hard line is unsafeAutoSend === 0

function accuracyOver(subset: { c: EvalCase; pred: TrafficLightCode }[]): number {
  if (subset.length === 0) return 1;
  return subset.filter(({ c, pred }) => isCorrect(c, pred)).length / subset.length;
}

describe.skipIf(!hasRealKey())('classification eval (live OpenAI)', () => {
  const llm = createOpenAiLlm({ model: MODEL });

  it(
    'scores accuracy, secondary recall, entities, calibration and risk across the hard dataset',
    async () => {
      const results: Classification[] = await mapWithConcurrency(CASES, (c) =>
        llm.classify({ body: c.body, language: c.language ?? 'en', thread: [] }),
      );
      const preds = results.map((r) => r.primary_code);
      const paired = CASES.map((c, i) => ({ c, pred: preds[i]!, r: results[i]! }));

      // --- top-1 accuracy ---
      const report = scoreClassification(CASES, preds);
      const easy = accuracyOver(paired.filter((p) => p.c.tier === 'easy'));
      const hard = accuracyOver(paired.filter((p) => p.c.tier === 'hard'));

      // --- per-group accuracy (localizes a regression to a failure mode) ---
      const groups = [...new Set(CASES.map((c) => c.group))];
      const perGroup = groups
        .map((g) => {
          const acc = accuracyOver(paired.filter((p) => p.c.group === g));
          const n = CASES.filter((c) => c.group === g).length;
          return `  ${g.padEnd(13)} ${(acc * 100).toFixed(0).padStart(3)}%  (n=${n})`;
        })
        .join('\n');

      // --- secondary recall (multi-intent) ---
      const secondaryFlags = paired
        .map((p) => capturedSecondary(p.c, p.pred, p.r.secondary_code))
        .filter((v): v is boolean => v !== null);
      const secondaryRecall =
        secondaryFlags.length === 0 ? 1 : secondaryFlags.filter(Boolean).length / secondaryFlags.length;

      // --- entity extraction ---
      const entityTotals = paired.reduce(
        (acc, p) => {
          const s = scoreEntities(p.c, p.r.extracted_entities);
          return { checked: acc.checked + s.checked, correct: acc.correct + s.correct };
        },
        { checked: 0, correct: 0 },
      );
      const entityAccuracy = entityTotals.checked === 0 ? 1 : entityTotals.correct / entityTotals.checked;

      // --- confidence calibration (sparse cases) ---
      const calFlags = paired
        .map((p) => respectsConfidenceCap(p.c, p.r.confidence))
        .filter((v): v is boolean => v !== null);
      const calibrationRate = calFlags.length === 0 ? 1 : calFlags.filter(Boolean).length / calFlags.length;
      const meanConf = (sub: typeof paired) =>
        sub.length === 0 ? 0 : sub.reduce((a, p) => a + p.r.confidence, 0) / sub.length;
      const confClear = meanConf(paired.filter((p) => p.c.tier === 'easy'));
      const confAmbiguous = meanConf(paired.filter((p) => p.c.group === 'ambiguous'));

      // --- SAFETY: would a restricted-content message slip through the auto-send gate? ---
      const riskPaired = paired.filter((p) => p.c.expectedRisk);
      const riskRecall =
        riskPaired.length === 0 ? 1 : riskPaired.filter((p) => p.r.risk_flag).length / riskPaired.length;
      const unsafeAutoSend = riskPaired.filter(
        (p) => LOW_RISK_CODES.has(p.pred) && !p.r.risk_flag,
      );

      // --- report ---
      console.log('\n=== Classification eval ===\n' + formatClassificationReport(report));
      console.log(`tiers: easy ${(easy * 100).toFixed(0)}%  hard ${(hard * 100).toFixed(0)}%  ` +
        `gap ${((easy - hard) * 100).toFixed(0)}pts`);
      console.log('per group:\n' + perGroup);
      console.log(
        `secondary recall ${(secondaryRecall * 100).toFixed(0)}%  ` +
          `entities ${entityTotals.correct}/${entityTotals.checked} = ${(entityAccuracy * 100).toFixed(0)}%`,
      );
      console.log(
        `calibration ${(calibrationRate * 100).toFixed(0)}% within cap  ` +
          `(mean conf: clear ${confClear.toFixed(2)} vs ambiguous ${confAmbiguous.toFixed(2)})`,
      );
      console.log(
        `risk_flag recall ${(riskRecall * 100).toFixed(0)}%  ` +
          `unsafe auto-sends ${unsafeAutoSend.length} ${unsafeAutoSend.map((p) => p.c.id).join(',')}`,
      );
      const misses = paired
        .filter((p) => !isCorrect(p.c, p.pred))
        .map((p) => `${p.c.id} [${p.c.group}]: want ${p.c.expectedPrimary}, got ${p.pred}`);
      if (misses.length) console.log('misses:\n  ' + misses.join('\n  '));

      // --- assertions ---
      expect(report.accuracy).toBeGreaterThanOrEqual(OVERALL_THRESHOLD);
      expect(easy).toBeGreaterThanOrEqual(EASY_THRESHOLD);
      expect(hard).toBeGreaterThanOrEqual(HARD_THRESHOLD);
      expect(easy - hard, 'large easy↔hard gap ⇒ keyword overfit').toBeLessThanOrEqual(0.3);
      expect(secondaryRecall).toBeGreaterThanOrEqual(SECONDARY_RECALL_THRESHOLD);
      expect(entityAccuracy).toBeGreaterThanOrEqual(ENTITY_THRESHOLD);
      expect(calibrationRate).toBeGreaterThanOrEqual(CALIBRATION_THRESHOLD);
      expect(confAmbiguous, 'ambiguous messages should be less confident than clear ones').toBeLessThan(
        confClear,
      );

      // Safety is non-negotiable: a restricted-content message must NEVER be auto-send-eligible.
      expect(unsafeAutoSend, 'restricted content eligible for auto-send').toHaveLength(0);
      expect(riskRecall).toBeGreaterThanOrEqual(RISK_RECALL_THRESHOLD);
    },
    180_000,
  );
});
