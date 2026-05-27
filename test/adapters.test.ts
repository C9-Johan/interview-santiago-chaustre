import { describe, it, expect } from 'vitest';
import { createMockLlm } from '../src/adapters/llm/mockLlm.js';
import { createMockGuesty } from '../src/adapters/guesty/mockGuesty.js';
import { createMemoryStore } from '../src/adapters/store/memoryStore.js';
import { Classification } from '../src/domain/types.js';
import type { ClassifyInput } from '../src/ports/LlmPort.js';

const baseInput = (body: string): ClassifyInput => ({
  body,
  language: 'en',
  thread: [],
});

describe('mockLlm.classify', () => {
  const llm = createMockLlm();

  it('classifies a booking message as G1 with no risk', async () => {
    const c = await llm.classify(baseInput('I want to book for next weekend'));
    expect(c.primary_code).toBe('G1');
    expect(c.risk_flag).toBe(false);
    // Returned object must satisfy the contract schema.
    expect(() => Classification.parse(c)).not.toThrow();
  });

  it('applies the priority rule: haggle + logistics → primary R1, secondary Y1', async () => {
    const c = await llm.classify(
      baseInput('any discount? also is parking included?'),
    );
    expect(c.primary_code).toBe('R1');
    expect(c.secondary_code).toBe('Y1');
  });

  it('treats a bare greeting as X1 with low confidence', async () => {
    const c = await llm.classify(baseInput('Hi 🙂'));
    expect(c.primary_code).toBe('X1');
    expect(c.confidence).toBeLessThan(0.65);
  });

  it('flags off-platform payment as risk', async () => {
    const c = await llm.classify(
      baseInput('can I pay via venmo off airbnb?'),
    );
    expect(c.risk_flag).toBe(true);
  });
});

describe('memoryStore', () => {
  it('dedups via seen() and toggles auto-response', () => {
    const store = createMemoryStore(true);
    expect(store.seen('a')).toBe(false);
    expect(store.seen('a')).toBe(true);

    expect(store.isAutoResponseEnabled()).toBe(true);
    store.setAutoResponseEnabled(false);
    expect(store.isAutoResponseEnabled()).toBe(false);
  });
});

describe('mockGuesty', () => {
  it('records notes and exposes them via getNotes()', async () => {
    const guesty = createMockGuesty();
    const { id } = await guesty.postNote('conv_1', 'hello note');
    const notes = guesty.getNotes();
    expect(notes).toHaveLength(1);
    expect(notes[0]?.id).toBe(id);
    expect(notes[0]?.conversationId).toBe('conv_1');
    expect(notes[0]?.body).toBe('hello note');
  });

  it('returns positive nights and total from checkAvailability', async () => {
    const guesty = createMockGuesty();
    const result = await guesty.checkAvailability(
      'soho-2br',
      '2026-04-24T22:00:00.000Z',
      '2026-04-26T16:00:00.000Z',
    );
    expect(result.available).toBe(true);
    expect(result.nights).toBeGreaterThan(0);
    expect(result.total).toBeGreaterThan(0);
  });
});
