import { pino } from 'pino';

/**
 * Structured logger. Pretty in dev, JSON otherwise. Use `traceLogger(correlationId)` to get a
 * child logger that stamps every line for one inbound message so a full classify→decide→send
 * trace can be grepped by id.
 */
export const logger = pino(
  process.env.NODE_ENV === 'production'
    ? {}
    : { transport: { target: 'pino-pretty', options: { colorize: true } } },
);

export function traceLogger(correlationId: string) {
  return logger.child({ correlationId });
}

export type TraceLogger = ReturnType<typeof traceLogger>;
