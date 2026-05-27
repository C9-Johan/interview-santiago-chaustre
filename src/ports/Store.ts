/**
 * Port for process state: idempotency dedup + the operator auto-response toggle. Adapter:
 * `memoryStore` (in-memory Maps). In production this is Redis/Postgres — out of scope here.
 */

export interface Store {
  /**
   * Idempotency check. Returns true if `key` was seen before; records it and returns false
   * otherwise. Used with message.postId and svix-id so retries don't double-process.
   */
  seen(key: string): boolean;

  /** Operator kill switch — gates auto-send (CHALLENGE.md §6: `auto_response_enabled`). */
  isAutoResponseEnabled(): boolean;
  setAutoResponseEnabled(enabled: boolean): void;
}
