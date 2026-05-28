import { generateObject, generateText, stepCountIs, tool } from 'ai';
import { openai } from '@ai-sdk/openai';
import { z } from 'zod';
import type { GuestyPort } from '../../ports/GuestyPort.js';
import type {
  ClassifyInput,
  GenerateReplyInput,
  LlmPort,
} from '../../ports/LlmPort.js';
import { Classification, ClassificationWire } from '../../domain/types.js';
import {
  CLASSIFY_SYSTEM_PROMPT,
  buildClassifyPrompt,
} from '../../domain/classifyPrompt.js';
import { CLOSER_SYSTEM_PROMPT, buildCloserPrompt } from '../../domain/closer.js';

/**
 * Optional, runtime overrides for the tunable surface of the adapter. Each field defaults to the
 * baked-in constant, so `createOpenAiLlm({ model })` behaves exactly as before. This exists so the
 * auto-research optimizer (`research/optimize.ts`) can evaluate candidate prompts/params WITHOUT
 * editing source mid-loop — it constructs an adapter with proposed overrides, scores it on the evals,
 * and only the winning values are ever written back to the constants above. Nothing here touches the
 * safety gate (`decide.ts`), which is never the model's call.
 */
export interface LlmOverrides {
  classifySystem?: string;
  closerSystem?: string;
  getListingDesc?: string;
  checkAvailabilityDesc?: string;
  /** Sampling temperature for both calls; undefined ⇒ provider default. */
  temperature?: number;
}

export const DEFAULT_GET_LISTING_DESC =
  'Fetch the listing facts (title, bedrooms, amenities, house rules, base price, address) to ground the reply.';
export const DEFAULT_CHECK_AVAILABILITY_DESC =
  'Check availability and the all-in total for a date window. Use before stating dates/price.';
/** Default sampling temperature; `undefined` ⇒ provider default. Materialized by the optimizer on a win. */
export const DEFAULT_TEMPERATURE: number | undefined = undefined;

/**
 * Real LlmPort via the Vercel AI SDK.
 *
 * Two distinct call shapes, by design (CHALLENGE.md flow): classify uses STRUCTURED OUTPUT
 * (generateObject with the Classification Zod schema, so the model can't return an off-taxonomy or
 * malformed result), and generate uses TOOL CALLING so the reply is grounded in real Guesty facts
 * (listing + availability) rather than hallucinated. Prompts live in the domain layer so wording is
 * shared/testable and not buried in the adapter. `overrides` lets the optimizer swap any tunable
 * value at construction time (see LlmOverrides); defaults reproduce the committed behavior.
 */
export function createOpenAiLlm(
  opts: { model: string },
  overrides: LlmOverrides = {},
): LlmPort {
  const model = openai(opts.model);
  const temperature = overrides.temperature ?? DEFAULT_TEMPERATURE;
  const classifySystem = overrides.classifySystem ?? CLASSIFY_SYSTEM_PROMPT;
  const closerSystem = overrides.closerSystem ?? CLOSER_SYSTEM_PROMPT;
  const getListingDesc = overrides.getListingDesc ?? DEFAULT_GET_LISTING_DESC;
  const checkAvailabilityDesc =
    overrides.checkAvailabilityDesc ?? DEFAULT_CHECK_AVAILABILITY_DESC;

  return {
    async classify(input: ClassifyInput): Promise<Classification> {
      const { object } = await generateObject({
        model,
        temperature,
        // Strict wire schema (all keys required) for OpenAI structured output; see types.ts.
        schema: ClassificationWire,
        system: classifySystem,
        prompt: buildClassifyPrompt(input),
      });
      // Coerce the validated wire result into the domain Classification.
      return Classification.parse(object);
    },

    async generateReply(
      input: GenerateReplyInput,
      guesty: GuestyPort,
    ): Promise<string> {
      const { text } = await generateText({
        model,
        temperature,
        system: closerSystem,
        prompt: buildCloserPrompt({
          message: input.message,
          classification: input.classification,
        }),
        tools: {
          get_listing: tool({
            description: getListingDesc,
            inputSchema: z.object({ listingId: z.string().optional() }),
            execute: async ({ listingId }) =>
              guesty.getListing(
                listingId ?? input.message.listingId ?? 'default',
              ),
          }),
          check_availability: tool({
            description: checkAvailabilityDesc,
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
