import { z } from 'zod';

/**
 * Domain types for the inquiry → reply loop.
 *
 * These are the contracts the rest of the app speaks in. The Guesty webhook shape is an
 * *integration* concern and is normalized into `GuestMessage` by `parseWebhook.ts` — nothing
 * downstream of parsing should reach into the raw Guesty payload.
 */

/** The full "traffic light" taxonomy from CHALLENGE.md §6. */
export const TRAFFIC_LIGHT_CODES = [
  'G1', 'G2', // GREEN — lay-downs
  'Y1', 'Y2', 'Y3', 'Y4', 'Y5', 'Y6', 'Y7', // YELLOW — hurdles
  'R1', 'R2', // RED — anchors (price)
  'X1', // GRAY — low signal
] as const;

export const TrafficLightCode = z.enum(TRAFFIC_LIGHT_CODES);
export type TrafficLightCode = z.infer<typeof TrafficLightCode>;

/** Sender role, derived from the Guesty `message.type` field. */
export type SenderRole = 'guest' | 'host' | 'system';

/** Structured facts the classifier pulls out of the message, used by the reply generator. */
export const ExtractedEntities = z.object({
  dates: z.array(z.string()).default([]),
  guestCount: z.number().int().positive().nullable().default(null),
  pets: z.boolean().nullable().default(null),
  vehicles: z.number().int().nonnegative().nullable().default(null),
});
export type ExtractedEntities = z.infer<typeof ExtractedEntities>;

/**
 * Output of the classification step. The LLM produces this; we validate it with Zod before
 * trusting it. `risk_flag` lets the model surface restricted content (off-platform payment,
 * address leakage, guarantee language) that must force escalation.
 */
export const Classification = z.object({
  primary_code: TrafficLightCode,
  secondary_code: TrafficLightCode.nullable().default(null),
  confidence: z.number().min(0).max(1),
  extracted_entities: ExtractedEntities.prefault({}),
  risk_flag: z.boolean().default(false),
  rationale: z.string().default(''),
});
export type Classification = z.infer<typeof Classification>;

/** Outcome of the hard-rules auto-send gate. */
export type DecisionAction = 'auto_send' | 'escalate';
export interface Decision {
  action: DecisionAction;
  /** Human-readable reason — names the failing condition on escalate. */
  reason: string;
}

/** A reservation as we need it (subset of the Guesty payload). */
export interface Reservation {
  id: string;
  checkIn?: string;
  checkOut?: string;
  confirmationCode?: string;
}

/**
 * The normalized message + context the pipeline operates on. One per inbound webhook,
 * after `parseWebhook` has resolved sender role, reservation fallback, and trimmed the body.
 */
export interface GuestMessage {
  /** Stable message id — used for idempotency. */
  postId: string;
  conversationId: string;
  body: string;
  sender: SenderRole;
  createdAt: string;
  platform: string;
  language: string;
  guestName?: string;
  reservation?: Reservation;
  /** Listing id if resolvable from the payload (drives tool lookups). */
  listingId?: string;
  /** True if a non-guest message appears later in the thread than this one. */
  hostAlreadyReplied: boolean;
  /** Full conversation thread, oldest → newest, for LLM context. */
  thread: Array<{ body: string; sender: SenderRole; createdAt: string }>;
}
