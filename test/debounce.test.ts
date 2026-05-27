import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createDebouncer } from '../src/pipeline/debounce.js';

const payload = (conversationId: string, body: string) => ({
  conversation: { _id: conversationId },
  message: { body },
});

describe('createDebouncer', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('is a transparent passthrough when window <= 0', () => {
    const downstream = vi.fn();
    const debounced = createDebouncer(0, downstream);
    debounced(payload('c1', 'hi'), 'req-1');
    expect(downstream).toHaveBeenCalledTimes(1);
  });

  it('coalesces a burst on one conversation into a single call with the latest payload + requestId', () => {
    const downstream = vi.fn();
    const debounced = createDebouncer(15_000, downstream);

    debounced(payload('c1', 'hi'), 'req-1');
    vi.advanceTimersByTime(5_000);
    debounced(payload('c1', 'is it free this weekend?'), 'req-2');
    vi.advanceTimersByTime(5_000);
    debounced(payload('c1', 'for 4 people'), 'req-3');

    expect(downstream).not.toHaveBeenCalled(); // window keeps resetting

    vi.advanceTimersByTime(15_000);
    expect(downstream).toHaveBeenCalledTimes(1);
    // The surviving call carries the latest payload AND its requestId.
    expect(downstream.mock.calls[0]![0]).toMatchObject({ message: { body: 'for 4 people' } });
    expect(downstream.mock.calls[0]![1]).toBe('req-3');
  });

  it('debounces separate conversations independently', () => {
    const downstream = vi.fn();
    const debounced = createDebouncer(15_000, downstream);

    debounced(payload('c1', 'a'), 'req-a');
    debounced(payload('c2', 'b'), 'req-b');
    vi.advanceTimersByTime(15_000);

    expect(downstream).toHaveBeenCalledTimes(2);
  });
});
