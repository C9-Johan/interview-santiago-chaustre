import 'dotenv/config';
import { generateObject } from 'ai';
import { openai } from '@ai-sdk/openai';
import { z } from 'zod';
import type { TrafficLightCode } from '../../src/domain/types.js';

/**
 * LLM-as-judge for C.L.O.S.E.R. replies. The heuristics in score.ts catch hard, falsifiable failures
 * (no ask, wrong price, off-topic); they cannot judge nuance — does the reply actually read well, stay
 * on the guest's real concern, and sound human? A second model scores that against a rubric.
 *
 * It is given the SAME listing facts the generator's tools returned, so "grounding" is a real check:
 * the judge can see when a reply invents an amenity or price. Run at temperature 0 for the most stable
 * scores we can get; it's still a model, so the evals assert MEAN scores with tolerance, never a single
 * reply's exact number.
 */

// The fixed facts the mock Guesty exposes — the judge's ground truth for "is this grounded".
const LISTING_FACTS = `
Listing: "Soho 2BR" — 2 bedrooms, sleeps 4. Amenities: self-check-in, full kitchen, WiFi,
quiet bedroom off the courtyard. Location: Spring St, Soho.
Pricing for the requested 2 nights (Apr 24–26): $200/night, $75 cleaning fee, $475 all-in total.
These are the ONLY true facts. Any other amenity, price, or claim is fabricated.
`.trim();

const JudgeSchema = z.object({
  grounding: z.number().int().min(1).max(5), // 5 = every fact/price is from the listing; 1 = fabricated
  relevance: z.number().int().min(1).max(5), // 5 = directly answers the guest's actual concern
  tone: z.number().int().min(1).max(5), // 5 = warm, natural, no generic filler/hedging
  hasExplicitAsk: z.boolean(), // does it end by asking for a concrete next step
  overall: z.number().int().min(1).max(5), // holistic: would a good host send this as-is
  reason: z.string(), // one line, for debugging a low score
});

export type JudgeVerdict = z.infer<typeof JudgeSchema>;

const SYSTEM = `You are a senior reservations manager grading an AI assistant's reply to a guest
inquiry for a short-term rental. Score strictly and consistently on a 1-5 scale. A reply that invents
facts or prices not in the listing must score 1-2 on grounding, no matter how well written. Reward
replies that answer the guest's SPECIFIC concern, state facts plainly without hedging, avoid generic
intros like "Thanks for reaching out", and end with a clear next step. Penalize fluff and vagueness.

${LISTING_FACTS}`;

export function createReplyJudge(opts: { model: string }) {
  const model = openai(opts.model);
  return {
    async judge(input: {
      guestMessage: string;
      code: TrafficLightCode;
      strategy: string;
      reply: string;
    }): Promise<JudgeVerdict> {
      const { object } = await generateObject({
        model,
        schema: JudgeSchema,
        temperature: 0,
        system: SYSTEM,
        prompt: [
          `Guest message: "${input.guestMessage}"`,
          `Assigned code: ${input.code} — strategy: ${input.strategy}`,
          `Assistant reply:\n"""${input.reply}"""`,
          `Grade the reply. Return the rubric scores.`,
        ].join('\n\n'),
      });
      return object;
    },
  };
}
