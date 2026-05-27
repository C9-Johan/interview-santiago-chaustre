import type { Store } from '../../ports/Store.js';

/**
 * In-memory Store: idempotency dedup + the operator auto-response toggle.
 *
 * State is process-local — fine for the challenge and single-instance dev, but in production this
 * must be shared/durable (Redis for the seen-set with TTL, Postgres/flag service for the toggle) so
 * dedup survives restarts and works across replicas.
 */
export function createMemoryStore(initialAutoResponse: boolean): Store {
  const seenKeys = new Set<string>();
  let autoResponseEnabled = initialAutoResponse;

  return {
    seen(key: string): boolean {
      if (seenKeys.has(key)) return true;
      seenKeys.add(key);
      return false;
    },

    isAutoResponseEnabled(): boolean {
      return autoResponseEnabled;
    },

    setAutoResponseEnabled(enabled: boolean): void {
      autoResponseEnabled = enabled;
    },
  };
}
