import 'dotenv/config';
import { describe, it, expect } from 'vitest';
import { createOpenAiLlm } from '../src/adapters/llm/openaiLlm.js';
import { Classification, TRAFFIC_LIGHT_CODES } from '../src/domain/types.js';

/**
 * Live smoke test for the real OpenAI adapter. Skipped automatically unless a real OPENAI_API_KEY is
 * present (loaded from .env above), so the offline suite stays green in CI and on machines without a
 * key. Makes ONE simple classify call and asserts the output is a schema-valid Classification — i.e.
 * the wiring (key, model, Vercel AI SDK structured output) actually works end to end.
 */
const apiKey = process.env.OPENAI_API_KEY;
const hasRealKey = Boolean(apiKey) && !apiKey!.includes('REPLACE');

describe.skipIf(!hasRealKey)('openaiLlm (live)', () => {
  it(
    'classifies a simple message into a valid Classification',
    async () => {
      const llm = createOpenAiLlm({ model: process.env.OPENAI_MODEL || 'gpt-4o-mini' });

      const result = await llm.classify({
        body: 'Hi! I want to book the apartment for next weekend.',
        language: 'en',
        thread: [],
      });

      // Output must satisfy the domain schema and the taxonomy.
      expect(() => Classification.parse(result)).not.toThrow();
      expect(TRAFFIC_LIGHT_CODES).toContain(result.primary_code);
      expect(result.confidence).toBeGreaterThanOrEqual(0);
      expect(result.confidence).toBeLessThanOrEqual(1);
      expect(typeof result.risk_flag).toBe('boolean');
    },
    20_000, // network call — allow generous timeout
  );
});
