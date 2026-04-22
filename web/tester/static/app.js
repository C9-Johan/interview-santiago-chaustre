// InquiryIQ tester UI.
// Two tabs backed by a single reactive state object:
//   Customer — play the guest, watch the bot reply or escalate inline.
//   Agent    — triage queue of escalations, with a detail pane that shows
//              the full conversation timeline so a human can take over.
//
// Arrow-js is ~3KB, no build step. reactive() proxies a plain object;
// html`...` templates track which proxy keys a template reads and patch
// only the DOM nodes that depend on them when those keys change.

import { reactive, html } from "https://esm.sh/@arrow-js/core@1";

const state = reactive({
  tab: "customer",
  messages: [],           // entries in the active guest conversation
  escalations: [],        // escalation queue (sorted newest-first)
  selectedEscID: null,    // currently focused escalation id (Agent view)
  selectedTimeline: [],   // conversation history for the selected escalation
  sending: false,
  error: null,
  lastStatus: null,
});

const presets = [
  "Hi! Is the Soho 2BR available Fri April 24 – Sun April 26 for 4 adults?",
  "Can I check in at 10pm? My flight lands late.",
  "Is parking included or extra?",
  "Any discount for a 5-night stay?",
  "Can I bring my dog? He's small and well-behaved.",
  "Hi",
];

// ---------- Form field accessors ----------

function formFields() {
  return {
    conversation_id: document.getElementById("conv-id").value.trim(),
    reservation_id:  document.getElementById("res-id").value.trim(),
    guest_name:      document.getElementById("guest-name").value.trim(),
    platform:        document.getElementById("platform").value,
  };
}

// ---------- API wrappers ----------

async function sendMessage(body) {
  if (state.sending) return;
  const trimmed = (body || "").trim();
  if (!trimmed) return;
  state.sending = true;
  state.error = null;
  try {
    const res = await fetch("/api/send", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...formFields(), body: trimmed }),
    });
    const data = await res.json().catch(() => ({}));
    state.lastStatus = data.service_status;
    if (!res.ok) state.error = data.error || `HTTP ${res.status}`;
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
  } catch (_) {}
}

// The service serializes Go structs with default field names. We flatten
// to snake_case so the view layer stays simple.
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
    secondary_code: raw.Classification?.SecondaryCode || raw.secondary_code,
    confidence: raw.Classification?.Confidence ?? raw.confidence,
    guest_message: raw.Reply?.Body || raw.guest_message || (raw.Turn?.Posts?.[raw.Turn?.Posts?.length-1]?.Body),
  };
}

async function refreshEscalations() {
  try {
    const res = await fetch("/api/escalations");
    if (!res.ok) return;
    const data = await res.json();
    const raw = Array.isArray(data) ? data : data.escalations || [];
    const seen = new Set();
    const items = raw
      .map(normalizeEscalation)
      .sort((a, b) => (b.created_at || "").localeCompare(a.created_at || ""))
      .filter(e => {
        const key = e.post_id || e.id;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
    state.escalations = items;
    if (state.selectedEscID) {
      const sel = items.find(e => e.id === state.selectedEscID);
      if (sel && sel.conversation_key) refreshTimelineFor(sel.conversation_key);
    }
  } catch (_) {}
}

async function refreshTimelineFor(convKey) {
  if (!convKey) return;
  try {
    const res = await fetch(`/api/conversations/${encodeURIComponent(convKey)}`);
    if (!res.ok) return;
    const data = await res.json();
    state.selectedTimeline = data.entries || [];
  } catch (_) {}
}

function selectEscalation(esc) {
  state.selectedEscID = esc.id;
  state.selectedTimeline = [];
  refreshTimelineFor(esc.conversation_key);
}

// ---------- Tab switching ----------

function selectTab(name) {
  state.tab = name;
  document.querySelectorAll(".tab").forEach(b => {
    const on = b.dataset.tab === name;
    b.classList.toggle("active", on);
    b.setAttribute("aria-selected", on ? "true" : "false");
  });
  document.querySelectorAll(".view").forEach(v => {
    const on = v.id === `view-${name}`;
    v.classList.toggle("active", on);
    if (on) v.removeAttribute("hidden"); else v.setAttribute("hidden", "");
  });
}

document.querySelectorAll(".tab").forEach(b => {
  b.addEventListener("click", () => selectTab(b.dataset.tab));
});

// Live badge on the Agent tab — count of escalations awaiting review.
function updateBadge() {
  const badge = document.getElementById("agent-badge");
  if (!badge) return;
  const n = state.escalations.length;
  badge.textContent = String(n);
  badge.classList.toggle("hidden", n === 0);
}

// ---------- Formatters ----------

const fmtTime = iso => {
  if (!iso) return "";
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const fmtRelative = iso => {
  if (!iso) return "";
  const diff = (Date.now() - new Date(iso).getTime()) / 1000;
  if (diff < 60)       return `${Math.floor(diff)}s ago`;
  if (diff < 3600)     return `${Math.floor(diff/60)}m ago`;
  if (diff < 86400)    return `${Math.floor(diff/3600)}h ago`;
  return `${Math.floor(diff/86400)}d ago`;
};

const reasonLabel = r => ({
  code_requires_human:     "Code requires human",
  confidence_below_floor:  "Low confidence",
  risk_flag:               "Risk flag raised",
  restricted_content:      "Restricted content",
  prompt_injection_suspected: "Possible prompt injection",
  classifier_failed:       "Classifier failed",
  generator_failed:        "Reply generation failed",
  empty_body:              "Empty message",
  auto_response_disabled:  "Auto-response off",
}[r] || (r || "Escalated"));

const formatDetail = d => {
  if (!d) return "";
  return Array.isArray(d) ? d.join("\n") : String(d);
};

// ---------- Customer view template ----------

const bubble = entry => html`
  <div class="bubble ${() => entry.role}">
    ${() => entry.body}
    <span class="meta">${() => `${entry.role} · ${fmtTime(entry.at)}`}</span>
  </div>
`;

const inlineEscalation = e => html`
  <div class="bubble escalate">
    <strong>Escalated to human</strong> — ${() => reasonLabel(e.reason)}
    ${() => e.primary_code ? html` · <code>${() => e.primary_code}</code>` : ""}
    ${() => e.detail ? html` · ${() => formatDetail(e.detail).split("\n")[0]}` : ""}
  </div>
`;

const customerView = html`
  <div class="customer-wrap">
    <div class="chat-card">
      <div class="chat-header">
        <div class="title">
          <div class="avatar">${() => (document.getElementById("guest-name")?.value || "G").slice(0,1).toUpperCase()}</div>
          <div>
            <div>${() => document.getElementById("guest-name")?.value || "Guest"}</div>
            <div class="status">agent online</div>
          </div>
        </div>
        <span class="count">${() => state.messages.length} messages</span>
      </div>
      <div class="chat-body">
        ${() => state.messages.length === 0
          ? html`<div class="empty">
              <strong>Say hi to the bot 👋</strong>
              Type below or pick a preset. Every message is signed and posted
              to the real <code>/webhooks/guesty/message-received</code> endpoint.
            </div>`
          : state.messages.map(bubble)}
        ${() => {
          const lastGuest = [...state.messages].reverse().find(m => m.role === "guest");
          if (!lastGuest) return "";
          const convID = document.getElementById("conv-id")?.value?.trim() || "";
          const esc = state.escalations.find(e =>
            e.conversation_key && e.conversation_key.toString().includes(convID));
          if (!esc) return "";
          if (esc.created_at && lastGuest.at && esc.created_at < lastGuest.at) return "";
          return inlineEscalation(esc);
        }}
      </div>
      <div class="presets">
        ${() => presets.map(p => html`
          <button onclick=${() => sendMessage(p)}>${() => p.length > 40 ? p.slice(0,37) + "…" : p}</button>
        `)}
      </div>
      <div class="composer">
        <textarea id="composer-input" placeholder="Type a guest message…" rows="2"
          onkeydown=${e => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              const el = document.getElementById("composer-input");
              sendMessage(el.value);
              el.value = "";
            }
          }}></textarea>
        <button class="send-btn" ?disabled=${() => state.sending} onclick=${() => {
          const el = document.getElementById("composer-input");
          sendMessage(el.value);
          el.value = "";
        }}>${() => state.sending ? "Sending…" : "Send"}</button>
      </div>
      ${() => state.error ? html`<div class="error-banner">${() => state.error}</div>` : ""}
    </div>
  </div>
`;

// ---------- Agent view template ----------

const escRow = e => html`
  <div class="esc-row ${() => state.selectedEscID === e.id ? "selected" : ""}"
       onclick=${() => selectEscalation(e)}>
    <div class="row-top">
      <span class="row-reason">${() => reasonLabel(e.reason)}</span>
      <span class="row-time">${() => fmtRelative(e.created_at)}</span>
    </div>
    ${() => e.guest_message ? html`<div class="row-preview">${() => e.guest_message}</div>` : ""}
    <div class="row-badges">
      ${() => e.primary_code ? html`<span class="chip code">${() => e.primary_code}</span>` : ""}
      ${() => e.platform ? html`<span class="chip">${() => e.platform}</span>` : ""}
      ${() => (typeof e.confidence === "number")
        ? html`<span class="chip">conf ${() => e.confidence.toFixed(2)}</span>`
        : ""}
    </div>
  </div>
`;

const timelineItem = t => html`
  <div class="tl-item ${() => t.role}">
    <div class="tl-role">${() => t.role}</div>
    <div>
      <div class="tl-body">${() => t.body}</div>
      <div class="tl-time">${() => fmtTime(t.at)}</div>
    </div>
  </div>
`;

const agentDetail = () => {
  const sel = state.escalations.find(e => e.id === state.selectedEscID);
  if (!sel) {
    return html`<div class="detail-empty">
      <span class="emoji">📥</span>
      <strong>Select an escalation</strong>
      <p>Pick an item from the queue to see the full conversation and take over.</p>
    </div>`;
  }
  return html`
    <div class="reason-banner">
      <div class="icon">!</div>
      <div class="txt">
        <strong>${() => reasonLabel(sel.reason)}</strong>
        <span>
          ${() => sel.primary_code ? `Code ${sel.primary_code}` : ""}
          ${() => (typeof sel.confidence === "number") ? ` · confidence ${sel.confidence.toFixed(2)}` : ""}
          ${() => sel.created_at ? ` · ${fmtRelative(sel.created_at)}` : ""}
        </span>
      </div>
    </div>

    ${() => sel.detail ? html`
      <div class="detail-section">
        <h3>Bot reasoning</h3>
        <div class="block mono">${() => formatDetail(sel.detail)}</div>
      </div>
    ` : ""}

    <div class="detail-section">
      <h3>Context</h3>
      <dl class="kv">
        <dt>Guest</dt><dd>${() => sel.guest_name || "—"}</dd>
        <dt>Conversation</dt><dd class="mono">${() => sel.conversation_key || "—"}</dd>
        <dt>Platform</dt><dd>${() => sel.platform || "—"}</dd>
        <dt>Last post</dt><dd class="mono">${() => sel.post_id || "—"}</dd>
      </dl>
    </div>

    <div class="detail-section">
      <h3>Conversation timeline</h3>
      ${() => state.selectedTimeline.length === 0
        ? html`<div class="block">No recorded messages yet — this escalation
                  fired on the first signed webhook.</div>`
        : html`<div class="timeline">${() => state.selectedTimeline.map(timelineItem)}</div>`}
    </div>
  `;
};

const agentView = html`
  <div class="agent-wrap">
    <div class="agent-card">
      <div class="agent-card-head">
        <h2>Triage queue</h2>
        <span class="pill">${() => state.escalations.length} open</span>
      </div>
      <div class="esc-list">
        ${() => state.escalations.length === 0
          ? html`<div class="empty">
              <span class="emoji">🎉</span>
              Nothing to review. The bot is handling every turn on its own.
            </div>`
          : state.escalations.map(escRow)}
      </div>
    </div>
    <div class="agent-card">
      <div class="agent-card-head">
        <h2>Detail</h2>
      </div>
      <div class="detail-body">${() => agentDetail()}</div>
    </div>
  </div>
`;

// ---------- Wire templates + poll ----------

customerView(document.getElementById("view-customer"));
agentView(document.getElementById("view-agent"));

setInterval(refreshConversation, 1500);
setInterval(refreshEscalations, 1500);
setInterval(updateBadge, 1500);
refreshConversation();
refreshEscalations();
updateBadge();
