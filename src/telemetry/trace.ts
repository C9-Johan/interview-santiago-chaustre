import { logger } from '../adapters/log/logger.js';

/**
 * Request-scoped tracing. One Tracer per processing run, keyed by a `requestId` that is generated at
 * the webhook entry and threaded through every step — so the live logs (pino child bound to
 * requestId) and the persisted trace file all line up for a single inbound message.
 *
 * We have no database, so the durable record is a JSON file written by a TraceSink (see
 * adapters/trace/fileTraceSink). Steps are buffered and flushed once on finish() — the pipeline
 * never throws past its own handlers, so finish() always runs and the file is complete.
 */

export interface TraceStep {
  ts: string;
  step: string;
  data?: unknown;
}

export interface TraceRecord {
  requestId: string;
  startedAt: string;
  finishedAt?: string;
  outcome?: string;
  postId?: string;
  conversationId?: string;
  steps: TraceStep[];
}

export interface TraceSink {
  write(record: TraceRecord): Promise<void>;
}

export interface Tracer {
  readonly id: string;
  /** Attach identifiers (known after parsing) for cross-referencing with transport logs. */
  context(ctx: { postId?: string; conversationId?: string }): void;
  /** Record one step: appended to the trace file AND logged live with the requestId. */
  step(step: string, data?: unknown): void;
  /** Flush the trace to the sink with a final outcome. Safe to await; never throws. */
  finish(outcome: string): Promise<void>;
}

export function createTracer(requestId: string, sink: TraceSink): Tracer {
  const log = logger.child({ requestId });
  const record: TraceRecord = {
    requestId,
    startedAt: new Date().toISOString(),
    steps: [],
  };

  return {
    id: requestId,

    context(ctx) {
      if (ctx.postId !== undefined) record.postId = ctx.postId;
      if (ctx.conversationId !== undefined) record.conversationId = ctx.conversationId;
    },

    step(step, data) {
      record.steps.push({ ts: new Date().toISOString(), step, data });
      log.info(data !== undefined ? { step, data } : { step }, step);
    },

    async finish(outcome) {
      record.outcome = outcome;
      record.finishedAt = new Date().toISOString();
      log.info({ outcome, steps: record.steps.length }, 'trace complete');
      try {
        await sink.write(record);
      } catch (err) {
        log.error({ err: String(err) }, 'failed to persist trace');
      }
    },
  };
}
