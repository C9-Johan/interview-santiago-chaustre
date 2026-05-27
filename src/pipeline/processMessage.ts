import type { LlmPort } from '../ports/LlmPort.js';
import type { GuestyPort } from '../ports/GuestyPort.js';
import type { Store } from '../ports/Store.js';
import { parseWebhook } from '../domain/parseWebhook.js';
import { decide } from '../domain/decide.js';
import { STRATEGY } from '../domain/taxonomy.js';
import { traceLogger } from '../adapters/log/logger.js';
import type { GuestMessage, Classification } from '../domain/types.js';

/**
 * The orchestrator. Runs AFTER the webhook has acked 200, so it owns its own errors — a throw here
 * must never crash the process. Sequence: parse → guards → classify → decide (hard rules) →
 * auto-send | escalate. Both outcomes leave an internal Guesty note so a human can see what happened.
 *
 * Built as a factory taking its ports via DI so it's trivially testable and adapter-agnostic.
 */
export function createProcessMessage(deps: {
  llm: LlmPort;
  guesty: GuestyPort;
  store: Store;
}): (payload: unknown) => Promise<void> {
  const { llm, guesty, store } = deps;

  return async function processMessage(payload: unknown): Promise<void> {
    let msg: GuestMessage;
    try {
      msg = parseWebhook(payload);
    } catch (err) {
      traceLogger('unparsed').error({ err: String(err) }, 'could not parse webhook payload');
      return;
    }

    const log = traceLogger(msg.postId);

    // --- Guards: skip without crashing (CHALLENGE / contract edge cases) ---
    if (msg.sender !== 'guest') {
      log.info({ sender: msg.sender }, 'not a guest message — ignoring');
      return;
    }
    if (msg.body.trim() === '') {
      log.info('empty/whitespace body (sticker/attachment/system) — ignoring');
      return;
    }
    if (msg.hostAlreadyReplied) {
      // A human already responded later in the thread — no need to auto-reply over them.
      log.info('host already replied in thread — skipping auto-reply');
      return;
    }

    // --- Classify (LLM, structured output) ---
    let classification: Classification;
    try {
      classification = await llm.classify({
        body: msg.body,
        language: msg.language,
        thread: msg.thread,
      });
    } catch (err) {
      log.error({ err: String(err) }, 'classification failed — escalating');
      await escalate(msg, 'classification failed', log);
      return;
    }
    log.info(
      {
        primary: classification.primary_code,
        secondary: classification.secondary_code,
        confidence: classification.confidence,
        risk: classification.risk_flag,
      },
      'classified',
    );

    // --- Decide (PURE hard-rules gate — never the LLM's call) ---
    const decision = decide(classification, {
      autoResponseEnabled: store.isAutoResponseEnabled(),
    });
    log.info({ action: decision.action, reason: decision.reason }, 'decision');

    if (decision.action === 'escalate') {
      await escalate(msg, decision.reason, log, classification);
      return;
    }

    // --- Auto-send: generate grounded C.L.O.S.E.R. reply, then post as internal note ---
    try {
      const reply = await llm.generateReply({ message: msg, classification }, guesty);
      const note = await guesty.postNote(msg.conversationId, reply);
      log.info({ noteId: note.id }, 'auto-reply posted as internal note');
    } catch (err) {
      // generateReply throws when a required fact is missing — escalate rather than fake it.
      log.error({ err: String(err) }, 'reply generation/send failed — escalating');
      await escalate(msg, 'reply generation failed', log, classification);
    }
  };

  /** Post a human-review note so escalations are visible to operators in the conversation. */
  async function escalate(
    msg: GuestMessage,
    reason: string,
    log: ReturnType<typeof traceLogger>,
    classification?: Classification,
  ): Promise<void> {
    const summary = classification
      ? `${classification.primary_code} (${STRATEGY[classification.primary_code]}), confidence ${classification.confidence}`
      : 'not classified';
    const body = `⚠️ NEEDS HUMAN REVIEW — ${reason}.\nClassification: ${summary}.\nGuest said: "${msg.body}"`;
    try {
      const note = await guesty.postNote(msg.conversationId, body);
      log.info({ noteId: note.id, reason }, 'escalation note posted');
    } catch (err) {
      log.error({ err: String(err), reason }, 'failed to post escalation note');
    }
  }
}
