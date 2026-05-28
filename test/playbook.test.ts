import { describe, it, expect } from 'vitest';
import { PLAYBOOK, playFor } from '../src/domain/playbook.js';
import { TRAFFIC_LIGHT_CODES, Classification } from '../src/domain/types.js';
import { ALWAYS_ESCALATE_CODES, LOW_RISK_CODES } from '../src/domain/taxonomy.js';
import { decide } from '../src/domain/decide.js';

/**
 * Always-on consistency checks (no model). The playbook is the eval oracle and now feeds the reply
 * prompt, so it must never disagree with the taxonomy sets or with decide.ts — otherwise the evals
 * would score against a stale expectation. These guard that seam.
 */
describe('playbook ⇄ taxonomy/decide consistency', () => {
  it('covers every traffic-light code exactly once', () => {
    for (const code of TRAFFIC_LIGHT_CODES) {
      expect(playFor(code).code).toBe(code);
    }
    expect(Object.keys(PLAYBOOK).sort()).toEqual([...TRAFFIC_LIGHT_CODES].sort());
  });

  it('expectedAction matches the low-risk set', () => {
    for (const code of TRAFFIC_LIGHT_CODES) {
      const expected = LOW_RISK_CODES.has(code) ? 'auto_send' : 'escalate';
      expect(playFor(code).expectedAction).toBe(expected);
    }
  });

  it('agrees with decide() on a clean high-confidence turn for every code', () => {
    for (const code of TRAFFIC_LIGHT_CODES) {
      const c = Classification.parse({ primary_code: code, confidence: 0.9, risk_flag: false });
      const d = decide(c, { autoResponseEnabled: true });
      expect(d.action).toBe(playFor(code).expectedAction);
    }
  });

  it('attaches a non-empty reply-facet term set to auto-send codes and none to escalate codes', () => {
    for (const code of TRAFFIC_LIGHT_CODES) {
      const play = playFor(code);
      if (play.expectedAction === 'auto_send') {
        expect(Array.isArray(play.replyMustMention)).toBe(true);
        expect(play.replyMustMention!.length).toBeGreaterThan(0);
      } else {
        expect(play.replyMustMention).toBeUndefined();
      }
    }
  });

  it('always-escalate codes never carry a reply facet', () => {
    for (const code of ALWAYS_ESCALATE_CODES) {
      expect(playFor(code).replyMustMention).toBeUndefined();
    }
  });
});
