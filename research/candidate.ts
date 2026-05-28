import { generateObject } from 'ai';
import { openai } from '@ai-sdk/openai';
import { z } from 'zod';
import type { LlmPort } from '../src/ports/LlmPort.js';
import { createOpenAiLlm } from '../src/adapters/llm/openaiLlm.js';
import {
  CLASSIFY_SYSTEM_PROMPT,
} from '../src/domain/classifyPrompt.js';
import { CLOSER_SYSTEM_PROMPT } from '../src/domain/closer.js';
import {
  DEFAULT_CHECK_AVAILABILITY_DESC,
  DEFAULT_GET_LISTING_DESC,
  DEFAULT_TEMPERATURE,
} from '../src/adapters/llm/openaiLlm.js';

/**
 * A candidate is one complete set of tunable values. The proposer always returns full values (not
 * diffs), so a candidate is self-contained: build an adapter from it, score it, done. `model` is fixed
 * to the worker model — see WORKER note in optimize.ts; we tune prompts/params, not model, by default.
 */
export interface Candidate {
  classifySystem: string;
  closerSystem: string;
  getListingDesc: string;
  checkAvailabilityDesc: string;
  /** undefined ⇒ provider default (the committed baseline). */
  temperature: number | undefined;
  model: string;
}

/** The committed source values — the optimizer's starting point and its comparison baseline. */
export function baselineCandidate(model: string): Candidate {
  return {
    classifySystem: CLASSIFY_SYSTEM_PROMPT,
    closerSystem: CLOSER_SYSTEM_PROMPT,
    getListingDesc: DEFAULT_GET_LISTING_DESC,
    checkAvailabilityDesc: DEFAULT_CHECK_AVAILABILITY_DESC,
    temperature: DEFAULT_TEMPERATURE,
    model,
  };
}

/** Construct a real OpenAI adapter that runs with this candidate's overrides. */
export function buildLlm(c: Candidate): LlmPort {
  return createOpenAiLlm(
    { model: c.model },
    {
      classifySystem: c.classifySystem,
      closerSystem: c.closerSystem,
      getListingDesc: c.getListingDesc,
      checkAvailabilityDesc: c.checkAvailabilityDesc,
      temperature: c.temperature,
    },
  );
}

/** Structured shape the proposer must return — full replacement values for the tunable surface. */
const ProposalSchema = z.object({
  rationale: z.string().describe('Why these changes should improve the failing groups.'),
  changeSummary: z.string().describe('One short line describing what you changed this iteration.'),
  classifySystem: z.string(),
  closerSystem: z.string(),
  getListingDesc: z.string(),
  checkAvailabilityDesc: z.string(),
  temperature: z.number().min(0).max(2),
});
export type Proposal = z.infer<typeof ProposalSchema>;

export interface ProposeContext {
  current: Candidate;
  trainReportText: string;
  misses: string[];
  history: { iter: number; summary: string; trainObjective: number; status: string }[];
}

const PROPOSER_SYSTEM = `You are a prompt-engineering optimizer for a short-term-rental AI agent. The agent has two LLM steps:
1) CLASSIFY a guest message into a fixed "traffic-light" taxonomy (codes G1,G2,Y1..Y7,R1,R2,X1) with a secondary code, confidence, extracted entities, and a risk_flag.
2) GENERATE a C.L.O.S.E.R. sales reply grounded in tool-provided listing/availability facts.

You improve results on a held-out eval by rewriting the system prompts, the two tool descriptions, and the temperature. You will be given the CURRENT values, the latest scores, and the specific failing cases.

HARD RULES — a violation gets your proposal discarded:
- Do NOT change the meaning of the taxonomy, the §6 priority order (RED > Y5 > Y2 > Y4 > Y1 > Y3 > Y6 > Y7 > GREEN > GRAY), or the auto-send gate. Those live in code you cannot touch.
- NEVER weaken risk detection. Keep (and you may strengthen) instructions to set risk_flag for off-platform payment, address leakage, and guarantee language. Risk recall must stay 100%.
- The classifier must stay calibrated: genuinely sparse messages should NOT be over-confident.
- Keep the C.L.O.S.E.R. reply grounded only in real tool facts (never invent prices/dates), 3–5 sentences, ending with an explicit ask.

Return COMPLETE values for every field (the full prompt text, not a diff). Preserve what already works; change only what the failures justify. Make a focused, meaningful edit each round, not a cosmetic one.`;

/**
 * Ask the optimizer model for ONE improved candidate. Hard-capped by `signal` (a 2-minute timeout in
 * the loop) passed straight to the AI SDK; on abort this returns `null` so the loop can skip the round
 * rather than hang. Returns the proposal plus token usage for the cost report.
 */
export async function propose(
  model: string,
  ctx: ProposeContext,
  signal: AbortSignal,
): Promise<{ proposal: Proposal; tokens: number } | null> {
  const historyText =
    ctx.history.length === 0
      ? '(none yet)'
      : ctx.history
          .map((h) => `  #${h.iter} obj=${h.trainObjective.toFixed(4)} [${h.status}] ${h.summary}`)
          .join('\n');

  const prompt = `CURRENT VALUES
=== classifySystem ===
${ctx.current.classifySystem}

=== closerSystem ===
${ctx.current.closerSystem}

=== getListingDesc ===
${ctx.current.getListingDesc}

=== checkAvailabilityDesc ===
${ctx.current.checkAvailabilityDesc}

=== temperature ===
${ctx.current.temperature ?? 'provider default'}

LATEST TRAIN SCORES
${ctx.trainReportText}

FAILING CASES (want X, got Y) — focus here:
${ctx.misses.length ? ctx.misses.map((m) => '  ' + m).join('\n') : '  (none — try improving calibration/secondary/closer instead)'}

WHAT YOU'VE TRIED (don't repeat low-scoring ideas):
${historyText}

Propose ONE improved candidate now.`;

  try {
    const { object, usage } = await generateObject({
      model: openai(model),
      schema: ProposalSchema,
      system: PROPOSER_SYSTEM,
      prompt,
      temperature: 0.5,
      abortSignal: signal,
    });
    return { proposal: object, tokens: usage.totalTokens ?? 0 };
  } catch (err) {
    if (signal.aborted) return null; // timed out — caller treats as a skipped round
    throw err;
  }
}
