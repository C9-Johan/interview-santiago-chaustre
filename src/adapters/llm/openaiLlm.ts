import { generateObject, generateText, stepCountIs, tool } from 'ai';
import { openai } from '@ai-sdk/openai';
import { z } from 'zod';
import type { GuestyPort } from '../../ports/GuestyPort.js';
import type {
  ClassifyInput,
  GenerateReplyInput,
  LlmPort,
} from '../../ports/LlmPort.js';
import { Classification } from '../../domain/types.js';
import {
  CLASSIFY_SYSTEM_PROMPT,
  buildClassifyPrompt,
} from '../../domain/classifyPrompt.js';
import { CLOSER_SYSTEM_PROMPT, buildCloserPrompt } from '../../domain/closer.js';

/**
 * Real LlmPort via the Vercel AI SDK.
 *
 * Two distinct call shapes, by design (CHALLENGE.md flow): classify uses STRUCTURED OUTPUT
 * (generateObject with the Classification Zod schema, so the model can't return an off-taxonomy or
 * malformed result), and generate uses TOOL CALLING so the reply is grounded in real Guesty facts
 * (listing + availability) rather than hallucinated. Prompts live in the domain layer so wording is
 * shared/testable and not buried in the adapter.
 */
export function createOpenAiLlm(opts: { model: string }): LlmPort {
  const model = openai(opts.model);

  return {
    async classify(input: ClassifyInput): Promise<Classification> {
      const { object } = await generateObject({
        model,
        schema: Classification,
        system: CLASSIFY_SYSTEM_PROMPT,
        prompt: buildClassifyPrompt(input),
      });
      // generateObject already validated against the schema; object is a Classification.
      return object;
    },

    async generateReply(
      input: GenerateReplyInput,
      guesty: GuestyPort,
    ): Promise<string> {
      const { text } = await generateText({
        model,
        system: CLOSER_SYSTEM_PROMPT,
        prompt: buildCloserPrompt({
          message: input.message,
          classification: input.classification,
        }),
        tools: {
          get_listing: tool({
            description:
              'Fetch the listing facts (title, bedrooms, amenities, house rules, base price, address) to ground the reply.',
            inputSchema: z.object({ listingId: z.string().optional() }),
            execute: async ({ listingId }) =>
              guesty.getListing(
                listingId ?? input.message.listingId ?? 'default',
              ),
          }),
          check_availability: tool({
            description:
              'Check availability and the all-in total for a date window. Use before stating dates/price.',
            inputSchema: z.object({
              listingId: z.string().optional(),
              from: z.string(),
              to: z.string(),
            }),
            execute: async ({ listingId, from, to }) =>
              guesty.checkAvailability(
                listingId ?? input.message.listingId ?? 'default',
                from,
                to,
              ),
          }),
        },
        // Allow a couple of tool round-trips before the model writes the final paragraph.
        stopWhen: stepCountIs(5),
      });

      // No usable text means we couldn't ground the reply — escalate instead of faking a beat.
      if (!text.trim()) {
        throw new Error('openaiLlm.generateReply produced no text');
      }
      return text;
    },
  };
}
