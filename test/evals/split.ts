import { CASES, type EvalCase } from './dataset.js';

/**
 * Deterministic train / held-out split for the optimizer.
 *
 * Why: the auto-research optimizer tunes prompts to maximize the eval score. If it tuned and was judged
 * on the SAME cases, it would overfit them (Goodhart). So it optimizes on TRAIN and a candidate is only
 * accepted if it also improves on TEST — cases it never saw during tuning.
 *
 * The split is stratified by `group` and stable (no RNG): within each group, sorted by id, every case
 * at a 1-mod-3 position goes to TEST (~⅓). That guarantees (a) reproducibility across runs and machines,
 * (b) each group is represented in TEST where it has ≥2 cases, and (c) a single-case group is never
 * stranded out of TRAIN. The eval tests still run the FULL set — this split is optimizer-only.
 */
function partition(): { train: EvalCase[]; test: EvalCase[] } {
  const byGroup = new Map<string, EvalCase[]>();
  for (const c of CASES) {
    (byGroup.get(c.group) ?? byGroup.set(c.group, []).get(c.group)!).push(c);
  }

  const train: EvalCase[] = [];
  const test: EvalCase[] = [];
  for (const group of [...byGroup.keys()].sort()) {
    const sorted = [...byGroup.get(group)!].sort((a, b) => a.id.localeCompare(b.id));
    sorted.forEach((c, i) => (i % 3 === 1 ? test : train).push(c));
  }
  return { train, test };
}

const { train, test } = partition();

export const TRAIN: readonly EvalCase[] = train;
export const TEST: readonly EvalCase[] = test;
