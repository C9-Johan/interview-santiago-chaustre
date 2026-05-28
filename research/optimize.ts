import 'dotenv/config';
import { execSync } from 'node:child_process';
import { appendFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { hasRealKey } from '../test/evals/score.js';
import { TEST, TRAIN } from '../test/evals/split.js';
import {
  checkConstraints,
  formatReport,
  objective,
  runEval,
  type EvalReport,
} from '../test/evals/run.js';
import {
  baselineCandidate,
  buildLlm,
  propose,
  type Candidate,
} from './candidate.js';
import { materialize } from './materialize.js';

/**
 * Auto-research optimizer. An OpenAI `gpt-4o` agent proposes changes to the worker's prompts, tool
 * descriptions, and temperature; each candidate is scored on the held-out eval (test/evals) and only a
 * change that improves the UNSEEN test split by a margin — without weakening the safety constraints —
 * is accepted and written to source. Everything runs offline against mock Guesty; only the OpenAI
 * calls cost money. Invoke explicitly via `npm run autoresearch` — never automatically.
 *
 * Flags: --quick (RUNS=1, MAX_ITERS=2), --classification-only (skip the costlier closer eval).
 * Env: RUNS, MAX_ITERS, MARGIN, PROPOSER_TIMEOUT_MS, OPTIMIZER_MODEL, OPENAI_MODEL (worker).
 */

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const args = new Set(process.argv.slice(2));
const QUICK = args.has('--quick');
const INCLUDE_CLOSER = !args.has('--classification-only');

// We tune prompts/params, NOT the model: swapping the worker to a bigger model is an expensive,
// trivial "win" that muddies the prompt-engineering signal. Model stays fixed at the worker model.
const WORKER_MODEL = process.env.OPENAI_MODEL || 'gpt-4o-mini';
const OPTIMIZER_MODEL = process.env.OPTIMIZER_MODEL || 'gpt-4o';
const RUNS = QUICK ? 1 : Number(process.env.RUNS ?? 3);
const MAX_ITERS = QUICK ? 2 : Number(process.env.MAX_ITERS ?? 6);
const MARGIN = Number(process.env.MARGIN ?? 0.02);
const PROPOSER_TIMEOUT_MS = Number(process.env.PROPOSER_TIMEOUT_MS ?? 120_000);

const evalOpts = { runs: RUNS, includeCloser: INCLUDE_CLOSER };

interface HistoryEntry {
  iter: number;
  summary: string;
  rationale: string;
  trainObjective: number;
  status: string;
  candidate?: Candidate;
}

async function main(): Promise<void> {
  if (!hasRealKey()) {
    console.error('No real OPENAI_API_KEY — the optimizer needs live model access. Aborting.');
    process.exit(1);
  }

  // Create the run directory up front so every step is traced LIVE, not just at the end:
  //   console.log  — full transcript (mirror of stdout)
  //   trace/iter-NN.md — each round's FULL proposed prompts + diff + scores + decision (incl. rejects)
  //   trace.jsonl  — one machine-readable line per round
  //   report.md / report.json — the final decision + winner (written at the end)
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const outDir = resolve(ROOT, 'research/runs', stamp);
  const traceDir = resolve(outDir, 'trace');
  mkdirSync(traceDir, { recursive: true });
  const consolePath = resolve(outDir, 'console.log');
  const jsonlPath = resolve(outDir, 'trace.jsonl');
  const log = (line = ''): void => {
    console.log(line);
    appendFileSync(consolePath, line + '\n');
  };

  const replyCount = TRAIN.filter(
    (c) => !c.expectedRisk && ['G1', 'G2', 'Y1', 'Y3', 'Y4', 'Y6', 'Y7'].includes(c.expectedPrimary),
  ).length;
  const perTrain = RUNS * (TRAIN.length + (INCLUDE_CLOSER ? replyCount : 0));
  const estWorkerCalls = perTrain * (1 + MAX_ITERS) + RUNS * (TEST.length + (INCLUDE_CLOSER ? replyCount : 0)) * 2;
  log(
    `Auto-research: worker=${WORKER_MODEL} optimizer=${OPTIMIZER_MODEL} runs=${RUNS} iters=${MAX_ITERS} ` +
      `margin=${MARGIN} closer=${INCLUDE_CLOSER}\n` +
      `Train=${TRAIN.length} Test=${TEST.length}. Est. ~${estWorkerCalls} worker calls + ≤${MAX_ITERS} optimizer calls.\n` +
      `Live trace → research/runs/${stamp}/  (console.log, trace/iter-NN.md, trace.jsonl)\n`,
  );

  // --- Baseline on TRAIN ---
  const baseline = baselineCandidate(WORKER_MODEL);
  log('Scoring baseline on TRAIN…');
  let bestTrain = await runEval(buildLlm(baseline), TRAIN, evalOpts);
  let best = baseline;
  let bestObj = objective(bestTrain);
  // Safety floor: candidates may not detect restricted content LESS often than today's baseline.
  const riskFloorTrain = bestTrain.classification.riskRecall;
  log('baseline TRAIN:\n' + formatReport(bestTrain) + '\n');
  writeFileSync(resolve(traceDir, 'iter-00-baseline.md'), iterMarkdown(0, 'baseline', baseline, baseline, bestTrain, '', ''));
  writeFileSync(
    resolve(traceDir, 'iter-00-baseline.json'),
    JSON.stringify(iterJson(0, 'baseline', baseline, baseline, bestTrain, '', ''), null, 2),
  );

  // --- Optimization loop ---
  const history: HistoryEntry[] = [];
  let optimizerTokens = 0;
  let consecutiveTimeouts = 0;

  for (let iter = 1; iter <= MAX_ITERS; iter++) {
    log(`\n--- iteration ${iter}/${MAX_ITERS} (proposing from best obj ${bestObj.toFixed(4)}) ---`);
    const signal = AbortSignal.timeout(PROPOSER_TIMEOUT_MS);
    const proposed = await propose(
      OPTIMIZER_MODEL,
      {
        current: best,
        trainReportText: formatReport(bestTrain),
        misses: bestTrain.classification.misses,
        history: history.map((h) => ({
          iter: h.iter,
          summary: h.summary,
          trainObjective: h.trainObjective,
          status: h.status,
        })),
      },
      signal,
    ).catch((err) => {
      log(`  proposer error: ${String(err)}`);
      return null;
    });

    if (!proposed) {
      consecutiveTimeouts++;
      history.push({ iter, summary: '(proposer timed out / errored)', rationale: '', trainObjective: 0, status: 'timeout' });
      appendFileSync(jsonlPath, JSON.stringify({ iter, status: 'timeout' }) + '\n');
      log(`  proposer produced nothing (2-min cap ${PROPOSER_TIMEOUT_MS}ms hit, or error).`);
      if (consecutiveTimeouts >= 2) {
        log('  two consecutive timeouts — ending loop early.');
        break;
      }
      continue;
    }
    consecutiveTimeouts = 0;
    optimizerTokens += proposed.tokens;

    const candidate: Candidate = { ...proposed.proposal, model: WORKER_MODEL };
    const changed = changedFields(baseline, candidate);
    log(`  change: ${proposed.proposal.changeSummary}`);
    log(`  rationale: ${proposed.proposal.rationale}`);
    log(`  fields touched vs baseline: ${changed.length ? changed.join(', ') : '(none)'}`);

    const report = await runEval(buildLlm(candidate), TRAIN, evalOpts);
    const constraints = checkConstraints(report, { minRiskRecall: riskFloorTrain });
    const obj = objective(report);

    let status: string;
    if (!constraints.ok) {
      status = `rejected (constraint: ${constraints.reasons.join('; ')})`;
    } else if (obj > bestObj + 1e-6) {
      best = candidate;
      bestTrain = report;
      bestObj = obj;
      status = 'accepted-best';
    } else {
      status = 'kept (no train improvement)';
    }
    log(`  TRAIN obj ${obj.toFixed(4)} (best ${bestObj.toFixed(4)}) — ${status}`);

    // Persist the FULL proposal (incl. rejected ones) so every code change is inspectable — md + json.
    const pad = String(iter).padStart(2, '0');
    writeFileSync(
      resolve(traceDir, `iter-${pad}.md`),
      iterMarkdown(iter, status, baseline, candidate, report, proposed.proposal.changeSummary, proposed.proposal.rationale),
    );
    writeFileSync(
      resolve(traceDir, `iter-${pad}.json`),
      JSON.stringify(
        iterJson(iter, status, baseline, candidate, report, proposed.proposal.changeSummary, proposed.proposal.rationale),
        null,
        2,
      ),
    );
    appendFileSync(
      jsonlPath,
      JSON.stringify({ iter, status, trainObjective: obj, changed, summary: proposed.proposal.changeSummary }) + '\n',
    );
    history.push({
      iter,
      summary: proposed.proposal.changeSummary,
      rationale: proposed.proposal.rationale,
      trainObjective: obj,
      status,
      candidate,
    });
  }

  // --- Held-out validation: baseline vs best on TEST (the cases tuning never saw) ---
  log('\nValidating on held-out TEST…');
  const baselineTest = await runEval(buildLlm(baseline), TEST, evalOpts);
  const improved = best !== baseline;
  const bestTest = improved ? await runEval(buildLlm(best), TEST, evalOpts) : baselineTest;

  const testConstraints = checkConstraints(bestTest, {
    minRiskRecall: baselineTest.classification.riskRecall,
  });
  const delta = objective(bestTest) - objective(baselineTest);
  const accept = improved && testConstraints.ok && delta >= MARGIN;

  log('baseline TEST:\n' + formatReport(baselineTest));
  log('\nbest TEST:\n' + formatReport(bestTest));
  log(
    `\nheld-out objective Δ = ${delta.toFixed(4)} (margin ${MARGIN}); ` +
      `constraints ${testConstraints.ok ? 'ok' : 'FAIL: ' + testConstraints.reasons.join('; ')} ⇒ ` +
      `${accept ? 'ACCEPT' : 'REJECT'}`,
  );

  // --- Write the final report (always) into the run dir created up front ---
  const md = renderMarkdown({ accept, delta, baseline, best, baselineTest, bestTest, bestTrain, history, optimizerTokens });
  writeFileSync(resolve(outDir, 'report.md'), md);
  writeFileSync(
    resolve(outDir, 'report.json'),
    JSON.stringify({ accept, delta, margin: MARGIN, baselineTest, bestTest, history, optimizerTokens, best: redact(best) }, null, 2),
  );
  log(`\nReport written to research/runs/${stamp}/report.md`);

  // --- Materialize on a real win, gated by tsc ---
  if (!accept) {
    log('No held-out improvement beyond margin — leaving source unchanged (harness kept).');
    return;
  }
  log('Materializing winning candidate into source…');
  const revert = materialize(best);
  try {
    execSync('npx tsc --noEmit', { cwd: ROOT, stdio: 'pipe' });
  } catch (err) {
    revert();
    log('Post-materialize tsc FAILED — reverted source. Treating as no-win.');
    log(String((err as { stdout?: Buffer }).stdout ?? err));
    return;
  }
  log(
    'Materialized + tsc clean. Next: run `npm test`, then create a branch, commit, and open a PR\n' +
      `(report body: research/runs/${stamp}/report.md).`,
  );
}

/** Drop the long prompt text from the summary JSON; the full text lives in the per-iter trace files. */
function redact(c: Candidate) {
  return { temperature: c.temperature, model: c.model };
}

const TUNABLE = ['classifySystem', 'closerSystem', 'getListingDesc', 'checkAvailabilityDesc'] as const;

/** Which tunable fields a candidate changed relative to the baseline (incl. temperature). */
function changedFields(baseline: Candidate, candidate: Candidate): string[] {
  const changed: string[] = TUNABLE.filter((k) => baseline[k] !== candidate[k]);
  if (baseline.temperature !== candidate.temperature) changed.push('temperature');
  return changed;
}

/** Full machine-readable record of one round: the complete candidate, the diff, scores, and decision. */
function iterJson(
  iter: number,
  status: string,
  baseline: Candidate,
  candidate: Candidate,
  report: EvalReport,
  summary: string,
  rationale: string,
) {
  return {
    iter,
    status,
    summary,
    rationale,
    changed: changedFields(baseline, candidate),
    objective: objective(report),
    candidate: {
      temperature: candidate.temperature,
      model: candidate.model,
      classifySystem: candidate.classifySystem,
      closerSystem: candidate.closerSystem,
      getListingDesc: candidate.getListingDesc,
      checkAvailabilityDesc: candidate.checkAvailabilityDesc,
    },
    report,
  };
}

/** Human-readable per-round trace: the diff vs baseline, the FULL proposed prompts, and the scores. */
function iterMarkdown(
  iter: number,
  status: string,
  baseline: Candidate,
  candidate: Candidate,
  report: EvalReport,
  summary: string,
  rationale: string,
): string {
  const changed = changedFields(baseline, candidate);
  const block = (label: string, key: (typeof TUNABLE)[number]): string =>
    `<details><summary>${label}${changed.includes(key) ? ' — CHANGED' : ''}</summary>\n\n\`\`\`\n${candidate[key]}\n\`\`\`\n</details>`;
  return `# Iteration ${iter} — ${status}

${iter === 0 ? '_Committed baseline._' : `**${summary}**\n\n${rationale}`}

- fields changed vs baseline: ${changed.length ? changed.join(', ') : '(none)'}
- temperature: \`${candidate.temperature ?? 'provider default'}\`
- objective (TRAIN): ${objective(report).toFixed(4)}

\`\`\`
${formatReport(report)}
\`\`\`

${block('classifySystem', 'classifySystem')}

${block('closerSystem', 'closerSystem')}

${block('getListingDesc', 'getListingDesc')}

${block('checkAvailabilityDesc', 'checkAvailabilityDesc')}
`;
}

function renderMarkdown(d: {
  accept: boolean;
  delta: number;
  baseline: Candidate;
  best: Candidate;
  baselineTest: EvalReport;
  bestTest: EvalReport;
  bestTrain: EvalReport;
  history: HistoryEntry[];
  optimizerTokens: number;
}): string {
  const winnerSummary =
    d.history.filter((h) => h.candidate === d.best).map((h) => h.summary).pop() ?? '(baseline — no change won)';
  return `# Auto-research run

**Decision: ${d.accept ? '✅ ACCEPT' : '❌ REJECT'}** — held-out objective Δ = ${d.delta.toFixed(4)} (margin ${MARGIN}).

Worker model \`${d.best.model}\`, optimizer \`${OPTIMIZER_MODEL}\`, ${RUNS} run(s)/eval, ${MAX_ITERS} max iters.
Optimizer tokens: ~${d.optimizerTokens}.

## Held-out (TEST) — baseline vs best
\`\`\`
BASELINE
${formatReport(d.baselineTest)}

BEST
${formatReport(d.bestTest)}
\`\`\`

## Winning change
${winnerSummary}

## Iteration history (TRAIN)
${d.history.map((h) => `- #${h.iter} obj=${h.trainObjective.toFixed(4)} [${h.status}] — ${h.summary}`).join('\n')}

## Materialized values (only if accepted)
${
  d.accept
    ? `- temperature: \`${d.best.temperature ?? 'provider default'}\`\n\n<details><summary>classifySystem</summary>\n\n\`\`\`\n${d.best.classifySystem}\n\`\`\`\n</details>\n\n<details><summary>closerSystem</summary>\n\n\`\`\`\n${d.best.closerSystem}\n\`\`\`\n</details>\n\n- get_listing: ${d.best.getListingDesc}\n- check_availability: ${d.best.checkAvailabilityDesc}`
    : '_(rejected — source unchanged)_'
}
`;
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
