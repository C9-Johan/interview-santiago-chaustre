import { describe, it, expect } from 'vitest';
import { decide } from '../src/domain/decide.js';
import { Classification } from '../src/domain/types.js';

/** Build a valid Classification with overrides — keeps each case focused on the field under test. */
function classify(overrides: Partial<Classification> = {}): Classification {
  return Classification.parse({
    primary_code: 'G1',
    confidence: 0.9,
    risk_flag: false,
    ...overrides,
  });
}

describe('decide() — hard-rules auto-send gate', () => {
  it('auto-sends a low-risk, high-confidence code when the toggle is on', () => {
    const d = decide(classify({ primary_code: 'G1', confidence: 0.9 }), {
      autoResponseEnabled: true,
    });
    expect(d.action).toBe('auto_send');
    expect(d.reason).toMatch(/G1/);
    expect(d.reason).toMatch(/low-risk/);
  });

  it('escalates when auto_response is disabled', () => {
    const d = decide(classify(), { autoResponseEnabled: false });
    expect(d.action).toBe('escalate');
    expect(d.reason).toMatch(/auto_response disabled/);
  });

  it('escalates when risk_flag is set (restricted content)', () => {
    const d = decide(classify({ risk_flag: true }), { autoResponseEnabled: true });
    expect(d.action).toBe('escalate');
    expect(d.reason).toMatch(/risk_flag/);
  });

  it('escalates when confidence is below the threshold', () => {
    const d = decide(classify({ confidence: 0.42 }), { autoResponseEnabled: true });
    expect(d.action).toBe('escalate');
    expect(d.reason).toMatch(/low confidence/);
    expect(d.reason).toMatch(/0\.42/);
  });

  it('escalates an ALWAYS_ESCALATE code Y2 even at high confidence', () => {
    const d = decide(classify({ primary_code: 'Y2', confidence: 0.99 }), {
      autoResponseEnabled: true,
    });
    expect(d.action).toBe('escalate');
    expect(d.reason).toMatch(/Y2/);
    expect(d.reason).toMatch(/human/);
  });

  it('escalates an ALWAYS_ESCALATE code R1 even at high confidence', () => {
    const d = decide(classify({ primary_code: 'R1', confidence: 0.99 }), {
      autoResponseEnabled: true,
    });
    expect(d.action).toBe('escalate');
    expect(d.reason).toMatch(/R1/);
    expect(d.reason).toMatch(/human/);
  });

  it('escalates a code not in the low-risk set (X1)', () => {
    const d = decide(classify({ primary_code: 'X1', confidence: 0.99 }), {
      autoResponseEnabled: true,
    });
    expect(d.action).toBe('escalate');
    expect(d.reason).toMatch(/X1/);
    expect(d.reason).toMatch(/low-risk set/);
  });
});
