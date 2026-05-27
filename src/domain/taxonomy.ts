import type { TrafficLightCode } from './types.js';

/**
 * Traffic-light taxonomy rules from CHALLENGE.md §6. Kept as data + pure helpers so the
 * decision gate (decide.ts) and the mock classifier can share one source of truth.
 */

/** Codes eligible for auto-send (low-risk set). */
export const LOW_RISK_CODES: ReadonlySet<TrafficLightCode> = new Set([
  'G1', 'G2', 'Y1', 'Y3', 'Y4', 'Y6', 'Y7',
]);

/** Codes that ALWAYS escalate to a human regardless of confidence. */
export const ALWAYS_ESCALATE_CODES: ReadonlySet<TrafficLightCode> = new Set([
  'Y2', 'Y5', 'R1', 'R2',
]);

/** Minimum confidence required to auto-send. */
export const MIN_CONFIDENCE = 0.65;

/**
 * Priority when multiple signals are present (highest first):
 *   RED > Y5 > Y2 > Y4 > Y1 > Y3 > Y6 > Y7 > GREEN > GRAY.
 * Used to pick the primary code when several are detected. Lower index = higher priority.
 */
export const PRIORITY_ORDER: readonly TrafficLightCode[] = [
  'R1', 'R2', // RED dominates everything
  'Y5', 'Y2', 'Y4', 'Y1', 'Y3', 'Y6', 'Y7',
  'G1', 'G2', // GREEN
  'X1', // GRAY
];

/** Pick the highest-priority code from a set of detected codes (CHALLENGE.md §6 decision rules). */
export function highestPriority(codes: TrafficLightCode[]): TrafficLightCode {
  if (codes.length === 0) return 'X1';
  return [...codes].sort(
    (a, b) => PRIORITY_ORDER.indexOf(a) - PRIORITY_ORDER.indexOf(b),
  )[0]!;
}

/** Human-readable strategy label per code, useful for logs and the reply prompt. */
export const STRATEGY: Record<TrafficLightCode, string> = {
  G1: 'Fast Track: confirm + booking link, minimal friction',
  G2: 'Personal Touch: validate context, then close',
  Y1: 'Problem Solver (logistics)',
  Y2: 'Authority — calm, standard-process (trust/admin)',
  Y3: 'Visualizer (product fit)',
  Y4: 'Accommodator (timing)',
  Y5: 'Boundaries — firm policy + alternative (permissions)',
  Y6: 'Confirm & Close (availability)',
  Y7: 'Transparent Anchor (price clarity)',
  R1: 'Value Stack — never drop price first (haggle)',
  R2: 'Takeaway — polite refusal + options (budget)',
  X1: 'Qualify — ask ≤ 2 questions to create a bookable path',
};
