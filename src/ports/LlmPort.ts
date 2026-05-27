import type { Classification, GuestMessage, SenderRole } from '../domain/types.js';
import type { GuestyPort } from './GuestyPort.js';

/**
 * Port for the LLM. Adapters: `mockLlm` (deterministic, offline, used by tests) and `openaiLlm`
 * (Vercel AI SDK — structured-output classify + tool-calling generate). Wiring is chosen at
 * startup based on whether OPENAI_API_KEY is set.
 */

export interface ClassifyInput {
  /** The guest turn to classify (may be several coalesced messages). */
  body: string;
  language: string;
  thread: Array<{ body: string; sender: SenderRole; createdAt: string }>;
}

export interface GenerateReplyInput {
  message: GuestMessage;
  classification: Classification;
}

export interface LlmPort {
  /** Classify the message into the traffic-light taxonomy. Output is Zod-validated by the caller. */
  classify(input: ClassifyInput): Promise<Classification>;

  /**
   * Generate a C.L.O.S.E.R. reply. The adapter may call `guesty` (getListing / checkAvailability)
   * as tools to ground the reply in real facts. Throw if a required fact can't be obtained so the
   * pipeline escalates instead of faking it.
   */
  generateReply(input: GenerateReplyInput, guesty: GuestyPort): Promise<string>;
}
