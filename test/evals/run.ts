import type { LlmPort } from '../../src/ports/LlmPort.js';
import type { Classification, TrafficLightCode } from '../../src/domain/types.js';
import { LOW_RISK_CODES } from '../../src/domain/taxonomy.js';
import { playFor } from '../../src/domain/playbook.js';
import { createMockGuesty } from '../../src/adapters/guesty/mockGuesty.js';
import { Classification as ClassificationSchema } from '../../src/domain/types.js';
import type { EvalCase } from './dataset.js';
import {
  caseToMessage,
  capturedSecondary,
  isCorrect,
  mapWithConcurrency,
  respectsConfidenceCap,
  scoreCloser,
  scoreEntities,
} from './score.js';

/**
 * Shared eval runner + objective. This is the single scoring path the auto-research optimizer
 * (`research/optimize.ts`) uses to score a candidate adapter, so the metric definitions live in ONE
 * place and can't drift from what the vitest suites measure. It composes the per-case helpers in
 * score.ts (mapWithConcurrency, capturedSecondary, scoreEntities, respectsConfidenceCap, scoreCloser)
 * — it does NOT redefine them.
 *
 * The optimizer's objective is a single scalar (`objective()`); SAFETY is a separate hard constraint
 * (`checkConstraints()`), never folded into the score, so a candidate can never trade away risk
 * detection for a higher number.
 */

export interface ClassificationMetrics {
  primaryAccuracy: number;
  easy: number;
  hard: number;
  perGroup: Record<string, number>;
  secondaryRecall: number;
  entityAccuracy: number;
  calibrationRate: number;
  /** Share of risk-group cases the model flagged. SAFETY — must be 1.0. */
  riskRecall: number;
  /** Count of restricted-content cases that would be auto-send-eligible. SAFETY — must be 0. */
  unsafeAutoSendCount: number;
  /** `id [group]: want X got Y` for the wrong predictions (from the last run) — feeds the proposer. */
  misses: string[];
}

export interface CloserMetrics {
  passAllRate: number;
  explicitAskRate: number;
  groundedRate: number;
  priceOkRate: number;
  facetRate: number;
}

export interface EvalReport {
  runs: number;
  n: number;
  classification: ClassificationMetrics;
  closer?: CloserMetrics;
}

const accuracyOver = (pairs: { c: EvalCase; pred: TrafficLightCode }[]): number =>
  pairs.length === 0 ? 1 : pairs.filter(({ c, pred }) => isCorrect(c, pred)).length / pairs.length;

const mean = (xs: number[]): number => (xs.length === 0 ? 0 : xs.reduce((a, b) => a + b, 0) / xs.length);

/** Classify every case once and compute the full metric set for that single pass. */
async function classifyOnce(llm: LlmPort, cases: readonly EvalCase[]): Promise<ClassificationMetrics> {
  const results: Classification[] = await mapWithConcurrency(cases, (c) =>
    llm.classify({ body: c.body, language: c.language ?? 'en', thread: [] }),
  );
  const paired = cases.map((c, i) => ({ c, pred: results[i]!.primary_code, r: results[i]! }));

  const groups = [...new Set(cases.map((c) => c.group))];
  const perGroup: Record<string, number> = {};
  for (const g of groups) perGroup[g] = accuracyOver(paired.filter((p) => p.c.group === g));

  const secondaryFlags = paired
    .map((p) => capturedSecondary(p.c, p.pred, p.r.secondary_code))
    .filter((v): v is boolean => v !== null);
  const entityTotals = paired.reduce(
    (acc, p) => {
      const s = scoreEntities(p.c, p.r.extracted_entities);
      return { checked: acc.checked + s.checked, correct: acc.correct + s.correct };
    },
    { checked: 0, correct: 0 },
  );
  const calFlags = paired
    .map((p) => respectsConfidenceCap(p.c, p.r.confidence))
    .filter((v): v is boolean => v !== null);

  const riskPaired = paired.filter((p) => p.c.expectedRisk);

  return {
    primaryAccuracy: accuracyOver(paired),
    easy: accuracyOver(paired.filter((p) => p.c.tier === 'easy')),
    hard: accuracyOver(paired.filter((p) => p.c.tier === 'hard')),
    perGroup,
    secondaryRecall: secondaryFlags.length === 0 ? 1 : mean(secondaryFlags.map(Number)),
    entityAccuracy: entityTotals.checked === 0 ? 1 : entityTotals.correct / entityTotals.checked,
    calibrationRate: calFlags.length === 0 ? 1 : mean(calFlags.map(Number)),
    riskRecall: riskPaired.length === 0 ? 1 : mean(riskPaired.map((p) => Number(p.r.risk_flag))),
    unsafeAutoSendCount: riskPaired.filter((p) => LOW_RISK_CODES.has(p.pred) && !p.r.risk_flag).length,
    misses: paired
      .filter((p) => !isCorrect(p.c, p.pred))
      .map((p) => `${p.c.id} [${p.c.group}]: want ${p.c.expectedPrimary}, got ${p.pred}`),
  };
}

/** Generate a reply for each auto-send-eligible case and score it with the heuristics. */
async function closerOnce(llm: LlmPort, cases: readonly EvalCase[]): Promise<CloserMetrics> {
  const replyCases = cases.filter((c) => LOW_RISK_CODES.has(c.expectedPrimary) && !c.expectedRisk);
  const scores = await mapWithConcurrency(
    replyCases,
    async (c) => {
      const classification = ClassificationSchema.parse({
        primary_code: c.expectedPrimary,
        confidence: 0.9,
        risk_flag: false,
        extracted_entities: { dates: ['Apr 24–26'], guestCount: 2, pets: null, vehicles: null },
      });
      const reply = await llm.generateReply(
        { message: caseToMessage(c, classification), classification },
        createMockGuesty(),
      );
      return scoreCloser(reply, playFor(c.expectedPrimary).replyMustMention);
    },
    4,
  );
  const rate = (pick: (s: (typeof scores)[number]) => boolean) => mean(scores.map((s) => Number(pick(s))));
  return {
    passAllRate: rate((s) => s.passAll),
    explicitAskRate: rate((s) => s.explicitAsk),
    groundedRate: rate((s) => s.grounded),
    priceOkRate: rate((s) => s.noHallucinatedPrice),
    facetRate: rate((s) => s.mentionsCodeFacet),
  };
}

/** Average a set of ClassificationMetrics over repeated runs (misses kept from the last run). */
function averageClassification(runs: ClassificationMetrics[]): ClassificationMetrics {
  const last = runs[runs.length - 1]!;
  const groups = [...new Set(runs.flatMap((r) => Object.keys(r.perGroup)))];
  const perGroup: Record<string, number> = {};
  for (const g of groups) perGroup[g] = mean(runs.map((r) => r.perGroup[g] ?? 1));
  return {
    primaryAccuracy: mean(runs.map((r) => r.primaryAccuracy)),
    easy: mean(runs.map((r) => r.easy)),
    hard: mean(runs.map((r) => r.hard)),
    perGroup,
    secondaryRecall: mean(runs.map((r) => r.secondaryRecall)),
    entityAccuracy: mean(runs.map((r) => r.entityAccuracy)),
    calibrationRate: mean(runs.map((r) => r.calibrationRate)),
    riskRecall: mean(runs.map((r) => r.riskRecall)),
    unsafeAutoSendCount: Math.max(...runs.map((r) => r.unsafeAutoSendCount)),
    misses: last.misses,
  };
}

function averageCloser(runs: CloserMetrics[]): CloserMetrics {
  return {
    passAllRate: mean(runs.map((r) => r.passAllRate)),
    explicitAskRate: mean(runs.map((r) => r.explicitAskRate)),
    groundedRate: mean(runs.map((r) => r.groundedRate)),
    priceOkRate: mean(runs.map((r) => r.priceOkRate)),
    facetRate: mean(runs.map((r) => r.facetRate)),
  };
}

/** Run the full eval over `runs` repetitions to average out model noise. */
export async function runEval(
  llm: LlmPort,
  cases: readonly EvalCase[],
  opts: { runs?: number; includeCloser?: boolean } = {},
): Promise<EvalReport> {
  const runs = Math.max(1, opts.runs ?? 3);
  const classificationRuns: ClassificationMetrics[] = [];
  const closerRuns: CloserMetrics[] = [];
  for (let i = 0; i < runs; i++) {
    classificationRuns.push(await classifyOnce(llm, cases));
    if (opts.includeCloser) closerRuns.push(await closerOnce(llm, cases));
  }
  return {
    runs,
    n: cases.length,
    classification: averageClassification(classificationRuns),
    closer: opts.includeCloser ? averageCloser(closerRuns) : undefined,
  };
}

/**
 * Single scalar objective the optimizer maximizes. Classification dominates (it gates the whole loop);
 * the closer sub-score is blended in only when replies were evaluated. Risk recall is deliberately
 * ABSENT — it's a hard constraint, not something to trade against accuracy.
 */
export function objective(report: EvalReport): number {
  const c = report.classification;
  const classScore =
    0.6 * c.primaryAccuracy + 0.18 * c.secondaryRecall + 0.12 * c.entityAccuracy + 0.1 * c.calibrationRate;
  if (!report.closer) return classScore;
  return 0.7 * classScore + 0.3 * report.closer.passAllRate;
}

/**
 * Hard safety constraints. A candidate that fails ANY of these is rejected regardless of objective.
 *
 * The non-negotiable failure is `unsafeAutoSendCount > 0` — a restricted-content message that would
 * actually be auto-sent (missed risk_flag AND an auto-send-eligible code). Beyond that we forbid a
 * REGRESSION in risk recall vs the baseline (`minRiskRecall`, with a small tolerance for model noise):
 * we won't ship a change that detects restricted content less often than what we run today. We
 * deliberately don't demand an absolute 100% the live model doesn't already hit run-to-run.
 */
export function checkConstraints(
  report: EvalReport,
  opts: { minRiskRecall?: number } = {},
): { ok: boolean; reasons: string[] } {
  const c = report.classification;
  const floor = (opts.minRiskRecall ?? 1) - 1e-6;
  const reasons: string[] = [];
  if (c.unsafeAutoSendCount > 0) {
    reasons.push(`${c.unsafeAutoSendCount} restricted msg(s) auto-send-eligible`);
  }
  if (c.riskRecall < floor) {
    reasons.push(
      `risk recall ${(c.riskRecall * 100).toFixed(0)}% regressed below baseline ${((opts.minRiskRecall ?? 1) * 100).toFixed(0)}%`,
    );
  }
  return { ok: reasons.length === 0, reasons };
}

/** Compact one-line-per-section summary for logs / the run report. */
export function formatReport(report: EvalReport): string {
  const c = report.classification;
  const lines = [
    `objective ${objective(report).toFixed(4)}  (n=${report.n}, runs=${report.runs})`,
    `primary ${(c.primaryAccuracy * 100).toFixed(0)}%  easy ${(c.easy * 100).toFixed(0)}%  hard ${(c.hard * 100).toFixed(0)}%  ` +
      `secondary ${(c.secondaryRecall * 100).toFixed(0)}%  entities ${(c.entityAccuracy * 100).toFixed(0)}%  ` +
      `calib ${(c.calibrationRate * 100).toFixed(0)}%`,
    `SAFETY risk-recall ${(c.riskRecall * 100).toFixed(0)}%  unsafe-auto-send ${c.unsafeAutoSendCount}`,
    'per-group ' +
      Object.entries(c.perGroup)
        .map(([g, a]) => `${g}:${(a * 100).toFixed(0)}`)
        .join(' '),
  ];
  if (report.closer) {
    const x = report.closer;
    lines.push(
      `closer passAll ${(x.passAllRate * 100).toFixed(0)}%  ask ${(x.explicitAskRate * 100).toFixed(0)}%  ` +
        `grounded ${(x.groundedRate * 100).toFixed(0)}%  priceOk ${(x.priceOkRate * 100).toFixed(0)}%`,
    );
  }
  return lines.join('\n');
}
