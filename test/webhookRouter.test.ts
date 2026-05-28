import { describe, it, expect, vi, beforeEach } from 'vitest';
import express, { type Express } from 'express';
import request from 'supertest';
import { Webhook } from 'svix';
import { createWebhookRouter, type WebhookDeps } from '../src/transport/webhookRouter.js';
import { verifyGuestySignature } from '../src/transport/verifySignature.js';

const ENDPOINT = '/webhooks/guesty/message-received';

/** Minimal valid payload — enough for the router to pull message.postId. */
const payload = {
  event: 'reservation.messageReceived',
  message: { postId: 'msg_1', body: 'hi', type: 'fromGuest' },
  conversation: { _id: 'conv_1' },
};

/**
 * Spy doubles for the router's ports. The router never touches concrete adapters, so we hand it a
 * vi.fn() process, a verify stub we can flip, and an in-memory seen-set we control per test.
 */
function makeDeps(overrides: Partial<WebhookDeps> = {}): {
  deps: WebhookDeps;
  process: ReturnType<typeof vi.fn>;
  verify: ReturnType<typeof vi.fn>;
  seenKeys: Set<string>;
} {
  const process = vi.fn();
  const verify = vi.fn(() => ({ ok: true }));
  const seenKeys = new Set<string>();
  const store = {
    // Mirrors memoryStore.seen: true if already present, else record and return false.
    seen: vi.fn((key: string) => {
      if (seenKeys.has(key)) return true;
      seenKeys.add(key);
      return false;
    }),
    isAutoResponseEnabled: () => true,
    setAutoResponseEnabled: () => {},
  };
  const log = { info: vi.fn(), warn: vi.fn(), error: vi.fn() } as unknown as WebhookDeps['log'];

  const deps: WebhookDeps = { store, verify, process, log, ...overrides };
  return { deps, process, verify, seenKeys };
}

function makeApp(deps: WebhookDeps): Express {
  const app = express();
  app.use(createWebhookRouter(deps));
  return app;
}

/**
 * process runs inside setImmediate AFTER the 200 flushes, so it hasn't fired when supertest's
 * promise resolves. Yield one macrotask tick so the assertion sees the real outcome.
 */
const nextTick = () => new Promise((resolve) => setImmediate(resolve));

describe('createWebhookRouter', () => {
  let deps: WebhookDeps;
  let process: ReturnType<typeof vi.fn>;
  let verify: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    ({ deps, process, verify } = makeDeps());
  });

  it('rejects a bad signature with 401 and never processes', async () => {
    verify.mockReturnValue({ ok: false, reason: 'signature mismatch' });

    const res = await request(makeApp(deps))
      .post(ENDPOINT)
      .set('Content-Type', 'application/json')
      .send(payload);

    expect(res.status).toBe(401);
    expect(res.body.error).toBe('signature mismatch');
    await nextTick();
    expect(process).not.toHaveBeenCalled();
  });

  it('returns 400 on invalid JSON (after the signature passes)', async () => {
    const res = await request(makeApp(deps))
      .post(ENDPOINT)
      .set('Content-Type', 'application/json')
      .send('{ not valid json');

    expect(res.status).toBe(400);
    expect(res.body.error).toBe('invalid JSON body');
    await nextTick();
    expect(process).not.toHaveBeenCalled();
  });

  it('accepts a valid first delivery with 200 and processes async', async () => {
    const res = await request(makeApp(deps))
      .post(ENDPOINT)
      .set('Content-Type', 'application/json')
      .set('svix-id', 'evt_1')
      .send(payload);

    expect(res.status).toBe(200);
    expect(res.body.status).toBe('accepted');
    // Processing is deferred to setImmediate so the 200 flushes first; yield a tick to see it run.
    await nextTick();
    expect(process).toHaveBeenCalledTimes(1);
    // process now receives the payload AND the trace requestId (also echoed in the 200 body).
    expect(process).toHaveBeenCalledWith(
      expect.objectContaining({ message: expect.any(Object) }),
      expect.any(String),
    );
    expect(res.body.requestId).toEqual(expect.any(String));
  });

  it('dedups a repeated postId with 200 duplicate and no processing', async () => {
    const app = makeApp(deps);
    await request(app).post(ENDPOINT).set('Content-Type', 'application/json').send(payload);
    await nextTick();
    process.mockClear();

    const res = await request(app)
      .post(ENDPOINT)
      .set('Content-Type', 'application/json')
      .send(payload);

    expect(res.status).toBe(200);
    expect(res.body.status).toBe('duplicate');
    await nextTick();
    expect(process).not.toHaveBeenCalled();
  });

  it('dedups on svix-id even when the postId is new (both keys recorded on first sight)', async () => {
    const app = makeApp(deps);
    // First delivery records BOTH msg:msg_1 and evt:evt_1.
    await request(app)
      .post(ENDPOINT)
      .set('Content-Type', 'application/json')
      .set('svix-id', 'evt_1')
      .send(payload);
    await nextTick();
    process.mockClear();

    // Same delivery (svix-id) but a different message postId — must still be caught by evt: key.
    const res = await request(app)
      .post(ENDPOINT)
      .set('Content-Type', 'application/json')
      .set('svix-id', 'evt_1')
      .send({ ...payload, message: { ...payload.message, postId: 'msg_2' } });

    expect(res.body.status).toBe('duplicate');
    await nextTick();
    expect(process).not.toHaveBeenCalled();
  });

  it('still acks 200 when payload has no postId or svix-id (nothing to dedup on)', async () => {
    const res = await request(makeApp(deps))
      .post(ENDPOINT)
      .set('Content-Type', 'application/json')
      .send({ event: 'reservation.messageReceived', conversation: { _id: 'c' } });

    expect(res.status).toBe(200);
    expect(res.body.status).toBe('accepted');
    await nextTick();
    expect(process).toHaveBeenCalledTimes(1);
  });
});

describe('verifyGuestySignature', () => {
  const secret = 'whsec_C2FuZG9rYW4tdGVzdC1zZWNyZXQtMTIzNDU2Nzg5';
  const bodyStr = JSON.stringify(payload);
  const rawBody = Buffer.from(bodyStr, 'utf8');

  /** Produce real Svix headers for a body using the library itself (so the HMAC is genuine). */
  function signedHeaders(id = 'msg_signed_1') {
    const wh = new Webhook(secret);
    const timestamp = new Date();
    const signature = wh.sign(id, timestamp, bodyStr);
    return {
      'svix-id': id,
      'svix-timestamp': String(Math.floor(timestamp.getTime() / 1000)),
      'svix-signature': signature,
    };
  }

  it('accepts everything when skip is true (the offline-demo default)', () => {
    const res = verifyGuestySignature({ skip: true }, rawBody, {});
    expect(res.ok).toBe(true);
  });

  it('accepts when no secret is configured', () => {
    const res = verifyGuestySignature({ skip: false }, rawBody, {});
    expect(res.ok).toBe(true);
  });

  it('accepts a genuinely Svix-signed body', () => {
    const res = verifyGuestySignature({ secret, skip: false }, rawBody, signedHeaders());
    expect(res.ok).toBe(true);
  });

  it('rejects a tampered body with a reason', () => {
    const headers = signedHeaders();
    const tampered = Buffer.from(bodyStr.replace('hi', 'tampered'), 'utf8');
    const res = verifyGuestySignature({ secret, skip: false }, tampered, headers);
    expect(res.ok).toBe(false);
    expect(res.reason).toBeTruthy();
  });

  it('rejects when signed with a different secret', () => {
    const headers = signedHeaders();
    const res = verifyGuestySignature(
      { secret: 'whsec_ZGlmZmVyZW50LXNlY3JldC12YWx1ZS0wMDAwMDAw', skip: false },
      rawBody,
      headers,
    );
    expect(res.ok).toBe(false);
  });
});
