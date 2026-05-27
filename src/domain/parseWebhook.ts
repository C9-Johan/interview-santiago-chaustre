import { z } from 'zod';
import type { GuestMessage, Reservation, SenderRole } from './types.js';

/**
 * Normalizes the raw Guesty `reservation.messageReceived` webhook into a `GuestMessage`.
 *
 * The schema is intentionally permissive: Guesty sends far more than we consume, fields are
 * frequently absent (no top-level reservationId, empty thread, empty body), and we must NOT crash
 * on those — the contract calls them out as normal cases (pre-booking inquiries, stickers, bursts).
 * We validate only the parts we actually read and throw a clear Error if the message itself is
 * missing, since without a message there is nothing to process.
 */

/** Map Guesty's `message.type` to our sender role. Unknown → system (never auto-processed). */
function toSenderRole(type: unknown): SenderRole {
  switch (type) {
    case 'fromGuest':
    case 'toHost':
      return 'guest';
    case 'fromHost':
    case 'toGuest':
      return 'host';
    default:
      return 'system';
  }
}

const ThreadItemSchema = z
  .object({
    postId: z.string().optional(),
    body: z.string().optional(),
    type: z.unknown().optional(),
    createdAt: z.string().optional(),
  })
  .passthrough();

const ReservationSchema = z
  .object({
    _id: z.string().optional(),
    checkIn: z.string().optional(),
    checkOut: z.string().optional(),
    confirmationCode: z.string().optional(),
    listingId: z.string().optional(),
  })
  .passthrough();

const WebhookSchema = z
  .object({
    reservationId: z.string().optional(),
    message: z
      .object({
        postId: z.string().optional(),
        body: z.string().optional(),
        type: z.unknown().optional(),
        module: z.string().optional(),
        createdAt: z.string().optional(),
      })
      .passthrough(),
    conversation: z
      .object({
        _id: z.string().optional(),
        language: z.string().optional(),
        listingId: z.string().optional(),
        integration: z
          .object({ platform: z.string().optional() })
          .passthrough()
          .optional(),
        meta: z
          .object({
            guestName: z.string().optional(),
            reservations: z.array(ReservationSchema).optional(),
          })
          .passthrough()
          .optional(),
        thread: z.array(ThreadItemSchema).optional(),
      })
      .passthrough()
      .optional(),
  })
  .passthrough();

export function parseWebhook(payload: unknown): GuestMessage {
  const parsed = WebhookSchema.safeParse(payload);
  if (!parsed.success) {
    throw new Error(`Unusable Guesty webhook payload: ${parsed.error.message}`);
  }
  const { message, conversation, reservationId } = parsed.data;
  const conv = conversation ?? {};

  const postId = message.postId;
  const conversationId = conv._id;
  if (!postId || !conversationId) {
    throw new Error(
      'Guesty webhook missing message.postId or conversation._id — cannot process',
    );
  }

  // Resolve reservation: prefer top-level reservationId, fall back to the first meta reservation.
  // Either may carry the dates; if neither exists this is a pre-booking inquiry (undefined).
  const firstRes = conv.meta?.reservations?.[0];
  let reservation: Reservation | undefined;
  if (reservationId) {
    reservation = {
      id: reservationId,
      checkIn: firstRes?.checkIn,
      checkOut: firstRes?.checkOut,
      confirmationCode: firstRes?.confirmationCode,
    };
  } else if (firstRes?._id) {
    reservation = {
      id: firstRes._id,
      checkIn: firstRes.checkIn,
      checkOut: firstRes.checkOut,
      confirmationCode: firstRes.confirmationCode,
    };
  }

  const listingId = conv.listingId ?? firstRes?.listingId ?? undefined;

  const thread = (conv.thread ?? []).map((item) => ({
    body: (item.body ?? '').trim(),
    sender: toSenderRole(item.type),
    createdAt: item.createdAt ?? '',
  }));

  // Host-already-replied: scan the thread (oldest → newest) for a non-guest message that appears
  // AFTER the current message. If the current message isn't in the thread, we can't tell → false.
  const rawThread = conv.thread ?? [];
  const currentIdx = rawThread.findIndex((item) => item.postId === postId);
  const hostAlreadyReplied =
    currentIdx >= 0 &&
    rawThread
      .slice(currentIdx + 1)
      .some((item) => toSenderRole(item.type) !== 'guest');

  return {
    postId,
    conversationId,
    body: (message.body ?? '').trim(),
    sender: toSenderRole(message.type),
    createdAt: message.createdAt ?? '',
    platform: conv.integration?.platform ?? message.module ?? 'unknown',
    language: conv.language ?? 'en',
    guestName: conv.meta?.guestName,
    reservation,
    listingId,
    hostAlreadyReplied,
    thread,
  };
}
