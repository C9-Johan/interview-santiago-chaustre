import { Webhook } from 'svix';

export interface VerifyResult {
  ok: boolean;
  reason?: string;
}

/** Headers can arrive as string | string[]; svix wants a single string per header. */
function firstHeader(value: string | string[] | undefined): string {
  return Array.isArray(value) ? (value[0] ?? '') : (value ?? '');
}

/**
 * Verify the Svix HMAC-SHA256 signature per GUESTY_WEBHOOK_CONTRACT.md "Transport & Signing".
 *
 * When verification is skipped (operator/dev toggle) or no secret is configured, we accept
 * everything. This is what lets the interviewer's unsigned fixtures through during the offline
 * demo — the default config runs with skipSignature=true and no secret.
 */
export function verifyGuestySignature(
  opts: { secret?: string; skip: boolean },
  rawBody: Buffer,
  headers: Record<string, string | string[] | undefined>,
): VerifyResult {
  if (opts.skip || opts.secret === undefined) {
    return { ok: true, reason: 'verification skipped' };
  }

  try {
    const wh = new Webhook(opts.secret);
    // svix verifies the HMAC over `${id}.${timestamp}.${body}` AND enforces the ±5min
    // timestamp drift check internally, so we don't re-implement replay protection here.
    wh.verify(rawBody.toString('utf8'), {
      'svix-id': firstHeader(headers['svix-id']),
      'svix-timestamp': firstHeader(headers['svix-timestamp']),
      'svix-signature': firstHeader(headers['svix-signature']),
    });
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: err instanceof Error ? err.message : String(err) };
  }
}
