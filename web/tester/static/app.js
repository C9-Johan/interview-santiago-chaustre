// Arrow-js UI for the InquiryIQ tester. Two reactive panels: a guest chat
// pane on the left (drives the service's webhook) and an escalation inbox
// on the right (polls the service's /escalations).
//
// Arrow-js is a tiny reactive library (~3KB). reactive() wraps a plain
// object; html`...` templates track reads on the proxy and patch the DOM
// where those reads appear. No build step, no framework boilerplate.

import { reactive, html } from "https://esm.sh/@arrow-js/core@1";

const state = reactive({
  messages: [],          // entries in the active conversation
  escalations: [],       // full escalations feed from the service
  sending: false,
  error: null,
  lastStatus: null,      // last webhook forward status for debugging
});

// One entry per "this turn ended with escalation" event. We maintain this
// locally (rather than inferring from polling) so the user can see the
// escalation inline with their chat even if the server record arrives
// seconds later.
const escalatedPostIDs = new Set();

const presets = [
  "Hi! Is the Soho 2BR available Fri April 24 – Sun April 26 for 4 adults?",
  "Can I check in at 10pm? My flight lands late.",
  "Is parking included or extra?",
  "Any discount for a 5-night stay?",
  "Can I bring my dog? He's small and well-behaved.",
  "Hi",
];

function formFields() {
  return {
    conversation_id: document.getElementById("conv-id").value.trim(),
    reservation_id: document.getElementById("res-id").value.trim(),
    guest_name: document.getElementById("guest-name").value.trim(),
    platform: document.getElementById("platform").value,
  };
}

async function sendMessage(body) {
  if (state.sending) return;
  const trimmed = (body || "").trim();
  if (!trimmed) return;

  state.sending = true;
  state.error = null;
  try {
    const payload = { ...formFields(), body: trimmed };
    const res = await fetch("/api/send", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const data = await res.json();
    state.lastStatus = data.service_status;
    if (!res.ok) {
      state.error = data.error || `HTTP ${res.status}`;
    }
  } catch (e) {
    state.error = e.message;
  } finally {
    state.sending = false;
    refreshConversation();
    refreshEscalations();
  }
}

async function refreshConversation() {
  const convID = document.getElementById("conv-id").value.trim();
  if (!convID) return;
  try {
    const res = await fetch(`/api/conversations/${encodeURIComponent(convID)}`);
    if (!res.ok) return;
    const data = await res.json();
    state.messages = data.entries || [];
  } catch (_) {
    // transient; the poll loop will retry
  }
}

// Normalizer: the service serializes domain.Escalation with default Go
// field names (PascalCase, no json tags). We flatten to snake_case here so
// the rest of the UI doesn't need to know.
function normalizeEscalation(raw) {
  return {
    id: raw.ID || raw.id,
    reason: raw.Reason || raw.reason,
    detail: raw.Detail || raw.detail,
    post_id: raw.PostID || raw.post_id,
    conversation_key: raw.ConversationKey || raw.conversation_key,
    guest_name: raw.GuestName || raw.guest_name,
    platform: raw.Platform || raw.platform,
    created_at: raw.CreatedAt || raw.created_at,
    primary_code: raw.Classification?.PrimaryCode || raw.primary_code,
    guest_message: raw.Reply?.Body || raw.guest_message,
  };
}

async function refreshEscalations() {
  try {
    const res = await fetch("/api/escalations");
    if (!res.ok) return;
    const data = await res.json();
    const rawItems = Array.isArray(data) ? data : data.escalations || [];
    const items = rawItems.map(normalizeEscalation);
    const seen = new Set();
    const sorted = items
      .slice()
      .sort((a, b) => (b.created_at || "").localeCompare(a.created_at || ""))
      .filter(e => {
        const key = e.post_id || e.id;
        if (seen.has(key)) return false;
        seen.add(key);
        if (e.post_id) escalatedPostIDs.add(e.post_id);
        return true;
      });
    state.escalations = sorted;
  } catch (_) {
    // transient
  }
}

setInterval(refreshConversation, 1500);
setInterval(refreshEscalations, 1500);
refreshConversation();
refreshEscalations();

const fmtTime = (iso) => {
  if (!iso) return "";
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const formatDetail = (detail) => {
  if (!detail) return "";
  return Array.isArray(detail) ? detail.join(", ") : String(detail);
};

const bubble = (entry) => html`
  <div class="bubble ${() => entry.role}">
    ${() => entry.body}
    <span class="meta">${() => `${entry.role} · ${fmtTime(entry.at)}`}</span>
  </div>
`;

const inlineEscalation = (e) => html`
  <div class="bubble escalate">
    <strong>Escalated</strong> — reason: <code>${() => e.reason || "unknown"}</code>
    ${() => e.primary_code ? ` · code ${e.primary_code}` : ""}
    ${() => e.detail ? ` · ${formatDetail(e.detail)}` : ""}
  </div>
`;

const chatPane = html`
  <div class="panel">
    <div class="panel-head">
      <div><strong>Guest chat</strong> — posts signed and forwarded to /webhooks/guesty/message-received</div>
      <div class="count">${() => state.messages.length} posts</div>
    </div>
    <div class="chat">
      ${() => state.messages.length === 0
        ? html`<div class="empty">No messages yet. Type something or pick a preset below.</div>`
        : state.messages.map(bubble)}
      ${() => {
        const lastGuest = [...state.messages].reverse().find(m => m.role === "guest");
        if (!lastGuest) return "";
        const esc = state.escalations.find(e =>
          e.conversation_key &&
          e.conversation_key.toString().includes(document.getElementById("conv-id").value.trim())
        );
        if (!esc) return "";
        // Only show the newest escalation inline if it arrived AFTER the
        // last guest post; older ones belong in the inbox on the right.
        if (esc.created_at && lastGuest.at && esc.created_at < lastGuest.at) return "";
        return inlineEscalation(esc);
      }}
    </div>
    <div class="presets">
      ${() => presets.map(p => html`
        <button onclick=${() => sendMessage(p)}>${() => p.length > 40 ? p.slice(0, 37) + "…" : p}</button>
      `)}
    </div>
    <div class="composer">
      <textarea id="composer-input" placeholder="Type a guest message…" rows="2"
        onkeydown=${(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            const el = document.getElementById("composer-input");
            sendMessage(el.value);
            el.value = "";
          }
        }}></textarea>
      <button ?disabled=${() => state.sending} onclick=${() => {
        const el = document.getElementById("composer-input");
        sendMessage(el.value);
        el.value = "";
      }}>${() => state.sending ? "…" : "Send"}</button>
    </div>
    ${() => state.error
      ? html`<div style="padding:8px 16px;color:var(--danger);font-size:13px;">${() => state.error}</div>`
      : ""}
  </div>
`;

const inboxPane = html`
  <div class="panel">
    <div class="panel-head">
      <div><strong>Escalation inbox</strong> — what a human reviewer picks up</div>
      <div class="count">${() => state.escalations.length} open</div>
    </div>
    <div class="inbox">
      ${() => state.escalations.length === 0
        ? html`<div class="empty">No escalations yet. Try a "discount" or "pet" message to trigger one.</div>`
        : state.escalations.map(e => html`
          <div class="esc-card">
            <div class="row">
              <span class="reason">${() => e.reason || "unknown"}</span>
              ${() => e.primary_code ? html`<span class="code">${() => e.primary_code}</span>` : ""}
              ${() => e.platform ? html`<span class="code">${() => e.platform}</span>` : ""}
            </div>
            ${() => e.detail ? html`<div class="detail">${() => formatDetail(e.detail)}</div>` : ""}
            ${() => e.guest_message ? html`<div class="body">${() => e.guest_message}</div>` : ""}
            <div class="timestamp">
              conv: ${() => e.conversation_key || "—"}
              ${() => e.post_id ? ` · post: ${e.post_id}` : ""}
              ${() => e.created_at ? ` · ${fmtTime(e.created_at)}` : ""}
            </div>
          </div>
        `)}
    </div>
  </div>
`;

html`${() => chatPane}${() => inboxPane}`(document.getElementById("app"));
