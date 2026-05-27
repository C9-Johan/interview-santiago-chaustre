import type { Classification, Decision } from './types.js';
import {
  ALWAYS_ESCALATE_CODES,
  LOW_RISK_CODES,
  MIN_CONFIDENCE,
} from './taxonomy.js';

/**
 * The hard-rules auto-send gate (CHALLENGE.md §6).
 *
 * This is deliberately NOT an LLM decision: the model proposes a classification, but whether we
 * auto-reply is decided by deterministic rules over that classification. Auto-send requires ALL
 * conditions to hold; the first failing one names the escalation reason. Order matters — we check
 * the cheapest/most-decisive guards first so the reason points at the real blocker.
 */
export function decide(
  c: Classification,
  opts: { autoResponseEnabled: boolean },
): Decision {
  if (!opts.autoResponseEnabled) {
    return { action: 'escalate', reason: 'auto_response disabled' };
  }

  // Restricted content (off-platform payment, address leakage, guarantees) always wins.
  if (c.risk_flag) {
    return { action: 'escalate', reason: 'restricted content (risk_flag)' };
  }

  if (c.confidence < MIN_CONFIDENCE) {
    return {
      action: 'escalate',
      reason: `low confidence ${c.confidence} < ${MIN_CONFIDENCE}`,
    };
  }

  // Codes that demand a human (Y2, Y5, R1, R2) regardless of confidence.
  if (ALWAYS_ESCALATE_CODES.has(c.primary_code)) {
    return {
      action: 'escalate',
      reason: `code ${c.primary_code} requires human review`,
    };
  }

  if (!LOW_RISK_CODES.has(c.primary_code)) {
    return {
      action: 'escalate',
      reason: `code ${c.primary_code} not in low-risk set`,
    };
  }

  return {
    action: 'auto_send',
    reason: `${c.primary_code} @ ${c.confidence} ≥ ${MIN_CONFIDENCE}, low-risk`,
  };
}
