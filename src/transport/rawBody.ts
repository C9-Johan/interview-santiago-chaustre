import express from 'express';

/**
 * Capture the request body as raw bytes (a Buffer) instead of letting Express parse JSON.
 *
 * Svix signs the EXACT bytes Guesty sent. If we let express.json() parse and we later
 * re-serialize the object, key order and whitespace can change, producing a different byte
 * string and a signature mismatch — even though the payload is semantically identical. So for
 * the webhook route we keep the original bytes around for HMAC verification and parse JSON
 * ourselves afterwards.
 *
 * The wildcard match type makes this apply regardless of Content-Type (Guesty sends
 * application/json, but we don't want a header quirk to skip raw capture). 1mb is comfortably
 * above a webhook payload.
 */
export const rawJsonBody = express.raw({ type: '*/*', limit: '1mb' });
