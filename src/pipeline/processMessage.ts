import type { LlmPort } from '../ports/LlmPort.js';
import type { GuestyPort } from '../ports/GuestyPort.js';
import type { Store } from '../ports/Store.js';
import { parseWebhook } from '../domain/parseWebhook.js';
import { decide } from '../domain/decide.js';
import { STRATEGY } from '../domain/taxonomy.js';
import { createTracer, type TraceSink } from '../telemetry/trace.js';
import type { GuestMessage, Classification } from '../domain/types.js';

/**
 * The orchestrator. Runs AFTER the webhook has acked 200, so it owns its own errors — a throw here
 * must never crash the process. Sequence: parse → guards → classify → decide (hard rules) →
 * auto-send | escalate. Both outcomes leave an internal Guesty note so a human can see what happened.
 *
 * Every step is traced: logged live (pino child bound to `requestId`) AND buffered into a JSON trace
 * file via the TraceSink, so a full run can be replayed/verified later without a database. The
 * `requestId` is generated at the webhook entry and threaded in here so transport and pipeline lines
 * share one id.
 *
 * Built as a factory taking its ports via DI so it's trivially testable and adapter-agnostic.
 */
export function createProcessMessage(deps: {
  llm: LlmPort;
  guesty: GuestyPort;
  store: Store;
  traceSink: TraceSink;
}): (payload: unknown, requestId: string) => Promise<void> {
  const { llm, guesty, store, traceSink } = deps;

  return async function processMessage(payload: unknown, requestId: string): Promise<void> {
    const trace = createTracer(requestId, traceSink);
    // First step captures exactly what the agent received — the raw inbound payload.
    trace.step('webhook_received', { payload });

    let msg: GuestMessage;
    try {
      msg = parseWebhook(payload);
    } catch (err) {
      trace.step('parse_failed', { error: String(err) });
      await trace.finish('error');
      return;
    }
    trace.context({ postId: msg.postId, conversationId: msg.conversationId });
    trace.step('parsed', { message: msg });

    // --- Guards: skip without crashing (CHALLENGE / contract edge cases) ---
    if (msg.sender !== 'guest') {
      trace.step('ignored', { reason: 'not a guest message', sender: msg.sender });
      await trace.finish('ignored');
      return;
    }
    if (msg.body.trim() === '') {
      trace.step('ignored', { reason: 'empty/whitespace body (sticker/attachment/system)' });
      await trace.finish('ignored');
      return;
    }
    if (msg.hostAlreadyReplied) {
      trace.step('ignored', { reason: 'host already replied in thread' });
      await trace.finish('ignored');
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
      trace.step('classify_failed', { error: String(err) });
      const noteId = await escalate(msg, 'classification failed');
      trace.step('escalation_note_posted', { noteId, reason: 'classification failed' });
      await trace.finish('escalated');
      return;
    }
    trace.step('classified', { classification });

    // --- Decide (PURE hard-rules gate — never the LLM's call) ---
    const decision = decide(classification, {
      autoResponseEnabled: store.isAutoResponseEnabled(),
    });
    trace.step('decided', { decision });

    if (decision.action === 'escalate') {
      const noteId = await escalate(msg, decision.reason, classification);
      trace.step('escalation_note_posted', { noteId, reason: decision.reason });
      await trace.finish('escalated');
      return;
    }

    // --- Auto-send: generate grounded C.L.O.S.E.R. reply, then post as internal note ---
    try {
      const reply = await llm.generateReply({ message: msg, classification }, guesty);
      trace.step('reply_generated', { reply });
      const note = await guesty.postNote(msg.conversationId, reply);
      trace.step('auto_reply_posted', { noteId: note.id });
      await trace.finish('auto_sent');
    } catch (err) {
      // generateReply throws when a required fact is missing — escalate rather than fake it.
      trace.step('reply_failed', { error: String(err) });
      const noteId = await escalate(msg, 'reply generation failed', classification);
      trace.step('escalation_note_posted', { noteId, reason: 'reply generation failed' });
      await trace.finish('escalated');
    }
  };

  /** Post a human-review note so escalations are visible to operators. Returns the note id or null. */
  async function escalate(
    msg: GuestMessage,
    reason: string,
    classification?: Classification,
  ): Promise<string | null> {
    const summary = classification
      ? `${classification.primary_code} (${STRATEGY[classification.primary_code]}), confidence ${classification.confidence}`
      : 'not classified';
    const body = `⚠️ NEEDS HUMAN REVIEW — ${reason}.\nClassification: ${summary}.\nGuest said: "${msg.body}"`;
    try {
      const note = await guesty.postNote(msg.conversationId, body);
      return note.id;
    } catch {
      return null;
    }
  }
}
