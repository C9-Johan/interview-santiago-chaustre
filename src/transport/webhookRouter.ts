import { randomUUID } from 'node:crypto';
import express, { type Request, type Response, type Router } from 'express';
import type { Store } from '../ports/Store.js';
import type { VerifyResult } from './verifySignature.js';
import { rawJsonBody } from './rawBody.js';

export interface WebhookDeps {
  store: Store;
  verify: (rawBody: Buffer, headers: Request['headers']) => VerifyResult;
  /** Fire-and-forget async pipeline. MUST NOT be awaited — the route acks first, then processes.
   *  `requestId` is the trace id that ties this delivery to every downstream step. */
  process: (payload: unknown, requestId: string) => void;
  log: typeof import('../adapters/log/logger.js').logger;
}

/**
 * Inbound Guesty webhook endpoint. Implements the contract's fast-ack pattern: verify → dedup →
 * 200 immediately → process async. Built as a factory so it takes its dependencies via DI and
 * never reaches into the pipeline or concrete adapters; the integration owner wires it in app.ts.
 */
export function createWebhookRouter(deps: WebhookDeps): Router {
  const router = express.Router();

  // rawJsonBody is applied per-route so req.body is the original Buffer (needed for HMAC).
  router.post(
    '/webhooks/guesty/message-received',
    rawJsonBody,
    (req: Request, res: Response) => {
      const rawBody = req.body as Buffer;
      // One trace id per delivery — flows through dedup, debounce, and the whole pipeline.
      const requestId = randomUUID();

      // a. Signature first. A bad signature is a hard reject — 401, and Guesty won't retry it
      // (intentional: replaying the same bad bytes would just fail again).
      const verification = deps.verify(rawBody, req.headers);
      if (!verification.ok) {
        deps.log.warn({ requestId, reason: verification.reason }, 'webhook signature rejected');
        res.status(401).json({ error: verification.reason });
        return;
      }

      // b. Parse the raw bytes ourselves now that the signature is settled.
      let payload: unknown;
      try {
        payload = JSON.parse(rawBody.toString('utf8'));
      } catch {
        deps.log.warn({ requestId }, 'webhook body was not valid JSON');
        res.status(400).json({ error: 'invalid JSON body' });
        return;
      }

      const svixId =
        typeof req.headers['svix-id'] === 'string' ? req.headers['svix-id'] : undefined;
      const postId =
        typeof payload === 'object' &&
        payload !== null &&
        'message' in payload &&
        typeof (payload as { message?: unknown }).message === 'object' &&
        (payload as { message: Record<string, unknown> }).message !== null
          ? (payload as { message: { postId?: unknown } }).message.postId
          : undefined;

      deps.log.info({ requestId, svixId, postId }, 'webhook received');

      // c. Idempotency. Dedup on message-level (postId) and delivery-level (svix-id). We call
      // seen() for BOTH keys so both get recorded even on first sight — using || would
      // short-circuit and skip recording the second key. The "seen before" decision is the OR
      // of the two results.
      const postIdSeen =
        typeof postId === 'string' ? deps.store.seen(`msg:${postId}`) : false;
      const svixIdSeen = svixId !== undefined ? deps.store.seen(`evt:${svixId}`) : false;
      if (postIdSeen || svixIdSeen) {
        deps.log.info({ requestId, svixId, postId }, 'webhook duplicate — skipping processing');
        res.status(200).json({ status: 'duplicate' });
        return;
      }

      // d. Ack immediately (contract requires <few hundred ms). Processing happens after.
      // We return the requestId so the caller can correlate with the trace file/logs.
      res.status(200).json({ status: 'accepted', requestId });
      deps.log.info({ requestId, svixId, postId }, 'webhook accepted');

      // e. Fire-and-forget AFTER the response. setImmediate hands control back to the event loop
      // so the response flushes first; the pipeline owns its own error handling/retries.
      setImmediate(() => deps.process(payload, requestId));
    },
  );

  return router;
}
