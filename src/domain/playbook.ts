import type { DecisionAction, TrafficLightCode } from './types.js';
import { ALWAYS_ESCALATE_CODES, LOW_RISK_CODES } from './taxonomy.js';

/**
 * The per-code "playbook": for each traffic-light code, what the agent is actually supposed to DO.
 *
 * The taxonomy (taxonomy.ts) answers "which code wins"; this answers "given that code, what's the
 * reply strategy and does the hard gate auto-send or escalate". It's the one place that turns a code
 * into an instruction, so two consumers can share it instead of duplicating the §6 strategy table:
 *   1. the C.L.O.S.E.R. generator prompt (closer.ts) — `guidance` is injected so the reply is shaped
 *      by the code, not just a generic six-beat template;
 *   2. the eval oracle (test/evals) — `expectedAction` and `replyMustMention` are the ground truth a
 *      classification/completion run is scored against.
 *
 * `expectedAction` is derived from the taxonomy sets (LOW_RISK_CODES ⇒ auto_send) and cross-checked
 * in playbook.test.ts, so this never silently drifts from decide.ts.
 */

export type TrafficColor = 'GREEN' | 'YELLOW' | 'RED' | 'GRAY';

export interface CodePlay {
  code: TrafficLightCode;
  color: TrafficColor;
  /** One-line strategy label (CHALLENGE.md §6). */
  strategy: string;
  /** What the reply must accomplish for THIS code — injected into the C.L.O.S.E.R. prompt. */
  guidance: string;
  /**
   * What the hard-rules gate does for this code on a clean turn (no risk_flag, confidence ≥ 0.65,
   * toggle on). Mirrors decide.ts: low-risk codes auto-send, everything else escalates.
   */
  expectedAction: DecisionAction;
  /**
   * For auto-send codes only: the facet the code is about, as a set of terms a grounded reply is
   * expected to surface. Declarative (not a regex) so the completion eval can match them with
   * stemming — morphological variants (date/dates, fee/fees) collapse. A term with a symbol or hyphen
   * ("$", "all-in") is matched as a raw substring. Escalate codes generate no reply, so they have none.
   */
  replyMustMention?: string[];
}

/** Derive the gate action from the taxonomy sets so it can't disagree with decide.ts. */
function actionFor(code: TrafficLightCode): DecisionAction {
  return LOW_RISK_CODES.has(code) ? 'auto_send' : 'escalate';
}

export const PLAYBOOK: Record<TrafficLightCode, CodePlay> = {
  G1: {
    code: 'G1',
    color: 'GREEN',
    strategy: 'Fast Track: confirm + booking link, minimal friction',
    guidance:
      'They are ready to commit. Restate the booking, state the all-in total from the tools, and remove every step between them and a confirmed reservation. End by asking to lock it in now.',
    expectedAction: actionFor('G1'),
    replyMustMention: ['$', 'total', 'book', 'reserve', 'reservation', 'hold', 'lock'],
  },
  G2: {
    code: 'G2',
    color: 'GREEN',
    strategy: 'Personal Touch: validate context, then close',
    guidance:
      'High intent tied to an occasion (wedding, family trip, work stay). Acknowledge the occasion specifically, fit the space to it with real facts, then move to close.',
    expectedAction: actionFor('G2'),
    replyMustMention: ['$', 'total', 'book', 'hold', 'stay', 'trip', 'wedding', 'work'],
  },
  Y1: {
    code: 'Y1',
    color: 'YELLOW',
    strategy: 'Problem Solver (logistics)',
    guidance:
      'Answer the logistics question (parking, directions, access) concretely from listing facts, resolve the blocker, then nudge toward booking.',
    expectedAction: actionFor('Y1'),
    replyMustMention: ['parking', 'park', 'access', 'direction', 'entrance', 'key', 'door', 'car', 'garage'],
  },
  Y2: {
    code: 'Y2',
    color: 'YELLOW',
    strategy: 'Authority — calm, standard-process (trust/admin)',
    guidance:
      'Trust/admin (deposit, refund, cancellation, ID). Needs the standard documented process and a human — do not improvise policy.',
    expectedAction: actionFor('Y2'),
  },
  Y3: {
    code: 'Y3',
    color: 'YELLOW',
    strategy: 'Visualizer (product fit)',
    guidance:
      'Product fit (beds, layout, stairs, size). Help them picture the space with real facts so the fit is obvious, then ask for the booking.',
    expectedAction: actionFor('Y3'),
    replyMustMention: ['bed', 'bedroom', 'sleep', 'room', 'layout', 'stair', 'space', 'fit', 'size', 'suit', 'comfortable'],
  },
  Y4: {
    code: 'Y4',
    color: 'YELLOW',
    strategy: 'Accommodator (timing)',
    guidance:
      'Timing (check-in/out, early/late, luggage). Accommodate within the house rules, state what is possible plainly, then close.',
    expectedAction: actionFor('Y4'),
    replyMustMention: ['check-in', 'checkin', 'check-out', 'checkout', 'early', 'late', 'luggage', 'time', 'arrive', 'arrival', 'drop'],
  },
  Y5: {
    code: 'Y5',
    color: 'YELLOW',
    strategy: 'Boundaries — firm policy + alternative (permissions)',
    guidance:
      'Permissions (pets, parties, extra visitors, rules). Needs a firm policy plus an alternative and a human — never auto-grant.',
    expectedAction: actionFor('Y5'),
  },
  Y6: {
    code: 'Y6',
    color: 'YELLOW',
    strategy: 'Confirm & Close (availability)',
    guidance:
      'Availability (dates, calendar, vacancy). Confirm the dates from check_availability with the all-in total, no hedging, then ask to hold them.',
    expectedAction: actionFor('Y6'),
    replyMustMention: ['open', 'available', 'availability', 'date', 'night', 'vacancy', 'free', '$'],
  },
  Y7: {
    code: 'Y7',
    color: 'YELLOW',
    strategy: 'Transparent Anchor (price clarity)',
    guidance:
      'Price clarity (total, fees, cleaning, taxes — asking what it costs, not haggling). Give the transparent all-in total and breakdown from the tools, anchored on value.',
    expectedAction: actionFor('Y7'),
    replyMustMention: ['$', 'total', 'fee', 'tax', 'clean', 'cleaning', 'price', 'cost', 'all-in'],
  },
  R1: {
    code: 'R1',
    color: 'RED',
    strategy: 'Value Stack — never drop price first (haggle)',
    guidance:
      'Haggle (discount, deal, best price). Value-stack and NEVER drop price first — route to a human.',
    expectedAction: actionFor('R1'),
  },
  R2: {
    code: 'R2',
    color: 'RED',
    strategy: 'Takeaway — polite refusal + options (budget)',
    guidance:
      'Budget (too expensive, cheaper). Polite takeaway with alternatives — route to a human.',
    expectedAction: actionFor('R2'),
  },
  X1: {
    code: 'X1',
    color: 'GRAY',
    strategy: 'Qualify — ask ≤ 2 questions to create a bookable path',
    guidance:
      'Low signal — not enough to act. Qualify with at most two questions to open a bookable path; not enough certainty to auto-send.',
    expectedAction: actionFor('X1'),
  },
};

/** Look up the play for a code (total over the taxonomy, so this never returns undefined). */
export function playFor(code: TrafficLightCode): CodePlay {
  return PLAYBOOK[code];
}

// Compile-time guard: every code in the ALWAYS_ESCALATE set must indeed escalate in the playbook.
// (LOW_RISK ⇒ auto_send is covered by actionFor; this pins the other direction at module load.)
for (const code of ALWAYS_ESCALATE_CODES) {
  if (PLAYBOOK[code].expectedAction !== 'escalate') {
    throw new Error(`playbook drift: ${code} should escalate`);
  }
}
