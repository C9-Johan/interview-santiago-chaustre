// InquiryIQ tester UI.
// Three tabs backed by a single reactive state object:
//   Customer — play the guest, watch the bot reply or escalate inline,
//              expand a "Why?" panel under each turn to see the classifier's
//              reasoning, extracted entities, tool calls + responses, and
//              the C.L.O.S.E.R. beats the generator hit.
//   Agent    — triage queue of escalations with a full conversation timeline.
//   Traces   — per-turn audit log across every conversation the tester has
//              seen in this session. Helpful when debugging a single bad
//              classification without hunting through scroll history.
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
  turnDetails: {},        // keyed by post_id → { classification, reply, escalation }
  openPanels: {},         // keyed by post_id → bool (expanded turn detail)
  bannerDismissed: false, // operator dismissed the "escalated, keep typing" banner
});

const presets = [
  "Hi! Is the Soho 2BR available Fri April 24 – Sun April 26 for 4 adults?",
  "Can I check in at 10pm? My flight lands late.",
  "Is parking included or extra?",
  "Any discount for a 5-night stay?",
  "Can I bring my dog? He's small and well-behaved.",
  "Hi",
];

// ---------- localStorage persistence for toolbar inputs ----------

const STORAGE_KEY = "inquiryiq.tester.form.v1";

function loadFormFromStorage() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return;
    const saved = JSON.parse(raw);
    const apply = (id, v) => {
      if (typeof v !== "string") return;
      const el = document.getElementById(id);
      if (el) el.value = v;
    };
    apply("guest-name", saved.guest_name);
    apply("conv-id",    saved.conversation_id);
    apply("res-id",     saved.reservation_id);
    apply("platform",   saved.platform);
  } catch (_) {}
}

function saveFormToStorage() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(formFields()));
  } catch (_) {}
}

// Bind change/input listeners so a typed value survives reload — browser
// input history is not reliable across container rebuilds.
function wireFormPersistence() {
  ["guest-name", "conv-id", "res-id", "platform"].forEach(id => {
    const el = document.getElementById(id);
    if (!el) return;
    el.addEventListener("input",  saveFormToStorage);
    el.addEventListener("change", saveFormToStorage);
  });
}

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
  state.bannerDismissed = false; // new turn → re-arm the banner
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
  const convID = document.getElementById("conv-id")?.value?.trim();
  if (!convID) return;
  try {
    const res = await fetch(`/api/conversations/${encodeURIComponent(convID)}`);
    if (!res.ok) return;
    const data = await res.json();
    const entries = data.entries || [];
    state.messages = entries;
    // Opportunistically fetch turn-details for every guest message we have
    // a post_id for — hydrates the "Why?" panels without waiting for the
    // operator to expand each one.
    entries.forEach(e => {
      if (e.post_id && !state.turnDetails[e.post_id]) {
        refreshTurnDetails(e.post_id);
      }
    });
  } catch (_) {}
}

// The service serializes Go structs with default field names. We flatten
// the commonly-referenced fields into snake_case and keep the full
// Classification/Reply payloads so the detail panel can render reasoning,
// entities, tool calls, and the bot's attempted reply without a second
// round-trip.
function normalizeEscalation(raw) {
  const cls = raw.Classification || raw.classification || null;
  const rep = raw.Reply || raw.reply || null;
  return {
    id: raw.ID || raw.id,
    reason: raw.Reason || raw.reason,
    detail: raw.Detail || raw.detail,
    post_id: raw.PostID || raw.post_id,
    conversation_key: raw.ConversationKey || raw.conversation_key,
    guest_name: raw.GuestName || raw.guest_name,
    platform: raw.Platform || raw.platform,
    created_at: raw.CreatedAt || raw.created_at,
    primary_code: cls?.PrimaryCode || raw.primary_code,
    secondary_code: cls?.SecondaryCode || raw.secondary_code,
    confidence: cls?.Confidence ?? raw.confidence,
    classification: cls,
    reply: rep,
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

// refreshTurnDetails fetches /admin/turn/{post_id} through the tester proxy
// and caches the result keyed by post_id. The service returns null values
// when classification or reply are still pending — we keep polling those
// via the caller's setInterval so the panel updates live without the
// operator having to re-open it.
async function refreshTurnDetails(postID) {
  if (!postID) return;
  try {
    const res = await fetch(`/api/turn-details/${encodeURIComponent(postID)}`);
    if (res.status === 503) {
      // Admin token not configured — surface once in the panel so the
      // operator knows why no details are showing up.
      state.turnDetails[postID] = { _disabled: true };
      return;
    }
    if (!res.ok) return;
    const data = await res.json();
    state.turnDetails[postID] = data;
  } catch (_) {}
}

// resetDemo wipes service-side and tester-side state so the next turn
// starts fresh. The button is visible in the header toolbar; we confirm
// before firing because this is destructive (drops conversation memory,
// escalation queue, classifications, replies, idempotency claims, and
// the tester's local logs).
async function resetDemo() {
  if (!confirm("Reset demo state?\n\nThis wipes conversations, escalations, memory, classifications, replies and idempotency on the service, plus the tester's local chat + tool-call log.")) {
    return;
  }
  const btn = document.getElementById("reset-btn");
  if (btn) { btn.disabled = true; btn.textContent = "Resetting…"; }
  try {
    const res = await fetch("/api/reset", { method: "POST" });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      state.error = data.error || `reset failed (HTTP ${res.status})`;
      return;
    }
    state.messages = [];
    state.escalations = [];
    state.selectedEscID = null;
    state.selectedTimeline = [];
    state.turnDetails = {};
    state.openPanels = {};
    state.bannerDismissed = false;
    state.error = null;
    state.lastStatus = null;
    refreshConversation();
    refreshEscalations();
  } catch (e) {
    state.error = e.message;
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = "Reset demo"; }
  }
}

// Periodically re-poll any turn whose classification or reply is still
// missing — the service's pipeline is async and the first fetch often
// beats the pipeline.
function repollPendingTurns() {
  const pending = Object.entries(state.turnDetails).filter(([_, d]) => {
    if (!d || d._disabled) return false;
    return !d.classification || !d.reply;
  });
  pending.forEach(([postID]) => refreshTurnDetails(postID));
}

function selectEscalation(esc) {
  state.selectedEscID = esc.id;
  state.selectedTimeline = [];
  refreshTimelineFor(esc.conversation_key);
}

function togglePanel(postID) {
  state.openPanels[postID] = !state.openPanels[postID];
  // Force a refresh on open so stale "missing" panels hydrate immediately.
  if (state.openPanels[postID]) refreshTurnDetails(postID);
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

document.getElementById("reset-btn")?.addEventListener("click", resetDemo);

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

// Pretty-print a JSON-ish value for tool call Arguments/Result. The service
// stores these as raw JSON (possibly a string when the model hands back
// text); we attempt to parse-and-reformat so the panel reads cleanly.
function fmtJSON(v) {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") {
    try { return JSON.stringify(JSON.parse(v), null, 2); } catch (_) { return v; }
  }
  try { return JSON.stringify(v, null, 2); } catch (_) { return String(v); }
}

// Build a compact list of "Entity: value" pairs from the classifier's
// extracted_entities payload. Go's default JSON encoding uses PascalCase
// since the Classification struct has no JSON tags.
function entityRows(ent) {
  if (!ent) return [];
  const out = [];
  const push = (k, v) => { if (v !== null && v !== undefined && v !== "") out.push([k, v]); };
  if (ent.CheckIn)     push("Check-in",  new Date(ent.CheckIn).toLocaleString());
  if (ent.CheckOut)    push("Check-out", new Date(ent.CheckOut).toLocaleString());
  if (ent.GuestCount != null) push("Guests", String(ent.GuestCount));
  if (ent.Pets != null)       push("Pets",   ent.Pets ? "yes" : "no");
  if (ent.Vehicles != null)   push("Vehicles", String(ent.Vehicles));
  if (ent.ListingHint)        push("Listing", ent.ListingHint);
  (ent.Additional || []).forEach(o => push(o.Label || o.Key || "note", o.Value || ""));
  return out;
}

// ---------- Turn detail panel ----------

const turnPanel = postID => html`
  <div class="turn-panel">
    ${() => {
      const d = state.turnDetails[postID];
      if (!d)            return html`<div class="tp-loading">Loading classifier output…</div>`;
      if (d._disabled)   return html`<div class="tp-disabled">Turn details unavailable (ADMIN_TOKEN not configured on the tester).</div>`;
      const cls  = d.classification;
      const rep  = d.reply;
      const esc  = d.escalation;
      return html`
        ${() => cls ? classificationBlock(cls) : html`<div class="tp-loading">Classifier still running…</div>`}
        ${() => rep ? replyBlock(rep) : ""}
        ${() => esc ? escalationBlock(esc) : ""}
      `;
    }}
  </div>
`;

const classificationBlock = cls => html`
  <div class="tp-section">
    <div class="tp-section-head">Classification</div>
    <div class="tp-chips">
      <span class="chip code">${() => cls.PrimaryCode || "?"}</span>
      ${() => cls.SecondaryCode ? html`<span class="chip">${() => cls.SecondaryCode}</span>` : ""}
      <span class="chip">conf ${() => (cls.Confidence ?? 0).toFixed(2)}</span>
      ${() => cls.RiskFlag ? html`<span class="chip danger">risk</span>` : ""}
      ${() => cls.NextAction ? html`<span class="chip">${() => cls.NextAction}</span>` : ""}
    </div>
    ${() => cls.Reasoning ? html`
      <div class="tp-kv">
        <div class="tp-k">Reasoning</div>
        <div class="tp-v">${() => cls.Reasoning}</div>
      </div>` : ""}
    ${() => cls.RiskReason ? html`
      <div class="tp-kv">
        <div class="tp-k">Risk reason</div>
        <div class="tp-v">${() => cls.RiskReason}</div>
      </div>` : ""}
    ${() => {
      const rows = entityRows(cls.ExtractedEntities);
      if (rows.length === 0) return "";
      return html`
        <div class="tp-kv">
          <div class="tp-k">Entities</div>
          <div class="tp-v">
            <div class="tp-entity-grid">
              ${() => rows.map(([k, v]) => html`
                <div class="tp-ent-k">${() => k}</div>
                <div class="tp-ent-v">${() => v}</div>
              `)}
            </div>
          </div>
        </div>
      `;
    }}
  </div>
`;

const replyBlock = rep => html`
  <div class="tp-section">
    <div class="tp-section-head">Generator</div>
    <div class="tp-chips">
      ${() => (typeof rep.confidence === "number") ? html`<span class="chip">conf ${() => rep.confidence.toFixed(2)}</span>` : ""}
      ${() => rep.abort_reason ? html`<span class="chip danger">abort: ${() => rep.abort_reason}</span>` : ""}
      ${() => closerChips(rep.closer_beats)}
    </div>
    ${() => (rep.used_tools && rep.used_tools.length) ? html`
      <div class="tp-kv">
        <div class="tp-k">Tools used</div>
        <div class="tp-v">
          <div class="tp-tools">
            ${() => rep.used_tools.map(t => html`
              <details class="tp-tool">
                <summary>
                  <span class="tp-tool-name">${() => t.name || "tool"}</span>
                  <span class="tp-tool-latency">${() => (t.latency_ms != null ? t.latency_ms + "ms" : "")}</span>
                  ${() => t.error ? html`<span class="chip danger">error</span>` : ""}
                </summary>
                <div class="tp-tool-body">
                  <div class="tp-tool-sub">Arguments</div>
                  <pre class="tp-code">${() => fmtJSON(t.arguments)}</pre>
                  <div class="tp-tool-sub">Result</div>
                  <pre class="tp-code">${() => t.error ? t.error : fmtJSON(t.result)}</pre>
                </div>
              </details>
            `)}
          </div>
        </div>
      </div>
    ` : ""}
    ${() => (rep.missing_info && rep.missing_info.length) ? html`
      <div class="tp-kv">
        <div class="tp-k">Missing info</div>
        <div class="tp-v">${() => rep.missing_info.join(", ")}</div>
      </div>` : ""}
    ${() => rep.reflection_reason ? html`
      <div class="tp-kv">
        <div class="tp-k">Reflection</div>
        <div class="tp-v">${() => rep.reflection_reason}</div>
      </div>` : ""}
  </div>
`;

const closerChips = beats => {
  if (!beats) return "";
  const labels = [
    ["clarify", "C"], ["label", "L"], ["overview", "O"],
    ["sell_certainty", "S"], ["explain", "E"], ["request", "R"],
  ];
  return html`
    <span class="tp-closer">
      ${() => labels.map(([k, l]) => html`
        <span class="${() => "tp-beat " + (beats[k] ? "on" : "off")}">${() => l}</span>
      `)}
    </span>
  `;
};

const escalationBlock = esc => html`
  <div class="tp-section tp-esc">
    <div class="tp-section-head">Escalation</div>
    <div class="tp-chips">
      <span class="chip danger">${() => reasonLabel(esc.Reason || esc.reason)}</span>
    </div>
    ${() => {
      const det = esc.Detail || esc.detail;
      return det ? html`<pre class="tp-code">${() => formatDetail(det)}</pre>` : "";
    }}
  </div>
`;

// ---------- Customer view template ----------

// bubble renders one chat row. Guest bubbles expose a "Why?" toggle that
// pulls up the full turn detail (classification, reply, tool calls,
// escalation) for the operator to inspect without leaving the chat.
const bubble = entry => html`
  <div class="${() => "bubble-wrap " + entry.role}">
    <div class="${() => "bubble " + entry.role}">
      ${() => entry.body}
      <span class="meta">
        ${() => `${entry.role} · ${fmtTime(entry.at)}`}
        ${() => entry.post_id ? html`
          <button class="why-btn"
                  @click="${() => togglePanel(entry.post_id)}">
            ${() => state.openPanels[entry.post_id] ? "hide why" : "why?"}
          </button>` : ""}
      </span>
    </div>
    ${() => (entry.post_id && state.openPanels[entry.post_id]) ? turnPanel(entry.post_id) : ""}
  </div>
`;

const inlineEscalation = e => html`
  <div class="bubble escalate">
    <strong>Escalated to human</strong> — ${() => reasonLabel(e.reason)}
    ${() => e.primary_code ? html` · <code>${() => e.primary_code}</code>` : ""}
    ${() => e.detail ? html` · ${() => formatDetail(e.detail).split("\n")[0]}` : ""}
  </div>
`;

// continueBanner — shown after an escalation so the operator knows the
// composer is still live. Dismissable; re-armed on every new outbound
// message (so the banner returns the next time the pipeline escalates).
const continueBanner = () => html`
  <div class="continue-banner">
    <div class="cb-icon">ℹ</div>
    <div class="cb-body">
      <strong>Escalated — an agent will follow up.</strong>
      <span>You can keep typing below; a new guest message starts a fresh turn and re-runs the classifier.</span>
    </div>
    <button class="cb-dismiss" @click="${() => { state.bannerDismissed = true; }}">dismiss</button>
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
      ${() => {
        if (state.bannerDismissed) return "";
        const lastGuest = [...state.messages].reverse().find(m => m.role === "guest");
        if (!lastGuest) return "";
        const convID = document.getElementById("conv-id")?.value?.trim() || "";
        const esc = state.escalations.find(e =>
          e.conversation_key && e.conversation_key.toString().includes(convID));
        if (!esc) return "";
        if (esc.created_at && lastGuest.at && esc.created_at < lastGuest.at) return "";
        return continueBanner();
      }}
      <div class="presets">
        ${() => presets.map(p => html`
          <button @click="${() => sendMessage(p)}">${() => p.length > 40 ? p.slice(0,37) + "…" : p}</button>
        `)}
      </div>
      <div class="composer">
        <textarea id="composer-input" placeholder="Type a guest message…" rows="2"
          @keydown="${e => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              const el = document.getElementById("composer-input");
              sendMessage(el.value);
              el.value = "";
            }
          }}"></textarea>
        <button class="send-btn" @click="${() => {
          const el = document.getElementById("composer-input");
          sendMessage(el.value);
          el.value = "";
        }}">${() => state.sending ? "Sending…" : "Send"}</button>
      </div>
      ${() => state.error ? html`<div class="error-banner">${() => state.error}</div>` : ""}
    </div>
  </div>
`;

// ---------- Agent view template ----------

// escalationStatus classifies the current state of an escalation based on
// the conversation timeline: "awaiting" means the bot's escalation is the
// latest event in the conversation (needs a human); "followed_up" means
// there is a later bot or note entry (the turn has moved on).
function escalationStatus(esc, timeline) {
  if (!esc || !esc.created_at) return "awaiting";
  const later = (timeline || []).some(t => t.at && t.at > esc.created_at);
  return later ? "followed_up" : "awaiting";
}

const statusPill = status => {
  if (status === "followed_up") {
    return html`<span class="chip ok">followed up</span>`;
  }
  return html`<span class="chip danger">awaiting agent</span>`;
};

const escRow = e => html`
  <div class="${() => 'esc-row' + (state.selectedEscID === e.id ? ' selected' : '')}"
       @click="${() => selectEscalation(e)}">
    <div class="row-top">
      <span class="row-reason">${() => reasonLabel(e.reason)}</span>
      <span class="row-time">${() => fmtRelative(e.created_at)}</span>
    </div>
    ${() => e.classification?.Reasoning
      ? html`<div class="row-preview">${() => e.classification.Reasoning}</div>`
      : (e.reply?.body ? html`<div class="row-preview">Bot wanted to say: ${() => e.reply.body}</div>` : "")}
    <div class="row-badges">
      ${() => statusPill(escalationStatus(e, state.selectedEscID === e.id ? state.selectedTimeline : []))}
      ${() => e.primary_code ? html`<span class="chip code">${() => e.primary_code}</span>` : ""}
      ${() => e.platform ? html`<span class="chip">${() => e.platform}</span>` : ""}
      ${() => (typeof e.confidence === "number")
        ? html`<span class="chip">conf ${() => e.confidence.toFixed(2)}</span>`
        : ""}
    </div>
  </div>
`;

const timelineItem = t => html`
  <div class="${() => 'tl-item ' + t.role}">
    <div class="tl-role">${() => t.role}</div>
    <div>
      <div class="tl-body">${() => t.body}</div>
      <div class="tl-time">${() => fmtTime(t.at)}</div>
    </div>
  </div>
`;

// guestMessageFor locates the guest post that triggered the escalation by
// matching post_id in the conversation timeline. Falls back to the latest
// guest entry when the escalation's post_id doesn't line up (e.g. the
// tester's in-memory log was reset).
function guestMessageFor(sel, timeline) {
  if (!sel || !timeline) return null;
  if (sel.post_id) {
    const byId = timeline.find(t => t.role === "guest" && t.post_id === sel.post_id);
    if (byId) return byId;
  }
  return [...timeline].reverse().find(t => t.role === "guest") || null;
}

const agentDetail = () => {
  const sel = state.escalations.find(e => e.id === state.selectedEscID);
  if (!sel) {
    return html`<div class="detail-empty">
      <span class="emoji">📥</span>
      <strong>Select an escalation</strong>
      <p>Pick an item from the queue to see the full conversation and take over.</p>
    </div>`;
  }
  const status    = escalationStatus(sel, state.selectedTimeline);
  const guestMsg  = guestMessageFor(sel, state.selectedTimeline);
  const cls       = sel.classification;
  const rep       = sel.reply;
  // Timeline entries that happened AFTER the escalation fired — these are
  // the host follow-ups, new guest turns, or bot replies on a later turn.
  const followups = (state.selectedTimeline || []).filter(t =>
    t.at && sel.created_at && t.at > sel.created_at
  );

  return html`
    <div class="${() => "status-banner " + (status === "followed_up" ? "ok" : "awaiting")}">
      <div class="sb-left">
        <div class="sb-title">
          <strong>${() => reasonLabel(sel.reason)}</strong>
          ${() => statusPill(status)}
        </div>
        <div class="sb-meta">
          ${() => sel.guest_name || "Guest"}
          ${() => sel.platform ? ` · ${sel.platform}` : ""}
          ${() => sel.created_at ? ` · ${fmtRelative(sel.created_at)}` : ""}
        </div>
      </div>
      ${() => (status === "awaiting" && rep?.body) ? html`
        <button class="copy-reply"
                @click="${() => navigator.clipboard?.writeText(rep.body)}">
          copy bot draft
        </button>` : ""}
    </div>

    ${() => guestMsg ? html`
      <div class="detail-section">
        <h3>Guest message</h3>
        <div class="guest-msg">
          <div class="gm-body">${() => guestMsg.body}</div>
          <div class="gm-meta">
            ${() => fmtTime(guestMsg.at)}
            ${() => sel.post_id ? html` · <code>${() => sel.post_id}</code>` : ""}
          </div>
        </div>
      </div>` : ""}

    ${() => cls ? html`
      <div class="detail-section">
        <h3>Classifier verdict</h3>
        <div class="verdict-card">
          <div class="tp-chips">
            <span class="chip code">${() => cls.PrimaryCode || "?"}</span>
            ${() => cls.SecondaryCode ? html`<span class="chip">${() => cls.SecondaryCode}</span>` : ""}
            <span class="chip">conf ${() => (cls.Confidence ?? 0).toFixed(2)}</span>
            ${() => cls.RiskFlag ? html`<span class="chip danger">risk</span>` : ""}
            ${() => cls.NextAction ? html`<span class="chip">${() => cls.NextAction}</span>` : ""}
          </div>
          ${() => cls.Reasoning ? html`
            <div class="reasoning">${() => cls.Reasoning}</div>` : ""}
          ${() => cls.RiskReason ? html`
            <div class="risk-reason">⚠ ${() => cls.RiskReason}</div>` : ""}
          ${() => {
            const rows = entityRows(cls.ExtractedEntities);
            if (rows.length === 0) return "";
            return html`<div class="entity-card">
              ${() => rows.map(([k, v]) => html`
                <div class="ec-k">${() => k}</div>
                <div class="ec-v">${() => v}</div>
              `)}
            </div>`;
          }}
        </div>
      </div>` : html`
      <div class="detail-section">
        <h3>Classifier verdict</h3>
        <div class="block">No classification recorded — the webhook escalated
          before the classifier produced a verdict.</div>
      </div>`}

    ${() => rep ? html`
      <div class="detail-section">
        <h3>Bot's drafted reply</h3>
        <div class="draft-card">
          <div class="draft-body">${() => rep.body || "(no body — generator aborted)"}</div>
          <div class="tp-chips" style="margin-top:10px">
            ${() => (typeof rep.confidence === "number")
              ? html`<span class="chip">conf ${() => rep.confidence.toFixed(2)}</span>` : ""}
            ${() => rep.abort_reason
              ? html`<span class="chip danger">abort: ${() => rep.abort_reason}</span>` : ""}
            ${() => closerChips(rep.closer_beats)}
          </div>
          ${() => (rep.used_tools && rep.used_tools.length) ? html`
            <div class="tools-card">
              <div class="tc-head">Tools used</div>
              ${() => rep.used_tools.map(t => html`
                <details class="tp-tool">
                  <summary>
                    <span class="tp-tool-name">${() => t.name || "tool"}</span>
                    <span class="tp-tool-latency">${() => (t.latency_ms != null ? t.latency_ms + "ms" : "")}</span>
                    ${() => t.error ? html`<span class="chip danger">error</span>` : ""}
                  </summary>
                  <div class="tp-tool-body">
                    <div class="tp-tool-sub">Arguments</div>
                    <pre class="tp-code">${() => fmtJSON(t.arguments)}</pre>
                    <div class="tp-tool-sub">Result</div>
                    <pre class="tp-code">${() => t.error ? t.error : fmtJSON(t.result)}</pre>
                  </div>
                </details>
              `)}
            </div>` : ""}
          ${() => (rep.missing_info && rep.missing_info.length) ? html`
            <div class="draft-missing">
              <strong>Missing info:</strong> ${() => rep.missing_info.join(", ")}
            </div>` : ""}
        </div>
      </div>` : html`
      <div class="detail-section">
        <h3>Bot's drafted reply</h3>
        <div class="block">No reply generated — escalated before the generator
          stage ran (${() => reasonLabel(sel.reason)}).</div>
      </div>`}

    ${() => sel.detail ? html`
      <div class="detail-section">
        <h3>Why it escalated</h3>
        <div class="block mono">${() => formatDetail(sel.detail)}</div>
      </div>` : ""}

    <div class="detail-section">
      <h3>${() => status === "followed_up" ? "Follow-up activity" : "Awaiting agent response"}</h3>
      ${() => {
        if (status === "followed_up") {
          return html`<div class="timeline">${() => followups.map(timelineItem)}</div>`;
        }
        return html`<div class="awaiting-note">
          No activity after this escalation fired. The auto-reply pipeline is
          waiting on a human agent — compose a reply and post it directly
          through Guesty to close the loop.
        </div>`;
      }}
    </div>

    <div class="detail-section">
      <h3>Full conversation</h3>
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

// ---------- Traces view template ----------

// traceRow renders one turn's full audit record: the guest message, the
// classifier verdict, which tools fired (with latencies), and the final
// disposition (auto-sent note or escalation).
const traceRow = (postID, detail) => html`
  <div class="trace-row">
    <div class="tr-head">
      <span class="tr-post mono">${() => postID}</span>
      ${() => detail.classification
        ? html`<span class="chip code">${() => detail.classification.PrimaryCode}</span>` : ""}
      ${() => detail.classification
        ? html`<span class="chip">conf ${() => (detail.classification.Confidence ?? 0).toFixed(2)}</span>` : ""}
      ${() => detail.escalation
        ? html`<span class="chip danger">escalated</span>`
        : (detail.reply ? html`<span class="chip">auto-sent</span>` : html`<span class="chip">pending</span>`)}
    </div>
    ${() => detail.reply?.body ? html`
      <div class="tr-body">${() => detail.reply.body}</div>` : ""}
    ${() => (detail.reply?.used_tools?.length)
      ? html`<div class="tr-tools">
          ${() => detail.reply.used_tools.map(t => html`
            <span class="tr-tool-chip">
              <span class="mono">${() => t.name}</span>
              <span class="tr-tool-lat">${() => (t.latency_ms != null ? t.latency_ms + "ms" : "")}</span>
            </span>
          `)}
        </div>`
      : ""}
    ${() => detail.classification?.Reasoning ? html`
      <div class="tr-reason">${() => detail.classification.Reasoning}</div>` : ""}
  </div>
`;

const tracesView = html`
  <div class="traces-wrap">
    <div class="traces-card">
      <div class="agent-card-head">
        <h2>Turn traces</h2>
        <span class="pill">${() => Object.keys(state.turnDetails).length} turns</span>
      </div>
      <div class="traces-list">
        ${() => {
          const entries = Object.entries(state.turnDetails)
            .filter(([_, d]) => d && !d._disabled);
          if (entries.length === 0) {
            return html`<div class="empty">
              <span class="emoji">🔎</span>
              No turns yet. Send a message from the Customer tab.
            </div>`;
          }
          return entries.map(([postID, d]) => traceRow(postID, d));
        }}
      </div>
    </div>
  </div>
`;

// ---------- Wire templates + poll ----------

loadFormFromStorage();
wireFormPersistence();

customerView(document.getElementById("view-customer"));
agentView(document.getElementById("view-agent"));
tracesView(document.getElementById("view-traces"));

setInterval(refreshConversation, 1500);
setInterval(refreshEscalations, 1500);
setInterval(updateBadge, 1500);
setInterval(repollPendingTurns, 2500);
refreshConversation();
refreshEscalations();
updateBadge();
