/* trpc-agent-go 测试监控平台前端 */
"use strict";

/* ---------------- helpers ---------------- */
const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

function esc(s) {
  if (s === null || s === undefined) return "";
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}
function fmtTime(t) {
  if (!t) return "-";
  const d = new Date(t);
  return d.toLocaleTimeString("zh-CN", { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
}
function fmtDur(ms) {
  if (ms === undefined || ms === null) return "-";
  if (ms < 1000) return ms + "ms";
  return (ms / 1000).toFixed(2) + "s";
}
function short(id) { return (id || "").slice(0, 8); }

async function api(path, opts = {}) {
  const o = { headers: {}, ...opts };
  if (o.body !== undefined && typeof o.body !== "string") {
    o.body = JSON.stringify(o.body);
    o.headers["Content-Type"] = "application/json";
  }
  const rsp = await fetch(path, o);
  const text = await rsp.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
  if (!rsp.ok) throw new Error((data && data.error) || rsp.status + " " + rsp.statusText);
  return data;
}

function toast(msg, kind = "", actions = null) {
  const el = document.createElement("div");
  el.className = "toast " + kind;
  el.innerHTML = `<div>${msg}</div>`;
  if (actions) {
    const act = document.createElement("div");
    act.className = "toast-actions";
    actions.forEach(([label, fn]) => {
      const b = document.createElement("button");
      b.className = "btn small"; b.textContent = label;
      b.onclick = () => { fn(); el.remove(); };
      act.appendChild(b);
    });
    el.appendChild(act);
  }
  $("#toasts").appendChild(el);
  setTimeout(() => el.remove(), actions ? 12000 : 5000);
}

function openModal(title, bodyEl) {
  $("#modal-title").textContent = title;
  const body = $("#modal-body");
  body.innerHTML = "";
  body.appendChild(bodyEl);
  $("#modal-mask").classList.remove("hidden");
}
function closeModal() { $("#modal-mask").classList.add("hidden"); }
$("#modal-close").onclick = closeModal;
$("#modal-mask").addEventListener("click", (e) => { if (e.target === $("#modal-mask")) closeModal(); });

const statusText = {
  running: "运行中", waiting: "等待介入", completed: "完成", ok: "成功",
  error: "错误", cancelled: "已取消", mocked: "已注入",
};
function dot(status) {
  return `<span class="status-dot st-${esc(status)}" title="${esc(statusText[status] || status)}"></span>`;
}

/* ---------------- state ---------------- */
const S = {
  page: "dashboard",
  logs: [], logPaused: false,
  runs: [], selectedRun: null, runDetail: null, selectedSpan: null,
  interventions: [], breakpoints: {},
  cases: [], testruns: [], expandedTestrun: null,
  chatSessions: {}, currentSession: null,
  settings: {}, status: {}, rules: [],
};

/* ---------------- SSE ---------------- */
let es = null;
function connectSSE() {
  es = new EventSource("/api/stream");
  es.onopen = () => {
    const c = $("#conn-status");
    c.textContent = "● 已连接"; c.className = "conn online";
  };
  es.onerror = () => {
    const c = $("#conn-status");
    c.textContent = "● 已断开，重连中…"; c.className = "conn offline";
  };
  es.onmessage = (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    handleHubMessage(msg);
  };
}

let runDetailTimer = null;
function scheduleRunDetailRefresh() {
  if (runDetailTimer) return;
  runDetailTimer = setTimeout(async () => {
    runDetailTimer = null;
    if (S.selectedRun) await loadRunDetail(S.selectedRun, true);
  }, 350);
}

function handleHubMessage(msg) {
  const d = msg.data;
  switch (msg.type) {
    case "hello":
      S.status = d; renderStatusChip(); if (S.page === "dashboard") renderStats();
      break;
    case "log":
      S.logs.push(d);
      if (S.logs.length > 3000) S.logs = S.logs.slice(-2500);
      appendLogLine(d);
      break;
    case "run_update":
    case "run_finished": {
      const i = S.runs.findIndex((r) => r.id === d.id);
      if (i >= 0) S.runs[i] = d; else S.runs.unshift(d);
      if (S.page === "traces") renderRunList();
      if (S.page === "dashboard") renderStats();
      if (S.selectedRun === d.id) scheduleRunDetailRefresh();
      if (msg.type === "run_finished") chatFinalize(d);
      else chatUpdateStatus(d);
      break;
    }
    case "run_output":
      chatOnOutput(d);
      break;
    case "intervention":
      if (d.state === "pending") {
        S.interventions.push(d.intervention);
        toast(`⏸ 断点命中：${esc(d.intervention.summary)}`, "warn",
          [["去处理", () => { location.hash = "#breakpoints"; }]]);
      } else {
        S.interventions = S.interventions.filter((iv) => iv.id !== d.id);
      }
      renderBpBadge();
      if (S.page === "breakpoints") renderInterventions();
      break;
    case "breakpoints":
      S.breakpoints = d; syncBreakpointInputs();
      break;
    case "testrun_update": {
      const i = S.testruns.findIndex((t) => t.id === d.id);
      if (i >= 0) S.testruns[i] = d; else S.testruns.unshift(d);
      if (S.page === "cases") renderTestruns();
      break;
    }
    case "status":
      S.status = { ...S.status, ...d }; renderStatusChip();
      break;
  }
}

/* ---------------- router ---------------- */
const pages = ["dashboard", "playground", "traces", "breakpoints", "cases", "rules", "settings"];
function route() {
  let h = (location.hash || "#dashboard").slice(1);
  let arg = null;
  if (h.includes("/")) { [h, arg] = h.split("/", 2); }
  if (!pages.includes(h)) h = "dashboard";
  S.page = h;
  pages.forEach((p) => $("#page-" + p).classList.toggle("hidden", p !== h));
  $$("#nav a").forEach((a) => a.classList.toggle("active", a.dataset.page === h));
  if (h === "dashboard") { renderStats(); renderLogStream(); }
  if (h === "playground") initPlayground();
  if (h === "traces") { loadRuns().then(() => { if (arg) selectRun(arg); }); }
  if (h === "breakpoints") { loadBreakpoints(); loadInterventions(); }
  if (h === "cases") { loadCases(); loadTestruns(); }
  if (h === "rules") loadRules();
  if (h === "settings") loadSettings();
}
window.addEventListener("hashchange", route);

/* ---------------- status ---------------- */
function renderStatusChip() {
  const st = S.status || {};
  $("#model-chip").textContent = `${st.provider || "?"} / ${st.model || "?"} · ${st.agent_name || ""}`;
}
async function loadStatus() {
  try { S.status = await api("/api/status"); renderStatusChip(); if (S.page === "dashboard") renderStats(); }
  catch (e) { /* ignore */ }
}

/* ---------------- dashboard ---------------- */
function renderStats() {
  const st = S.status || {}; const runs = st.runs || {};
  const cards = [
    ["运行总数", runs.total ?? 0],
    ["进行中", runs.running ?? 0],
    ["已完成", runs.completed ?? 0],
    ["失败", runs.failed ?? 0],
    ["累计 Tokens", runs.total_tokens ?? 0],
    ["测试用例", st.cases ?? 0],
    ["待介入", (st.breakpoints && st.breakpoints.pending) ?? 0],
  ];
  $("#stat-row").innerHTML = cards.map(([l, n]) =>
    `<div class="stat-card"><div class="lbl">${l}</div><div class="num">${n}</div></div>`).join("");
}

function logLineHTML(e) {
  const data = e.data ? " " + esc(JSON.stringify(e.data)) : "";
  const run = e.run_id ? ` <span class="run" data-run="${esc(e.run_id)}">[${short(e.run_id)}]</span>` : "";
  return `<div class="log-line"><span class="t">${fmtTime(e.time)}</span> ` +
    `<span class="lv-${esc(e.level)}">[${esc(e.level)}]</span> ` +
    `<span class="cat">[${esc(e.category)}]</span>${run} ${esc(e.message)}<span class="t">${data}</span></div>`;
}
function logMatchesFilter(e) {
  const lv = $("#log-level").value, cat = $("#log-category").value;
  return (!lv || e.level === lv) && (!cat || e.category === cat);
}
function appendLogLine(e) {
  if (S.page !== "dashboard" || !logMatchesFilter(e)) return;
  const box = $("#log-stream");
  box.insertAdjacentHTML("beforeend", logLineHTML(e));
  while (box.children.length > 1500) box.removeChild(box.firstChild);
  if (!S.logPaused) box.scrollTop = box.scrollHeight;
}
function renderLogStream() {
  const box = $("#log-stream");
  box.innerHTML = S.logs.filter(logMatchesFilter).slice(-1200).map(logLineHTML).join("");
  if (!S.logPaused) box.scrollTop = box.scrollHeight;
}
$("#log-level").onchange = renderLogStream;
$("#log-category").onchange = renderLogStream;
$("#log-clear").onclick = () => { S.logs = []; renderLogStream(); };
$("#log-pause").onclick = () => {
  S.logPaused = !S.logPaused;
  $("#log-pause").textContent = S.logPaused ? "▶ 恢复滚动" : "⏸ 暂停滚动";
};
$("#log-stream").addEventListener("click", (e) => {
  const t = e.target.closest(".run");
  if (t) location.hash = "#traces/" + t.dataset.run;
});

/* ---------------- playground ---------------- */
let pgInited = false;
function initPlayground() {
  if (!S.currentSession) newSession();
  renderSessionSelect();
  syncBreakpointInputs();
  if (pgInited) return;
  pgInited = true;
  $("#pg-new-session").onclick = () => { newSession(); renderSessionSelect(); renderChat(); };
  $("#pg-session").onchange = () => { S.currentSession = $("#pg-session").value; renderChat(); };
  $("#chat-send").onclick = sendChat;
  $("#chat-text").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendChat(); }
  });
  $("#pg-bp-model").onchange = $("#pg-bp-tool").onchange = () => {
    saveBreakpoints($("#pg-bp-model").checked, $("#pg-bp-tool").checked, null);
  };
  $("#pg-view-session").onclick = viewSessionEvents;
}
function newSession() {
  const id = "session-" + Math.random().toString(36).slice(2, 10);
  S.chatSessions[id] = { messages: [] };
  S.currentSession = id;
}
function renderSessionSelect() {
  const sel = $("#pg-session");
  sel.innerHTML = Object.keys(S.chatSessions).map((id) =>
    `<option value="${id}" ${id === S.currentSession ? "selected" : ""}>${id}</option>`).join("");
}
function curSess() { return S.chatSessions[S.currentSession]; }

async function sendChat() {
  const txt = $("#chat-text").value.trim();
  if (!txt) return;
  $("#chat-text").value = "";
  const sess = curSess();
  sess.messages.push({ role: "user", content: txt });
  const botMsg = { role: "bot", content: "", runId: null, status: "running", steps: [] };
  sess.messages.push(botMsg);
  renderChat();
  try {
    const rsp = await api("/api/chat", { method: "POST", body: { message: txt, session_id: S.currentSession } });
    botMsg.runId = rsp.run_id;
    renderChat();
  } catch (e) {
    botMsg.status = "error"; botMsg.content = "请求失败: " + e.message;
    renderChat();
  }
}
function chatOnOutput(d) {
  const sess = curSess(); if (!sess) return;
  const m = sess.messages.find((x) => x.runId === d.run_id);
  if (!m) return;
  if (d.partial) m.content += d.delta;
  else if (d.content) m.content = d.content;
  renderChatBubbleContent(m);
}
function chatUpdateStatus(sum) {
  const sess = curSess(); if (!sess) return;
  const m = sess.messages.find((x) => x.runId === sum.id);
  if (!m || m.meta) return;
  if (m.status !== sum.status && (sum.status === "waiting" || sum.status === "running")) {
    m.status = sum.status;
    if (S.page === "playground") renderChat();
  }
}
function chatFinalize(sum) {
  Object.values(S.chatSessions).forEach((sess) => {
    const m = sess.messages.find((x) => x.runId === sum.id);
    if (!m) return;
    m.status = sum.status;
    if (!m.content && sum.final_output) m.content = sum.final_output;
    if (sum.status === "error" && sum.error) m.content = (m.content || "") + "\n[错误] " + sum.error;
    m.meta = { duration: sum.duration_ms, tokens: (sum.usage || {}).total_tokens, model_calls: sum.model_calls, tool_calls: sum.tool_calls };
    if (S.page === "playground") renderChat();
  });
}
function renderChatBubbleContent(m) {
  if (S.page !== "playground") return;
  const el = document.querySelector(`[data-chat-run="${m.runId}"] .bubble`);
  if (el) { el.textContent = m.content || "…"; scrollChat(); }
}
function renderChat() {
  const sess = curSess(); if (!sess) return;
  $("#chat-box").innerHTML = sess.messages.map((m) => {
    if (m.role === "user") {
      return `<div class="msg user"><div class="bubble">${esc(m.content)}</div></div>`;
    }
    const metaBits = [];
    if (m.status === "running") metaBits.push(`<span class="waiting-tag">⏳ 执行中…</span>`);
    if (m.status === "waiting") metaBits.push(`<span class="waiting-tag">⏸ 等待人工介入（断点）</span>`);
    if (m.meta) metaBits.push(`${fmtDur(m.meta.duration)} · 模型×${m.meta.model_calls} · 工具×${m.meta.tool_calls} · ${m.meta.tokens || 0} tok`);
    if (m.runId) metaBits.push(`<a data-goto-run="${m.runId}">查看链路 ↗</a>`);
    return `<div class="msg bot" data-chat-run="${esc(m.runId || "")}">` +
      `<div class="bubble">${esc(m.content || "…")}</div>` +
      `<div class="meta">${metaBits.join(" · ")}</div></div>`;
  }).join("");
  $("#chat-box").querySelectorAll("[data-goto-run]").forEach((a) => {
    a.onclick = () => { location.hash = "#traces/" + a.dataset.gotoRun; };
  });
  scrollChat();
}
function scrollChat() { const b = $("#chat-box"); b.scrollTop = b.scrollHeight; }

async function viewSessionEvents() {
  try {
    const d = await api(`/api/sessions/${encodeURIComponent(S.currentSession)}`);
    const div = document.createElement("div");
    div.innerHTML = `<div class="hint" style="margin-bottom:8px">会话中已持久化的事件（这些事件会在下一轮被 ContentRequestProcessor 回放为上下文）</div>` +
      (d.events || []).map((e) =>
        `<div class="event-row">[${fmtTime(e.time)}] ${esc(e.author)} · ${esc(e.role)}` +
        (e.tool_calls ? ` · 工具调用: ${esc(e.tool_calls.map((t) => t.name).join(","))}` : "") +
        (e.tool_name ? ` · [${esc(e.tool_name)}]` : "") +
        ` ${esc((e.content || "").slice(0, 200))}</div>`).join("") || "<div class='iv-empty'>暂无事件</div>";
    openModal(`会话事件 · ${S.currentSession}`, div);
  } catch (e) { toast("获取会话失败: " + e.message, "error"); }
}

/* ---------------- traces ---------------- */
async function loadRuns() {
  try { S.runs = await api("/api/runs?limit=200"); renderRunList(); } catch (e) { }
}
$("#trace-refresh").onclick = loadRuns;
function renderRunList() {
  const box = $("#run-list");
  if (!S.runs.length) { box.innerHTML = `<div class="iv-empty">还没有运行记录。去「调试台」发一条消息，或运行测试用例。</div>`; return; }
  box.innerHTML = S.runs.map((r) => {
    const src = r.source === "testcase" ? `<span class="tag src-generated">用例:${esc(r.case_name || r.case_id)}</span>` : `<span class="tag">调试台</span>`;
    return `<div class="run-item ${r.id === S.selectedRun ? "sel" : ""}" data-run="${r.id}">
      <div class="top">${dot(r.status)} <b>#${r.seq}</b> ${src} ${r.intervened ? "✋" : ""}<span class="spacer"></span><span class="hint">${fmtTime(r.started_at)}</span></div>
      <div class="input">${esc(r.input)}</div>
      <div class="sub"><span>${fmtDur(r.duration_ms)}</span><span>模型×${r.model_calls}</span><span>工具×${r.tool_calls}</span><span>${r.usage.total_tokens} tok</span></div>
    </div>`;
  }).join("");
  box.querySelectorAll(".run-item").forEach((el) => { el.onclick = () => selectRun(el.dataset.run); });
}
async function selectRun(id) {
  S.selectedRun = id;
  renderRunList();
  await loadRunDetail(id, false);
}
async function loadRunDetail(id, keepSpan) {
  try {
    S.runDetail = await api("/api/runs/" + id);
    if (!keepSpan) S.selectedSpan = null;
    renderTraceDetail();
  } catch (e) { $("#trace-detail").innerHTML = `<div class="iv-empty">${esc(e.message)}</div>`; }
}
function renderTraceDetail() {
  const r = S.runDetail;
  if (!r) return;
  $("#trace-title").innerHTML = `运行 #${r.seq} · ${dot(r.status)} ${esc(statusText[r.status] || r.status)}` +
    ` · ${esc(r.model_name)} ${r.status === "running" || r.status === "waiting" ? `<button class="btn small danger" id="run-cancel">取消运行</button>` : ""}`;
  const spans = r.spans || [];
  const byParent = {};
  spans.forEach((s) => { (byParent[s.parent_id || ""] = byParent[s.parent_id || ""] || []).push(s); });

  const kindIcon = { agent: "🤖", model: "🧠", tool: "🔧" };
  function renderNode(s, depth) {
    const kids = byParent[s.id] || [];
    return `<div class="span-node ${S.selectedSpan === s.id ? "sel" : ""}" data-span="${s.id}" style="margin-left:${depth * 26}px">
      ${dot(s.status)} <span>${kindIcon[s.kind] || "•"}</span> <b>${esc(s.name)}</b>
      ${s.intervened ? `<span class="iv-mark" title="有人工介入">✋介入</span>` : ""}
      <span class="dur">${s.ended_at ? fmtDur(s.duration_ms) : "…"}</span>
    </div>` + kids.map((k) => renderNode(k, depth + 1)).join("");
  }
  const roots = byParent[""] || [];
  const treeHTML = roots.map((s) => renderNode(s, 0)).join("") ||
    `<div class="iv-empty">尚无 span（等待 Agent 回调触发）</div>`;

  const kv = `<div class="kv">
    <div class="k">输入</div><div>${esc(r.input)}</div>
    <div class="k">最终输出</div><div>${esc(r.final_output || "(尚未产生)")}</div>
    <div class="k">会话 / 用户</div><div>${esc(r.session_id)} / ${esc(r.user_id)}</div>
    <div class="k">Token 用量</div><div>提示 ${r.usage.prompt_tokens} + 生成 ${r.usage.completion_tokens} = ${r.usage.total_tokens}</div>
    ${r.error ? `<div class="k">错误</div><div class="fail">${esc(r.error)}</div>` : ""}
  </div>`;

  $("#trace-detail").innerHTML = kv +
    `<h3 style="margin:6px 0">调用链路</h3><div class="span-tree">${treeHTML}</div>` +
    `<div id="span-detail" class="span-detail"></div>` +
    `<h3 style="margin:14px 0 6px">事件流（${(r.events || []).length}）</h3>` +
    `<div>${(r.events || []).slice(-80).map((e) =>
      `<div class="event-row">#${e.seq} [${fmtTime(e.time)}] ${esc(e.author)} · ${esc(e.object)}${e.partial ? " (delta)" : ""}` +
      (e.tool_calls ? ` · ⚙ ${esc(e.tool_calls.join(","))}` : "") +
      (e.error ? ` · <span class="fail">${esc(e.error)}</span>` : "") +
      ` ${esc(e.preview || "")}</div>`).join("")}</div>`;

  const cancelBtn = $("#run-cancel");
  if (cancelBtn) cancelBtn.onclick = async () => {
    try { await api(`/api/runs/${r.id}/cancel`, { method: "POST", body: {} }); toast("已发送取消", "ok"); }
    catch (e) { toast(e.message, "error"); }
  };
  $("#trace-detail").querySelectorAll(".span-node").forEach((el) => {
    el.onclick = () => { S.selectedSpan = el.dataset.span; renderTraceDetail(); };
  });
  if (S.selectedSpan) renderSpanDetail(spans.find((s) => s.id === S.selectedSpan));
}

function provMsgHTML(p, idx) {
  const tc = (p.tool_calls || []).map((t) =>
    `<div class="ctx-msg-body mono">→ 工具调用 ${esc(t.name)} (${esc(t.id)})\n参数: ${esc(t.arguments)}</div>`).join("");
  const origin = p.origin ? `<div class="ctx-reason">来源事件: ${esc(p.origin.author || "")} @ ${esc(p.origin.event_time || "")} · invocation=${esc(short(p.origin.invocation_id))} · ${p.origin.scope === "current_invocation" ? "本轮" : "历史轮"}</div>` : "";
  return `<div class="ctx-msg">
    <div class="ctx-msg-head" data-ctx-toggle="${idx}">
      <span class="role-pill">${esc(p.role)}</span>
      <span class="src-pill src-${esc(p.source)}">${esc(p.source_label)}</span>
      <span class="hint">#${p.index} · ${p.content_chars} 字</span>
      ${p.tool_name ? `<span class="hint">[${esc(p.tool_name)}]</span>` : ""}
      <span class="spacer"></span><span class="hint">点击展开/收起</span>
    </div>
    <div class="ctx-msg-body" data-ctx-body="${idx}">${esc(p.content) || "<i>(空内容)</i>"}</div>
    ${tc}
    <div class="ctx-reason">💡 <b>为什么拼进来：</b>${esc(p.reason)}</div>
    ${origin}
  </div>`;
}

function renderSpanDetail(sp) {
  const box = $("#span-detail");
  if (!sp) { box.innerHTML = ""; return; }
  const d = sp.detail || {};
  let html = `<h3 style="margin:4px 0 8px">Span 详情：${esc(sp.name)} ${dot(sp.status)} ${sp.intervened ? "✋有人工介入" : ""}</h3>`;

  if (sp.kind === "model") {
    const prov = d.provenance || [];
    const rsp = d.response || {};
    const gen = d.gen_config || {};
    html += `<div class="kv">
      <div class="k">生成配置</div><div class="mono">${esc(JSON.stringify(gen))}</div>
      <div class="k">可用工具</div><div>${esc((d.tools || []).join(", ") || "无")}</div>
      <div class="k">消息数 / 字数</div><div>${d.msg_count} 条 / ${d.total_chars} 字</div>
    </div>`;
    html += `<div class="tabs">
      <div class="tab active" data-tab="ctx">🧩 拼接的上下文（${prov.length} 条，含来源）</div>
      <div class="tab" data-tab="rsp">📤 模型响应</div>
      <div class="tab" data-tab="raw">原始 JSON</div>
    </div>`;
    html += `<div data-pane="ctx">${prov.map(provMsgHTML).join("") || "<div class='iv-empty'>无</div>"}</div>`;
    const tcs = (rsp.tool_calls || []).map((t) => `<div class="ctx-msg-body mono">⚙ ${esc(t.name)}(${esc(t.arguments)})</div>`).join("");
    html += `<div data-pane="rsp" class="hidden">
      ${rsp.mocked ? `<div class="ctx-reason">🧪 该响应由人工在断点处注入，未调用真实模型</div>` : ""}
      <div class="ctx-msg"><div class="ctx-msg-head"><span class="role-pill">assistant</span>
      <span class="hint">finish=${esc(rsp.finish_reason || "-")}</span></div>
      <div class="ctx-msg-body">${esc(rsp.content || "(无文本内容)")}</div>${tcs}</div>
      ${rsp.usage ? `<div class="hint">用量: ${esc(JSON.stringify(rsp.usage))}</div>` : ""}
    </div>`;
    html += `<div data-pane="raw" class="hidden"><pre class="json">${esc(JSON.stringify(d, null, 2))}</pre></div>`;
    if (d.original_messages) {
      html += `<div class="ctx-reason">✋ 此调用的上下文曾被人工修改；原始消息保存在「原始 JSON」的 original_messages 字段。</div>`;
    }
  } else if (sp.kind === "tool") {
    html += `<div class="kv">
      <div class="k">工具</div><div>${esc(d.tool_name)}</div>
      <div class="k">Tool Call ID</div><div class="mono">${esc(d.tool_call_id)}</div>
      <div class="k">参数</div><div><pre class="json">${esc(prettyJSON(d.arguments))}</pre></div>
      ${d.original_arguments ? `<div class="k">原始参数(被修改前)</div><div><pre class="json">${esc(prettyJSON(d.original_arguments))}</pre></div>` : ""}
      <div class="k">结果</div><div><pre class="json">${esc(prettyJSON(d.result))}</pre></div>
      ${sp.error ? `<div class="k">错误</div><div class="fail">${esc(sp.error)}</div>` : ""}
    </div>`;
  } else {
    html += `<pre class="json">${esc(JSON.stringify(d, null, 2))}</pre>`;
  }
  box.innerHTML = html;

  box.querySelectorAll(".tab").forEach((t) => {
    t.onclick = () => {
      box.querySelectorAll(".tab").forEach((x) => x.classList.toggle("active", x === t));
      box.querySelectorAll("[data-pane]").forEach((p) =>
        p.classList.toggle("hidden", p.dataset.pane !== t.dataset.tab));
    };
  });
  box.querySelectorAll("[data-ctx-toggle]").forEach((h) => {
    h.onclick = () => {
      const b = box.querySelector(`[data-ctx-body="${h.dataset.ctxToggle}"]`);
      if (b) b.classList.toggle("hidden");
    };
  });
}
function prettyJSON(s) {
  if (s === undefined || s === null || s === "") return "";
  if (typeof s !== "string") return JSON.stringify(s, null, 2);
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; }
}

/* ---------------- breakpoints ---------------- */
async function loadBreakpoints() {
  try { S.breakpoints = await api("/api/breakpoints"); syncBreakpointInputs(); } catch (e) { }
}
function syncBreakpointInputs() {
  const b = S.breakpoints || {};
  const set = (id, v) => { const el = $(id); if (el) el.checked = !!v; };
  set("#bp-model", b.model_enabled); set("#bp-tool", b.tool_enabled);
  set("#pg-bp-model", b.model_enabled); set("#pg-bp-tool", b.tool_enabled);
  if (b.timeout_sec) $("#bp-timeout").value = b.timeout_sec;
  renderBpBadge();
}
async function saveBreakpoints(modelOn, toolOn, timeoutSec) {
  try {
    S.breakpoints = await api("/api/breakpoints", {
      method: "POST",
      body: {
        model_enabled: modelOn, tool_enabled: toolOn,
        timeout_sec: timeoutSec || parseInt($("#bp-timeout").value || "0", 10) || 0,
      },
    });
    syncBreakpointInputs();
    toast("断点配置已更新", "ok");
  } catch (e) { toast(e.message, "error"); }
}
$("#bp-save").onclick = () => saveBreakpoints($("#bp-model").checked, $("#bp-tool").checked, parseInt($("#bp-timeout").value, 10));

async function loadInterventions() {
  try { S.interventions = await api("/api/interventions") || []; renderInterventions(); renderBpBadge(); } catch (e) { }
}
function renderBpBadge() {
  const n = (S.interventions || []).length;
  const b = $("#bp-badge");
  b.textContent = n; b.classList.toggle("hidden", n === 0);
}

function renderInterventions() {
  const box = $("#iv-list");
  const list = S.interventions || [];
  if (!list.length) {
    box.innerHTML = `<div class="iv-empty">当前没有等待介入的调用。开启上方断点后，在「调试台」发消息即可在此拦截。</div>`;
    return;
  }
  box.innerHTML = "";
  list.forEach((iv) => box.appendChild(renderInterventionCard(iv)));
}

function renderInterventionCard(iv) {
  const card = document.createElement("div");
  card.className = "iv-card";
  const isModel = iv.kind === "model_request";
  const head = document.createElement("div");
  head.className = "head";
  head.innerHTML = `<span class="iv-kind">${isModel ? "模型请求" : "工具调用"}</span>
    <b>${esc(iv.summary)}</b>
    <span class="hint">run=${short(iv.run_id)} · ${fmtTime(iv.created_at)}</span>
    <span class="countdown" data-deadline="${esc(iv.deadline)}"></span>
    <span class="spacer"></span>
    <a class="btn small" href="#traces/${esc(iv.run_id)}">查看链路</a>`;
  card.appendChild(head);

  const body = document.createElement("div");
  card.appendChild(body);

  const actions = document.createElement("div");
  actions.className = "iv-actions";
  card.appendChild(actions);

  const resolve = async (payload, okMsg) => {
    try {
      await api(`/api/interventions/${iv.id}`, { method: "POST", body: payload });
      toast(okMsg, "ok");
    } catch (e) { toast(e.message, "error"); }
  };

  if (isModel) {
    // Editable message list.
    const msgs = (iv.payload && iv.payload.messages || []).map((m) => ({ ...m }));
    const prov = (iv.payload && iv.payload.provenance) || [];
    const listEl = document.createElement("div");
    body.appendChild(listEl);

    function redraw() {
      listEl.innerHTML = "";
      msgs.forEach((m, i) => {
        const p = prov[i];
        const row = document.createElement("div");
        row.className = "edit-msg";
        row.innerHTML = `<div class="edit-msg-head">
          <select class="sel" data-role>
            ${["system", "user", "assistant", "tool"].map((r) => `<option ${m.role === r ? "selected" : ""}>${r}</option>`).join("")}
          </select>
          ${p ? `<span class="src-pill src-${esc(p.source)}">${esc(p.source_label)}</span>` : ""}
          ${m.tool_calls ? `<span class="hint">含 ${m.tool_calls.length} 个工具调用(保留)</span>` : ""}
          ${m.tool_name ? `<span class="hint">[${esc(m.tool_name)}]</span>` : ""}
          <span class="spacer"></span>
          <button class="btn small" data-up ${i === 0 ? "disabled" : ""}>↑</button>
          <button class="btn small" data-down ${i === msgs.length - 1 ? "disabled" : ""}>↓</button>
          <button class="btn small danger" data-del>删除</button>
        </div>
        <textarea rows="3" data-content></textarea>`;
        row.querySelector("[data-content]").value = m.content || "";
        row.querySelector("[data-role]").onchange = (e) => { m.role = e.target.value; };
        row.querySelector("[data-content]").oninput = (e) => { m.content = e.target.value; };
        row.querySelector("[data-del]").onclick = () => { msgs.splice(i, 1); redraw(); };
        row.querySelector("[data-up]").onclick = () => { [msgs[i - 1], msgs[i]] = [msgs[i], msgs[i - 1]]; redraw(); };
        row.querySelector("[data-down]").onclick = () => { [msgs[i + 1], msgs[i]] = [msgs[i], msgs[i + 1]]; redraw(); };
        if (p) {
          const why = document.createElement("div");
          why.className = "ctx-reason";
          why.innerHTML = `💡 ${esc(p.reason)}`;
          row.appendChild(why);
        }
        listEl.appendChild(row);
      });
      const addBtn = document.createElement("button");
      addBtn.className = "btn small";
      addBtn.textContent = "＋ 添加消息";
      addBtn.onclick = () => { msgs.push({ role: "user", content: "" }); redraw(); };
      listEl.appendChild(addBtn);
    }
    redraw();

    const mockArea = document.createElement("textarea");
    mockArea.rows = 2;
    mockArea.placeholder = "（可选）在此输入要注入的模型响应文本，点「注入响应」后将跳过真实模型调用";
    body.appendChild(mockArea);

    actions.innerHTML = "";
    const mk = (label, cls, fn) => {
      const b = document.createElement("button");
      b.className = "btn " + cls; b.textContent = label; b.onclick = fn;
      actions.appendChild(b);
    };
    mk("▶ 直接放行", "primary", () => resolve({ action: "continue" }, "已放行"));
    mk("✏️ 修改后放行", "warn", () => resolve({ action: "modify", messages: msgs, note: "edited in UI" }, "已按修改后的上下文放行"));
    mk("🧪 注入响应", "", () => {
      if (!mockArea.value.trim()) { toast("请先填写要注入的响应内容", "warn"); return; }
      resolve({ action: "mock", mock_content: mockArea.value }, "已注入响应，跳过模型调用");
    });
    mk("🛑 中止", "danger", () => resolve({ action: "abort" }, "已中止该调用"));
  } else {
    const argsArea = document.createElement("textarea");
    argsArea.rows = 4;
    argsArea.value = prettyJSON(iv.payload && iv.payload.arguments);
    const lbl = document.createElement("div");
    lbl.className = "hint";
    lbl.textContent = `工具 ${iv.payload && iv.payload.tool_name} 的调用参数（可编辑 JSON）：`;
    body.appendChild(lbl);
    body.appendChild(argsArea);
    const mockArea = document.createElement("textarea");
    mockArea.rows = 2;
    mockArea.placeholder = "（可选）注入工具结果（JSON 或纯文本），点「注入结果」后将跳过真实工具执行";
    body.appendChild(mockArea);

    const mk = (label, cls, fn) => {
      const b = document.createElement("button");
      b.className = "btn " + cls; b.textContent = label; b.onclick = fn;
      actions.appendChild(b);
    };
    mk("▶ 直接放行", "primary", () => resolve({ action: "continue" }, "已放行"));
    mk("✏️ 修改参数放行", "warn", () => {
      try { JSON.parse(argsArea.value); } catch (e) { toast("参数不是合法 JSON: " + e.message, "error"); return; }
      resolve({ action: "modify", tool_args: JSON.parse(argsArea.value), note: "args edited" }, "已按修改后的参数执行");
    });
    mk("🧪 注入结果", "", () => {
      if (!mockArea.value.trim()) { toast("请先填写要注入的结果", "warn"); return; }
      let v; try { v = JSON.parse(mockArea.value); } catch { v = mockArea.value; }
      resolve({ action: "mock", tool_result: v }, "已注入工具结果");
    });
    mk("🛑 中止", "danger", () => resolve({ action: "abort" }, "已中止该工具调用"));
  }
  return card;
}

setInterval(() => {
  $$(".countdown").forEach((el) => {
    const dl = new Date(el.dataset.deadline).getTime();
    const left = Math.max(0, Math.floor((dl - Date.now()) / 1000));
    el.textContent = `⏳ ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")} 后自动放行`;
  });
}, 1000);

/* ---------------- test cases ---------------- */
async function loadCases() {
  try { S.cases = await api("/api/testcases") || []; renderCases(); } catch (e) { }
}
function renderCases() {
  const tb = $("#case-tbody");
  tb.innerHTML = S.cases.map((c) => `<tr>
    <td><input type="checkbox" class="case-check" value="${c.id}"></td>
    <td><b>${esc(c.name)}</b><div class="hint">${esc(c.description || "")}</div></td>
    <td>${c.turns.length} 轮<div class="hint">${esc(c.turns[0] || "")}</div></td>
    <td>${c.assertions.length} 条</td>
    <td><span class="tag src-${esc(c.source)}">${esc(c.source)}</span></td>
    <td class="hint">${new Date(c.updated_at).toLocaleString("zh-CN")}</td>
    <td>
      <button class="btn small" data-run-case="${c.id}">▶</button>
      <button class="btn small" data-edit-case="${c.id}">编辑</button>
      <button class="btn small danger" data-del-case="${c.id}">删</button>
    </td>
  </tr>`).join("") || `<tr><td colspan="7" class="iv-empty">暂无用例，点击「新建用例」或「生成用例」</td></tr>`;

  tb.querySelectorAll("[data-run-case]").forEach((b) => {
    b.onclick = () => startTestRun([b.dataset.runCase]);
  });
  tb.querySelectorAll("[data-edit-case]").forEach((b) => {
    b.onclick = () => openCaseEditor(S.cases.find((c) => c.id === b.dataset.editCase));
  });
  tb.querySelectorAll("[data-del-case]").forEach((b) => {
    b.onclick = async () => {
      if (!confirm("确认删除该用例？")) return;
      try { await api("/api/testcases/" + b.dataset.delCase, { method: "DELETE" }); toast("已删除", "ok"); loadCases(); }
      catch (e) { toast(e.message, "error"); }
    };
  });
}
$("#case-check-all").onchange = (e) => {
  $$(".case-check").forEach((c) => { c.checked = e.target.checked; });
};

const ASSERT_TYPES = [
  ["contains", "输出包含"],
  ["not_contains", "输出不包含"],
  ["regex", "正则匹配"],
  ["tool_called", "调用了工具"],
  ["tool_not_called", "未调用工具"],
  ["no_error", "无错误"],
  ["max_latency_ms", "耗时上限(ms)"],
  ["max_model_calls", "模型调用上限"],
];

function openCaseEditor(existing) {
  const c = existing ? JSON.parse(JSON.stringify(existing)) : { name: "", description: "", turns: [""], assertions: [{ type: "no_error", value: "" }] };
  const root = document.createElement("div");
  root.className = "form-grid";
  root.innerHTML = `
    <label>用例名称 <input class="inp" data-f="name" value="${esc(c.name)}"></label>
    <label>描述 <input class="inp" data-f="description" value="${esc(c.description || "")}"></label>
    <div><b>用户输入轮次</b>（依次在同一会话中发送）<div data-turns></div>
      <button class="btn small" data-add-turn>＋ 加一轮</button></div>
    <div><b>断言</b><div data-asserts></div>
      <button class="btn small" data-add-assert>＋ 加断言</button></div>
    <div><button class="btn primary" data-save>${existing ? "保存修改" : "创建用例"}</button></div>`;

  const turnsEl = root.querySelector("[data-turns]");
  const assertsEl = root.querySelector("[data-asserts]");
  function redrawTurns() {
    turnsEl.innerHTML = "";
    c.turns.forEach((t, i) => {
      const row = document.createElement("div");
      row.className = "turn-row";
      row.innerHTML = `<span class="hint">第${i + 1}轮</span><input class="inp" style="flex:1">
        <button class="btn small danger">删</button>`;
      row.querySelector("input").value = t;
      row.querySelector("input").oninput = (e) => { c.turns[i] = e.target.value; };
      row.querySelector("button").onclick = () => { c.turns.splice(i, 1); redrawTurns(); };
      turnsEl.appendChild(row);
    });
  }
  function redrawAsserts() {
    assertsEl.innerHTML = "";
    c.assertions.forEach((a, i) => {
      const row = document.createElement("div");
      row.className = "assert-row";
      const sel = document.createElement("select");
      sel.className = "sel";
      sel.innerHTML = ASSERT_TYPES.map(([v, l]) => `<option value="${v}" ${a.type === v ? "selected" : ""}>${l}</option>`).join("");
      sel.onchange = () => { a.type = sel.value; };
      const inp = document.createElement("input");
      inp.className = "inp"; inp.style.flex = "1"; inp.placeholder = "断言值";
      inp.value = a.value || "";
      inp.oninput = () => { a.value = inp.value; };
      const del = document.createElement("button");
      del.className = "btn small danger"; del.textContent = "删";
      del.onclick = () => { c.assertions.splice(i, 1); redrawAsserts(); };
      row.append(sel, inp, del);
      assertsEl.appendChild(row);
    });
  }
  redrawTurns(); redrawAsserts();
  root.querySelector("[data-add-turn]").onclick = () => { c.turns.push(""); redrawTurns(); };
  root.querySelector("[data-add-assert]").onclick = () => { c.assertions.push({ type: "contains", value: "" }); redrawAsserts(); };
  root.querySelector("[data-save]").onclick = async () => {
    c.name = root.querySelector('[data-f="name"]').value;
    c.description = root.querySelector('[data-f="description"]').value;
    try {
      if (existing) await api("/api/testcases/" + existing.id, { method: "PUT", body: c });
      else await api("/api/testcases", { method: "POST", body: c });
      toast(existing ? "用例已更新" : "用例已创建", "ok");
      closeModal(); loadCases();
    } catch (e) { toast(e.message, "error"); }
  };
  openModal(existing ? "编辑用例" : "新建用例", root);
}
$("#case-new").onclick = () => openCaseEditor(null);

$("#case-import").onclick = () => {
  const root = document.createElement("div");
  root.className = "form-grid";
  root.innerHTML = `
    <div class="hint">粘贴用例 JSON 数组（与导出格式一致），或选择文件。格式:
      [{"name":"…","turns":["…"],"assertions":[{"type":"contains","value":"…"}]}]</div>
    <input type="file" accept=".json" data-file>
    <textarea rows="12" data-json placeholder='[{"name":"加法","turns":["计算 1+1"],"assertions":[{"type":"tool_called","value":"calculator"}]}]'></textarea>
    <div><button class="btn primary" data-do>导入</button></div>`;
  root.querySelector("[data-file]").onchange = async (e) => {
    const f = e.target.files[0];
    if (f) root.querySelector("[data-json]").value = await f.text();
  };
  root.querySelector("[data-do]").onclick = async () => {
    let list;
    try { list = JSON.parse(root.querySelector("[data-json]").value); }
    catch (e) { toast("JSON 解析失败: " + e.message, "error"); return; }
    try {
      const r = await api("/api/testcases/import", { method: "POST", body: list });
      toast(`成功导入 ${r.imported} 条用例`, "ok");
      closeModal(); loadCases();
    } catch (e) { toast(e.message, "error"); }
  };
  openModal("导入测试用例", root);
};

$("#case-export").onclick = () => { window.open("/api/testcases/export", "_blank"); };

$("#case-generate").onclick = () => {
  const root = document.createElement("div");
  root.className = "form-grid";
  root.innerHTML = `
    <label>生成数量 <input class="inp num" type="number" value="6" min="1" max="30" data-count></label>
    <label>生成方式
      <select class="sel" data-mode>
        <option value="auto">auto（有真实模型用 LLM，否则用模板）</option>
        <option value="template">template（内置模板，离线可用）</option>
        <option value="llm">llm（用当前模型生成）</option>
      </select>
    </label>
    <label>测试重点（LLM 模式下生效）<input class="inp" data-focus placeholder="如：重点覆盖除零等异常场景"></label>
    <div><button class="btn primary" data-do>生成并保存</button></div>`;
  root.querySelector("[data-do]").onclick = async () => {
    const btn = root.querySelector("[data-do]");
    btn.disabled = true; btn.textContent = "生成中…";
    try {
      const r = await api("/api/testcases/generate", {
        method: "POST",
        body: {
          count: parseInt(root.querySelector("[data-count]").value, 10),
          mode: root.querySelector("[data-mode]").value,
          focus: root.querySelector("[data-focus]").value,
        },
      });
      toast(`已生成 ${r.generated} 条用例（mode=${r.mode}）`, "ok");
      closeModal(); loadCases();
    } catch (e) { toast(e.message, "error"); btn.disabled = false; btn.textContent = "生成并保存"; }
  };
  openModal("自动生成测试用例", root);
};

async function startTestRun(ids) {
  try {
    const tr = await api("/api/testruns", { method: "POST", body: { case_ids: ids || [] } });
    toast(`批量测试已启动（${tr.total} 条用例）`, "ok");
    S.expandedTestrun = tr.id;
    loadTestruns();
  } catch (e) { toast(e.message, "error"); }
}
$("#case-run-all").onclick = () => startTestRun([]);
$("#case-run-selected").onclick = () => {
  const ids = $$(".case-check").filter((c) => c.checked).map((c) => c.value);
  if (!ids.length) { toast("请先勾选要运行的用例", "warn"); return; }
  startTestRun(ids);
};

async function loadTestruns() {
  try { S.testruns = await api("/api/testruns") || []; renderTestruns(); } catch (e) { }
}
$("#testrun-refresh").onclick = loadTestruns;

function renderTestruns() {
  const box = $("#testrun-list");
  if (!S.testruns.length) { box.innerHTML = `<div class="iv-empty">还没有批量测试记录</div>`; return; }
  box.innerHTML = "";
  S.testruns.forEach((tr) => {
    const el = document.createElement("div");
    el.className = "tr-card";
    const pct = tr.total ? Math.round(tr.done / tr.total * 100) : 0;
    el.innerHTML = `<div class="tr-head" data-toggle>
      ${dot(tr.status)} <b>${esc(tr.id)}</b>
      <span class="tr-prog"><div style="width:${pct}%"></div></span>
      <span>${tr.done}/${tr.total}</span>
      <span class="pass">✓ ${tr.passed}</span><span class="fail">✗ ${tr.failed}</span>
      ${tr.errored ? `<span class="fail">💥 ${tr.errored}</span>` : ""}
      <span class="hint">${fmtTime(tr.started_at)} · ${fmtDur(tr.duration_ms)}</span>
      <span class="spacer"></span>
      ${tr.status === "running" ? `<button class="btn small danger" data-cancel>取消</button>` : ""}
    </div><div data-results class="${S.expandedTestrun === tr.id ? "" : "hidden"}"></div>`;

    const resBox = el.querySelector("[data-results]");
    (tr.results || []).forEach((cr) => {
      const c = document.createElement("div");
      c.className = "case-result";
      c.innerHTML = `<div><b class="${cr.status === "passed" ? "pass" : "fail"}">${cr.status === "passed" ? "✅" : cr.status === "failed" ? "❌" : "💥"} ${esc(cr.case_name)}</b>
        <span class="hint"> ${fmtDur(cr.duration_ms)} · ${cr.usage.total_tokens} tok · 链路: ${(cr.run_ids || []).map((id) => `<a href="#traces/${id}" style="color:var(--accent)">${short(id)}</a>`).join(" ")}</span></div>
        ${cr.error ? `<div class="fail">${esc(cr.error)}</div>` : ""}
        <div class="hint" style="margin:4px 0">最终输出: ${esc((cr.final_output || "").slice(0, 220))}</div>
        ${(cr.assertions || []).map((a) => `<div class="assert ${a.passed ? "pass" : "fail"}">${a.passed ? "✓" : "✗"} [${esc(a.type)}] ${esc(a.message)}${a.actual ? ` (实际: ${esc(a.actual)})` : ""}</div>`).join("")}`;
      resBox.appendChild(c);
    });
    el.querySelector("[data-toggle]").onclick = (e) => {
      if (e.target.closest("[data-cancel]") || e.target.closest("a")) return;
      S.expandedTestrun = S.expandedTestrun === tr.id ? null : tr.id;
      renderTestruns();
    };
    const cancelBtn = el.querySelector("[data-cancel]");
    if (cancelBtn) cancelBtn.onclick = async () => {
      try { await api(`/api/testruns/${tr.id}/cancel`, { method: "POST", body: {} }); toast("已请求取消", "ok"); }
      catch (e) { toast(e.message, "error"); }
    };
    box.appendChild(el);
  });
}

/* ---------------- rules ---------------- */
async function loadRules() {
  try {
    S.rules = await api("/api/context-rules");
    $("#rules-list").innerHTML = S.rules.map((r) => `<div class="rule-card">
      <div class="rule-order">${r.order}</div>
      <div>
        <h3>${esc(r.name)} <span class="hint">${esc(r.stage)}</span></h3>
        <div class="rule-cond">⚡ 拼接条件：${esc(r.condition)}</div>
        <div class="rule-effect">${esc(r.effect)}</div>
      </div></div>`).join("");
  } catch (e) { }
}

/* ---------------- settings ---------------- */
async function loadSettings() {
  try {
    S.settings = await api("/api/settings");
    const s = S.settings;
    $("#set-provider").value = s.provider;
    $("#set-model").value = s.model;
    $("#set-baseurl").value = s.base_url || "";
    $("#set-apikey").value = "";
    $("#set-key-state").textContent = s.api_key_set ? "（已配置 Key，留空保持不变）" : "（未配置 Key）";
    $("#set-streaming").checked = !!s.streaming;
    $("#set-temp").value = s.temperature;
    $("#set-maxtok").value = s.max_tokens;
    $("#set-agent").value = s.agent_name;
    $("#set-instruction").value = s.instruction;
  } catch (e) { toast(e.message, "error"); }
}
$("#set-save").onclick = async () => {
  try {
    await api("/api/settings", {
      method: "POST",
      body: {
        provider: $("#set-provider").value,
        model: $("#set-model").value,
        base_url: $("#set-baseurl").value,
        api_key: $("#set-apikey").value,
        streaming: $("#set-streaming").checked,
        temperature: parseFloat($("#set-temp").value) || 0.7,
        max_tokens: parseInt($("#set-maxtok").value, 10) || 2000,
        agent_name: $("#set-agent").value,
        instruction: $("#set-instruction").value,
        description: (S.settings || {}).description || "",
      },
    });
    toast("配置已保存，Agent 已重建", "ok");
    loadSettings(); loadStatus();
  } catch (e) { toast("保存失败: " + e.message, "error"); }
};

/* ---------------- boot ---------------- */
connectSSE();
loadStatus();
loadBreakpoints();
loadInterventions();
api("/api/logs?limit=300").then((logs) => { S.logs = logs || []; if (S.page === "dashboard") renderLogStream(); }).catch(() => { });
route();
setInterval(loadStatus, 15000);
