import { logger } from '../adapters/log/logger.js';

/**
 * Burst debounce (CHALLENGE / GUESTY_WEBHOOK_CONTRACT §"Burst messages"). Guests fire several short
 * messages in a row ("Hi" → "is it free this weekend?" → "for 4 people"), each a separate webhook.
 * Processing each one wastes LLM calls and classifies a half-formed turn. Instead we wait a short
 * window per conversation and process once the guest pauses.
 *
 * We coalesce by simply processing the LATEST payload after the window: per the contract, every
 * webhook carries the full `conversation.thread`, so the most recent one already contains the whole
 * turn — no manual message stitching needed. Idempotency still lives upstream in the webhook router
 * (per postId / svix-id), which protects Svix retries; debounce only collapses distinct burst
 * messages.
 *
 * Disabled when windowMs <= 0 (the default) — then it's a transparent passthrough, so the rest of
 * the system behaves exactly as if debounce didn't exist.
 *
 * NOTE: per-process, in-memory timers. A multi-replica deployment needs a shared scheduler (e.g. a
 * delayed queue keyed by conversationId) so bursts that land on different replicas still coalesce.
 */
export function createDebouncer(
  windowMs: number,
  downstream: (payload: unknown, requestId: string) => void,
): (payload: unknown, requestId: string) => void {
  if (windowMs <= 0) return downstream;

  const pending = new Map<string, NodeJS.Timeout>();

  return function debounced(payload: unknown, requestId: string): void {
    const key = conversationKey(payload);

    // Reset the window: a newer message means the guest is still typing their turn. The surviving
    // timer carries the LATEST payload + requestId, so the coalesced turn traces under one id.
    const existing = pending.get(key);
    if (existing) clearTimeout(existing);

    const timer = setTimeout(() => {
      pending.delete(key);
      logger.info(
        { conversationId: key, requestId },
        'debounce window elapsed — processing coalesced turn',
      );
      downstream(payload, requestId);
    }, windowMs);
    // Don't let a pending debounce keep the process (or a test runner) alive.
    timer.unref?.();

    pending.set(key, timer);
  };
}

/** Best-effort conversation id for bucketing. Falls back to a unique key so unkeyable payloads
 *  still get processed (just without coalescing) rather than being dropped or merged together. */
function conversationKey(payload: unknown): string {
  if (typeof payload === 'object' && payload !== null) {
    const conv = (payload as { conversation?: { _id?: unknown } }).conversation;
    if (conv && typeof conv._id === 'string') return conv._id;
  }
  return `solo:${Math.random().toString(36).slice(2)}`;
}
