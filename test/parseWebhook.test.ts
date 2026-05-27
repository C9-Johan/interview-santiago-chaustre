import { describe, it, expect } from 'vitest';
import { parseWebhook } from '../src/domain/parseWebhook.js';

/** The "Minimum Test Payload" from GUESTY_WEBHOOK_CONTRACT.md — the happy path. */
const minimalPayload = {
  event: 'reservation.messageReceived',
  reservationId: 'res_test_001',
  message: {
    postId: 'msg_test_001',
    body: 'Is the Soho 2BR available Fri-Sun for 4 adults? What\'s the total?',
    createdAt: '2026-04-20T14:31:09Z',
    type: 'fromGuest',
    module: 'airbnb2',
  },
  conversation: {
    _id: 'conv_test_001',
    guestId: 'guest_test_001',
    language: 'en',
    status: 'OPEN',
    integration: { platform: 'airbnb2' },
    meta: {
      guestName: 'Sarah',
      reservations: [
        {
          _id: 'res_test_001',
          checkIn: '2026-04-24T22:00:00.000Z',
          checkOut: '2026-04-26T16:00:00.000Z',
          confirmationCode: 'TESTCODE1',
        },
      ],
    },
    thread: [],
  },
  meta: { eventId: 'evt_test_001', messageId: 'msgid_test_001' },
};

describe('parseWebhook()', () => {
  it('maps the minimal happy-path payload', () => {
    const m = parseWebhook(minimalPayload);
    expect(m.postId).toBe('msg_test_001');
    expect(m.conversationId).toBe('conv_test_001');
    expect(m.body).toContain('Soho 2BR');
    expect(m.sender).toBe('guest');
    expect(m.platform).toBe('airbnb2');
    expect(m.language).toBe('en');
    expect(m.guestName).toBe('Sarah');
    expect(m.reservation).toEqual({
      id: 'res_test_001',
      checkIn: '2026-04-24T22:00:00.000Z',
      checkOut: '2026-04-26T16:00:00.000Z',
      confirmationCode: 'TESTCODE1',
    });
    expect(m.hostAlreadyReplied).toBe(false);
    expect(m.thread).toEqual([]);
  });

  it('falls back to meta.reservations[0] when top-level reservationId is missing', () => {
    const { reservationId, ...rest } = minimalPayload;
    const m = parseWebhook(rest);
    expect(m.reservation?.id).toBe('res_test_001');
    expect(m.reservation?.confirmationCode).toBe('TESTCODE1');
  });

  it('leaves reservation undefined when neither reservationId nor meta.reservations present', () => {
    const payload = {
      ...minimalPayload,
      conversation: {
        ...minimalPayload.conversation,
        meta: { guestName: 'Sarah', reservations: [] },
      },
    };
    const { reservationId, ...rest } = payload;
    const m = parseWebhook(rest);
    expect(m.reservation).toBeUndefined();
  });

  it('trims whitespace-only body to "" without throwing', () => {
    const payload = {
      ...minimalPayload,
      message: { ...minimalPayload.message, body: '   \n  ' },
    };
    const m = parseWebhook(payload);
    expect(m.body).toBe('');
  });

  it('maps sender roles: fromGuest→guest, fromHost→host, system→system', () => {
    const guest = parseWebhook(minimalPayload);
    expect(guest.sender).toBe('guest');

    const host = parseWebhook({
      ...minimalPayload,
      message: { ...minimalPayload.message, type: 'fromHost' },
    });
    expect(host.sender).toBe('host');

    const sys = parseWebhook({
      ...minimalPayload,
      message: { ...minimalPayload.message, type: 'system' },
    });
    expect(sys.sender).toBe('system');
  });

  it('detects hostAlreadyReplied when a fromHost message follows the current one in thread', () => {
    const payload = {
      ...minimalPayload,
      conversation: {
        ...minimalPayload.conversation,
        thread: [
          { postId: 'msg_test_001', body: 'guest question', type: 'fromGuest', createdAt: '2026-04-20T14:31:09Z' },
          { postId: 'msg_test_002', body: 'host answer', type: 'fromHost', createdAt: '2026-04-20T14:40:00Z' },
        ],
      },
    };
    const m = parseWebhook(payload);
    expect(m.hostAlreadyReplied).toBe(true);
    expect(m.thread).toHaveLength(2);
    expect(m.thread[1]?.sender).toBe('host');
  });

  it('reports hostAlreadyReplied false when the current message is last in thread', () => {
    const payload = {
      ...minimalPayload,
      conversation: {
        ...minimalPayload.conversation,
        thread: [
          { postId: 'msg_test_000', body: 'earlier host msg', type: 'fromHost', createdAt: '2026-04-20T14:00:00Z' },
          { postId: 'msg_test_001', body: 'guest question', type: 'fromGuest', createdAt: '2026-04-20T14:31:09Z' },
        ],
      },
    };
    const m = parseWebhook(payload);
    expect(m.hostAlreadyReplied).toBe(false);
  });

  it('throws on a payload missing the message object', () => {
    expect(() => parseWebhook({ conversation: { _id: 'c' } })).toThrow();
  });
});
