/* Gleam Web UI — 前端逻辑（无构建、无依赖）
 * 架构：EventSource 接收实时事件 + fetch 调用 REST API。
 * 遵循 ui-ux-pro-max：实时遥测标注、操作全程有反馈、无障碍（aria-live / focus 管理）。 */
'use strict';

/* ---------- 本机 API 口令 ----------
 * 服务端每次启动生成一个口令，注入首页的 <meta name="gleam-token">（见 internal/webui/guard.go）。
 * 所有同源 /api/ 请求都要带上它：fetch 走请求头；EventSource 与 <img> 设不了请求头，走查询参数
 * （服务端只对 GET 认查询参数）。这里包一层全局 fetch / EventSource，而不是逐个调用点去改：
 * 漏掉一处就是一个静默 401，而新加的调用点不会记得这件事。 */
const GLEAM_TOKEN = (document.querySelector('meta[name="gleam-token"]') || {}).content || '';
const isOwnAPI = (u) => {
  try { const x = new URL(u, location.href); return x.origin === location.origin && x.pathname.startsWith('/api/'); }
  catch { return false; }
};
const withToken = (u) => {
  if (!GLEAM_TOKEN || !isOwnAPI(u)) return u;
  const x = new URL(u, location.href);
  x.searchParams.set('token', GLEAM_TOKEN);
  return x.pathname + x.search;
};
(function installTokenTransport() {
  if (!GLEAM_TOKEN) return;
  const nativeFetch = window.fetch.bind(window);
  window.fetch = (input, init = {}) => {
    const url = typeof input === 'string' ? input : (input && input.url) || String(input);
    if (!isOwnAPI(url)) return nativeFetch(input, init);
    const headers = new Headers(init.headers || (input instanceof Request ? input.headers : undefined));
    headers.set('X-Gleam-Token', GLEAM_TOKEN);
    return nativeFetch(input, { ...init, headers });
  };
  const NativeEventSource = window.EventSource;
  if (NativeEventSource) {
    const Wrapped = function (url, cfg) { return new NativeEventSource(withToken(url), cfg); };
    Wrapped.prototype = NativeEventSource.prototype;
    Wrapped.CONNECTING = 0; Wrapped.OPEN = 1; Wrapped.CLOSED = 2;
    window.EventSource = Wrapped;
  }
})();

/* ---------- 图标（内联 SVG，禁止 emoji 当图标） ---------- */
const ICONS = {
  send: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M22 2 11 13M22 2l-7 20-4-9-9-4 20-7z"/></svg>',
  check: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M20 6 9 17l-5-5"/></svg>',
  x: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12"/></svg>',
  alert: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><path d="M12 9v4M12 17h.01"/></svg>',
  bulb: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M9 18h6M10 22h4M12 2a7 7 0 0 0-4 12.7c.6.5 1 1.4 1 2.3h6c0-.9.4-1.8 1-2.3A7 7 0 0 0 12 2z"/></svg>',
  zap: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M13 2 4.5 13.5H11L10 22l8.5-11.5H12L13 2z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>',
  play: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 4.5v15l13-7.5-13-7.5z"/></svg>',
  stop: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>',
  search: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>',
  spinner: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" class="spin"><path d="M21 12a9 9 0 1 1-6.2-8.6"/></svg>',
  eye: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z"/><circle cx="12" cy="12" r="3"/></svg>',
  gear: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>',
};

const shorten = (s, n) => { const r = [...String(s == null ? '' : s)]; return r.length <= n ? r.join('') : r.slice(0, n).join('') + String.fromCharCode(0x2026); };
const PERM_LABELS = { readonly: '只读放行', user_approved: '需我批准', full_access: '完全访问' };

const $ = (sel, root = document) => root.querySelector(sel);
const el = (tag, cls, text) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
};
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

/* ---------- 基础 API ---------- */
async function api(method, url, body) {
  const opts = { method, headers: { 'Content-Type': 'application/json' } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  let resp;
  try {
    resp = await fetch(url, opts);
  } catch {
    // 浏览器给的是 "Failed to fetch" 这类英文原文：换成用户看得懂的一句，原因只可能是本机服务没在答话
    throw new Error('连不上本机的 Gleam 服务，请确认它仍在运行');
  }
  let data = null;
  try { data = await resp.json(); } catch { /* 空响应 */ }
  if (!resp.ok) {
    const e = new Error((data && data.error) || `HTTP ${resp.status}`);
    e.status = resp.status; // 409 这类「不是错了，是要用户做选择」的响应要靠状态码分支
    throw e;
  }
  return data;
}

/* ---------- 列表加载失败：替换骨架屏，给原因和重试，而不是让骨架一直闪 ---------- */
function loadError(wrap, err, retry) {
  if (!wrap) return;
  wrap.innerHTML = '';
  const box = el('div', 'empty empty--error');
  box.innerHTML = `${ICONS.alert || ''}<div class="empty-title">没能加载</div><p class="empty-desc">${esc(err && err.message ? err.message : String(err || ''))}</p>`;
  if (retry) {
    const b = el('button', 'btn btn-secondary btn-sm', '重试');
    b.type = 'button';
    b.addEventListener('click', retry);
    box.appendChild(b);
  }
  wrap.appendChild(box);
}

/* ---------- Toast ---------- */
function toast(text, kind = 'info', ms = 4500) {
  const region = $('#toast-region');
  const t = el('div', `toast toast--${kind}`, text);
  t.title = '点击关闭';
  t.addEventListener('click', () => t.remove()); // 长文案/报错允许手动关掉，不必等超时
  region.appendChild(t);
  while (region.children.length > 4) region.firstChild.remove();
  setTimeout(() => {
    t.classList.add('toast--leaving');
    setTimeout(() => t.remove(), 300);
  }, ms);
}

/* ---------- 模态（焦点圈 + Esc + 焦点归还） ---------- */
// Esc/点遮罩/✕ 关闭必须等价于「取消」：注册了 onCancel 的确认框若被这样关掉，
// Promise 也要落地，否则 await 它的流程永远挂起（2026-09-24 交互走查）。
const Modal = (() => {
  let lastFocus = null;
  let cancelHook = null;
  const overlay = $('#modal-overlay');
  const box = $('#modal');
  function open(titleHTML, buildContent, onCancel) {
    lastFocus = document.activeElement;
    cancelHook = onCancel || null;
    box.innerHTML = `<div class="modal-head"><h2 class="modal-title" id="modal-title">${titleHTML}</h2>` +
      `<button class="modal-close" type="button" aria-label="关闭">✕</button></div>`;
    box.querySelector('.modal-close').addEventListener('click', () => close(false));
    buildContent(box);
    overlay.hidden = false;
    // 初始焦点跳过关闭钮：确认框要落在取消/确定上，别让用户回车即关
    const first = [...box.querySelectorAll('input, textarea, button, select')]
      .find((e) => !e.classList.contains('modal-close'));
    (first || box).focus();
  }
  function close(byButton) {
    if (overlay.hidden) return;
    overlay.hidden = true;
    box.innerHTML = '';
    if (lastFocus) lastFocus.focus();
    const c = cancelHook; cancelHook = null;
    if (!byButton && c) c();
  }
  // settle：按钮路径关闭，视为用户已做出选择，不触发取消钩子
  function settle(fn) { cancelHook = null; close(true); fn(); }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(false); });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !overlay.hidden) close(false);
    if (e.key === 'Tab' && !overlay.hidden) {
      const focusables = box.querySelectorAll('button, input, textarea, select, [tabindex]:not([tabindex="-1"])');
      if (!focusables.length) return;
      const first = focusables[0], last = focusables[focusables.length - 1];
      if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
      else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
    }
  });
  return { open, close: () => close(false), settle };
})();

// 应用内确认框（替代原生 confirm，避免弹出系统浏览器标题/地址）。
function confirmModal(message, title = '请确认', { okText = '确定', danger = false } = {}) {
  return new Promise((resolve) => {
    Modal.open(esc(title), (box) => {
      const p = el('p', 'modal-text');
      p.textContent = message;
      box.appendChild(p);
      const actions = el('div', 'modal-actions');
      const cancel = el('button', 'btn btn-secondary', '取消');
      const ok = el('button', danger ? 'btn btn-danger' : 'btn btn-primary', okText);
      cancel.addEventListener('click', () => Modal.settle(() => resolve(false)));
      ok.addEventListener('click', () => Modal.settle(() => resolve(true)));
      actions.appendChild(cancel); actions.appendChild(ok);
      box.appendChild(actions);
    }, () => resolve(false));
  });
}

// 应用内提示框（替代原生 alert）。
function alertModal(message, title = '提示') {
  return new Promise((resolve) => {
    Modal.open(esc(title), (box) => {
      const p = el('p', 'modal-text');
      p.textContent = message;
      box.appendChild(p);
      const actions = el('div', 'modal-actions');
      const ok = el('button', 'btn btn-primary', '我知道了');
      ok.addEventListener('click', () => Modal.settle(resolve));
      actions.appendChild(ok);
      box.appendChild(actions);
    }, resolve);
  });
}

// 应用内输入框（替代原生 prompt）。
function promptModal(label, oldValue = '', title = '请输入') {
  return new Promise((resolve) => {
    Modal.open(esc(title), (box) => {
      const lab = el('label', 'field-label', label);
      const input = el('input', 'input');
      input.value = oldValue || '';
      box.appendChild(lab); box.appendChild(input);
      const actions = el('div', 'modal-actions');
      const cancel = el('button', 'btn btn-secondary', '取消');
      const ok = el('button', 'btn btn-primary', '保存');
      const done = (val) => Modal.settle(() => resolve(val));
      cancel.addEventListener('click', () => done(null));
      ok.addEventListener('click', () => done(input.value.trim()));
      input.addEventListener('keydown', (e) => { if (e.isComposing || e.keyCode === 229) return; if (e.key === 'Enter') done(input.value.trim()); });
      actions.appendChild(cancel); actions.appendChild(ok);
      box.appendChild(actions);
      setTimeout(() => { input.focus(); input.select(); }, 0);
    }, () => resolve(null));
  });
}

/* ---------- 导航 ---------- */
const VIEW_LOADERS = { goals: loadGoals, skills: loadSkills, memory: initMemoryOnce, schedules: loadSchedules, tools: loadTools, settings: () => { loadSettingsProfile(); return loadSettings(); }, market: loadMarket, growth: loadGrowth, geo: loadGEO, readiness: loadReadiness, feedback: loadFeedbackView };

function showView(name) {
  document.querySelectorAll('.nav-item[data-view]').forEach((b) => {
    b.toggleAttribute('aria-current', b.dataset.view === name);
  });
  document.querySelectorAll('.view').forEach((v) => v.classList.remove('active'));
  document.documentElement.dataset.view = name;
  const view = $('#view-' + name);
  if (view) view.classList.add('active');
  (VIEW_LOADERS[name] || (() => {}))();
  // 选中折叠区里的项时，在「更多」上给一个激活小点
  const inMore = document.querySelector(`#nav-more-body .nav-item[data-view="${name}"]`);
  $('#nav-more').classList.toggle('has-active', !!inMore);
}

document.querySelectorAll('.nav-item[data-view]').forEach((btn) => {
  btn.addEventListener('click', () => {
    showView(btn.dataset.view);
    if (btn.closest('#nav-more-body')) setMoreOpen(false);
  });
});

// 图标轨道（≤1100px）把文字 span 整个隐掉了：display:none 的文本不参与可访问名计算，
// 于是 12 个导航按钮在读屏里全成了无名按钮，鼠标悬停也没有任何提示。
// 名字仍只写在 HTML 的 span 里，这里派生一次，避免两处各维护一份说法。
document.querySelectorAll('.nav-item').forEach((btn) => {
  if (btn.hasAttribute('aria-label')) return;
  const label = btn.querySelector(':scope > span:not(.nav-badge)');
  const text = label && label.textContent.trim();
  if (!text) return;
  btn.setAttribute('aria-label', text);
  btn.title = text;
});

// 「更多」折叠
function setMoreOpen(open) {
  const body = $('#nav-more-body');
  const toggle = $('#nav-more-toggle');
  body.hidden = !open;
  toggle.setAttribute('aria-expanded', String(open));
  $('#nav-more').classList.toggle('open', open);
}
$('#nav-more-toggle').addEventListener('click', (e) => {
  e.stopPropagation();
  setMoreOpen($('#nav-more-body').hidden);
});
// 与 + 菜单一致的收起契约：点击外部或 Esc 即收起
document.addEventListener('click', (e) => {
  if (!$('#nav-more-body').hidden && !e.target.closest('#nav-more')) setMoreOpen(false);
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && !$('#nav-more-body').hidden) setMoreOpen(false);
});

// 快捷任务卡：把模板作为可编辑草稿交给同一个提交流程。
document.querySelectorAll('.quick-card').forEach((card) => {
  card.addEventListener('click', () => {
    goalInput.value = card.dataset.prompt || '';
    const task = card.dataset.task;
    const taskBtn = document.querySelector(`#task-seg button[data-task="${task}"]`);
    if (taskBtn) taskBtn.click();
    autoResize();
    goalInput.focus();
  });
});

/* ---------- 连接状态（无左下角常驻显示，仅断线时轻提示） ---------- */
let connIsUp = true;
function setConn(state) {
  if (state === 'up') {
    connIsUp = true;
  } else if (state === 'down' && connIsUp) {
    connIsUp = false;
    toast('连接暂时中断，正在自动重连…', 'warning', 4000);
  }
}

/* ---------- 目标模式 ---------- */
const tasks = new Map();       // task_id -> { info, card }
let currentMode = 'auto';      // 安全模式：auto（完全访问）| plan_first（请我批准）
let currentTask = 'work';      // 任务模式：chat | work | code
let currentRole = 'general';

const PERM_FRIENDLY = { auto: '完全访问', plan_first: '请我批准', interactive: '请我批准' };
// 任务档位的人读名：与 #task-seg 的三个按钮一致。卡片徽标以前直接印枚举值，
// 刷新后从"完全访问 · 编程"变成 auto/plan_first，同一件事两种说法。
const TASK_LABEL = { chat: '对话', work: '工作', code: '编程' };

function setPerm(mode) {
  if (mode !== 'auto') {
    _doSetPerm(mode);
    return;
  }
  PermWarning.show().then((confirmed) => {
    if (!confirmed) {
      _doSetPerm(currentMode, false);
      return;
    }
    _doSetPerm(mode);
    toast('已切换到“完全访问”，高风险操作仍会请你批准', 'warning', 5000);
  });
}

$('#perm-seg').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-perm]');
  if (!btn) return;
  setPerm(btn.dataset.perm);
});

$('#task-seg').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-task]');
  if (!btn) return;
  currentTask = btn.dataset.task;
  document.querySelectorAll('#task-seg button').forEach((b) =>
    b.setAttribute('aria-pressed', String(b === btn)));
  // 对话模式与工作区/权限模式无关：隐藏以聚焦
  $('#ws-chip').hidden = currentTask === 'chat';
  $('#perm-seg').hidden = currentTask === 'chat';
  goalInput.placeholder = composerPlaceholder();
});

// 输入框占位：会话里统一写「继续当前会话…」，首页按任务档位给例子
function composerPlaceholder() {
  if (typeof viewingConvo !== 'undefined' && viewingConvo) return '继续当前会话…';
  return currentTask === 'chat'
    ? '随便聊点什么…'
    : currentTask === 'code'
      ? '描述要改的代码或要修的问题，例如：修复 smoke.sh 里的编码问题'
      : '一切从这里开始… 描述任务，或输入 @ 引用';
}

const goalInput = $('#goal-input');
goalInput.addEventListener('keydown', (e) => {
  if (e.isComposing || e.keyCode === 229) return;
  // L4：0 结果时 Esc 也要能关弹层（导航键仍需有候选项）
  if (!mentionPop.hidden && e.key === 'Escape') { onMentionKey(e); return; }
  if (!mentionPop.hidden && mentionState.items.length && (e.key === 'Enter' || e.key === 'Tab' || e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
    onMentionKey(e);
    return;
  }
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submitGoal(); }
});
// 输入框自适应高度
function autoResize() {
  goalInput.style.height = 'auto';
  goalInput.style.height = Math.min(goalInput.scrollHeight, 200) + 'px';
}
goalInput.addEventListener('input', autoResize);
setTimeout(autoResize, 0);
$('#goal-submit').addEventListener('click', submitGoal);

// 聊天区滚动到底部：只在用户本来就贴在底部时自动滚——
// 上滑翻历史时，进度/流式事件不该把人拽回底部（滚动劫持）。
let chatStickBottom = true;
(function bindChatStick() {
  const sc = $('#chat-scroll');
  if (!sc) return;
  sc.addEventListener('scroll', () => {
    chatStickBottom = sc.scrollHeight - sc.scrollTop - sc.clientHeight < 80;
  }, { passive: true });
})();
function scrollToBottom(smooth = true) {
  const sc = $('#chat-scroll');
  if (!sc || !chatStickBottom) return;
  sc.scrollTo({ top: sc.scrollHeight, behavior: smooth ? 'smooth' : 'auto' });
}

let PROVIDER = 'glm';
let API_KEY_SET = false;
// 提交去重：按钮 disabled 挡不住 textarea 的 Enter——连按两次会起两个任务。
let goalSubmitting = false;

async function submitGoal() {
  if (goalSubmitting) return;
  const displayGoal = goalInput.value.trim();
  if (displayGoal.length < 2) { toast('请先描述目标', 'error'); goalInput.focus(); return; }
  chatStickBottom = true; // 用户自己提交时始终跟到底部
  const btn = $('#goal-submit');
  goalSubmitting = true;
  btn.disabled = true;
  try {
    // 预检：真实模型但未配置 Key → 应用内弹窗引导，而不是提交后 401
    if (PROVIDER !== 'mock' && !API_KEY_SET) {
      const go = await confirmModal('还没有填写模型 API Key，现在不填的话 AI 无法回复。要现在去填写吗？（密钥只保存在本机，下次免填）', '需要配置模型密钥', { okText: '去填写' });
      if (go) {
        showView('settings');
        setTimeout(() => { switchSettingsTab?.('llm'); $('#set-api-key').focus(); }, 300);
      }
      return;
    }
    await ensureConvo();
    const submitted = await api('POST', '/api/goals', {
      goal: displayGoal,
      mode: currentMode,
      task_mode: currentTask, role: currentRole,
      conversation_id: currentConvo ? currentConvo.id : undefined,
      references: refs.map((r) => ({ kind: r.kind, label: r.label, value: r.refText })),
    });
    if (submitted.warning) { toast(submitted.warning, 'error', 8000); }
    const task_id = submitted.task_id;
    goalInput.value = '';
    autoResize();
    clearRefs();
    if (viewingConvo) {
      // 会话视图：渲染为对话气泡（工作/编程任务的执行详情可事后展开）
      appendLiveConvoTurn(displayGoal, task_id, currentTask);
    } else {
      const info = { task_id, goal: displayGoal, mode: currentMode, task_mode: currentTask, status: 'running', events: [] };
      renderTaskCard(info, true);
    }
    toast('目标已提交', 'success');
  } catch (err) {
    toast(`提交失败：${err.message}`, 'error');
  } finally {
    btn.disabled = false;
    goalSubmitting = false;
    goalInput.focus();
  }
}

/* ---------- 加号菜单 ---------- */
const plusBtn = $('#plus-btn');
const plusMenu = $('#plus-menu');

function togglePlus(open) {
  const show = open ?? plusMenu.hidden;
  plusMenu.hidden = !show;
  plusBtn.setAttribute('aria-expanded', String(show));
}
plusBtn.addEventListener('click', (e) => { e.stopPropagation(); togglePlus(); });
plusMenu.addEventListener('click', (e) => e.stopPropagation());
document.addEventListener('click', () => togglePlus(false));
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') togglePlus(false); });

plusMenu.addEventListener('click', (e) => {
  const item = e.target.closest('button[data-plus]');
  if (!item) return;
  togglePlus(false);
  const act = item.dataset.plus;
  if (act === 'file') openFilePicker();
  else if (act === 'folder') openWorkspaceDialog();
  else if (act === 'goal') openGoalPicker();
  else if (act === 'plan') setPerm(currentMode === 'plan_first' ? 'auto' : 'plan_first');
  else if (act === 'plugin') openPluginPicker();
  else if (act === 'mention') { goalInput.focus(); insertAtCursor('@'); }
  else if (act === 'browser') BrowserPane.toggle();
});

function insertAtCursor(text) {
  const ta = goalInput;
  const start = ta.selectionStart, end = ta.selectionEnd;
  ta.value = ta.value.slice(0, start) + text + ta.value.slice(end);
  ta.selectionStart = ta.selectionEnd = start + text.length;
  ta.dispatchEvent(new Event('input', { bubbles: true }));
}

/* ---------- 引用芯片 ---------- */
const refs = [];   // { id, kind, label, refText }
const refsBox = $('#composer-refs');

const REF_KIND_CN = { file: '文件', goal: '目标', skill: '技能', mcp: '插件', memory: '记忆' };

function renderRefs() {
  refsBox.hidden = refs.length === 0;
  refsBox.innerHTML = '';
  refs.forEach((r, i) => {
    const chip = el('span', 'ref-chip');
    chip.innerHTML = `<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">${r.kind === 'file' ? '<path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M13 2v7h7"/>' : r.kind === 'skill' ? '<path d="M13 2 4.5 13.5H11L10 22l8.5-11.5H12L13 2z"/>' : '<ellipse cx="12" cy="5.5" rx="8" ry="2.8"/><path d="M4 5.5V18c0 1.5 3.6 2.8 8 2.8s8-1.3 8-2.8V5.5"/>'}</svg><span class="ref-kind">${esc(REF_KIND_CN[r.kind] || r.kind)}</span><span class="ref-label" title="${esc(r.label)}">${esc(r.label)}</span>`;
    const x = el('button', 'ref-x');
    x.type = 'button';
    x.setAttribute('aria-label', '移除引用');
    x.innerHTML = '<svg viewBox="0 0 24 24" width="10" height="10" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12"/></svg>';
    x.addEventListener('click', () => { refs.splice(i, 1); renderRefs(); });
    chip.appendChild(x);
    refsBox.appendChild(chip);
  });
}

function addRef(kind, label, refText) {
  const id = kind + ':' + label;
  if (refs.some((r) => r.id === id)) return;
  refs.push({ id, kind, label, refText });
  renderRefs();
}

function clearRefs() { refs.length = 0; renderRefs(); }

function openGoalPicker() {
  Modal.open('引用一个历史目标', (box) => {
    const list = el('div', 'row-list');
    box.appendChild(el('p', 'field-hint', '引用目标只提供背景，不会自动重新执行原任务。'));
    box.appendChild(list);
    const all = [...tasks.values()];
    if (!all.length) list.innerHTML = '<p class="field-hint">还没有历史目标。</p>';
    all.forEach((t) => {
      const row = el('button', 'ws-row');
      row.type = 'button';
      row.innerHTML = `<div class="row-main"><div class="row-title">${esc(t.info.goal)}</div><div class="row-sub">${esc(STATUS_LABEL[t.info.status] || t.info.status)}</div></div>`;
      row.addEventListener('click', () => {
        addRef('goal', t.info.goal, '@goal:' + t.info.task_id);
        Modal.close();
      });
      list.appendChild(row);
    });
  });
}

async function openPluginPicker() {
  Modal.open('引用一个插件', (box) => {
    const list = el('div', 'row-list');
    box.appendChild(el('p', 'field-hint', '这里引用已安装的能力；需要新插件时前往市场安装。'));
    box.appendChild(list);
    Promise.all([api('GET', '/api/skills'), api('GET', '/api/mcp')]).then(([skills, mcp]) => {
      const items = [
        ...(skills.skills || []).map((s) => ({ kind: 'skill', name: s.name, sub: `${s.disabled ? '已停用 · ' : ''}${s.description || '技能'}` })),
        ...(mcp.mcp || []).map((s) => ({
          kind: 'plugin', name: s.name,
          // 三态要说清：停用的服务器连工具都没有，引用它只会让模型找不到能力
          sub: `${!s.enabled ? '已停用' : s.connected ? '已连接' : '未连接'} · ${s.tools || 0} 个工具`,
        })),
      ];
      if (!items.length) { list.innerHTML = '<p class="field-hint">还没有已安装插件或技能，可前往市场添加。</p>'; return; }
      items.forEach((item) => {
        const row = el('button', 'ws-row'); row.type = 'button';
        row.innerHTML = `<div class="row-main"><div class="row-title">${esc(item.name)}</div><div class="row-sub">${esc(item.sub)}</div></div><span class="badge badge--mode">${esc(item.kind)}</span>`;
        row.addEventListener('click', () => { addRef(item.kind, item.name, '@' + item.kind + ':' + item.name); Modal.close(); });
        list.appendChild(row);
      });
    }).catch(() => { list.innerHTML = '<p class="field-hint">插件列表暂时不可用，请稍后重试。</p>'; });
  });
}

/* 文件选择器：复用工作区浏览，选择文件而非目录 */
function openFilePicker() {
  Modal.open('选择要引用的文件', (box) => {
    const wrap = el('div');
    wrap.innerHTML = `
      <p class="field-hint" style="margin: 0 0 var(--space-3);">从工作区选择文件，选中后以 @ 引用插入目标。也可输入关键词模糊搜索。</p>
      <div class="field" style="margin-bottom: var(--space-3);">
        <label class="field-label">搜索文件名</label>
        <div style="display:flex; gap: var(--space-2);">
          <input class="input" id="fp-search" placeholder="如：report、*.md" autocomplete="off">
          <button class="btn btn-secondary btn-sm" id="fp-go">搜索</button>
        </div>
      </div>
      <div class="ws-crumb" id="fp-crumb"></div>
      <div id="fp-list" style="max-height: 280px; overflow: auto;"></div>`;
    box.appendChild(wrap);

    let cwd = '';
    async function browse(path) {
      const list = $('#fp-list');
      let ws;
      try { ws = await api('GET', '/api/workspace'); }
      catch (err) { list.innerHTML = `<p class="field-hint" style="margin:0;color:var(--color-destructive);">${esc(err.message)}</p>`; return; }
      cwd = path || ws.workspace || '.';
      const crumb = $('#fp-crumb');
      crumb.innerHTML = '';
      if (cwd !== (ws.workspace || '')) {
        const up = el('button', 'btn btn-secondary btn-sm', '上一级');
        up.addEventListener('click', () => browse(parentOf(cwd, ws.workspace)));
        crumb.appendChild(up);
      }
      crumb.appendChild(el('span', null, relPath(cwd, ws.workspace) || cwd));
      list.innerHTML = '<div class="skeleton" style="height:48px"></div>';
      try {
        const res = await api('POST', '/api/tools/call', { name: 'file.list', args: { path: cwd } });
        // file.list 返回 { path, count, entries: [...] }，entries 才是数组
        const entries = (res.output?.entries || []).filter((e) => !e.name.startsWith('.'));
        entries.sort((a, b) => (b.is_dir - a.is_dir) || a.name.localeCompare(b.name));
        list.innerHTML = '';
        if (!entries.length) list.innerHTML = '<p class="field-hint" style="margin:0;">此目录为空</p>';
        const base = cwd.replace(/[\/\\]+$/, '');
        entries.forEach((e) => {
          const full = base + (/[\/\\]/.test(base.slice(-1)) ? '' : '/') + e.name;
          const row = el('div', 'ws-row');
          row.innerHTML = `<div class="row-main"><div class="row-title"><span class="file-kind">${e.is_dir ? '目录' : '文件'}</span> ${esc(e.name)}</div>${e.is_dir ? '' : `<div class="row-sub">${esc(full)}</div>`}</div>`;
          row.addEventListener('click', () => { if (e.is_dir) browse(full); else { Modal.close(); addFileRef(full); } });
          list.appendChild(row);
        });
      } catch (err) { list.innerHTML = `<p class="field-hint" style="margin:0;color:var(--color-destructive);">${esc(err.message)}</p>`; }
    }

    function parentOf(path, root) {
      if (path === root) return root;
      const parts = path.replace(/[\/\\]+$/, '').split(/[\/\\]/);
      parts.pop();
      const parent = parts.join('/') || root;
      return parent.length >= (root || '').length ? parent : root;
    }

    async function search(q) {
      if (!q) { browse(''); return; }
      const ws = await api('GET', '/api/workspace');
      const root = ws.workspace || '.';
      try {
        const res = await api('POST', '/api/tools/call', { name: 'file.search', args: { root, pattern: '*' + q + '*' } });
        // file.search 返回 { root, pattern, count, files: [路径字符串] }
        const hits = res.output?.files || [];
        const list = $('#fp-list');
        $('#fp-crumb').innerHTML = `<span>搜索 "${esc(q)}" · ${hits.length} 个结果</span>`;
        list.innerHTML = '';
        if (!hits.length) { list.innerHTML = '<p class="field-hint" style="margin:0;">没有匹配文件</p>'; return; }
        hits.slice(0, 50).forEach((p) => {
          const name = String(p).replace(/[\/\\]+$/, '').split(/[\/\\]/).pop();
          const row = el('div', 'ws-row');
          row.innerHTML = `<div class="row-main"><div class="row-title"><span class="file-kind">文件</span> ${esc(name)}</div><div class="row-sub">${esc(p)}</div></div>`;
          row.addEventListener('click', () => { Modal.close(); addFileRef(p); });
          list.appendChild(row);
        });
      } catch (err) { toast('搜索失败：' + err.message, 'error'); }
    }

    $('#fp-go').addEventListener('click', () => search($('#fp-search').value.trim()));
    $('#fp-search').addEventListener('keydown', (e) => { if (e.key === 'Enter') search($('#fp-search').value.trim()); });

    const actions = el('div', 'modal-actions');
    const closeBtn = el('button', 'btn btn-secondary', '取消');
    closeBtn.type = 'button';
    closeBtn.addEventListener('click', Modal.close);
    actions.appendChild(closeBtn);
    box.appendChild(actions);

    browse('');
    setTimeout(() => $('#fp-search').focus(), 60);
  });
}

async function addFileRef(absPath) {
  // 工作区相对路径更短更可读
  let label = absPath;
  try {
    const ws = await api('GET', '/api/workspace');
    if (ws.workspace && absPath.startsWith(ws.workspace)) {
      const rel = absPath.slice(ws.workspace.length).replace(/^[\/\\]+/, '');
      if (rel) label = rel;
    }
  } catch { /* 用绝对路径兜底 */ }
  addRef('file', label, '@' + absPath);
  toast('已引用文件：' + label, 'success', 2500);
}

/* ---------- @ 自动补全 ---------- */
const mentionPop = $('#mention-pop');
let mentionState = { active: false, items: [], idx: -1, query: '' };

goalInput.addEventListener('input', (e) => onMentionInput());
goalInput.addEventListener('blur', () => setTimeout(() => closeMention(), 180));

function getMentionQuery() {
  const pos = goalInput.selectionStart;
  const before = goalInput.value.slice(0, pos);
  const m = before.match(/@([^\s@]*)$/);
  return m ? { at: m.index, q: m[1] } : null;
}

let mentionTimer = null;
let mentionRequest = 0;
function onMentionInput() {
  const mq = getMentionQuery();
  if (!mq) { closeMention(); return; }
  mentionState.query = mq.q;
  clearTimeout(mentionTimer);
  const request = ++mentionRequest;
  mentionTimer = setTimeout(() => fetchMentions(mq.q, request), 120);
}

async function fetchMentions(q, request) {
  const results = [];
  try {
    // 文件
    const ws = await api('GET', '/api/workspace');
    if (q.length >= 1) {
      const res = await api('POST', '/api/tools/call', { name: 'file.search', args: { root: ws.workspace || '.', pattern: '*' + q + '*' } });
      (res.output?.files || []).slice(0, 6).forEach((p) => {
        const name = String(p).replace(/[\/\\]+$/, '').split(/[\/\\]/).pop();
        results.push({ kind: 'file', label: relPath(p, ws.workspace), title: name, ref: '@' + p });
      });
    }
    // 技能
    if (q.length >= 0) {
      const { skills } = await api('GET', '/api/skills');
      (skills || []).filter((s) => s.name.toLowerCase().includes(q.toLowerCase())).slice(0, 4).forEach((s) => {
        results.push({ kind: 'skill', label: s.name, title: s.name, ref: '@skill:' + s.name });
      });
    }
    // 记忆
    if (q.length >= 2) {
      const { hits } = await api('GET', '/api/memory?q=' + encodeURIComponent(q));
      (hits || []).slice(0, 3).forEach((h) => {
        results.push({ kind: 'memory', label: shorten(h.content, 30), title: h.content, ref: '@memory:' + h.id });
      });
    }
  } catch { /* 静默降级 */ }
  if (request === mentionRequest && getMentionQuery()?.q === q) renderMentions(results);
}

function relPath(abs, ws) {
  // file.search 返回反斜杠路径、ws.workspace 是正斜杠——不归一 startsWith 永远失配，
  // @提及弹层就会把整条绝对路径怼到用户眼前（2026-09-23 QA 报告 M1）。
  const a = String(abs).replace(/\\/g, '/');
  const w = String(ws || '').replace(/\\/g, '/').replace(/\/+$/, '');
  if (w && a.startsWith(w + '/')) { const r = a.slice(w.length + 1); return r || abs; }
  return abs;
}

function renderMentions(items) {
  mentionState.items = items;
  mentionState.idx = items.length ? 0 : -1;
  if (!items.length) {
    mentionPop.innerHTML = '<div class="mention-empty">没有匹配项 — 直接输入路径或继续描述</div>';
    mentionPop.hidden = false;
    return;
  }
  // 分组
  const groups = {};
  items.forEach((it) => { (groups[it.kind] = groups[it.kind] || []).push(it); });
  const order = ['file', 'skill', 'memory'];
  const labels = { file: '文件', skill: '技能', memory: '记忆' };
  let flat = [];
  mentionPop.innerHTML = '';
  order.forEach((k) => {
    if (!groups[k]) return;
    const g = el('div', 'mention-group', labels[k]);
    mentionPop.appendChild(g);
    groups[k].forEach((it) => {
      const idx = flat.length;
      flat.push(it);
      const btn = el('button', 'mention-item');
      btn.type = 'button';
      btn.setAttribute('role', 'option');
      const icon = k === 'file' ? '<path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M13 2v7h7"/>' : k === 'skill' ? '<path d="M13 2 4.5 13.5H11L10 22l8.5-11.5H12L13 2z"/>' : '<ellipse cx="12" cy="5.5" rx="8" ry="2.8"/><path d="M4 5.5V18c0 1.5 3.6 2.8 8 2.8s8-1.3 8-2.8V5.5"/>';
      btn.innerHTML = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">${icon}</svg><span class="m-title">${esc(it.label)}</span><span class="m-sub">${esc(labels[k])}</span>`;
      btn.addEventListener('mousedown', (e) => e.preventDefault());
      btn.addEventListener('click', () => pickMention(it));
      mentionPop.appendChild(btn);
    });
  });
  mentionState.items = flat;
  mentionState.idx = 0;
  highlightMention();
  mentionPop.hidden = false;
}

function highlightMention() {
  const btns = mentionPop.querySelectorAll('.mention-item');
  btns.forEach((b, i) => b.setAttribute('aria-selected', String(i === mentionState.idx)));
  if (btns[mentionState.idx]) btns[mentionState.idx].scrollIntoView({ block: 'nearest' });
}

function onMentionKey(e) {
  if (mentionPop.hidden) return;
  if (e.key === 'Escape') { e.preventDefault(); closeMention(); return; }
  if (!mentionState.items.length) return;
  if (e.key === 'ArrowDown') { e.preventDefault(); mentionState.idx = (mentionState.idx + 1) % mentionState.items.length; highlightMention(); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); mentionState.idx = (mentionState.idx - 1 + mentionState.items.length) % mentionState.items.length; highlightMention(); }
  else if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); pickMention(mentionState.items[mentionState.idx]); }
}

function pickMention(it) {
  const mq = getMentionQuery();
  if (!mq) { closeMention(); return; }
  const before = goalInput.value.slice(0, mq.at);
  const after = goalInput.value.slice(goalInput.selectionStart);
  // 在 @ 引用后补一个空格，并把文件作为芯片
  goalInput.value = before + '@' + it.label + ' ' + after;
  const caret = (before + '@' + it.label + ' ').length;
  goalInput.selectionStart = goalInput.selectionEnd = caret;
  closeMention();
  goalInput.focus();
  // 所有引用都进入结构化请求；芯片只是让用户确认当前上下文。
  addRef(it.kind, it.label, it.ref);
}

function closeMention() {
  mentionRequest++;
  clearTimeout(mentionTimer);
  mentionPop.hidden = true;
  mentionState.items = [];
  mentionState.idx = -1;
}
/* 任务卡渲染 */
const STATUS_LABEL = { running: '运行中', success: '已完成', partial: '部分完成', failed: '失败', cancelled: '已取消' };
let currentGoalFilter = 'all';

// 审批卡只有在任务还活着时才算"需要处理"。任务已经定格（成功/失败/取消）却还留着一张
// "需要你的批准"，是在向用户要一个已经不存在的决定——超时自动拒绝、或在另一个窗口里批掉，
// 都会留下这种残留，而徽标、工作台标题、现场栏三处会同时跟着说谎。
function approvalCardOf(task) {
  if (!task || (task.info && task.info.status !== 'running')) return null;
  return task.card.querySelector('.approval-card');
}

function taskGroup(task) {
  if (approvalCardOf(task)) return 'attention';
  if (task.info.status === 'running') return 'active';
  if (task.info.status === 'failed' || task.info.status === 'partial') return 'attention';
  return 'done';
}

function refreshWorkbench() {
  const all = [...tasks.values()];
  const counts = { all: all.length, active: 0, done: 0, attention: 0 };
  all.forEach((task) => { counts[taskGroup(task)]++; });
  document.querySelectorAll('[data-count]').forEach((node) => {
    node.textContent = counts[node.dataset.count] || 0;
  });

  const attention = counts.attention;
  const active = counts.active;
  // 徽标数的是"还能决定的审批"，和上面 attention 用同一个判据（任务已定格的残留卡片不算）；
  // 工具页直调的审批没有任务归属，单独并入，否则这条计数会把它们漏掉。
  pendingApprovals = all.filter((task) => approvalCardOf(task)).length
    + document.querySelectorAll('.approval-card.standalone-approval').length;
  const approvalBadge = $('#approval-badge');
  approvalBadge.hidden = pendingApprovals === 0;
  approvalBadge.textContent = pendingApprovals;
  const title = $('#workbench-title');
  const detail = $('#workbench-detail');
  const dot = $('#workbench-dot');
  dot.dataset.state = attention ? 'attention' : active ? 'active' : 'idle';
  if (attention) {
    title.textContent = `${attention} 项需要处理`;
    detail.textContent = '有审批或异常任务值得查看';
  } else if (active) {
    title.textContent = `${active} 个目标正在推进`;
    detail.textContent = 'Gleam 会在需要你决定时提醒你';
  } else {
    title.textContent = all.length ? '当前任务已处理完毕' : '准备就绪';
    detail.textContent = all.length ? '可以开始下一个目标' : '可以开始一个新目标';
  }

  all.forEach((task) => {
    const group = taskGroup(task);
    task.card.hidden = currentGoalFilter !== 'all' && group !== currentGoalFilter;
  });
  const visible = all.filter((task) => !task.card.hidden).length;
  $('#goals-filtered-empty').hidden = all.length === 0 || visible > 0;

  const feed = $('#goal-feed');
  all.sort((a, b) => {
    const priority = (task) => approvalCardOf(task) ? 0
      : taskGroup(task) === 'active' ? 1
        : taskGroup(task) === 'attention' ? 2 : 3;
    return priority(a) - priority(b);
  }).forEach((task) => feed.appendChild(task.card));

  // 首屏三块指标与现场栏共用一次计算：两处各数一遍，迟早会给出两个数。
  // 「本周完成」按完成时刻算，没有 finished_at 的退回开始时刻——
  // 按提交时刻算会把一个上周提交、今早才跑完的任务记到上周去。
  const weekAgo = Date.now() - 7 * 864e5;
  const weekDone = all.filter((task) => {
    const info = task.info || {};
    if (info.status !== 'success' && info.status !== 'partial') return false;
    const at = Date.parse((info.result && info.result.finished_at) || info.started_at || '');
    return Number.isFinite(at) && at >= weekAgo;
  }).length;
  LiveRail.summary(counts, attention, weekDone);
}

$('#goal-filters').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-filter]');
  if (!button) return;
  currentGoalFilter = button.dataset.filter;
  document.querySelectorAll('#goal-filters button').forEach((item) =>
    item.setAttribute('aria-pressed', String(item === button)));
  refreshWorkbench();
});

function statusBadge(status) {
  const s = STATUS_LABEL[status] || status;
  return `<span class="badge badge--${esc(status)}">${esc(s)}</span>`;
}

function scoreRing(score) {
  const clamped = Math.max(0, Math.min(100, score));
  const cls = clamped >= 80 ? '' : clamped >= 40 ? 'score-ring--warn' : 'score-ring--bad';
  const offset = 100 - clamped;
  return `<svg class="score-ring ${cls}" viewBox="0 0 36 36" role="img" aria-label="完成度 ${clamped} / 100">
    <circle class="ring-bg" cx="18" cy="18" r="15.9"/>
    <circle class="ring-fg" cx="18" cy="18" r="15.9" stroke-dasharray="100" stroke-dashoffset="${offset}"/>
    <text x="18" y="18" text-anchor="middle" dy=".35em">${clamped}</text>
  </svg>`;
}

// mountStopButton 给运行中的任务一个明确的"停止"：目标是后台跑的（关掉页面不中断），
// 没有入口就只能等它自己跑完——一个会循环重试的目标可以一直烧 token。
// 停止入口挂在给定容器里。卡片和会话气泡都要能中止：会话模式下 .goal-card 是
// display:none 的，只在卡片上挂按钮等于运行中的对话没有停止按钮。
function mountStopButton(host, taskID, status) {
  if (!host) return;
  const btn = host.querySelector('[data-act="cancel"]');
  if (status !== 'running') { if (btn) btn.remove(); return; }
  if (btn) return;
  const stop = el('button', 'btn btn-ghost btn-sm goal-stop');
  stop.type = 'button';
  stop.dataset.act = 'cancel';
  stop.textContent = '停止';
  stop.title = '请求停止这个目标：已跑完的步骤保留，结果照常写回';
  stop.addEventListener('click', async () => {
    stop.disabled = true;
    try {
      await api('POST', '/api/goals/' + encodeURIComponent(taskID) + '/cancel');
      toast('已请求停止，收尾后会把结果写在原处', 'info');
    } catch (err) {
      stop.disabled = false;
      toast(`停止失败：${err.message}`, 'error');
    }
  });
  host.appendChild(stop);
}

function renderTaskCard(info, prepend) {
  // 去重：SSE 事件与 submit 响应可能并发渲染同一任务
  const existing = tasks.get(info.task_id);
  if (existing) {
    // 卡片可能因为切到会话视图而被摘出 feed：回来时必须重新挂上，
    // 否则运行中的任务只活在 tasks 里，屏幕上没有可以审批的那张卡。
    if (!existing.card.isConnected) $('#goal-feed').appendChild(existing.card);
    existing.info = info;
    const badge = existing.card.querySelector('.goal-meta .badge');
    if (badge) badge.outerHTML = statusBadge(info.status);
    mountStopButton(existing.card.querySelector('.goal-meta'), existing.info.task_id, existing.info.status);
    if (info.result) applyResult(info.task_id, info.result);
    return existing;
  }
  const card = el('article', 'card goal-card card--glass');
  card.dataset.taskId = info.task_id;

  const head = el('div', 'goal-head');
  const main = el('div');
  main.style.flex = '1';
  main.appendChild(el('p', 'goal-text', info.goal));
  const meta = el('div', 'goal-meta');
  // 后台触发（定时任务）没有安全模式记录，宁可缺这个徽标，也不替它编一个"请我批准"
  const modeLabel = PERM_FRIENDLY[info.mode] || '';
  const taskLabel = TASK_LABEL[info.task_mode] || '';
  const runLabel = [modeLabel, taskLabel].filter(Boolean).join(' · ');
  meta.innerHTML = `${statusBadge(info.status)}${runLabel ? `<span class="badge badge--mode">${esc(runLabel)}</span>` : ''}<span class="stat">${esc((info.task_id || '').slice(0, 8))}</span>`;
  main.appendChild(meta);
  head.appendChild(main);
  const ringSlot = el('div');
  ringSlot.dataset.role = 'ring';
  head.appendChild(ringSlot);
  card.appendChild(head);

  const timeline = el('div', 'timeline');
  timeline.dataset.role = 'timeline';
  timeline.setAttribute('role', 'log');
  card.appendChild(timeline);

  const errSlot = el('div'); errSlot.hidden = true; errSlot.dataset.role = 'error';
  const checkSlot = el('div'); checkSlot.hidden = true; checkSlot.dataset.role = 'checks';
  const changeSlot = el('div'); changeSlot.hidden = true; changeSlot.dataset.role = 'changes';
  const slots = { summary: el('div', 'goal-summary'), approval: el('div'), suggestion: el('div'), skill: el('div'), error: errSlot, checks: checkSlot, changes: changeSlot };
  card.appendChild(slots.approval);
  card.appendChild(slots.error);
  card.appendChild(slots.checks);
  card.appendChild(slots.changes);
  card.appendChild(slots.summary);
  card.appendChild(slots.suggestion);
  card.appendChild(slots.skill);

  const feed = $('#goal-feed');
  const empty = $('#goals-empty');
  if (empty) empty.remove();
  if (prepend && feed.firstChild) feed.insertBefore(card, feed.firstChild);
  else feed.appendChild(card);

  tasks.set(info.task_id, { info, card, slots });
  mountStopButton(card.querySelector('.goal-meta'), info.task_id, info.status);
  // 先回放历史事件（会重建工具执行折叠块），再定格结果——
  // 否则 applyResult 里的 settleRunSection 会在折叠块尚不存在时空跑，回放又把汇总刷成"进行中"
  (info.events || []).forEach((ev) => applyEvent(info.task_id, ev.type, ev.data));
  if (info.result) applyResult(info.task_id, info.result);
  refreshWorkbench();
  scrollToBottom(false);
  return tasks.get(info.task_id);
}

// renderChecks 渲染验收清单：做了什么承诺、逐条验到哪一步。
// "完成"应该是可核对的，而不是一个孤零零的分数——分数是生成者自己给的，清单是逐条判的。
function renderChecks(t, result) {
  const slot = t.slots && t.slots.checks;
  if (!slot) return;
  const acc = Array.isArray(result.acceptance) ? result.acceptance : [];
  const checks = Array.isArray(result.checks) ? result.checks : [];
  if (!acc.length && !checks.length) {
    slot.hidden = true;
    return;
  }
  const byName = new Map(checks.map((c) => [c.criterion, c]));
  const rows = (acc.length ? acc : checks.map((c) => c.criterion)).map((criterion) => {
    const c = byName.get(criterion) || {};
    const state = c.passed === true ? 'ok' : (c.passed === false ? 'miss' : 'unknown');
    const mark = state === 'ok' ? '✓' : (state === 'miss' ? '✗' : '·');
    const note = c.note ? `<div class="check-note">${esc(c.note)}</div>` : '';
    return `<li class="check-item check-item--${state}"><span class="check-mark">${mark}</span>
      <div><div class="check-text">${esc(criterion)}</div>${note}</div></li>`;
  });
  const passed = checks.filter((c) => c.passed === true).length;
  const head = checks.length ? `验收清单 ${passed}/${checks.length} 项通过` : '验收清单';
  slot.innerHTML = `<div class="checks"><div class="checks-head">${head}</div><ul class="check-list">${rows.join('')}</ul></div>`;
  slot.hidden = false;
}

// CH_KIND 把后端枚举译成人话（M3 的口径：界面不裸奔枚举）。
// 兜底用"动过"而不是把英文原样吐出去：后端加一种类型时，界面少一个分支是难免的，
// 但那一行仍然必须说清"这里动过一个路径"，只是说不出是哪一类。
const CH_KIND = { added: '新建', modified: '修改', deleted: '删除', dir: '建目录', moved: '移动', touched: '动过' };

function chBytes(n) {
  const v = Number(n);
  if (!Number.isFinite(v)) return '—';
  if (v < 1024) return v + ' B';
  if (v < 1024 * 1024) return (v / 1024).toFixed(1) + ' KB';
  return (v / 1048576).toFixed(1) + ' MB';
}

// renderChanges 渲染"本次改动"：动了哪些路径、现在多大、能不能退回去。
//
// 这一区存在的理由是**核对不了的那半边**：验收清单说"承诺的事做到了"，
// 但用户真正会问的是"我的盘上被写了什么、我能不能不认账"。
// 所以每一行都必须带一个可点的出口（对比 / 还原），退不回去的则直接把原因摆出来，
// 而不是留一个按下去什么也不会发生的按钮。
//
// 卡片与「执行详情」弹层共用 renderChangesInto：改动清单的形态只有一处定义，
// 两个入口各自渲染一份就会早晚漂移成两套说法。
function renderChanges(t, result) {
  const slot = t.slots && t.slots.changes;
  if (!slot) return;
  renderChangesInto(slot, t.info.task_id, result && result.changes);
}

function renderChangesInto(slot, taskID, raw) {
  const rows = Array.isArray(raw) ? raw : [];
  slot.innerHTML = '';
  if (!rows.length) {
    slot.hidden = true;
    return;
  }
  const box = el('div', 'changes');
  const head = el('div', 'changes-head');
  const canBack = rows.filter((c) => c.reversible).length;
  head.textContent = `本次改动 ${rows.length} 个路径`;
  const stat = el('span', 'stat', `可还原 ${canBack}/${rows.length}`);
  stat.title = '可还原 = 动这个文件之前留过一份内容；没留住的退不回去，也不假装退得回去';
  head.appendChild(document.createTextNode(' · '));
  head.appendChild(stat);
  box.appendChild(head);

  const list = el('ul', 'change-list');
  rows.forEach((c) => list.appendChild(changeRow(taskID, c)));
  box.appendChild(list);
  slot.appendChild(box);
  slot.hidden = false;
}

function changeRow(taskID, c) {
  const li = el('li', `change-item change-item--${CH_KIND[c.kind] ? c.kind : 'touched'}`);
  const kind = el('span', 'change-kind', CH_KIND[c.kind] || CH_KIND.touched);
  const path = el('span', 'change-path', relPath(c.path, spaceState.workspace));
  path.title = c.path + (c.tool ? ` · 由 ${c.tool} 改的` : '');
  const meta = el('span', 'change-meta');
  // 改动前多大 → 现在多大：只报一个数会漏掉"其实是覆盖写"这件事
  meta.textContent = c.prev_bytes ? `${chBytes(c.prev_bytes)} → ${chBytes(c.bytes)}` : chBytes(c.bytes);
  meta.title = c.ok === false && c.note ? `产物核对没过：${c.note}` : (c.note || '');
  li.append(kind, path, meta);

  const acts = el('div', 'change-acts');
  if (c.reverted) {
    acts.appendChild(el('span', 'change-done', '已还原'));
  }
  if (c.reversible) {
    const diff = el('button', 'btn btn-ghost btn-sm', '对比');
    diff.type = 'button';
    diff.addEventListener('click', () => openChangeDiff(taskID, c.path));
    const back = el('button', 'btn btn-ghost btn-sm', '还原');
    back.type = 'button';
    back.addEventListener('click', () => revertChange(taskID, c));
    acts.append(diff, back);
  } else if (!c.reverted) {
    const why = el('span', 'change-blocked', '不可还原');
    why.title = c.blocked || '写前内容未留存，无法还原';
    acts.appendChild(why);
  }
  li.appendChild(acts);
  return li;
}

// openChangeDiff 拉「写前 → 现在」的行级对比并就地展示。
//
// 弹层骨架**同步**建好（含关闭按钮和一行"加载中"），内容到了再填：
// Modal 的初始焦点在 buildContent 返回的那一刻就定了，异步建骨架会让这个弹层
// 一打开没有任何焦点，键盘用户按 Esc 之前那段路等于没有入口。
function openChangeDiff(taskID, path) {
  Modal.open('写前对比', (box) => {
    const wrap = el('div');
    const legend = el('div', 'diff-legend', '加载中…');
    const body = el('div');
    const actions = el('div', 'modal-actions');
    const close = el('button', 'btn btn-secondary', '关闭');
    close.type = 'button';
    close.addEventListener('click', Modal.close);
    actions.appendChild(close);
    wrap.append(legend, body, actions);
    box.appendChild(wrap);

    api('GET', `/api/goals/${encodeURIComponent(taskID)}/diff?path=${encodeURIComponent(path)}`)
      .then((d) => fillDiff(legend, body, d, path))
      .catch((err) => {
        legend.textContent = '';
        body.appendChild(el('div', 'modal-text', `拿不到对比：${err.message}`));
      });
  });
}

function fillDiff(legend, body, d, requestPath) {
  const diff = d.diff || {};
  const lines = Array.isArray(diff.lines) ? diff.lines : [];
  legend.textContent = `${relPath(d.path || requestPath, spaceState.workspace)}`
    + ` · ${chBytes(d.before_bytes)} → ${chBytes(d.after_bytes)}`
    + ` · +${diff.added || 0} −${diff.deleted || 0}`;
  if (!lines.length) {
    body.appendChild(el('div', 'modal-text', diff.note || '前后没有行级差异（内容一样，或只有体量变化）'));
    return;
  }
  const pre = el('div', 'diff');
  pre.setAttribute('role', 'region');
  pre.setAttribute('aria-label', '写前与现在的逐行对比');
  lines.forEach((l) => {
    const row = el('div', `diff-line diff-line--${l.kind}`);
    row.appendChild(el('span', 'diff-no', l.no ? String(l.no) : ''));
    row.appendChild(el('span', 'diff-sign', l.kind === 'add' ? '+' : (l.kind === 'del' ? '−' : '')));
    row.appendChild(el('span', 'diff-text', l.text));
    pre.appendChild(row);
  });
  body.appendChild(pre);
  if (diff.note) body.appendChild(el('div', 'diff-note', diff.note));
}

// revertChange 把一条路径退回到本次任务开始之前。
//
// 二次确认必须点名路径：这一按下去覆盖的是**用户自己的文件**，
// 而"取消"在界面上从来不该是危险动作。
async function revertChange(taskID, c) {
  const name = relPath(c.path, spaceState.workspace);
  const pair = c.kind === 'moved' ? '（一次移动涉及的两个路径会一起退回）' : '';
  const ok = await confirmModal(
    `把「${name}」退回到这次任务开始前的样子？${pair}当前内容不会保留。`,
    '还原改动', { okText: '还原', danger: true });
  if (!ok) return;
  try {
    const res = await api('POST', `/api/goals/${encodeURIComponent(taskID)}/revert`, { path: c.path });
    const notes = (res.reverted || []).map((r) => r.note).join('；');
    if (notes) toast(notes, 'success');
    (res.failed || []).forEach((f) => toast(`没能还原：${f}`, 'error', 9000));
    // 清单从服务端回来，不在前端拼"还原后应该长什么样"：
    // 同一件事有两处算法，就早晚会出现刷新前后两个说法。
    await refreshTaskResult(taskID);
  } catch (err) {
    toast(`还原失败：${err.message}`, 'error', 9000);
  }
}

// refreshTaskResult 重新拉一次任务详情并定格到卡片上（还原之后清单变了样）。
async function refreshTaskResult(taskID) {
  try {
    const info = await api('GET', '/api/goals/' + encodeURIComponent(taskID));
    if (info && info.result) applyResult(taskID, info.result);
  } catch (err) {
    toast(`改动已提交，但清单没刷新到最新：${err.message}`, 'error', 9000);
  }
}

/* 过程呈现（渐进披露）：
   旧版把每条进度都刷成「45% · …」长流水，噪声大。现在：
   - 阶段推进（规划/复盘/预算等）各占一句里程碑短句，同阶段连续事件原地更新；
   - 执行步骤明细收进折叠块，汇总句为「执行工具 N 次，其中 M 次失败」，结束后自动收起。 */
const PHASE_CN = { plan: '规划', execute: '执行', reflect: '复盘', budget: '预算' };

function demoteMilestone(live) {
  if (live && live.state === 'active' && live.el.isConnected) {
    live.el.className = 'timeline-item timeline-item--done';
    live.state = 'done';
  }
}

function renderProgressLine(t, tl, data) {
  const msg = (data.message || '').trim();
  if (data.phase === 'execute' && msg && !msg.startsWith('开始执行')) {
    addRunStep(t, tl, data, msg);
    return;
  }
  addMilestone(t, tl, data, msg);
}

function addMilestone(t, tl, data, msg) {
  demoteMilestone(t.msLive);
  const cn = PHASE_CN[data.phase] || data.phase;
  const state = data.kind === 'error' ? 'error' : data.kind === 'warn' ? 'warn' : 'active';
  const live = t.msLive;
  // 同阶段且仍是最末一行：原地更新，不重复刷屏
  if (live && live.phase === data.phase && tl.lastElementChild === live.el) {
    live.el.className = `timeline-item timeline-item--${state}`;
    live.el.innerHTML = `<span class="tl-kind">${esc(cn)}</span>${esc(msg || cn)}`;
    live.state = state;
    return;
  }
  const item = el('div', `timeline-item timeline-item--${state} timeline-item--new`);
  item.innerHTML = `<span class="tl-kind">${esc(cn)}</span>${esc(msg || cn)}`;
  tl.appendChild(item);
  while (tl.children.length > 40) tl.firstChild.remove();
  t.msLive = { phase: data.phase, el: item, state };
}

function ensureRunSection(t, tl) {
  if (t.run) return t.run;
  demoteMilestone(t.msLive);
  t.msLive = null;
  const section = el('details', 'tl-run');
  section.open = true;
  const sum = el('summary');
  const list = el('div', 'tl-run-list');
  section.append(sum, list);
  tl.appendChild(section);
  t.run = { section, sum, list, runs: 0, fails: 0 };
  paintRunSummary(t.run, true);
  return t.run;
}

function addRunStep(t, tl, data, msg) {
  const p = ensureRunSection(t, tl);
  if (msg.startsWith('✅')) p.runs++;
  else if (msg.startsWith('❌')) { p.runs++; p.fails++; }
  const item = el('div', `tl-run-item${data.kind === 'error' ? ' tl-run-item--error' : data.kind === 'warn' ? ' tl-run-item--warn' : ''}`);
  item.textContent = msg;
  p.list.appendChild(item);
  while (p.list.children.length > 80) p.list.firstChild.remove();
  paintRunSummary(p, true);
}

function paintRunSummary(p, running) {
  const bits = [p.runs ? `执行工具 ${p.runs} 次` : '正在执行…'];
  if (p.fails) bits.push(`其中 ${p.fails} 次失败`);
  // running 只代表「本次事件到达时仍在跑」；定格（settled）后不再回弹"进行中"——
  // 偶有 progress 事件晚于 completed 到达，否则已完成的卡会重新挂上"进行中"（2026-09-24 交互走查）
  p.sum.textContent = bits.join('，') + ((running && !p.settled && p.runs) ? ' · 进行中' : '');
}

// settleRunSection 任务结束后定格汇总；有失败时保持展开，方便直接看到红的那行
function settleRunSection(t) {
  if (!t.run) return;
  t.run.settled = true;
  t.run.section.open = t.run.fails > 0;
  paintRunSummary(t.run, false);
}

// ensureTask 保证目标卡存在（SSE 事件可能早于 submit 响应到达）。
// 并发调用合并为一次拉取；无法从服务端补拉时返回 null。
const ensureJobs = new Map();
async function ensureTask(taskID) {
  const cached = tasks.get(taskID);
  if (cached) return cached;
  if (ensureJobs.has(taskID)) return ensureJobs.get(taskID);
  const job = (async () => {
    try {
      const info = await api('GET', '/api/goals/' + taskID);
      return tasks.get(taskID) || renderTaskCard(info, true);
    } catch { return null; }
    finally { ensureJobs.delete(taskID); }
  })();
  ensureJobs.set(taskID, job);
  return job;
}

function applyEvent(taskID, type, data) {
  const t = tasks.get(taskID);
  if (!t) return;
  const tl = t.slots ? t.card.querySelector('[data-role="timeline"]') : null;
  if (!tl) return;
  if (type === 'progress') {
    // 对话模式：后端按 LLM 增量片段推送，直接累积流式渲染到回复区，不刷时间线噪声
    if (data.phase === 'chat') {
      // 定格之后不再收增量：晚到的片段会把已完成的回复重新刷成"进行中"
      if (t.info.status && t.info.status !== 'running') return;
      if (data.kind === 'llm' && data.message) {
        t.streamBuf = (t.streamBuf || '') + data.message;
        const sum = t.slots.summary;
        sum.classList.add('streaming');
        sum.dataset.typed = t.streamBuf;
        sum.innerHTML = renderMarkdown(t.streamBuf);
        scrollToBottom();
      }
      // chat 的 info（思考中/已回复）与 error 交给 completed 事件统一展示
      return;
    }
    renderProgressLine(t, tl, data);
    tl.scrollTop = tl.scrollHeight;
    scrollToBottom();
  } else if (type === 'approval') {
    renderApproval(t, data);
    scrollToBottom();
  } else if (type === 'suggestion') {
    const isGEO = typeof data.text === 'string' && data.text.startsWith('GEO 优化建议');
    const c = el('div', 'callout callout--suggestion');
    c.innerHTML = `${ICONS.bulb}<div class="callout-body"><div class="callout-title">${isGEO ? 'GEO 优化建议' : '主动提议'}</div>${esc(data.text)}</div>`;
    if (isGEO) {
      const go = el('button', 'btn btn-secondary btn-sm geo-goto', '在 GEO 板块查看');
      go.addEventListener('click', () => showView('geo'));
      c.querySelector('.callout-body').appendChild(go);
    }
    t.slots.suggestion.replaceChildren(c);
    scrollToBottom();
  } else if (type === 'suggest_skill') {
    renderSkillSuggestion(t, data);
    scrollToBottom();
  }
}

function renderApproval(t, ap) {
  // 幂等：同一 approval id 已渲染/已处理过就直接跳过（避免 SSE 重连 / ensureTask 重放导致重复）
  if (!ap || !ap.id) return;
  if (t.card.querySelector(`[data-approval-id="${ap.id}"]`)) return;
  if (t.resolvedApprovals && t.resolvedApprovals.has(ap.id)) return;
  // 已完成/已取消的任务不再渲染审批
  if (t.info.status && t.info.status !== 'running') return;

  const riskCls = ap.risk === 'high' ? 'risk-high' : 'risk-medium';
  const box = el('div', 'approval-card');
  box.dataset.approvalId = ap.id;
  box.innerHTML = `
    <div class="approval-head">${ICONS.alert}<span class="approval-title">需要你的批准</span><span class="badge badge--${riskCls}">${ap.risk === 'high' ? '高风险' : '中风险'}</span></div>
    <ul class="approval-list">${(ap.plan || []).map((p) => `<li>${esc(p)}</li>`).join('')}</ul>
    ${ap.reason ? `<div class="approval-reason">${esc(ap.reason)}</div>` : ''}
    <div class="approval-actions">
      <button class="btn btn-primary btn-sm" data-act="approve">${ICONS.check} 批准执行</button>
      <button class="btn btn-secondary btn-sm" data-act="deny">${ICONS.x} 拒绝</button>
    </div>`;
  box.querySelector('[data-act="approve"]').addEventListener('click', () => resolveApproval(ap.id, true, box, t));
  box.querySelector('[data-act="deny"]').addEventListener('click', () => resolveApproval(ap.id, false, box, t));
  t.slots.approval.replaceChildren(box);
  refreshWorkbench();
}

async function resolveApproval(id, approved, box, task) {
  try {
    await api('POST', `/api/approvals/${id}`, { approved, note: approved ? '' : '用户拒绝' });
    const note = el('div', 'approval-resolved', approved ? '✓ 已批准' : '✗ 已拒绝');
    box.classList.remove('approval-card');
    box.removeAttribute('data-approval-id');
    box.replaceChildren(note);
    if (task) {
      task.resolvedApprovals = task.resolvedApprovals || new Set();
      task.resolvedApprovals.add(id);
    }
    refreshWorkbench();
  } catch (err) {
    toast(err.message, 'error');
  }
}

// renderStandaloneApproval 渲染工具页直调产生的独立审批卡（无任务归属）。
// 挂在目标流顶部，批准/拒绝与任务内审批走同一裁决接口。
function renderStandaloneApproval(ap) {
  if (!ap || !ap.id) return;
  const feed = $('#goal-feed');
  if (feed.querySelector(`[data-approval-id="${ap.id}"]`)) return;

  const riskCls = ap.risk === 'high' ? 'risk-high' : 'risk-medium';
  const box = el('div', 'approval-card standalone-approval');
  box.dataset.approvalId = ap.id;
  box.innerHTML = `
    <div class="approval-head">${ICONS.alert}<span class="approval-title">工具调用需要批准</span><span class="badge badge--${riskCls}">${ap.risk === 'high' ? '高风险' : '中风险'}</span></div>
    <ul class="approval-list">${(ap.plan || []).map((p) => `<li>${esc(p)}</li>`).join('')}</ul>
    ${ap.reason ? `<div class="approval-reason">${esc(ap.reason)}</div>` : ''}
    <div class="approval-actions">
      <button class="btn btn-primary btn-sm" data-act="approve">${ICONS.check} 批准执行</button>
      <button class="btn btn-secondary btn-sm" data-act="deny">${ICONS.x} 拒绝</button>
    </div>`;
  box.querySelector('[data-act="approve"]').addEventListener('click', () => resolveApproval(ap.id, true, box, null));
  box.querySelector('[data-act="deny"]').addEventListener('click', () => resolveApproval(ap.id, false, box, null));
  feed.prepend(box);
  // 用户可能停留在其它视图，弹提示引导
  toast('工具调用需要你的批准，请前往目标视图处理', 'info');
}

let pendingApprovals = 0;
function updateApprovalBadge(delta) {
  pendingApprovals = Math.max(0, pendingApprovals + delta);
  const badge = $('#approval-badge');
  badge.hidden = pendingApprovals === 0;
  badge.textContent = pendingApprovals;
}

function applyResult(taskID, result) {
  const t = tasks.get(taskID);
  if (!t) return;
  t.info.status = result.status;
  t.info.result = result;
  // 任务定格了，卡片上还没裁决的审批就地失效：留着两个按钮等于邀请用户去点一个
  // 已经不存在的决定（在另一个窗口批掉、或超时被自动拒绝，都会走到这一步）。
  t.card.querySelectorAll('.approval-card').forEach((box) => {
    box.classList.remove('approval-card');
    box.removeAttribute('data-approval-id');
    box.replaceChildren(el('div', 'approval-resolved', '本轮已结束，未等到你的决定'));
  });
  const badge = t.card.querySelector('.goal-meta .badge');
  if (badge) badge.outerHTML = statusBadge(result.status);
  const ringSlot = t.card.querySelector('[data-role="ring"]');
  if (ringSlot && (result.status === 'success' || result.status === 'partial' || result.status === 'failed')) {
    ringSlot.innerHTML = scoreRing(result.score);
  }
  // 失败原因必须可见（此前 error 只存在于数据里，用户看不到）
  const errSlot = t.card.querySelector('[data-role="error"]');
  if (errSlot) {
    if (result.error) {
      // 标题跟着状态走：部分完成时叫"失败原因"会把人吓一跳，其实只是有几条验收没达标
      const title = result.status === 'partial' ? '未达标说明' : '失败原因';
      errSlot.innerHTML = `<div class="callout callout--error">${ICONS.alert}<div class="callout-body"><div class="callout-title">${title}</div>${esc(result.error)}</div></div>`;
      errSlot.hidden = false;
    } else {
      errSlot.hidden = true;
    }
  }
  // 用时统计（开始/结束时间齐全且已结束时显示）
  if (result.started_at && result.finished_at) {
    const dur = (new Date(result.finished_at) - new Date(result.started_at)) / 1000;
    if (Number.isFinite(dur) && dur >= 0) {
      const label = dur >= 90 ? `用时 ${Math.round(dur / 60)} 分钟` : `用时 ${dur.toFixed(1)} 秒`;
      const meta = t.card.querySelector('.goal-meta');
      let stat = meta.querySelector('[data-role="duration"]');
      if (!stat) {
        stat = el('span', 'stat');
        stat.dataset.role = 'duration';
        meta.appendChild(stat);
      }
      stat.textContent = label;
    }
  }
  // 消耗统计：这次任务花了多少（模型调用 + token + 工具执行）
  const u = result.usage;
  if (u && (u.llm_calls || u.tool_calls)) {
    const meta2 = t.card.querySelector('.goal-meta');
    let stat = meta2 && meta2.querySelector('[data-role="usage"]');
    if (meta2 && !stat) {
      stat = el('span', 'stat');
      stat.dataset.role = 'usage';
      meta2.appendChild(stat);
    }
    if (stat) {
      const tokens = (u.prompt_tokens || 0) + (u.completion_tokens || 0);
      const approx = (u.estimated_calls || 0) > 0 ? '≈' : '';
      const tools = u.tool_calls ? ` · ${u.tool_calls} 次工具` : '';
      stat.textContent = `${approx}${formatTokens(tokens)} tokens · ${u.llm_calls} 次调用${tools}`;
      // 缓存命中率：输入里有多少是复用服务端前缀缓存的（命中部分按折扣计价）。
      // 它反映提示词布局是否缓存友好；厂商不返回该字段时保持沉默，不假装是 0%。
      const cached = u.cached_tokens || 0;
      const rate = u.prompt_tokens > 0 && cached > 0
        ? `；缓存命中 ${Math.round((cached / u.prompt_tokens) * 100)}%（${formatTokens(cached)} tokens）`
        : '';
      stat.title = `输入 ${u.prompt_tokens} / 输出 ${u.completion_tokens} token`
        + (u.retries ? `；${u.retries} 次重试` : '')
        + rate
        + ((u.estimated_calls || 0) > 0 ? '（≈ 表示部分调用未返回用量，按字数估算）' : '');
    }
  }
  // 任务结束：工具执行折叠块定格汇总句并收起
  settleRunSection(t);
  renderChecks(t, result);
  // 改动清单与验收清单同时定格：前者答"我的盘上被写了什么、能不能不认账"，
  // 只在「执行详情」弹层渲染会让 README 那句"任务卡上列"落空。
  renderChanges(t, result);
  // 回复渲染：对话模式已流式实时输出的内容，收尾时定格为最终结果（失败则保留已流出部分），不再重播打字机
  const summaryEl = t.slots.summary;
  const text = result.summary || '';
  if (t.streamBuf != null) {
    const finalText = text || t.streamBuf;
    summaryEl.classList.remove('streaming');
    summaryEl.dataset.typed = finalText;
    summaryEl.innerHTML = renderMarkdown(finalText);
    t.streamBuf = null;
  } else if (summaryEl.dataset.typed !== text) {
    summaryEl.dataset.typed = text;
    typeInto(summaryEl, text);
  }
  const stopBtn = t.card.querySelector('[data-act="cancel"]');
  if (stopBtn) stopBtn.remove();
  refreshWorkbench();
  scrollToBottom();
}

// typeInto 打字机式渐进渲染文本（每 16ms 两个字符，视觉上像正在回复）。
function typeInto(elm, text) {
  if (!text || matchMedia('(prefers-reduced-motion: reduce)').matches) {
    elm.innerHTML = renderMarkdown(text);
    return;
  }
  if (elm._typer) clearInterval(elm._typer);
  elm.textContent = '';
  let i = 0;
  elm._typer = setInterval(() => {
    i = Math.min(text.length, i + 2);
    elm.textContent = text.slice(0, i);
    if (i >= text.length) {
      clearInterval(elm._typer);
      elm._typer = null;
      // 打字完成后用 Markdown 渲染替换纯文本
      elm.innerHTML = renderMarkdown(text);
    }
  }, 16);
}

// 极简 Markdown 渲染器：覆盖桌面聊天输出最常见语法。
// 支持：代码块/行内代码、加粗、斜体、删除线、标题、无序/有序列表、链接、引用、水平线、表格、段落。
// 输入文本先转义再按 Markdown 语法转 HTML，保证 XSS 安全。
function renderMarkdown(src) {
  if (!src) return '';
  // 1. 先按行切分，提取代码块（避免对代码块内容做行内转换）
  const blocks = [];
  let inCode = false;
  let codeLang = '';
  let codeBuf = [];
  let htmlParts = [];
  const pushBlock = (html) => { htmlParts.push(html); };

  const lines = src.split(/\r?\n/);
  let i = 0;
  let listStack = []; // [{tag:'ul'|'ol'}]
  let paraBuf = [];
  let quoteBuf = [];
  let tableBuf = [];

  const flushList = () => {
    while (listStack.length) {
      const { tag } = listStack.pop();
      htmlParts.push(`</${tag}>`);
    }
  };
  const flushPara = () => {
    if (!paraBuf.length) return;
    const text = paraBuf.join(' ');
    htmlParts.push(`<p>${inlineMd(text)}</p>`);
    paraBuf = [];
  };
  const flushQuote = () => {
    if (!quoteBuf.length) return;
    htmlParts.push(`<blockquote>${quoteBuf.map((q) => `<p>${inlineMd(q)}</p>`).join('')}</blockquote>`);
    quoteBuf = [];
  };
  const flushTable = () => {
    if (!tableBuf.length) return;
    const header = tableBuf[0];
    const aligns = tableBuf[1];
    // 不是合法表格（缺分隔行）：按普通段落输出，避免吞掉含 | 的正文
    if (tableBuf.length < 2 || !/^\|?[\s:|-]+\|?[\s:|-]*$/.test(aligns)) {
      tableBuf.forEach((row) => paraBuf.push(row));
      tableBuf = [];
      flushPara();
      return;
    }
    const cells = splitTableRow(header);
    const alignsCells = splitTableRow(aligns);
    const alignOf = (idx) => {
      const a = (alignsCells[idx] || '').trim();
      if (a.startsWith(':') && a.endsWith(':')) return 'center';
      if (a.endsWith(':')) return 'right';
      return 'left';
    };
    let t = '<table><thead><tr>';
    cells.forEach((c, idx) => { t += `<th style="text-align:${alignOf(idx)}">${inlineMd(c)}</th>`; });
    t += '</tr></thead><tbody>';
    for (let r = 2; r < tableBuf.length; r++) {
      t += '<tr>';
      splitTableRow(tableBuf[r]).forEach((c, idx) => {
        t += `<td style="text-align:${alignOf(idx)}">${inlineMd(c)}</td>`;
      });
      t += '</tr>';
    }
    t += '</tbody></table>';
    htmlParts.push(t);
    tableBuf = [];
  };

  for (; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    // 代码块围栏
    if (trimmed.startsWith('```')) {
      if (inCode) {
        htmlParts.push(`<pre><code class="lang-${escAttr(codeLang)}">${escHtml(codeBuf.join('\n'))}</code></pre>`);
        inCode = false;
        codeBuf = [];
        codeLang = '';
      } else {
        flushPara(); flushList(); flushQuote(); flushTable();
        inCode = true;
        codeLang = trimmed.slice(3).trim();
      }
      continue;
    }
    if (inCode) { codeBuf.push(line); continue; }

    // 空行：结束当前段落/引用/列表/表格
    if (!trimmed) {
      flushPara(); flushList(); flushQuote(); flushTable();
      continue;
    }

    // 表格行（简单检测：以 | 开头或包含 |）
    if (trimmed.includes('|') && !trimmed.startsWith('!')) {
      flushPara(); flushList(); flushQuote();
      tableBuf.push(trimmed);
      continue;
    } else if (tableBuf.length) {
      flushTable();
    }

    // 标题
    const hMatch = trimmed.match(/^(#{1,6})\s+(.+)$/);
    if (hMatch) {
      flushPara(); flushList(); flushQuote();
      const level = hMatch[1].length;
      pushBlock(`<h${level}>${inlineMd(hMatch[2])}</h${level}>`);
      continue;
    }

    // 水平线
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      flushPara(); flushList(); flushQuote();
      pushBlock('<hr>');
      continue;
    }

    // 引用
    if (trimmed.startsWith('>')) {
      flushPara(); flushList(); flushTable();
      quoteBuf.push(trimmed.replace(/^>\s?/, ''));
      continue;
    } else if (quoteBuf.length) {
      flushQuote();
    }

    // 无序列表
    const ulMatch = trimmed.match(/^[-*+]\s+(.+)$/);
    if (ulMatch) {
      flushPara(); flushQuote(); flushTable();
      if (!listStack.length || listStack[listStack.length - 1].tag !== 'ul') {
        flushList();
        listStack.push({ tag: 'ul' });
        htmlParts.push('<ul>');
      }
      htmlParts.push(`<li>${inlineMd(ulMatch[1])}</li>`);
      continue;
    }
    // 有序列表
    const olMatch = trimmed.match(/^\d+\.\s+(.+)$/);
    if (olMatch) {
      flushPara(); flushQuote(); flushTable();
      if (!listStack.length || listStack[listStack.length - 1].tag !== 'ol') {
        flushList();
        listStack.push({ tag: 'ol' });
        htmlParts.push('<ol>');
      }
      htmlParts.push(`<li>${inlineMd(olMatch[1])}</li>`);
      continue;
    }
    if (listStack.length && !ulMatch && !olMatch) {
      flushList();
    }

    // 普通段落
    paraBuf.push(trimmed);
  }
  flushPara(); flushList(); flushQuote(); flushTable();
  if (inCode && codeBuf.length) {
    htmlParts.push(`<pre><code class="lang-${escAttr(codeLang)}">${escHtml(codeBuf.join('\n'))}</code></pre>`);
  }
  return htmlParts.join('');
}

// splitTableRow 分割表格行（去掉首尾 | 后按 | 切分）。
function splitTableRow(row) {
  const trimmed = row.trim().replace(/^\|/, '').replace(/\|$/, '');
  return trimmed.split('|').map((c) => c.trim());
}

// inlineMd 处理行内 Markdown：代码、加粗、斜体、删除线、链接。
// 先转义 HTML，再按 Markdown 语法还原。
function inlineMd(text) {
  if (!text) return '';
  let s = escHtml(text);
  // 行内代码 `code`
  s = s.replace(/`([^`\n]+)`/g, (_, c) => `<code>${c}</code>`);
  // 图片 ![alt](url)
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)(?:\s+"([^"]*)")?\)/g, (_, alt, url, title) =>
    `<img src="${escAttr(safeHref(url))}" alt="${escAttr(alt)}"${title ? ` title="${escAttr(title)}"` : ''} loading="lazy">`);
  // 链接 [text](url)
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)(?:\s+"([^"]*)")?\)/g, (_, txt, url, title) =>
    `<a href="${escAttr(safeHref(url))}" target="_blank" rel="noopener noreferrer"${title ? ` title="${escAttr(title)}"` : ''}>${txt}</a>`);
  // 加粗 **text** / __text__
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/__([^_]+)__/g, '<strong>$1</strong>');
  // 斜体 *text* / _text_（避免误伤 ** 已处理后的内容）
  s = s.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>');
  s = s.replace(/(^|[^_])_([^_\n]+)_(?!_)/g, '$1<em>$2</em>');
  // 删除线 ~~text~~
  s = s.replace(/~~([^~]+)~~/g, '<del>$1</del>');
  return s;
}

function escHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}
function escAttr(s) {
  return escHtml(s).replace(/`/g, '&#96;');
}

// safeHref 只放行无副作用的链接协议。模型输出的 Markdown 直接进 href，
// javascript: / data: 会让一次回答变成任意代码执行；带协议的未知方案一律降级成 '#'。
function safeHref(url) {
  const u = String(url).trim();
  if (/^(?:https?:|mailto:|#|\/)/i.test(u)) return u;
  if (/^[a-z][a-z0-9+.\-]*:/i.test(u)) return '#';
  return u;
}

function renderSkillSuggestion(t, data) {
  const sk = data.skill;
  if (!sk) return;
  const c = el('div', 'callout callout--skill');
  const body = el('div', 'callout-body');
  body.innerHTML = `<div class="callout-title">可固化为技能「${esc(sk.name)}」</div>本次任务包含 ${(sk.steps || []).length} 个步骤，保存后可一键复用。`;
  const btn = el('button', 'btn btn-secondary btn-sm', '保存为技能');
  btn.addEventListener('click', () => openSkillSaveDialog(sk));
  c.appendChild(body);
  c.appendChild(btn);
  t.slots.skill.replaceChildren(c);
}

/* ---------- 启动：拉取历史任务 ---------- */
async function loadGoals() {
  // 「目标」是工作台的入口视图：从会话视图点回来时必须先拆掉对话态，
  // 否则命令面板和快捷卡还藏着的feed里留着上一段对话的气泡。
  if (viewingConvo) {
    setThreadMode(false);
    currentConvo = null;
    $('#goals-title').textContent = '目标';
    $('#goal-feed').innerHTML = '';
  }
  try {
    const { goals } = await api('GET', '/api/goals');
    (goals || []).slice().reverse().forEach((info) => renderTaskCard(info, false));
    if (!goals || !goals.length) {
      resetFeedToEmpty('不止于对话，把事做完', '上下文留在本机，Gleam 帮你一步步推进', { home: true });
    }
  } catch { /* 首次为空 */ }
}

/* ---------- SSE ---------- */
// sseData 一条坏帧不该杀掉整个监听器：onerror 之外没有重试，
// 半途抛出的 JSON.parse 会让这张任务卡永久停在"运行中"。
function sseData(e) {
  try { return JSON.parse(e.data); } catch { return null; }
}

function connectSSE() {
  const es = new EventSource('/api/events');
  es.onopen = () => setConn('up');
  es.onerror = () => setConn('down');
  es.addEventListener('progress', async (e) => {
    const d = sseData(e);
    if (!d || !d.task_id) return;
    // 现场栏先看一眼：它要在会话视图里也照常滚，所以放在会话分支之前
    LiveRail.observe(d);
    if (viewingConvo && convoLive.has(d.task_id)) {
      // 会话视图：对话模式 LLM 增量直接进气泡；其它进度仅保留思考态
      if (d.phase === 'chat' && d.kind === 'llm' && d.message) convoStream(d.task_id, d.message);
      return;
    }
    const t = await ensureTask(d.task_id);
    if (t) applyEvent(d.task_id, 'progress', d);
  });
  es.addEventListener('approval', async (e) => {
    const ap = sseData(e);
    if (!ap) return;
    LiveRail.approval(ap);
    // 工具页直调（无任务归属）的审批：渲染独立审批卡，否则用户无处裁决、只能等超时自动拒绝
    if (!ap.task_id) { renderStandaloneApproval(ap); return; }
    if (viewingConvo && convoLive.has(ap.task_id)) { convoApproval(ap); return; }
    const t = await ensureTask(ap.task_id);
    if (t) renderApproval(t, ap);
    else { pendingApprovals++; updateApprovalBadge(0); toast('有新的审批请求，请前往目标视图', 'info'); }
  });
  es.addEventListener('completed', async (e) => {
    const r = sseData(e);
    if (!r) return;
    LiveRail.result(r);
    // 候补目标跟着终态重算：后端刻意把「落盘」排在「广播」前面，此刻归档一定已经在盘上。
    // 不接这一条，用户刚修好的那个反复失败还会挂在卡上，直到他刷新页面。
    loadCues().catch(() => {});
    // 目标视图里已有这张卡（如定时任务触发）时必须先定格它——
    // 否则会话态下 completed 被对话分支接走，卡片会永远停在"运行中"
    if (tasks.has(r.task_id)) applyResult(r.task_id, r);
    if (viewingConvo) {
      convoComplete(r.task_id, r);
      if (r.status === 'success') toast(`目标完成（${r.score}/100）`, 'success');
      else if (r.status !== 'cancelled') toast(`目标${STATUS_LABEL[r.status] || r.status}（${r.score}/100）`, r.status === 'failed' ? 'error' : 'info');
      return;
    }
    await ensureTask(r.task_id);
    applyResult(r.task_id, r);
    if (r.status === 'success') toast(`目标完成（${r.score}/100）`, 'success');
    else if (r.status !== 'cancelled') toast(`目标${STATUS_LABEL[r.status] || r.status}（${r.score}/100）`, r.status === 'failed' ? 'error' : 'info');
  });
  // task_done 是后台任务（定时/变化触发）唯一的实时出口：它们不经过 /api/goals 提交，
  // 不接这个事件，一个每天跑、天天失败的任务可以静默失败到用户自己发现。
  es.addEventListener('task_done', async (e) => {
    const d = sseData(e);
    if (!d || !d.task_id) return;
    const ok = d.result && d.result.status === 'success';
    // title =「定时任务「X」+状态」，line =「状态：目标 · 完成度」——直接拼会把状态词说两遍。
    // 状态词就是 title 去掉 origin 后剩下的那截，用它把 line 的前缀剪掉。
    const state = d.title && d.origin ? d.title.slice(d.origin.length) : '';
    const body = state && d.line && d.line.startsWith(state + '：') ? d.line.slice(state.length + 1) : d.line;
    toast(`${d.title || '后台任务'}${body ? '：' + body : ''}`, ok ? 'success' : 'error', 8000);
    loadSchedules().catch(() => {});
    loadCues().catch(() => {});
    if (!viewingConvo) loadGoals().catch(() => {}); // 会话视图里别把用户踢回工作台
  });
  es.addEventListener('suggestion', async (e) => {
    const d = sseData(e);
    if (!d || !d.task_id) return;
    if (viewingConvo && !tasks.has(d.task_id)) return; // 会话视图暂不内联主动提议（目标视图已有卡片的仍要更新）
    await ensureTask(d.task_id);
    applyEvent(d.task_id, 'suggestion', d);
    // 创作产出自动分析完成后，静默刷新 GEO 板块（留档历史 + 统计）
    if (typeof d.text === 'string' && d.text.startsWith('GEO 优化建议')) {
      loadGEO().catch(() => {});
    }
  });
  es.addEventListener('suggest_skill', async (e) => {
    const d = sseData(e);
    if (!d || !d.task_id) return;
    if (viewingConvo && !tasks.has(d.task_id)) return; // 同上：不抢会话视图，但已存在的卡片要更新
    await ensureTask(d.task_id);
    applyEvent(d.task_id, 'suggest_skill', d);
  });
}

/* ---------- 技能 ---------- */
async function loadSkills() {
  const grid = $('#skills-grid');
  grid.innerHTML = '<div class="skeleton" style="height:80px"></div><div class="skeleton" style="height:80px"></div>';
  try {
    const { skills } = await api('GET', '/api/skills');
    grid.innerHTML = '';
    if (!skills || !skills.length) {
      grid.innerHTML = `<div class="empty">${ICONS.zap}<div class="empty-title">还没有技能</div><p class="empty-desc">完成一次多步骤任务后，Gleam 会主动建议把流程固化为技能；也可以去市场直接安装。</p><button class="btn btn-secondary btn-sm" id="skills-empty-market">去市场看看</button></div>`;
      const go = grid.querySelector('#skills-empty-market');
      if (go) go.addEventListener('click', () => showView('market'));
      return;
    }
    skills.forEach((sk) => grid.appendChild(skillCard(sk)));
  } catch (err) {
    loadError(grid, err, loadSkills);
  }
}

function skillCard(sk) {
  const card = el('div', 'card');
  const head = el('div', 'row');
  head.style.padding = '0';
  const main = el('div', 'row-main');
  main.innerHTML = `<div class="row-title">${esc(sk.name)} <span class="badge badge--version">v${sk.version}</span>
    ${sk.disabled ? '<span class="badge badge--cancelled">已停用</span>' : ''}</div>
    <div class="row-sub">${esc(sk.description || '')}</div>
    <div class="stat">运行 ${sk.runs} 次 · 成功 ${sk.successes} · ${(sk.steps || []).length} 步${sk.params && sk.params.length ? ' · 参数: ' + esc(sk.params.join(', ')) : ''}</div>`;
  const actions = el('div', 'row-actions');
  if (!sk.disabled) {
    const runBtn = el('button', 'btn btn-primary btn-sm');
    runBtn.innerHTML = ICONS.play + ' 运行';
    runBtn.addEventListener('click', () => openSkillRunDialog(sk));
    actions.appendChild(runBtn);
  }
  // 停用而不是删除：技能是用户攒下来的做法，临时不想让它被引用时，不该连步骤一起扔
  const toggle = el('button', 'btn btn-ghost btn-sm');
  toggle.type = 'button';
  toggle.textContent = sk.disabled ? '启用' : '停用';
  toggle.title = sk.disabled ? '启用后重新进入技能清单，可被引用与运行' : '停用后保留内容与统计，但不再进技能清单';
  toggle.setAttribute('aria-label', `${sk.disabled ? '启用' : '停用'}技能 ${sk.name}`);
  toggle.addEventListener('click', async () => {
    toggle.disabled = true;
    try {
      await api('POST', `/api/skills/${encodeURIComponent(sk.name)}/enabled`, { enabled: sk.disabled });
      toast(sk.disabled ? `技能「${sk.name}」已启用` : `技能「${sk.name}」已停用`, 'success');
      loadSkills();
    } catch (err) {
      toast(err.message, 'error');
      toggle.disabled = false;
    }
  });
  actions.appendChild(toggle);
  const delBtn = el('button', 'btn btn-danger btn-sm');
  delBtn.innerHTML = ICONS.trash;
  delBtn.setAttribute('aria-label', `删除技能 ${sk.name}`);
  delBtn.addEventListener('click', async () => {
    if (!await confirmModal(`删除技能「${sk.name}」？此操作不可恢复。`, '删除技能', { okText: '删除', danger: true })) return;
    try { await api('DELETE', `/api/skills/${encodeURIComponent(sk.name)}`); toast('技能已删除', 'success'); loadSkills(); }
    catch (err) { toast(err.message, 'error'); }
  });
  actions.appendChild(delBtn);
  head.appendChild(main);
  head.appendChild(actions);
  card.appendChild(head);
  return card;
}

function openSkillRunDialog(sk) {
  Modal.open(`运行技能「${esc(sk.name)}」`, (box) => {
    const form = el('form');
    (sk.params || []).forEach((p) => {
      const f = el('div', 'field');
      f.innerHTML = `<label class="field-label" for="p-${esc(p)}">${esc(p)}</label>`;
      const input = el('input', 'input');
      input.id = 'p-' + p;
      input.name = p;
      f.appendChild(input);
      form.appendChild(f);
    });
    if (!(sk.params || []).length) form.appendChild(el('p', 'field-hint', '该技能无需参数。'));
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const run = el('button', 'btn btn-primary', '执行');
    run.innerHTML = ICONS.play + ' 执行';
    actions.appendChild(cancel);
    actions.appendChild(run);
    form.appendChild(actions);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      run.disabled = true;
      run.innerHTML = ICONS.spinner + ' 执行中';
      const params = {};
      (sk.params || []).forEach((p) => { params[p] = form.elements[p].value; });
      try {
        const out = await api('POST', `/api/skills/${encodeURIComponent(sk.name)}/run`, { params });
        Modal.close();
        toast(`技能执行${out.status === 'success' ? '成功' : '结束'}（${out.summary || ''}）`, out.status === 'success' ? 'success' : 'info', 6000);
      } catch (err) {
        toast(`执行失败：${err.message}`, 'error');
        run.disabled = false;
        run.innerHTML = ICONS.play + ' 执行';
      }
    });
    box.appendChild(form);
  });
}

function openSkillSaveDialog(sk) {
  Modal.open(`保存技能「${esc(sk.name)}」`, (box) => {
    const form = el('form');
    const nameF = el('div', 'field');
    nameF.innerHTML = `<label class="field-label" for="sk-name">技能名</label>`;
    const nameInput = el('input', 'input');
    nameInput.id = 'sk-name';
    nameInput.value = sk.name;
    nameF.appendChild(nameInput);
    form.appendChild(nameF);
    const descF = el('div', 'field');
    descF.innerHTML = `<label class="field-label" for="sk-desc">描述</label>`;
    const descInput = el('input', 'input');
    descInput.id = 'sk-desc';
    descInput.value = sk.description || '';
    descF.appendChild(descInput);
    form.appendChild(descF);
    const preview = el('pre');
    preview.textContent = JSON.stringify(sk.steps, null, 2);
    form.appendChild(preview);
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const save = el('button', 'btn btn-primary', '保存');
    actions.appendChild(cancel);
    actions.appendChild(save);
    form.appendChild(actions);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const skillName = nameInput.value.trim();
      if (!skillName) { toast('技能名不能为空', 'error'); nameInput.focus(); return; }
      if (save.disabled) return; // 连点两次会覆盖保存或报重名
      save.disabled = true;
      try {
        await api('POST', '/api/skills', {
          name: skillName, description: descInput.value.trim(), steps: sk.steps,
        });
        Modal.close();
        toast(`技能「${skillName}」已保存`, 'success');
      } catch (err) {
        toast(`保存失败：${err.message}`, 'error');
      } finally {
        save.disabled = false;
      }
    });
    box.appendChild(form);
  });
}

/* ---------- 记忆 ---------- */
let memoryInited = false;
function initMemoryOnce() {
  if (memoryInited) return;
  memoryInited = true;
  $('#memory-save').addEventListener('click', async () => {
    const btn = $('#memory-save');
    const content = $('#memory-content').value.trim();
    if (!content) { toast('先写下想让它记住的事', 'error'); return; }
    if (btn.disabled) return;
    btn.disabled = true;
    try {
      await api('POST', '/api/memory', { content });
      $('#memory-content').value = '';
      toast('已写入长期记忆', 'success');
      searchMemory();
    } catch (err) { toast(err.message, 'error'); }
    finally { btn.disabled = false; }
  });
  $('#memory-search').addEventListener('keydown', (e) => { if (e.key === 'Enter') searchMemory(); });
}

async function searchMemory() {
  const q = $('#memory-search').value.trim() || $('#memory-content').value.trim();
  const hits = $('#memory-hits');
  if (!q) {
    hits.innerHTML = `<div class="empty empty--card">${ICONS.search}<div class="empty-title">搜索你的长期记忆</div><p class="empty-desc">在右上角输入关键词回车即可；上方也可以直接写入一条新记忆。</p></div>`;
    return;
  }
  try {
    const { hits: list } = await api('GET', `/api/memory?q=${encodeURIComponent(q)}&k=8`);
    hits.innerHTML = '';
    if (!list || !list.length) {
      hits.innerHTML = `<div class="empty empty--card">${ICONS.search}<div class="empty-title">没有相关记忆</div><p class="empty-desc">换个关键词试试，或在上方写入这条你想让它记住的事。</p></div>`;
      return;
    }
    list.forEach((h) => {
      const row = el('div', 'row');
      row.style.padding = 'var(--space-2) 0';
      // score 是词法向量相似度（0–1），裸数字没人看得懂：换算成相关度百分比并解释口径
      const pct = Math.round(Math.max(0, Math.min(1, Number(h.score) || 0)) * 100);
      const main = el('div', 'row-main');
      main.innerHTML = `<div class="row-sub" style="font-size: var(--fs-md); color: var(--color-fg);">${esc(h.content)}</div>
        ${h.tags && h.tags.length ? `<div class="row-sub">${h.tags.map((t) => '#' + esc(t)).join(' ')}</div>` : ''}`;
      const score = el('span', 'hit-score');
      score.textContent = `相关度 ${pct}%`;
      score.title = '这条记忆与搜索词的相关度（词法相似度换算，同义词不算相关），越高越相关';
      const del = el('button', 'btn btn-ghost btn-sm mem-del');
      del.type = 'button';
      del.innerHTML = ICONS.trash;
      del.title = '删除这条记忆（不再参与检索）';
      del.setAttribute('aria-label', `删除记忆：${(h.content || '').slice(0, 20)}`);
      del.addEventListener('click', async () => {
        if (!await confirmModal(`删除这条记忆？\n「${(h.content || '').slice(0, 60)}」\n\n删除后不再参与检索。`, '删除记忆', { okText: '删除', danger: true })) return;
        try { await api('DELETE', `/api/memory/${encodeURIComponent(h.id)}`); toast('记忆已删除', 'success'); searchMemory(); }
        catch (err) { toast(err.message, 'error'); }
      });
      row.appendChild(score);
      row.appendChild(main);
      row.appendChild(del);
      hits.appendChild(row);
    });
  } catch (err) { toast(err.message, 'error'); }
}

/* ---------- 定时任务 ---------- */
async function loadSchedules() {
  const list = $('#schedules-list');
  list.innerHTML = '<div class="skeleton" style="height:56px"></div>';
  try {
    const { jobs } = await api('GET', '/api/schedules');
    LiveRail.schedules((jobs || []).length);
    list.innerHTML = '';
    if (!jobs || !jobs.length) {
      list.innerHTML = `<div class="empty empty--card"><div class="empty-hero" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/></svg></div><div class="empty-title">暂无定时任务</div><p class="empty-desc">创建一个任务，让 Gleam 按时自动执行目标。</p></div>`;
      setSchFormOpen(true);
      return;
    }
    jobs.forEach((j, i) => { const c = scheduleCard(j); c.dataset.order = String(i); list.appendChild(c); });
    applySchView();
  } catch (err) { loadError(list, err, loadSchedules); }
}


// 定时任务卡片：开关 · 标题 + 状态 · 目标预览 · 时间胶囊 · 更多（删除）
function scheduleCard(j) {
  const card = el('article', 'auto-card' + (j.enabled ? '' : ' is-off'));
  card.dataset.enabled = String(!!j.enabled);
  card.dataset.name = j.name || '';
  card.dataset.next = j.next_run && j.enabled ? String(new Date(j.next_run).getTime()) : '';
  const head = el('div', 'auto-card-head');
  const sw = el('button', 'switch');
  sw.type = 'button';
  sw.setAttribute('role', 'switch');
  sw.setAttribute('aria-checked', String(!!j.enabled));
  sw.setAttribute('aria-label', `${j.enabled ? '暂停' : '恢复'}定时任务 ${j.name}`);
  sw.title = j.enabled ? '暂停后到点不再执行，随时可恢复' : '恢复按原计划执行';
  sw.addEventListener('click', async () => {
    sw.disabled = true;
    try { await api('POST', `/api/schedules/${encodeURIComponent(j.name)}/enabled`, { enabled: !j.enabled }); toast(j.enabled ? '任务已暂停' : '任务已恢复', 'success'); loadSchedules(); }
    catch (err) { toast(err.message, 'error'); sw.disabled = false; }
  });
  const more = el('button', 'icon-btn auto-card-more');
  more.type = 'button';
  more.title = '更多';
  more.setAttribute('aria-label', `更多操作：${j.name}`);
  more.setAttribute('aria-haspopup', 'menu');
  more.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="5" cy="12" r="1.4"/><circle cx="12" cy="12" r="1.4"/><circle cx="19" cy="12" r="1.4"/></svg>';
  const menu = el('div', 'pop-menu auto-card-menu');
  menu.hidden = true;
  menu.setAttribute('role', 'menu');
  const del = el('button', 'menu-item menu-item--danger');
  del.type = 'button';
  del.setAttribute('role', 'menuitem');
  del.innerHTML = ICONS.trash + '<span>删除</span>';
  del.addEventListener('click', async () => {
    menu.hidden = true;
    const ok = await confirmModal(`删除定时任务「${j.name}」？此操作不可恢复。`, '删除任务');
    if (!ok) return;
    try { await api('DELETE', `/api/schedules/${encodeURIComponent(j.name)}`); toast('已删除', 'success'); loadSchedules(); }
    catch (err) { toast(err.message, 'error'); }
  });
  menu.appendChild(del);
  more.addEventListener('click', (e) => {
    e.stopPropagation();
    document.querySelectorAll('.auto-card-menu').forEach((m) => { if (m !== menu) m.hidden = true; });
    menu.hidden = !menu.hidden;
  });
  head.appendChild(sw);
  head.appendChild(more);
  head.appendChild(menu);

  const title = el('div', 'auto-card-title');
  title.innerHTML = `<strong>${esc(j.name)}</strong><span class="auto-card-state">${j.enabled ? '已启用' : '已停用'}</span>`;
  const goal = el('p', 'auto-card-goal', j.goal || '');
  goal.title = j.goal || '';
  const freq = j.schedule_text || j.when_text || (j.interval_sec ? '每隔 ' + humanInterval(j.interval_sec) : '按计划执行');
  const foot = el('div', 'auto-card-foot');
  foot.innerHTML = `<span class="pill"><svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/></svg>${esc(freq)}</span>`
    + (j.next_run && j.enabled ? `<span class="auto-card-next">下次 ${esc(new Date(j.next_run).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }))}</span>` : '');
  card.appendChild(head);
  card.appendChild(title);
  card.appendChild(goal);
  card.appendChild(foot);
  return card;
}
document.addEventListener('click', () => document.querySelectorAll('.auto-card-menu').forEach((m) => { m.hidden = true; }));

// 定时任务列表的筛选（全部 / 已启用 / 已停用）与排序：只在前端重排已拉到的卡片
let schFilter = 'all';
function applySchView() {
  const list = $('#schedules-list');
  const cards = [...list.querySelectorAll(':scope > .auto-card')];
  const by = $('#sch-sort').value;
  cards.sort((a, b) => {
    if (by === 'name') return a.dataset.name.localeCompare(b.dataset.name, 'zh-CN');
    if (by === 'next') {
      const x = Number(a.dataset.next) || Infinity; const y = Number(b.dataset.next) || Infinity;
      if (x !== y) return x - y;
    }
    return Number(a.dataset.order) - Number(b.dataset.order);
  });
  cards.forEach((c) => {
    c.hidden = schFilter !== 'all' && (c.dataset.enabled === 'true') !== (schFilter === 'on');
    list.appendChild(c);
  });
}
document.querySelectorAll('#sch-filter [data-sfilter]').forEach((b) => b.addEventListener('click', () => {
  schFilter = b.dataset.sfilter;
  document.querySelectorAll('#sch-filter [data-sfilter]').forEach((x) => {
    x.classList.toggle('active', x === b);
    x.setAttribute('aria-selected', String(x === b));
  });
  applySchView();
}));
$('#sch-sort').addEventListener('change', applySchView);

function setSchFormOpen(open) {
  const form = $('#sch-form');
  const btn = $('#sch-new-toggle');
  form.hidden = !open;
  btn.setAttribute('aria-expanded', String(open));
  if (open) setTimeout(() => $('#sch-name').focus(), 0);
}
$('#sch-new-toggle').addEventListener('click', () => setSchFormOpen($('#sch-form').hidden));

// 自然语言时间芯片：点击填充并高亮，手动编辑则取消高亮。
const whenInput = $('#sch-when');
document.querySelectorAll('#sch-when-chips .when-chip').forEach((chip) => {
  chip.addEventListener('click', () => {
    whenInput.value = chip.dataset.when;
    document.querySelectorAll('#sch-when-chips .when-chip').forEach((c) => c.classList.remove('active'));
    chip.classList.add('active');
    whenInput.focus();
  });
});
whenInput.addEventListener('input', () => {
  document.querySelectorAll('#sch-when-chips .when-chip').forEach((c) => {
    c.classList.toggle('active', c.dataset.when === whenInput.value.trim());
  });
});

$('#sch-create').addEventListener('click', async () => {
  const btn = $('#sch-create');
  const name = $('#sch-name').value.trim();
  const goal = $('#sch-goal').value.trim();
  const when = $('#sch-when').value.trim();
  const interval = parseInt($('#sch-interval').value, 10) || 0;
  if (!name || !goal) { toast('任务名与“到点要做什么”都要填哦', 'error'); return; }
  // 任务名会进 REST 路径：带斜杠的名字创建得出来，却永远停不掉、删不掉
  if (/[/\\]/.test(name)) { toast('任务名不能含 / 或 \\，换一个说法即可', 'error'); $('#sch-name').focus(); return; }
  if (!when && !interval) { toast('请用一句话说说执行时间，例如“每天早上9点”', 'error'); return; }
  btn.disabled = true;
  try {
    await api('POST', '/api/schedules', { name, goal, when, interval_sec: interval });
    toast('定时任务已创建', 'success');
    $('#sch-name').value = ''; $('#sch-goal').value = ''; $('#sch-when').value = ''; $('#sch-interval').value = '';
    document.querySelectorAll('#sch-when-chips .when-chip').forEach((c) => c.classList.remove('active'));
    loadSchedules();
  } catch (err) { toast(err.message, 'error'); }
  finally { btn.disabled = false; }
});

// 把秒数转成小白可读的间隔。
function humanInterval(sec) {
  sec = Number(sec) || 0;
  if (sec >= 86400 && sec % 86400 === 0) return (sec / 86400) + ' 天';
  if (sec >= 3600 && sec % 3600 === 0) return (sec / 3600) + ' 小时';
  if (sec >= 60 && sec % 60 === 0) return (sec / 60) + ' 分钟';
  return sec + ' 秒';
}

/* ---------- 工具 ---------- */
async function loadTools() {
  const list = $('#tools-list');
  list.innerHTML = '<div class="skeleton" style="height:56px"></div>';
  try {
    const { tools } = await api('GET', '/api/tools');
    list.innerHTML = '';
    tools.forEach((t) => {
      const row = el('div', 'card row');
      const main = el('div', 'row-main');
      main.innerHTML = `<div class="row-title"><code class="tool-name">${esc(t.name)}</code>
        <select class="input select-sm perm-select" data-tool="${esc(t.name)}" title="权限级别：只读自动放行 / 需我批准 / 完全访问（始终审批）">
          <option value="readonly">只读放行</option>
          <option value="user_approved">需我批准</option>
          <option value="full_access">完全访问</option>
          <option value="default">内置默认${t.overridden ? '（当前 ' + esc(PERM_LABELS[t.permission] || t.permission) + '）' : ''}</option>
        </select>
        ${t.overridden ? '<span class="badge badge--mode">已覆盖</span>' : ''}</div>
        <div class="row-sub">${esc(t.description)}</div>`;
      const details = el('details', 'schema');
      details.innerHTML = `<summary>参数说明</summary>${schemaSummary(t.schema)}
        <details class="tl-raw"><summary>查看原始 schema</summary><pre>${esc(JSON.stringify(t.schema, null, 2))}</pre></details>`;
      main.appendChild(details);
      const callBtn = el('button', 'btn btn-secondary btn-sm', '调用');
      callBtn.addEventListener('click', () => openToolCallDialog(t));
      row.appendChild(main);
      row.appendChild(callBtn);
      list.appendChild(row);
      const sel = row.querySelector('.perm-select');
      sel.value = t.overridden ? t.permission : 'default';
      sel.addEventListener('change', async () => {
        try {
          await api('POST', '/api/tools/permission', { name: t.name, permission: sel.value });
          toast(`工具 ${t.name} 权限已更新（${sel.value === 'default' ? '恢复内置默认' : (PERM_LABELS[sel.value] || sel.value)}）`, 'success');
          loadTools();
        } catch (err) {
          toast(err.message, 'error');
          loadTools();
        }
      });
    });
  } catch (err) { loadError(list, err, loadTools); }
}

// schemaSummary（L6，2026-09-23 QA）：JSON Schema 直 dump 对不写代码的人是天书，
// 先渲染「参数名 · 类型 · 必填 · 一句话说明」表，原始 JSON 收进二级折叠。
function schemaSummary(schema) {
  const props = schema && schema.properties;
  if (!props || !Object.keys(props).length) return '<div class="row-sub">此工具不需要参数。</div>';
  const required = new Set((schema.required || []));
  const rows = Object.entries(props).map(([k, v]) => {
    const type = v.type || (v.enum ? '枚举' : '任意');
    const req = required.has(k) ? '<span class="badge badge--warn">必填</span>' : '<span class="row-sub">可选</span>';
    const desc = esc(v.description || '—');
    const enm = v.enum ? `<div class="row-sub">可选值：${v.enum.map((x) => esc(String(x))).join(' · ')}</div>` : '';
    const dft = v.default !== undefined ? `<div class="row-sub">默认：${esc(JSON.stringify(v.default))}</div>` : '';
    return `<tr><td><code>${esc(k)}</code></td><td>${esc(type)}</td><td>${req}</td><td>${desc}${enm}${dft}</td></tr>`;
  }).join('');
  return `<table class="schema-table"><thead><tr><th>参数</th><th>类型</th><th></th><th>说明</th></tr></thead><tbody>${rows}</tbody></table>`;
}

function openToolCallDialog(t) {
  Modal.open(`调用 <code style="font-family:var(--font-mono);font-size:var(--fs-md)">${esc(t.name)}</code>`, (box) => {
    const form = el('form');
    const f = el('div', 'field');
    f.innerHTML = `<label class="field-label" for="tool-args">参数（JSON）</label>`;
    const ta = el('textarea', 'textarea');
    ta.id = 'tool-args';
    ta.rows = 6;
    ta.value = '{}';
    if (t.schema && t.schema.properties) {
      const sample = {};
      Object.entries(t.schema.properties).forEach(([k, v]) => { sample[k] = v.type === 'integer' || v.type === 'number' ? 0 : v.type === 'boolean' ? false : ''; });
      ta.value = JSON.stringify(sample, null, 2);
    }
    f.appendChild(ta);
    form.appendChild(f);
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const run = el('button', 'btn btn-primary', '执行');
    actions.appendChild(cancel);
    actions.appendChild(run);
    form.appendChild(actions);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      let args;
      try { args = JSON.parse(ta.value || '{}'); }
      catch { toast('参数不是合法 JSON', 'error'); return; }
      run.disabled = true;
      run.innerHTML = ICONS.spinner + ' 执行中';
      try {
        const out = await api('POST', '/api/tools/call', { name: t.name, args });
        Modal.close();
        Modal.open(`执行结果 · ${esc(t.name)}`, (b) => {
          const pre = el('pre');
          pre.textContent = JSON.stringify(out.output, null, 2);
          b.appendChild(pre);
          const act = el('div', 'modal-actions');
          const close = el('button', 'btn btn-secondary', '关闭');
          close.addEventListener('click', Modal.close);
          act.appendChild(close);
          b.appendChild(act);
        });
      } catch (err) {
        toast(`调用失败：${err.message}`, 'error', 6500);
        run.disabled = false;
        run.textContent = '执行';
      }
    });
    box.appendChild(form);
  });
}

/* ---------- 设置 ---------- */
// 分段选择器通用逻辑：单选并同步 aria-pressed
function bindSegmented(sel) {
  const root = $(sel);
  root.addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-val]');
    if (!btn) return;
    root.querySelectorAll('button').forEach((b) => b.setAttribute('aria-pressed', String(b === btn)));
  });
}
bindSegmented('#set-style');
bindSegmented('#set-safety-mode');

function segValue(sel) {
  const btn = $(sel + ' button[aria-pressed="true"]');
  return btn ? btn.dataset.val : '';
}
function setSegValue(sel, val) {
  $(sel).querySelectorAll('button').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.val === val)));
}
function numValue(id) {
  const v = parseInt($('#' + id).value, 10);
  return Number.isFinite(v) ? v : undefined;
}
// 0 是合法取值（确定性输出），|| undefined 会把它当成空值丢掉，等于永远存不进 0
function floatValue(id) {
  const v = parseFloat($('#' + id).value);
  return Number.isFinite(v) ? v : undefined;
}

async function loadSettings() {
  await loadProviders();
  try {
    const s = await api('GET', '/api/settings');
    fillSettingsFields(s);
    syncRuntimeState(s);
    loadContext();
    loadAudit().catch(() => {});
    loadConnections().catch(() => {});
  } catch (err) {
    toast(`加载设置失败：${err.message}`, 'error');
  }
}

/* ---------- 安全门控留痕 ---------- */
const AUDIT_LABEL = {
  denied: '已拦截', approved: '已放行', auto: '自动放行', reviewed_block: '审核模型加拦',
  manual_revert: '用户手动还原',
};

async function loadAudit() {
  const box = $('#audit-list');
  if (!box) return;
  try {
    const data = await api('GET', '/api/security/audit?limit=30');
    const list = data.entries || [];
    if (!list.length) {
      box.innerHTML = '<p class="empty-hint">暂无留痕。被拦截、被放行或被审核模型标记的操作会记录在这里。</p>';
      return;
    }
    box.innerHTML = list.map((e) => {
      const blocked = e.action === 'denied' || e.action === 'reviewed_block';
      const t = e.time ? new Date(e.time).toLocaleString('zh-CN', { hour12: false }) : '';
      const detail = e.detail ? `<div class="audit-detail">${esc(e.detail)}</div>` : '';
      return `<div class="audit-item${blocked ? ' audit-item--block' : ''}">
        <div class="audit-head">
          <span class="audit-action">${esc(AUDIT_LABEL[e.action] || e.action)}</span>
          <code>${esc(e.tool)}</code>
          <span class="audit-risk">${esc(e.risk || '')}</span>
        </div>
        <div class="audit-reason">${esc(e.reason || '')}</div>
        ${detail}
        <div class="audit-time">${esc(t)}</div>
      </div>`;
    }).join('');
  } catch (err) {
    box.innerHTML = `<p class="empty-hint">加载留痕失败：${esc(err.message)}</p>`;
  }
}

/* ---------- 连接与出网台账 ---------- */
// 五个问句就是这张表的口径，顺序固定：先问通到哪，再问谁能触发，最后问想关掉动哪里。
// 每个字段都由后端算好（连 kindText 这种显示口径也在后端），这里只做呈现——
// 前端一旦开始自己拼（自己判是不是回环、自己把 out 翻成"出网"），同一件事就有两个 owner，
// 而漂移的方向永远是"界面看起来一切正常"。
const CX_FIELD_LABEL = [
  ['target', '通到哪'], ['trigger', '谁能触发'], ['leaves', '会离开本机'],
  ['trace', '留痕在哪'], ['off', '想关掉动哪里'],
];

async function loadConnections() {
  const box = $('#cx-conn-list');
  if (!box) return;
  const scope = $('#cx-conn-scope');
  try {
    const data = await api('GET', '/api/connections');
    if (scope) scope.textContent = data.egressScope || '';
    const rows = data.rows || [];
    if (!rows.length) {
      box.innerHTML = '<p class="empty-hint">没有查到任何常驻边界。</p>';
      return;
    }
    box.innerHTML = rows.map(connectionRow).join('');
  } catch (err) {
    if (scope) scope.textContent = '';
    box.innerHTML = `<p class="empty-hint">加载台账失败：${esc(err.message)}</p>`;
  }
}

function connectionRow(r) {
  const fields = CX_FIELD_LABEL
    .filter(([key]) => r[key])
    .map(([key, label]) => `<div class="cx-field"><dt>${label}</dt><dd>${esc(r[key])}</dd></div>`)
    .join('');
  const badges = [`<span class="cx-badge cx-badge--${esc(r.kind)}">${esc(r.kindText)}</span>`];
  // 未留痕必须自己戴牌子：这一格沉默，用户读到的就是"这张表说一切有据可查"。
  if (r.untraced) badges.push('<span class="cx-badge cx-badge--untraced">查不到痕迹</span>');
  const stats = r.stats ? `<p class="cx-stats">${esc(r.stats)}</p>` : '';
  const alert = r.alert ? `<p class="cx-alert">${esc(r.alert)}</p>` : '';
  const cls = ['cx-item', 'cx-item--' + esc(r.kind)];
  if (r.alert) cls.push('cx-item--warn');
  return `<div class="${cls.join(' ')}">
    <div class="cx-head">
      <span class="cx-title">${esc(r.title)}</span>
      ${badges.join('')}
      <span class="cx-status">${esc(r.status)}</span>
    </div>
    <dl class="cx-fields">${fields}</dl>
    ${stats}${alert}
  </div>`;
}

/* ---------- 候补目标 ---------- */
// 这一屏是「主动性」在界面上的唯一落点。口径全在后端（线索、阈值、文案、配额），
// 这里只做呈现和两个动作。为什么动作只有两个：
// **采纳不等于执行**——填进输入区之后，这句话要不要说、怎么说，仍然是用户在回车键上
// 做的决定。前端要是顺手把 goal 直接提交，「只提议不执行」就只剩 Go 注释里成立了。
//
// 同理，这里不判"该提什么"、不算严重度、不译枚举：signal_text / reason_text 由后端带过来，
// 界面自己 map 一次就是第二个 owner，漂移方向跟连接台账那条一样。
let cueGoals = new Map(); // id -> 填进输入区的那句话（长文本不放 data-*，那是给人看的属性）

async function loadCues() {
  const deck = $('#cu-deck');
  if (!deck) return;
  try {
    renderCues(await api('GET', '/api/cues'));
  } catch (err) {
    // 读不动就明说，并清掉上一轮的卡片：那些卡可能早已被处置，留着就是拿旧账当现状。
    cueGoals = new Map();
    deck.hidden = false;
    $('#cu-scope').textContent = `候补目标读不出来：${err.message}`;
    $('#cu-list').innerHTML = '';
    $('#cu-tail').textContent = '';
    $('#cu-note').textContent = '';
    $('#cu-restore').hidden = true;
  }
}

function renderCues(led) {
  const deck = $('#cu-deck');
  const rows = led.rows || [];
  const sup = led.suppressed || [];
  // 什么时候露出这一块：有卡要提、有已按下的要能撤销、有历史可交代（空态也是交代），
  // 或后端有话要说（归档读不动、处置记录没写下去）。三者都没有就整块收起——
  // 一个永远空着的「建议」面板比没有更吵，而且它会在用户每次升级后被当成坏了的东西。
  const silent = !rows.length && !sup.length && !led.scanned && !led.note;
  deck.hidden = silent;
  if (silent) return;

  $('#cu-scope').textContent = led.coverage || '';
  $('#cu-note').textContent = led.note || '';
  $('#cu-tail').textContent = led.truncated || '';
  cueGoals = new Map(rows.map((r) => [r.id, r.goal || '']));
  $('#cu-list').innerHTML = rows.length
    ? rows.map(cueCard).join('')
    : '<p class="cue-empty">最近的任务里没有反复出问题的地方，这一轮没什么可提的。</p>';

  const box = $('#cu-restore');
  box.hidden = !sup.length;
  $('#cu-restore-count').textContent = String(sup.length);
  $('#cu-restore-list').innerHTML = sup.map(cueRestoredRow).join('');
  if (!sup.length) {
    $('#cu-restore-list').hidden = true;
    $('#cu-restore-toggle').setAttribute('aria-expanded', 'false');
  }
}

function cueCard(r) {
  const ev = (r.evidence || []).map((s) => `<li>${esc(s)}</li>`).join('');
  const seen = r.last_seen ? `<span class="cue-seen">最近一次 ${esc(r.last_seen)}</span>` : '';
  return `<article class="cue-card" data-id="${esc(r.id)}">
    <div class="cue-card-head">
      <span class="cue-badge">${esc(r.signal_text || r.signal)}</span>
      <h3 class="cue-card-title">${esc(r.title)}</h3>
    </div>
    <p class="cue-why">${esc(r.why)}</p>
    ${ev ? `<ul class="cue-evidence">${ev}</ul>` : ''}
    <p class="cue-goal" title="采纳时填进输入区的就是这句话">${esc(r.goal)}</p>
    <div class="cue-actions">
      <button class="btn btn-primary btn-sm" type="button" data-act="adopt"
        title="把这句话填进下方输入区——按回车才会开始跑">填进输入区</button>
      <button class="btn btn-secondary btn-sm" type="button" data-act="dismiss"
        title="按下去之后不再提这一条，可以在下面的「已按下」里撤销">别再提</button>
      ${seen}
    </div>
  </article>`;
}

function cueRestoredRow(s) {
  return `<div class="cue-restored" data-fp="${esc(s.fingerprint)}">
    <span class="cue-restored-reason">${esc(s.reason_text || s.reason)}</span>
    <span class="cue-restored-label">${esc(s.title || s.goal || s.fingerprint)}</span>
    <button class="btn btn-ghost btn-sm" type="button" data-act="unsuppress">撤销</button>
  </div>`;
}

// 采纳：填输入区，不提交。这里复用快捷卡的同一条路（同一个输入区、同一个 autoResize 与 focus），
// 于是"采纳"和"我自己打了一句"在后续流程里没有任何区别——都还要那一次回车。
function adoptCue(id) {
  const goal = cueGoals.get(id) || '';
  if (!goal) {
    toast('这张卡已经不成立了（历史变了），重新拉一次', 'warning');
    loadCues().catch(() => {});
    return;
  }
  goalInput.value = goal;
  autoResize();
  goalInput.focus();
  toast('已填进输入区。还没有开始跑——按回车才会提交', 'info', 6000);
  api('POST', `/api/cues/${encodeURIComponent(id)}/adopt`, {}).catch((err) => {
    // 记不上状态不等于这次采纳失败：话已经在输入区了。要说的是"它还会再来"。
    toast(`这句话已填好，但没能记下「已采纳」：${err.message}`, 'warning', 6000);
  }).finally(() => loadCues().catch(() => {}));
}

function dismissCue(id) {
  api('POST', `/api/cues/${encodeURIComponent(id)}/dismiss`, {}).catch((err) => {
    if (err.status === 404) toast('这张卡已经不成立了，重新拉一次', 'warning');
    else toast(`记下「别再提」失败：${err.message}`, 'error');
  }).finally(() => loadCues().catch(() => {}));
}

function unsuppressCue(fp) {
  api('POST', '/api/cues/unsuppress', { fingerprint: fp })
    .catch((err) => toast(`撤销失败：${err.message}`, 'error'))
    .finally(() => loadCues().catch(() => {}));
}

$('#cu-list').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-act]');
  const card = e.target.closest('.cue-card');
  if (!btn || !card) return;
  const id = card.dataset.id;
  if (btn.dataset.act === 'adopt') adoptCue(id);
  else if (btn.dataset.act === 'dismiss') dismissCue(id);
});

$('#cu-restore-list').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-act="unsuppress"]');
  const row = e.target.closest('.cue-restored');
  if (!btn || !row) return;
  unsuppressCue(row.dataset.fp);
});

$('#cu-restore-toggle').addEventListener('click', () => {
  const list = $('#cu-restore-list');
  list.hidden = !list.hidden;
  $('#cu-restore-toggle').setAttribute('aria-expanded', String(!list.hidden));
});

$('#cu-refresh').addEventListener('click', () => loadCues().catch(() => {}));

function fillSettingsFields(s) {
  $('#set-name').value = s.persona?.name || 'Gleam';
  setSegValue('#set-style', s.persona?.style || 'efficient');
  setSegValue('#set-safety-mode', s.safety?.mode || 'auto');
  _doSetPerm(s.safety?.mode || 'auto', false);
  $('#set-approval-timeout').value = s.safety?.approval_timeout_seconds;
  $('#set-max-replans').value = s.agent?.max_replans;
  $('#set-max-steps').value = s.agent?.max_steps;
  $('#set-step-timeout').value = s.agent?.step_timeout_seconds;
  $('#set-step-retries').value = s.agent?.step_retries;
  $('#set-done-threshold').value = s.agent?.done_threshold;
  $('#set-max-concurrency').value = s.agent?.max_concurrency;
  $('#set-context-compress').checked = !!s.agent?.context_compress;
  $('#set-skill-optimize').checked = !!s.agent?.skill_auto_optimize;
  $('#set-dedupe-calls').checked = s.agent?.dedupe_calls !== false;
  $('#set-max-llm-calls').value = s.agent?.max_llm_calls_per_task ?? 0;
  $('#set-max-tokens-task').value = s.agent?.max_tokens_per_task ?? 0;
  $('#set-max-duration').value = s.agent?.max_task_duration_seconds ?? 0;
  $('#set-stuck-threshold').value = s.agent?.stuck_threshold ?? 0;
  $('#set-max-output-runes').value = s.agent?.max_output_runes ?? 0;
  $('#set-max-tool-schemas').value = s.agent?.max_tool_schemas ?? 0;
  $('#set-chat-acceptance').checked = s.agent?.chat_acceptance !== false;
  $('#set-ai-review').checked = s.safety?.ai_review !== false;
  $('#set-short-cap').value = s.memory?.short_term_capacity;
  $('#set-max-items').value = s.memory?.max_items;
  if (s.llm) {
    $('#set-provider').value = s.llm.provider_id || '';
    fillPlans();
    if (s.llm.provider_id) $('#set-plan').value = s.llm.plan || 'token';
    $('#set-protocol').value = s.llm.protocol || 'openai_chat';
    $('#set-model').value = s.llm.model || '';
    if ($('#set-fast-model')) $('#set-fast-model').value = s.llm.fast_model || '';
    if ($('#set-tiers')) $('#set-tiers').value = formatTiers(s.llm.tiers);
    $('#set-base-url').value = s.llm.base_url || '';
    $('#set-api-key').value = '';
    $('#set-temperature').value = s.llm.temperature;
    $('#set-max-tokens').value = s.llm.max_tokens;
    $('#set-llm-timeout').value = s.llm.timeout_seconds;
  }
}

/* ---------- 厂商预设（官方接入入口 × 套餐） ---------- */
let PROVIDERS = [];
let providersBound = false;
async function loadProviders() {
  // L11：/api/providers 要 0.5–1.5s，先放占位，避免下拉框出现「空列表」真空期
  const sel = $('#set-provider');
  sel.innerHTML = '<option value="" disabled>厂商列表加载中…</option>';
  try {
    const { providers } = await api('GET', '/api/providers');
    PROVIDERS = providers || [];
  } catch { PROVIDERS = []; }
  sel.innerHTML = '<option value="">自定义 / 手动填写</option>' +
    PROVIDERS.map((p) => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join('');
  // 事件只绑一次：加载失败时 PROVIDERS 为空，若以长度作标志会重复累积监听器
  if (!providersBound) {
    providersBound = true;
    sel.addEventListener('change', fillPlans);
    $('#set-plan').addEventListener('change', applyPreset);
  }
}

function fillPlans() {
  const p = PROVIDERS.find((x) => x.id === $('#set-provider').value);
  const planSel = $('#set-plan');
  if (!p) {
    planSel.innerHTML = '<option value="">—</option>';
    planSel.disabled = true;
    return;
  }
  planSel.disabled = false;
  planSel.innerHTML = p.plans
    .map((pl) => `<option value="${esc(pl.kind)}">${esc(pl.label || pl.kind)}</option>`)
    .join('');
  applyPreset();
}

// applyPreset 套餐切换即回填官方入口/默认模型/协议（用户仍可手改）
function applyPreset() {
  const p = PROVIDERS.find((x) => x.id === $('#set-provider').value);
  if (!p) return;
  const kind = $('#set-plan').value;
  const pl = p.plans.find((x) => x.kind === kind) || p.plans[0];
  if (pl) {
    $('#set-base-url').value = pl.base_url;
    $('#set-model').value = pl.model;
    $('#set-protocol').value = pl.protocol;
  }
}
/* ---------- 设置页 tab 切换 ---------- */
function switchSettingsTab(stab) {
  const tab = document.querySelector(`.settings-tab[data-stab="${stab}"]`);
  if (tab) tab.click();
}
document.querySelectorAll('.settings-tab').forEach((tab) => {
  tab.addEventListener('click', () => {
    const stab = tab.dataset.stab;
    document.querySelectorAll('.settings-tab').forEach((t) => {
      t.classList.toggle('active', t === tab);
      t.setAttribute('aria-selected', String(t === tab));
    });
    document.querySelectorAll('.settings-panel').forEach((p) => {
      const show = p.dataset.stab === stab;
      p.classList.toggle('active', show);
      p.hidden = !show;
    });
    if (stab === 'go') loadGoStatus();
    if (stab === 'memory') loadContext();
  });
});

// 设置搜索：按分区名与分区内的字段文字过滤左侧导航
$('#settings-search').addEventListener('input', (e) => {
  const q = e.target.value.trim().toLowerCase();
  let any = false;
  document.querySelectorAll('.settings-nav .settings-tab').forEach((tab) => {
    const panel = document.querySelector(`.settings-panel[data-stab="${tab.dataset.stab}"]`);
    const hay = (tab.textContent + ' ' + (panel ? panel.textContent : '')).toLowerCase();
    const hit = !q || hay.includes(q);
    tab.hidden = !hit;
    any = any || hit;
  });
  document.querySelectorAll('.settings-nav .settings-group-label').forEach((lab) => {
    let n = lab.nextElementSibling, vis = false;
    while (n && n.classList.contains('settings-tab')) { if (!n.hidden) vis = true; n = n.nextElementSibling; }
    lab.hidden = !vis;
  });
  $('#settings-search-empty').hidden = any;
  if (q && any) {
    const cur = document.querySelector('.settings-nav .settings-tab.active');
    if (cur && cur.hidden) document.querySelector('.settings-nav .settings-tab:not([hidden])').click();
  }
});

// 设置页顶部概况：只读 /api/growth 里已有的统计
async function loadSettingsProfile() {
  $('#sp-name').textContent = $('#me-name').textContent || '我的';
  $('#sp-sub').textContent = $('#me-sub').textContent || '本地模式';
  try {
    const { stats } = await api('GET', '/api/growth');
    if (!stats) return;
    $('#sp-tasks').textContent = stats.total_tasks || 0;
    $('#sp-skills').textContent = stats.total_skills || 0;
    $('#sp-streak').textContent = stats.recent_streak || 0;
    $('#sp-week').textContent = stats.week_tokens ? formatTokens(stats.week_tokens) : '—';
  } catch { /* 统计拿不到就留「—」 */ }
}

/* ---------- 分模块保存 ---------- */
function syncRuntimeState(s) {
  if (!s) return;
  if (s.llm) {
    PROVIDER = s.llm.provider || 'glm';
    API_KEY_SET = !!s.llm.api_key_set;
    // 密钥按接入主机绑定：换厂商后原来那把不会发出去，所以这里必须说清
    // "当前能用"与"存着的是哪一家"是两件事，否则用户只看到一个假的"未设置"。
    const host = s.llm.api_key_host || '';
    const curHost = s.llm.api_key_host_cur || '';
    const keyInput = $('#set-api-key');
    if (API_KEY_SET) {
      keyInput.placeholder = curHost ? `已设置，仅发往 ${curHost}；留空表示不修改` : '已设置，留空表示不修改';
    } else if (host) {
      keyInput.placeholder = `已存的密钥属于 ${host}，换厂商需重新填写`;
    } else {
      keyInput.placeholder = '未设置';
    }
    const clearBtn = $('#set-clear-key');
    if (clearBtn) clearBtn.hidden = !(API_KEY_SET || host);
  }
  LiveRail.runtime(s);
  ComposerMeta.model(s);
}

/* ---------- 模型连通性测试与密钥清除 ---------- */
const LLM_TEST_HINTS = {
  auth: '鉴权失败（401/403）：检查 API Key 是否正确或已过期',
  not_found: '找不到地址或模型（404）：核对 API 端点与模型名',
  rate_limited: '被限流（429）：稍后再试',
  provider: '厂商侧故障（5xx）：服务暂不可用，稍后再试',
  api: 'API 返回错误',
  timeout: '连接超时（15 秒）：检查网络或 API 端点',
  network: '网络不通：检查端点地址、代理或防火墙',
  config: '配置不完整：先选择厂商或填写模型名',
};

async function testLLM() {
  const btn = $('#set-test-llm');
  const out = $('#llm-test-result');
  if (!btn || !out) return;
  btn.disabled = true;
  out.hidden = false;
  out.className = 'llm-test-result testing';
  out.textContent = '正在探测…';
  const body = {
    provider_id: $('#set-provider').value,
    plan: $('#set-plan').value || '',
    protocol: $('#set-protocol').value,
    base_url: $('#set-base-url').value.trim(),
    model: $('#set-model').value.trim(),
  };
  const key = $('#set-api-key').value.trim();
  if (key) body.api_key = key;
  try {
    const r = await api('POST', '/api/llm/test', body);
    if (r.ok) {
      const lat = Number.isFinite(r.latency_ms) ? ` · ${r.latency_ms}ms` : '';
      out.textContent = r.kind === 'mock' ? 'Mock 模型：未发起真实网络调用' : `连接成功${lat}`;
      out.className = 'llm-test-result ok';
    } else {
      out.textContent = (LLM_TEST_HINTS[r.kind] || '连接失败') + (r.message ? `（${r.message}）` : '');
      out.className = 'llm-test-result fail';
    }
  } catch (err) {
    out.textContent = `测试请求失败：${err.message}`;
    out.className = 'llm-test-result fail';
  } finally {
    btn.disabled = false;
  }
}

// 拉取模型列表：按表单当前值请求（不必先保存），结果填进 datalist 供点选。
// 拉不到不阻塞——模型框仍可手输，列表只是加速器。
async function fetchModels() {
  const btn = $('#set-fetch-models');
  const out = $('#llm-test-result');
  if (!btn) return;
  btn.disabled = true;
  if (out) { out.hidden = false; out.className = 'llm-test-result testing'; out.textContent = '正在拉取模型列表…'; }
  const body = {
    provider_id: $('#set-provider').value,
    plan: $('#set-plan').value || '',
    protocol: $('#set-protocol').value,
    base_url: $('#set-base-url').value.trim(),
  };
  const key = $('#set-api-key').value.trim();
  if (key) body.api_key = key;
  try {
    const r = await api('POST', '/api/llm/models', body);
    const dl = $('#model-list');
    if (r.ok && dl && Array.isArray(r.models)) {
      dl.replaceChildren(...r.models.map((m) => {
        const opt = document.createElement('option');
        opt.value = m.id;
        if (m.display_name) opt.label = m.id + '（' + m.display_name + '）';
        return opt;
      }));
      if (out) {
        out.textContent = r.kind === 'mock' ? 'Mock 模型：没有在线模型列表' : `已获取 ${r.count} 个模型，模型框可直接点选`;
        out.className = 'llm-test-result ok';
      }
    } else if (out) {
      out.textContent = (LLM_TEST_HINTS[r.kind] || '获取失败') + (r.message ? `（${r.message}）` : '');
      out.className = 'llm-test-result fail';
    }
  } catch (err) {
    if (out) { out.textContent = `拉取请求失败：${err.message}`; out.className = 'llm-test-result fail'; }
  } finally {
    btn.disabled = false;
  }
}

// 两步确认：第一下只武装按钮，4 秒内再点才真正清除（误点成本 = 重新输 key）
let clearKeyArmedAt = 0;
let clearKeyTimer = null;
async function clearAPIKey() {
  const btn = $('#set-clear-key');
  if (!btn) return;
  if (Date.now() - clearKeyArmedAt > 4000) {
    clearKeyArmedAt = Date.now();
    btn.textContent = '再点一次确认清除';
    btn.classList.add('btn-armed');
    clearTimeout(clearKeyTimer);
    clearKeyTimer = setTimeout(() => {
      btn.textContent = '清除密钥';
      btn.classList.remove('btn-armed');
    }, 4000);
    return;
  }
  clearTimeout(clearKeyTimer);
  btn.textContent = '清除密钥';
  btn.classList.remove('btn-armed');
  clearKeyArmedAt = 0;
  await saveModule('set-clear-key', { llm: { clear_api_key: true } }, {
    onSuccess: () => {
      $('#set-api-key').value = '';
      const out2 = $('#llm-test-result');
      if (out2) { out2.hidden = true; out2.textContent = ''; }
      toast('API Key 已清除', 'success');
    },
  });
}

// inputLabel 取控件的中文名（label[for]），报错时说得清是哪一项。
function inputLabel(id) {
  const l = document.querySelector(`label[for="${id}"]`);
  return l ? l.textContent.trim() : id;
}

async function saveModule(btnId, patch, opts = {}) {
  const btn = $('#' + btnId);
  if (!btn) return;
  // 数字框被清空时 numValue 返回 undefined，这个键在 JSON 里**整个消失**：后端只收到
  // 其余字段、回 200，于是提示"已保存并生效"，用户以为改了其实没改。宁可拦住。
  const blank = (opts.numeric || []).filter((id) => {
    const e = $('#' + id);
    return e && !e.value.trim();
  });
  if (blank.length) {
    toast(`${blank.map(inputLabel).join('、')}：还没填数值`, 'error', 6000);
    $('#' + blank[0]).focus();
    return;
  }
  btn.disabled = true;
  try {
    await api('POST', '/api/settings', patch);
    // L13：5 个分区同款按钮，toast 要说出刚保存的是哪一块
    toast(`${opts.label || '设置'}已保存并生效`, 'success');
    try {
      const s = await api('GET', '/api/settings');
      if (s.llm) fillSettingsFields(s);
      syncRuntimeState(s);
    } catch { /* 后台刷新失败不影响保存提示 */ }
    if (opts.onSuccess) opts.onSuccess();
    if (opts.reloadContext) loadContext();
  } catch (err) {
    toast(`保存失败：${err.message}`, 'error');
  } finally {
    btn.disabled = false;
  }
}

async function savePersona() {
  await saveModule('set-save-persona', {
    persona: { name: $('#set-name').value.trim(), style: segValue('#set-style') },
  }, { label: '人设设置' });
}

async function saveSafety() {
  const mode = segValue('#set-safety-mode');
  await saveModule('set-save-safety', {
    safety: {
      mode,
      approval_timeout_seconds: numValue('set-approval-timeout'),
      ai_review: $('#set-ai-review').checked,
    },
  }, { label: '安全设置', numeric: ['set-approval-timeout'], onSuccess: () => _doSetPerm(mode, false) });
}

async function saveEngine() {
  await saveModule('set-save-engine', {
    agent: {
      max_replans: numValue('set-max-replans'),
      max_steps: numValue('set-max-steps'),
      step_timeout_seconds: numValue('set-step-timeout'),
      step_retries: numValue('set-step-retries'),
      done_threshold: numValue('set-done-threshold'),
      max_concurrency: numValue('set-max-concurrency'),
      context_compress: $('#set-context-compress').checked,
      skill_auto_optimize: $('#set-skill-optimize').checked,
      dedupe_calls: $('#set-dedupe-calls').checked,
      max_llm_calls_per_task: numValue('set-max-llm-calls'),
      max_tokens_per_task: numValue('set-max-tokens-task'),
      max_task_duration_seconds: numValue('set-max-duration'),
      stuck_threshold: numValue('set-stuck-threshold'),
      max_output_runes: numValue('set-max-output-runes'),
      max_tool_schemas: numValue('set-max-tool-schemas'),
      chat_acceptance: $('#set-chat-acceptance').checked,
    },
  }, {
    label: '引擎设置',
    reloadContext: true,
    numeric: ['set-max-replans', 'set-max-steps', 'set-step-timeout', 'set-step-retries',
      'set-done-threshold', 'set-max-concurrency', 'set-max-llm-calls', 'set-max-tokens-task',
      'set-max-duration', 'set-stuck-threshold', 'set-max-output-runes', 'set-max-tool-schemas'],
  });
}

async function saveMemory() {
  await saveModule('set-save-memory', {
    memory: { short_term_capacity: numValue('set-short-cap'), max_items: numValue('set-max-items') },
  }, { label: '记忆设置', numeric: ['set-short-cap', 'set-max-items'], reloadContext: true });
}

async function saveLLM() {
  const parsed = $('#set-tiers') ? parseTiers($('#set-tiers').value) : { tiers: undefined, bad: [] };
  if (parsed.bad.length) {
    toast(`档位有 ${parsed.bad.length} 行无效，本次未保存：${parsed.bad.join('；')}`, 'error', 6000);
    return;
  }
  const patch = {
    llm: {
      provider_id: $('#set-provider').value,
      plan: $('#set-provider').value ? ($('#set-plan').value || 'token') : '',
      protocol: $('#set-protocol').value,
      base_url: $('#set-base-url').value.trim(),
      model: $('#set-model').value.trim(),
      fast_model: $('#set-fast-model') ? $('#set-fast-model').value.trim() : undefined,
      tiers: parsed.tiers,
      temperature: floatValue('set-temperature'),
      max_tokens: numValue('set-max-tokens'),
      timeout_seconds: numValue('set-llm-timeout'),
    },
  };
  const key = $('#set-api-key').value.trim();
  if (key) patch.llm.api_key = key;
  await saveModule('set-save-llm', patch, {
    label: '模型设置',
    numeric: ['set-temperature', 'set-max-tokens', 'set-llm-timeout'],
    onSuccess: () => { $('#set-api-key').value = ''; },
  });
}

if ($('#audit-refresh')) $('#audit-refresh').addEventListener('click', () => loadAudit());
if ($('#cx-conn-refresh')) $('#cx-conn-refresh').addEventListener('click', () => loadConnections());

$('#set-save-persona').addEventListener('click', savePersona);
$('#set-save-safety').addEventListener('click', saveSafety);
$('#set-save-engine').addEventListener('click', saveEngine);
$('#set-save-memory').addEventListener('click', saveMemory);
$('#set-save-llm').addEventListener('click', saveLLM);
if ($('#set-test-llm')) $('#set-test-llm').addEventListener('click', testLLM);
if ($('#set-fetch-models')) $('#set-fetch-models').addEventListener('click', fetchModels);
if ($('#set-clear-key')) $('#set-clear-key').addEventListener('click', clearAPIKey);

/* ---------- 上下文（会话自动压缩状态） ---------- */
function renderContext(ctx) {
  // 输入区的水位条读的是这同一次取数：一次拉、两处画。各拉各的就会出现
  // 设置页说 40%、输入区说 25% 的两个面板。
  ComposerMeta.context(ctx);
  const stats = $('#context-stats');
  if (!ctx.enabled) {
    stats.textContent = '自动压缩已关闭';
    $('#context-summary').hidden = true;
    return;
  }
  const saved = ctx.est_tokens_saved > 0 ? ` · 已省约 ${ctx.est_tokens_saved} tokens` : '';
  stats.textContent = `窗口内 ${ctx.short_turns}/${ctx.short_cap} 轮 · 待压缩 ${ctx.overflow} 轮 · 摘要 ${ctx.summary_chars} 字${saved}`;
  const pre = $('#context-summary');
  if (ctx.summary) {
    pre.textContent = ctx.summary;
    pre.hidden = false;
  } else {
    pre.hidden = true;
  }
}

async function loadContext() {
  try {
    renderContext(await api('GET', '/api/context'));
  } catch { /* 静默 */ }
}

$('#context-compress').addEventListener('click', async () => {
  const btn = $('#context-compress');
  btn.disabled = true;
  try {
    const { compressed, context } = await api('POST', '/api/context/compress');
    toast(compressed ? '已压缩早期上下文' : '没有待压缩的早期对话', compressed ? 'success' : 'info');
    renderContext(context);
  } catch (err) {
    toast(err.message, 'error');
  } finally {
    btn.disabled = false;
  }
});

$('#context-clear').addEventListener('click', async () => {
  if (!await confirmModal('清空滚动摘要与待压缩历史？短期记忆与长期记忆不受影响。', '清空摘要', { okText: '清空', danger: true })) return;
  try {
    const { context } = await api('POST', '/api/context/clear');
    toast('摘要已清空', 'success');
    renderContext(context);
  } catch (err) {
    toast(err.message, 'error');
  }
});

/* ---------- 输入区就地控件：模型切换 + 上下文水位（批次 F12） ----------
 *
 * 这两个数字在设置页和现场栏本来就有，但**看得到、改不动**：切一次模型要点
 * 设置 → 模型 → 改下拉 → 保存四步；上下文满了没有任何地方提醒。这里把它们
 * 抬到发消息的那一行。
 *
 * 三条界线，避免变成"第二套事实"：
 *   - 弹层只**选**，不**编辑**。自定义模型名、base_url、协议仍然只在设置页；
 *   - 百分比不在这里算。`/api/context` 的 `fill_pct` 由 memory.fillPct 负责，
 *     这里只把它写成宽度和文字（一处事实一处算法）；
 *   - 当前模型名与现场栏同源（`/api/info` 的 model），两边各自画同一个值。
 *
 * 元素缺失时整个模块不起手（而不是画一个永远不动的按钮）：
 * scripts/check-dom-anchors.py 的反向判据罩着 `cp-` 前缀。
 */
const ComposerMeta = (() => {
  const modelBtn = $('#cp-model');
  const ctxBtn = $('#cp-context');
  if (!modelBtn || !ctxBtn) return { model: () => {}, context: () => {}, live: () => {} };

  const modelName = $('#cp-model-name');
  const modelPop = $('#cp-model-pop');
  const modelCur = $('#cp-model-cur');
  const modelProv = $('#cp-model-provider');
  const modelList = $('#cp-model-list');
  const modelFetchBtn = $('#cp-model-fetch');
  const meterFill = $('#cp-meter-fill');
  const pctEl = $('#cp-context-pct');
  const badge = $('#cp-context-badge');
  const ctxPop = $('#cp-context-pop');
  const ctxRead = $('#cp-context-read');
  const compressBtn = $('#cp-context-compress');

  // 候选清单要用的三份本机数据。各自到齐时补画一次，不等最慢的那个。
  let llmCfg = null;     // /api/settings 的 llm 段
  let fetched = [];      // 最近一次「拉取厂商模型」的结果
  let liveModel = '';    // /api/info 的 model：当前**真的在跑**的模型
  let lastCtx = null;    // 最近一次 /api/context，供弹层重开时立刻画

  const TIER_CN = { economy: '经济', coding: '编程', office: '办公', reasoning: '推理' };
  const PLAN_CN = { token: '按量', coding: '编程套餐', agent: '智能体套餐' };

  /* ---------- 弹层开合：同一时刻只开一个 ---------- */
  function setOpen(which) {
    [[modelBtn, modelPop], [ctxBtn, ctxPop]].forEach(([btn, pop]) => {
      const show = btn === which;
      pop.hidden = !show;
      btn.setAttribute('aria-expanded', String(show));
    });
  }
  const closeAll = () => setOpen(null);
  document.addEventListener('click', closeAll);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeAll(); });
  [modelPop, ctxPop].forEach((pop) => pop.addEventListener('click', (e) => e.stopPropagation()));
  modelBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const show = modelPop.hidden;
    closeAll();
    if (show) { setOpen(modelBtn); paintModelList(); }
  });
  ctxBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const show = ctxPop.hidden;
    closeAll();
    if (show) { setOpen(ctxBtn); paintContextPanel(lastCtx); loadContext(); }
  });

  /* ---------- 模型 ---------- */

  // candidateModels 列出**能一键切**的模型：全部来自本机已有配置，不联网。
  // 只给当前套餐的厂商默认模型——别的套餐要连 base_url 与协议一起换，
  // 在这里点一下就会把 key 的绑定主机和入口对不上（F4 刚修过的那类错）。
  function candidateModels() {
    const out = [];
    const seen = new Set();
    const push = (id, kind) => {
      const m = String(id || '').trim();
      if (!m || seen.has(m)) return;
      seen.add(m);
      out.push({ id: m, kind });
    };
    const cur = liveModel || (llmCfg && llmCfg.model) || '';
    push(cur, '当前');
    if (llmCfg) {
      push(llmCfg.fast_model, '辅助模型');
      const tiers = llmCfg.tiers || {};
      Object.keys(tiers).sort().forEach((k) => push(tiers[k], (TIER_CN[k] || k) + '档'));
    }
    fetched.forEach((m) => push(m, '厂商列表'));
    return out;
  }

  function paintModelChip() {
    const m = liveModel || (llmCfg && llmCfg.model) || '';
    const isMock = (llmCfg && llmCfg.provider === 'mock') || PROVIDER === 'mock';
    modelName.textContent = isMock ? '演示模型' : (m ? shorten(m, 18) : '未配模型');
    modelName.title = isMock ? '离线演示模型：不发起真实网络调用' : (m || '还没有可用模型：去设置里选择厂商并填写模型名');
    // 小标签只写本机配置里真有的东西：mock 档，或当前模型恰好是某个档位的模型
    const tagEl = $('#cp-model-tag');
    if (tagEl) {
      let tag = '';
      if (isMock) tag = 'mock';
      else if (llmCfg && llmCfg.tiers && m) {
        const k = Object.keys(llmCfg.tiers).find((key) => llmCfg.tiers[key] === m);
        if (k) tag = TIER_CN[k] || k;
      }
      tagEl.textContent = tag;
      tagEl.hidden = !tag;
    }
    modelCur.textContent = m || '未配置';
    const bits = [];
    if (llmCfg && llmCfg.provider) {
      // 显示名取 /api/providers 那一份（与设置页下拉同一个来源）；厂商表还没到齐时退回 id
      const pv = PROVIDERS.find((x) => x.id === llmCfg.provider);
      bits.push(pv ? pv.name : llmCfg.provider);
    }
    if (llmCfg && llmCfg.plan) bits.push(PLAN_CN[llmCfg.plan] || llmCfg.plan);
    if (llmCfg && llmCfg.api_key_set === false) bits.push('无密钥');
    modelProv.textContent = bits.join(' · ');
    // 无密钥不是"能选个模型就好"：切了也调不通，所以芯片要自己红一下。
    modelBtn.dataset.tone = (llmCfg && llmCfg.api_key_set === false) ? 'warn' : '';
  }

  function paintModelList() {
    modelList.replaceChildren();
    const items = candidateModels();
    const cur = liveModel || (llmCfg && llmCfg.model) || '';
    if (!items.length) {
      modelList.appendChild(el('div', 'cp-pop-note', '本机还没有可选模型。拉取一份，或去设置里手输模型名。'));
      return;
    }
    items.forEach((it) => {
      const b = el('button', 'plus-item');
      b.type = 'button';
      const isCur = it.id === cur;
      if (isCur) b.setAttribute('aria-current', 'true');
      b.appendChild(el('span', null, it.id));
      b.appendChild(el('span', 'plus-sub', isCur ? '使用中' : it.kind));
      b.disabled = isCur;
      b.addEventListener('click', () => switchModel(it.id));
      modelList.appendChild(b);
    });
  }

  async function switchModel(id) {
    closeAll();
    const prev = liveModel || (llmCfg && llmCfg.model) || '';
    liveModel = id;          // 先乐观改名：下一行就要发请求，别让用户等一个来回才看到反馈
    paintModelChip();
    paintModelList();
    try {
      const s = await api('POST', '/api/settings', { llm: { model: id } });
      llmCfg = s.llm || llmCfg;
      liveModel = (s.llm && s.llm.model) || id;
      syncRuntimeState(s);   // 顺手把现场栏、密钥提示一起对上
      toast(`已切到 ${liveModel}，下一个任务开始用它`, 'success');
    } catch (err) {
      liveModel = prev;      // 失败要改回去：留着一个没生效的名字比报错更糟
      paintModelChip();
      toast(`切换模型失败：${err.message}`, 'error');
    }
    paintModelList();
  }

  modelFetchBtn.addEventListener('click', async () => {
    modelFetchBtn.disabled = true;
    const old = modelFetchBtn.textContent;
    modelFetchBtn.textContent = '拉取中…';
    try {
      // 空体 = 用**已保存**的配置去拉：这里没有表单可读，也不该假装读得到。
      const r = await api('POST', '/api/llm/models', {});
      if (r.ok && Array.isArray(r.models)) {
        fetched = r.models.map((m) => m.id).filter(Boolean);
        toast(`已拉取 ${r.count || fetched.length} 个模型，点一下就切`, 'success');
      } else {
        toast((LLM_TEST_HINTS[r.kind] || '拉取失败') + (r.message ? `（${r.message}）` : ''), 'error');
      }
    } catch (err) {
      toast(`拉取失败：${err.message}`, 'error');
    } finally {
      modelFetchBtn.disabled = false;
      modelFetchBtn.textContent = old;
      paintModelList();
    }
  });

  $('#cp-model-goto').addEventListener('click', () => { closeAll(); showView('settings'); switchSettingsTab('llm'); });

  /* ---------- 上下文水位 ---------- */

  function paintMeter(ctx) {
    if (!ctx || typeof ctx.fill_pct !== 'number') {
      // 字段缺失就是接线断了。宁可空着，也不在前端拿 turns/cap 自己除一个凑数。
      pctEl.textContent = '—';
      ctxBtn.dataset.ready = 'false';
      meterFill.style.width = '0%';
      delete meterFill.dataset.level;
      badge.hidden = true;
      ctxBtn.title = '上下文水位读数不可用';
      return;
    }
    const pct = ctx.fill_pct;
    ctxBtn.dataset.ready = 'true';
    meterFill.style.width = pct + '%';
    meterFill.dataset.level = pct >= 90 ? 'high' : pct >= 60 ? 'warn' : 'fresh';
    pctEl.textContent = pct + '%';
    badge.hidden = !ctx.overflow;
    badge.textContent = String(ctx.overflow || 0);
    badge.title = ctx.overflow ? `待压缩 ${ctx.overflow} 轮` : '';
    ctxBtn.dataset.full = pct >= 100 ? 'true' : 'false';
    ctxBtn.title = (ctx.enabled ? '' : '自动压缩已关闭 · ')
      + `上下文水位 ${pct}%（窗口 ${ctx.short_turns}/${ctx.short_cap} 轮，待压缩 ${ctx.overflow} 轮）`;
  }

  function paintContextPanel(ctx) {
    if (!ctx) return;
    if (ctx.enabled) delete ctxRead.dataset.off;
    else ctxRead.dataset.off = 'true';
    const saved = ctx.est_tokens_saved > 0 ? ` · 累计已省约 ${ctx.est_tokens_saved} tokens` : '';
    const bar = $('#cp-context-bar');
    if (bar) { bar.style.width = Math.max(0, Math.min(100, ctx.fill_pct || 0)) + '%'; bar.dataset.level = ctx.fill_pct >= 90 ? 'high' : ctx.fill_pct >= 60 ? 'warn' : 'fresh'; }
    ctxRead.textContent = ctx.enabled
      ? `窗口 ${ctx.short_turns}/${ctx.short_cap} 轮 · 水位 ${ctx.fill_pct}% · 待压缩 ${ctx.overflow} 轮 · 摘要 ${ctx.summary_chars} 字${saved}`
      : `自动压缩已关闭：窗口 ${ctx.short_turns}/${ctx.short_cap} 轮，窗口外的对话不会被摘要接住`;
  }

  compressBtn.addEventListener('click', async () => {
    compressBtn.disabled = true;
    try {
      const { compressed, context } = await api('POST', '/api/context/compress');
      toast(compressed ? '已压缩早期上下文' : '没有待压缩的早期对话', compressed ? 'success' : 'info');
      renderContext(context);
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      compressBtn.disabled = false;
    }
  });

  $('#cp-context-goto').addEventListener('click', () => { closeAll(); showView('settings'); switchSettingsTab('memory'); });

  return {
    // model(s)：/api/settings 到了就刷新（保存后也走这里）。
    model: (s) => {
      if (s && s.llm) llmCfg = s.llm;
      paintModelChip();
      if (!modelPop.hidden) paintModelList();
    },
    // context(ctx)：/api/context 到了就刷新（水位条的唯一画者）。
    context: (ctx) => {
      lastCtx = ctx;
      paintMeter(ctx);
      if (!ctxPop.hidden) paintContextPanel(ctx);
    },
    // live(id)：/api/info 到了就刷新——现场栏和这里读同一个值。
    live: (id) => {
      if (!id) return;
      liveModel = id;
      paintModelChip();
      if (!modelPop.hidden) paintModelList();
    },
  };
})();

/* ---------- 心跳：页面打开期间每 5 秒上报存活（桌面端 app 模式据此判断窗口是否关闭） ---------- */
setInterval(() => { fetch('/api/heartbeat', { method: 'POST' }).catch(() => {}); }, 5000);


/* ---------- Go 工具链检测 ---------- */
async function loadGoStatus(customPath) {
  const body = $('#go-status-body');
  if (!body) return;
  body.innerHTML = '<div class="skeleton" style="height:48px"></div>';
  try {
    const url = customPath ? ('/api/go-status?path=' + encodeURIComponent(customPath)) : '/api/go-status';
    const st = await api('GET', url);
    renderGoStatus(st);
  } catch (err) {
    body.innerHTML = '<p class="field-hint" style="color:var(--color-destructive);">检测失败：' + esc(err.message) + '</p>';
  }
}

function renderGoStatus(st) {
  const body = $('#go-status-body');
  if (!body) return;
  if (st.found) {
    body.innerHTML = '<div class="row" style="padding: var(--space-3) 0; align-items:center;">' +
      '<div class="row-main"><div class="row-title" style="color: var(--color-accent);">' + ICONS.check + ' Go 已检测到</div>' +
      '<div class="row-sub">' + esc(st.version || st.path) + '</div>' +
      '<div class="row-sub">来源: ' + esc(st.source || 'PATH') + ' · ' + esc(st.root || st.bin_dir || '') + '</div></div></div>';
  } else {
    body.innerHTML = '<div class="row" style="padding: var(--space-3) 0; align-items:center;">' +
      '<div class="row-main"><div class="row-title">' + ICONS.alert + ' 未检测到 Go 工具链</div>' +
      '<div class="row-sub">构建技能插件、从源码编译 Gleam 需要 Go 1.22+。请在下方填写路径或运行安装脚本。</div></div>' +
      '<button class="btn btn-primary btn-sm" id="go-install-btn">安装 Go</button></div>';
    const ib = $('#go-install-btn');
    if (ib) ib.addEventListener('click', () => installGoGuided());
  }
}

async function installGoGuided() {
  Modal.open('安装 Go 工具链', (box) => {
    const wrap = el('div');
    wrap.innerHTML = '<p class="field-hint" style="margin: 0 0 var(--space-3);">' +
      'Gleam 将自动下载并安装 Go 1.23.4（约 80MB，从阿里云镜像下载）。安装完成后自动写入 PATH。</p>' +
      '<p class="field-hint" style="margin: 0 0 var(--space-3);">也可以手动安装后，在下方填写路径检测。</p>';
    box.appendChild(wrap);
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const install = el('button', 'btn btn-primary', ICONS.zap + ' 自动下载安装');
    install.type = 'button';
    install.addEventListener('click', async () => {
      install.innerHTML = ICONS.spinner + ' 检查中…';
      install.disabled = true;
      try {
        const res = await api('POST', '/api/go-status/install', {});
        if (res.found) {
          renderGoStatus(res);
          toast('Go 已就绪：' + (res.version || ''), 'success', 6000);
          Modal.close();
        } else if (res.message) {
          // Show manual install instructions
          wrap.innerHTML = '<div class="callout callout--warn" style="margin: 0 0 var(--space-3);">' + ICONS.alert +
            '<div><div class="row-title">需要手动安装 Go</div>' +
            '<div class="row-sub">自动安装需要管理员权限。请按以下步骤操作：</div></div></div>' +
            '<ol style="margin: 0 0 var(--space-3) var(--space-4); padding-left: var(--space-4); line-height: 2;">' +
            '<li>在 Gleam 项目目录下运行安装脚本：<br><code class="inline-code">powershell -ExecutionPolicy Bypass -File scripts/install.ps1</code></li>' +
            '<li>或手动下载 Go：<br><a href="' + esc(res.download || 'https://go.dev/dl/') + '" target="_blank" style="color:var(--color-accent);">' + esc(res.download || 'https://go.dev/dl/') + '</a></li>' +
            '<li>安装完成后，在下方输入路径并点击「检测」</li>' +
            '</ol>';
          // Re-add path input
          const pathDiv = el('div', 'field');
          pathDiv.innerHTML = '<label class="field-label" for="go-path-input2">Go 安装路径</label>' +
            '<div style="display:flex; gap: var(--space-2);">' +
            '<input class="input" id="go-path-input2" placeholder="C:\\Go">' +
            '<button class="btn btn-primary btn-sm" id="go-path-check2">检测</button></div>';
          wrap.appendChild(pathDiv);
          $('#go-path-check2').addEventListener('click', async () => {
            const p = $('#go-path-input2').value.trim();
            if (!p) return;
            try {
              const st = await api('GET', '/api/go-status?path=' + encodeURIComponent(p));
              if (st.found) {
                renderGoStatus(st);
                toast('Go 检测成功！', 'success');
                Modal.close();
              } else {
                toast('未在此路径找到 go.exe，请检查路径', 'error');
              }
            } catch (e) { toast(e.message, 'error'); }
          });
          toast(res.message, 'info', 6000);
        }
      } catch (err) {
        toast('操作失败：' + err.message, 'error');
      } finally {
        install.disabled = false;
        install.innerHTML = ICONS.zap + ' 重试';
      }
    });
    actions.appendChild(cancel);
    actions.appendChild(install);
    box.appendChild(actions);
  });
}

$('#go-path-check')?.addEventListener('click', () => {
  const p = $('#go-path-input').value.trim();
  loadGoStatus(p);
});
$('#go-path-input')?.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') { const p = $('#go-path-input').value.trim(); loadGoStatus(p); }
});


/* ---------- 市场：MCP 服务器 + 技能模板 ---------- */
bindSegmented('#mc-trust');


// 标签筛选：只用目录里真有的 tags，客户端过滤，不新增后端
let mcpTagActive = '';
function renderMCPTags(presets) {
  const row = $('#mcp-tags');
  const tags = [...new Set(presets.flatMap((p) => p.tags || []))].slice(0, 12);
  row.innerHTML = '';
  if (!tags.length) return;
  if (mcpTagActive && !tags.includes(mcpTagActive)) mcpTagActive = '';
  ['', ...tags].forEach((t) => {
    const b = el('button', 'chip' + (t === mcpTagActive ? ' active' : ''), t || '全部');
    b.type = 'button';
    b.setAttribute('aria-pressed', String(t === mcpTagActive));
    b.addEventListener('click', () => { mcpTagActive = t; renderMCPTags(presets); applyMCPTagFilter(); });
    row.appendChild(b);
  });
  applyMCPTagFilter();
}
function applyMCPTagFilter() {
  document.querySelectorAll('#mcp-presets .market-item').forEach((c) => {
    c.hidden = !!mcpTagActive && !(c.dataset.tags || '').split('|').includes(mcpTagActive);
  });
}
document.querySelectorAll('#market-tabs .text-tab').forEach((tab) => {
  tab.addEventListener('click', () => {
    const k = tab.dataset.mtab;
    document.querySelectorAll('#market-tabs .text-tab').forEach((t) => {
      t.classList.toggle('active', t === tab);
      t.setAttribute('aria-selected', String(t === tab));
    });
    document.querySelectorAll('#view-market .market-panel').forEach((pn) => { pn.hidden = pn.dataset.mtab !== k; });
  });
});
document.querySelectorAll('[data-goto-view]').forEach((b) => b.addEventListener('click', () => showView(b.dataset.gotoView)));

async function loadMarket() {
  loadMCPMarket();
  loadSkillMarket();
  loadMCPInstalled();
}

async function loadMCPMarket() {
  const wrap = $('#mcp-presets');
  wrap.innerHTML = '<div class="skeleton" style="height:64px"></div>';
  try {
    const { presets } = await api('GET', '/api/market/mcp?q=' + encodeURIComponent($('#mcp-search').value.trim()));
    wrap.innerHTML = '';
    if (!presets || !presets.length) {
      wrap.innerHTML = `<div class="empty">${ICONS.search}<div class="empty-title">没有匹配的 MCP 服务器</div></div>`;
      return;
    }
    presets.forEach((p) => wrap.appendChild(mcpPresetCard(p)));
    renderMCPTags(presets);
  } catch (err) { loadError(wrap, err, loadMCPMarket); }
}

// 市场条目：字母图标 · 名称 · 一行说明；细节进 title，不再堆三行小字
function marketTile(name) {
  const t = el('span', 'market-icon', ([...String(name || '?')][0] || '?').toUpperCase());
  t.setAttribute('aria-hidden', 'true');
  return t;
}
function mcpPresetCard(p) {
  const card = el('div', 'market-item');
  card.dataset.tags = (p.tags || []).join('|');
  card.appendChild(marketTile(p.name));
  const main = el('div', 'market-main');
  main.innerHTML = `<div class="market-name">${esc(p.name)}${p.installed ? '<span class="badge badge--success">已安装</span>' : ''}${p.params && p.params.length ? '<span class="badge badge--mode">需配置</span>' : ''}</div>
    <div class="market-desc">${esc(p.desc)}</div>`;
  main.title = `${p.desc || ''}\n${p.command} · 信任 ${PERM_LABELS[p.trust] || p.trust}${p.tags && p.tags.length ? ' · ' + p.tags.join(' / ') : ''}`;
  const actions = el('div', 'row-actions');
  const btn = el('button', 'btn btn-secondary btn-sm', p.installed ? '重装' : '安装');
  btn.addEventListener('click', () => openMCPInstallDialog(p));
  actions.appendChild(btn);
  card.appendChild(main);
  card.appendChild(actions);
  return card;
}

function openMCPInstallDialog(p) {
  Modal.open(`${p.installed ? '重装' : '安装'} ${esc(p.name)}`, (box) => {
    const form = el('form');
    if (p.installed) {
      form.appendChild(el('p', 'field-hint', '该服务器已安装。重装会用下面填的命令与参数替换现有配置；只想临时别跑，请取消后到「已安装」里点停用。'));
    }
    (p.params || []).forEach((pm) => {
      const f = el('div', 'field');
      f.innerHTML = `<label class="field-label" for="mp-${esc(pm.key)}">${esc(pm.label)}${pm.required ? ' *' : ''}</label>`;
      const input = el('input', 'input');
      input.id = 'mp-' + pm.key;
      input.placeholder = pm.placeholder || '';
      input.dataset.key = pm.key;
      f.appendChild(input);
      form.appendChild(f);
    });
    if (!(p.params || []).length) form.appendChild(el('p', 'field-hint', '该服务器无需额外参数。'));
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const go = el('button', 'btn btn-primary', p.installed ? '确认重装' : '安装');
    actions.appendChild(cancel);
    actions.appendChild(go);
    form.appendChild(actions);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const params = {};
      (p.params || []).forEach((pm) => { params[pm.key] = form.querySelector('#mp-' + pm.key).value; });
      go.disabled = true;
      go.innerHTML = ICONS.spinner + ' 连接中…';
      const send = async (force) => {
        const out = await api('POST', '/api/market/mcp/install', { id: p.id, params, trust: p.trust, force });
        Modal.close();
        if (out.warning) toast(out.warning, 'info', 7000);
        else toast(`${p.name} 已连接（${out.tools} 个工具已注册）`, 'success', 6000);
        loadMCPInstalled();
        loadMCPMarket();
      };
      try {
        await send(p.installed);
      } catch (err) {
        // 目录里的「已安装」是打开面板那一刻的快照，可能已经过时：409 就当场补一次确认
        if (err.status === 409 && await confirmModal(err.message, '已经装过了', { okText: '重装它', danger: true })) {
          try { await send(true); return; } catch (err2) { toast(`安装失败：${err2.message}`, 'error', 6500); }
        } else {
          toast(`安装失败：${err.message}`, 'error', 6500);
        }
        go.disabled = false;
        go.textContent = p.installed ? '确认重装' : '安装';
      }
    });
    box.appendChild(form);
  });
}

async function loadSkillMarket() {
  const wrap = $('#skill-presets');
  wrap.innerHTML = '<div class="skeleton" style="height:64px"></div>';
  try {
    const { presets } = await api('GET', '/api/market/skills?q=' + encodeURIComponent($('#skill-search').value.trim()));
    wrap.innerHTML = '';
    if (!presets || !presets.length) {
      wrap.innerHTML = `<div class="empty">${ICONS.search}<div class="empty-title">没有匹配的技能模板</div></div>`;
      return;
    }
    presets.forEach((s) => {
      const card = el('div', 'market-item');
      card.appendChild(marketTile(s.name));
      const main = el('div', 'market-main');
      main.innerHTML = `<div class="market-name">${esc(s.name)}${s.installed ? '<span class="badge badge--success">已安装</span>' : ''}</div>
        <div class="market-desc">${esc(s.description)}</div>`;
      main.title = `${s.description || ''}\n${(s.steps || []).length} 步 · 参数: ${(s.params || []).join(', ') || '无'}`;
      const actions = el('div', 'row-actions');
      const btn = el('button', 'btn btn-secondary btn-sm', s.installed ? '重装' : '安装');
      btn.addEventListener('click', async () => {
        if (s.installed && !await confirmModal(`技能「${s.name}」已经装过了。重装会把它恢复成市场模板，你改过的步骤会被替换；只想临时别用它，请取消后到技能页点停用。`,
          '已经装过了', { okText: '重装它', danger: true })) return;
        btn.disabled = true;
        try {
          await api('POST', '/api/market/skills/install', { name: s.name, force: s.installed });
          toast(`技能「${s.name}」已${s.installed ? '重装' : '安装'}，可在技能页运行`, 'success');
          btn.textContent = '已安装';
          loadSkills();
        } catch (err) {
          // 目录里的标记可能过时（别处刚装/删过）：409 就当场补一次确认
          if (err.status === 409 && await confirmModal(err.message, '已经装过了', { okText: '重装它', danger: true })) {
            try {
              await api('POST', '/api/market/skills/install', { name: s.name, force: true });
              toast(`技能「${s.name}」已重装，可在技能页运行`, 'success');
              btn.textContent = '已安装';
              loadSkills();
              return;
            } catch (err2) { toast(err2.message, 'error'); }
          } else {
            toast(err.message, 'error');
          }
          btn.disabled = false;
        }
      });
      actions.appendChild(btn);
      card.appendChild(main);
      card.appendChild(actions);
      wrap.appendChild(card);
    });
  } catch (err) { loadError(wrap, err, loadSkillMarket); }
}

async function loadMCPInstalled() {
  const wrap = $('#mcp-installed');
  try {
    const { mcp } = await api('GET', '/api/mcp');
    wrap.innerHTML = '';
    if (!mcp || !mcp.length) {
      wrap.innerHTML = `<div class="empty">${ICONS.zap}<div class="empty-title">尚未安装任何 MCP 服务器</div><p class="empty-desc">从上方目录选择安装，或使用自定义接入。</p></div>`;
      return;
    }
    mcp.forEach((m) => {
      const row = el('div', 'card row');
      const main = el('div', 'row-main');
      // 三态分开说：停用是用户自己的选择，未连接是服务器的故障——混成一句「未连接」，
      // 用户就会去点重连，而重连一个自己关掉的东西本来就该被拒。
      const state = !m.enabled
        ? '<span class="badge badge--cancelled">已停用</span>'
        : m.connected
          ? `<span class="badge badge--success">已连接 · ${m.tools} 个工具</span>`
          : `<span class="badge badge--warn">未连接</span>`;
      main.innerHTML = `<div class="row-title">${esc(m.name)} ${state}</div>
        <div class="row-sub"><code style="font-family: var(--font-mono); font-size: var(--fs-xs);">${esc(m.command)} ${esc((m.args || []).join(' '))}</code></div>
        <div class="stat">信任 ${esc(PERM_LABELS[m.trust] || m.trust)}${m.connected ? ` · ${m.tools} 个工具` : ''}</div>`;
      const actions = el('div', 'row-actions');
      const toggle = el('button', 'btn btn-ghost btn-sm');
      toggle.type = 'button';
      toggle.textContent = m.enabled ? '停用' : '启用';
      toggle.title = m.enabled ? '停用后保留命令与参数，工具暂时从注册表摘掉' : '启用后重新连接并挂回工具';
      toggle.setAttribute('aria-label', `${m.enabled ? '停用' : '启用'}工具服务 ${m.name}`);
      toggle.addEventListener('click', async () => {
        toggle.disabled = true;
        try {
          const out = await api('POST', `/api/mcp/${encodeURIComponent(m.name)}/enabled`, { enabled: !m.enabled });
          if (out.warning) toast(out.warning, 'info', 7000);
          else toast(out.enabled ? `已启用（${out.tools} 个工具）` : '已停用，工具已从注册表摘掉', 'success');
          loadMCPInstalled();
        } catch (err) {
          toast(err.message, 'error');
          toggle.disabled = false;
        }
      });
      actions.appendChild(toggle);
      if (m.enabled && !m.connected) {
        const retry = el('button', 'btn btn-secondary btn-sm', '重连');
        retry.addEventListener('click', async () => {
          retry.disabled = true;
          retry.textContent = '连接中…';
          try {
            const out = await api('POST', `/api/mcp/${encodeURIComponent(m.name)}/reconnect`);
            if (out.warning) toast(out.warning, 'info', 7000);
            else toast(`已连接（${out.tools} 个工具）`, 'success');
            loadMCPInstalled();
          } catch (err) { toast(err.message, 'error'); retry.disabled = false; retry.textContent = '重连'; }
        });
        actions.appendChild(retry);
      }
      const del = el('button', 'btn btn-danger btn-sm');
      del.innerHTML = ICONS.trash;
      del.setAttribute('aria-label', `卸载 ${m.name}`);
      del.addEventListener('click', async () => {
        if (!await confirmModal(`移除工具服务「${m.name}」？移除后它提供的工具将不再可用。`, '移除工具服务', { okText: '移除', danger: true })) return;
        try {
          const out = await api('DELETE', '/api/mcp/' + encodeURIComponent(m.name));
          toast(`已卸载（移除 ${out.removed_tools} 个工具）`, 'success');
          loadMCPInstalled();
          loadMCPMarket();
        } catch (err) { toast(err.message, 'error'); }
      });
      actions.appendChild(del);
      row.appendChild(main);
      row.appendChild(actions);
      wrap.appendChild(row);
    });
  } catch (err) { toast(err.message, 'error'); }
}

$('#mcp-search').addEventListener('keydown', (e) => { if (e.key === 'Enter') loadMCPMarket(); });
$('#skill-search').addEventListener('keydown', (e) => { if (e.key === 'Enter') loadSkillMarket(); });

$('#mc-install').addEventListener('click', async () => {
  const command = $('#mc-command').value.trim();
  if (!command) { toast('命令不能为空', 'error'); return; }
  const args = $('#mc-args').value.trim().split(/\s+/).filter(Boolean);
  const btn = $('#mc-install');
  const send = async (force) => {
    const out = await api('POST', '/api/mcp', {
      name: $('#mc-name').value.trim(),
      command,
      args,
      trust: segValue('#mc-trust') || 'user_approved',
      force,
    });
    if (out.warning) toast(out.warning, 'info', 7000);
    else toast(`已连接（${out.tools} 个工具已注册）`, 'success', 6000);
    $('#mc-name').value = ''; $('#mc-command').value = ''; $('#mc-args').value = '';
    loadMCPInstalled();
    loadMCPMarket();
  };
  btn.disabled = true;
  try {
    await send(false);
  } catch (err) {
    if (err.status === 409 && await confirmModal(err.message, '已经装过了', { okText: '重装它', danger: true })) {
      try { await send(true); } catch (err2) { toast(`安装失败：${err2.message}`, 'error', 6500); }
    } else {
      toast(`安装失败：${err.message}`, 'error', 6500);
    }
  } finally {
    btn.disabled = false;
  }
});

/* ---------- 工作区（任务文件夹） ---------- */
function wsShort(path) {
  if (!path) return '选择工作区';
  const parts = String(path).replace(/[\/]+$/, '').split(/[\/]/);
  return parts[parts.length - 1] || path;
}

async function loadWorkspace() {
  try {
    const ws = await api('GET', '/api/workspace');
    renderWorkspace(ws);
  } catch { /* 静默 */ }
}

function renderWorkspace(ws) {
  const text = $('#ws-chip-text');
  if (text) {
    text.textContent = ws.workspace ? '工作区 · ' + wsShort(ws.workspace) : '选择工作区';
    text.title = ws.workspace || '';
  }
  const foot = $('#composer-ws-text');
  if (foot) {
    foot.textContent = ws.workspace ? wsShort(ws.workspace) : '选择工作区（可选）';
    foot.title = ws.workspace || '';
  }
}
$('#composer-ws').addEventListener('click', () => openWorkspaceDialog());

$('#ws-chip').addEventListener('click', () => openWorkspaceDialog());

/* ================= 多会话（左侧常驻列表） ================= */
let currentConvo = null;     // 当前激活会话 {id,title,...}
let viewingConvo = false;    // 主区是否处于“会话对话”视图（否则是目标工作台）
const convoLive = new Map(); // task_id -> { assistantEl, buf } 进行中气泡

const CONVO_ICONS = {
  edit: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4 12.5-12.5z"/></svg>',
  del: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>',
};

async function ensureConvo() {
  if (currentConvo) return currentConvo;
  const c = await api('POST', '/api/conversations', { space_id: (typeof spaceState !== 'undefined' && spaceState.activeId) || 'default' });
  currentConvo = c;
  await enterConvoView(c, { create: true });
  return c;
}

/* ================= 微光空间 + 会话分组 ================= */
let spaceState = { activeId: 'default', spaces: [], workspace: '' };
const SPACE_EXPAND_KEY = 'gleam.space.expanded';

const SPACE_ICONS = {
  chevron: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg>',
  edit: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4 12.5-12.5z"/></svg>',
  del: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>',
  add: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>',
};

function loadExpandedSpaces() {
  try { return new Set(JSON.parse(localStorage.getItem(SPACE_EXPAND_KEY) || '[]')); }
  catch { return new Set(); }
}
function saveExpandedSpaces(set) {
  try { localStorage.setItem(SPACE_EXPAND_KEY, JSON.stringify([...set])); } catch {}
}

async function loadSpaces() {
  try {
    const view = await api('GET', '/api/spaces');
    spaceState.activeId = view.active_id || 'default';
    spaceState.spaces = view.spaces || [];
    spaceState.workspace = view.workspace || '';
    if (view.workspace) renderWorkspace({ workspace: view.workspace });
  } catch {
    spaceState.spaces = [];
  }
  return spaceState;
}

async function loadConvoList(activeId) {
  const root = $('#convo-list');
  root.innerHTML = '';

  await loadSpaces();
  let items = [];
  try {
    const res = await api('GET', '/api/conversations');
    items = res.conversations || [];
  } catch { items = []; }

  const bySpace = new Map();
  spaceState.spaces.forEach((sp) => bySpace.set(sp.id, []));
  items.forEach((it) => {
    const sid = it.space_id || 'default';
    if (!bySpace.has(sid)) bySpace.set(sid, []);
    bySpace.get(sid).push(it);
  });

  if (!spaceState.spaces.length) {
    root.appendChild(el('div', 'convo-empty', '还没有空间'));
    return;
  }

  const expanded = loadExpandedSpaces();
  const currentConvoId = activeId || (currentConvo && currentConvo.id);

  spaceState.spaces.forEach((sp) => {
    const list = bySpace.get(sp.id) || [];
    const isActive = sp.id === spaceState.activeId;
    // 激活空间默认展开；用户折叠过的偏好也尊重（激活空间仍展开）
    const open = isActive || expanded.has(sp.id);
    root.appendChild(spaceGroupEl(sp, list, { open, active: isActive, currentConvoId }));
  });
}

function spaceGroupEl(sp, list, opts) {
  const group = el('div', 'space-group' + (opts.open ? ' open' : '') + (opts.active ? ' active' : ''));
  group.dataset.spaceId = sp.id;

  const head = el('button', 'space-group-head');
  head.type = 'button';
  head.title = sp.path ? `绑定文件夹：${sp.path}` : sp.name;

  const chevron = el('span', 'space-chevron');
  chevron.innerHTML = SPACE_ICONS.chevron;
  const dot = el('span', 'space-dot');
  const name = el('span', 'space-name', sp.name || '未命名空间');
  const count = el('span', 'space-count', String(list.length));

  const ops = el('span', 'space-group-ops');
  const addBtn = el('button', 'space-op');
  addBtn.type = 'button'; addBtn.innerHTML = SPACE_ICONS.add; addBtn.title = '在此空间新建对话';
  addBtn.addEventListener('click', (e) => { e.stopPropagation(); startNewConvo(sp.id); });
  const editBtn = el('button', 'space-op');
  editBtn.type = 'button'; editBtn.innerHTML = SPACE_ICONS.edit; editBtn.title = '重命名空间';
  editBtn.addEventListener('click', (e) => { e.stopPropagation(); renameSpace(sp.id, sp.name); });
  ops.appendChild(addBtn); ops.appendChild(editBtn);
  if (!sp.is_default) {
    const delBtn = el('button', 'space-op');
    delBtn.type = 'button'; delBtn.innerHTML = SPACE_ICONS.del; delBtn.title = '删除空间（对话移入默认空间）';
    delBtn.addEventListener('click', (e) => { e.stopPropagation(); deleteSpace(sp.id, sp.name, list.length); });
    ops.appendChild(delBtn);
  }

  head.appendChild(chevron); head.appendChild(dot); head.appendChild(name);
  head.appendChild(count); head.appendChild(ops);

  const body = el('div', 'space-group-body');
  if (list.length) {
    list.forEach((it) => body.appendChild(convoItemEl(it, it.id === opts.currentConvoId)));
  } else {
    body.appendChild(el('div', 'space-group-empty', '暂无对话'));
  }

  // 点击空间标题：展开/折叠，并切换为当前激活空间（联动绑定的文件夹）
  head.addEventListener('click', () => toggleSpace(sp.id, group));

  group.appendChild(head);
  group.appendChild(body);
  return group;
}

async function toggleSpace(spaceId, groupEl) {
  const expanded = loadExpandedSpaces();
  const willOpen = !groupEl.classList.contains('open');
  if (willOpen) {
    groupEl.classList.add('open');
    expanded.add(spaceId);
  } else {
    // 激活空间不允许折叠收起（保持当前空间始终可见）
    if (spaceId === spaceState.activeId) return;
    groupEl.classList.remove('open');
    expanded.delete(spaceId);
  }
  saveExpandedSpaces(expanded);

  // 切换激活空间（后端会联动切换绑定的工作文件夹）
  if (spaceId !== spaceState.activeId) {
    try {
      const view = await api('POST', `/api/spaces/${encodeURIComponent(spaceId)}/activate`);
      spaceState.activeId = view.active_id || spaceId;
      if (view.workspace) renderWorkspace({ workspace: view.workspace });
      await loadConvoList(currentConvo && currentConvo.id);
    } catch (err) { toast(`切换空间失败：${err.message}`, 'error'); }
  }
}

async function renameSpace(id, oldName) {
  Modal.open('重命名微光空间', (box) => {
    const input = document.createElement('input');
    input.className = 'input';
    input.type = 'text';
    input.value = oldName || '';
    input.maxLength = 30;
    box.appendChild(input);
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button'; cancel.addEventListener('click', Modal.close);
    const ok = el('button', 'btn btn-primary', '保存');
    ok.type = 'button';
    ok.addEventListener('click', async () => {
      const name = input.value.trim();
      if (!name) { toast('名称不能为空', 'info'); return; }
      try {
        const view = await api('PATCH', `/api/spaces/${encodeURIComponent(id)}`, { name });
        spaceState.spaces = view.spaces || spaceState.spaces;
        Modal.close();
        await loadConvoList(currentConvo && currentConvo.id);
        toast('空间已重命名', 'success');
      } catch (err) { toast(`重命名失败：${err.message}`, 'error'); }
    });
    actions.appendChild(cancel); actions.appendChild(ok);
    box.appendChild(actions);
    setTimeout(() => input.focus(), 50);
  });
}

async function deleteSpace(id, name, count) {
  const msg = count > 0
    ? `删除空间「${name}」？其中 ${count} 个对话会移入「默认空间」，不会丢失。`
    : `确定删除空间「${name}」？`;
  if (!await confirmModal(msg, '删除空间', { okText: '删除', danger: true })) return;
  try {
    const view = await api('DELETE', `/api/spaces/${encodeURIComponent(id)}`);
    spaceState.activeId = view.active_id || 'default';
    spaceState.spaces = view.spaces || [];
    if (view.workspace) renderWorkspace({ workspace: view.workspace });
    await loadConvoList(currentConvo && currentConvo.id);
    toast('空间已删除，对话已移入默认空间', 'success');
  } catch (err) { toast(`删除失败：${err.message}`, 'error'); }
}

function createSpaceDialog() {
  Modal.open('新建微光空间', (box) => {
    const wrap = el('div');
    wrap.innerHTML = `
      <p class="field-hint" style="margin: 0 0 var(--space-3);">每个空间对应一个独立文件夹，对话按空间隔离分组。可先不绑定文件夹，之后在工作区里再设置。</p>
      <div class="field">
        <label class="field-label">空间名称</label>
        <input class="input" id="sp-name" type="text" maxlength="30" placeholder="例如：官网改版、学习笔记" />
      </div>
      <div class="field">
        <label class="field-label">绑定文件夹（可选）</label>
        <div class="ws-crumb" id="sp-crumb"></div>
        <div id="sp-dirs" style="max-height: 220px; overflow: auto;"></div>
      </div>`;
    box.appendChild(wrap);
    const nameInput = $('#sp-name', box);
    let chosenPath = '';

    async function browse(path) {
      const res = await api('GET', '/api/fs?path=' + encodeURIComponent(path || ''));
      chosenPath = res.current || path || '';
      const crumb = $('#sp-crumb', box);
      crumb.innerHTML = '';
      if (res.parent) {
        const up = el('button', 'btn btn-secondary btn-sm', '上一级');
        up.addEventListener('click', () => browse(res.parent));
        crumb.appendChild(up);
      }
      crumb.appendChild(el('span', null, chosenPath || '根目录（未选择）'));
      const dirs = $('#sp-dirs', box);
      dirs.innerHTML = '';
      (res.dirs || []).forEach((d) => {
        const row = el('div', 'ws-row');
        row.innerHTML = `<div class="row-main"><div class="row-title">${esc(d.name)}</div><div class="row-sub">${esc(d.path)}</div></div>`;
        row.addEventListener('click', () => browse(d.path));
        dirs.appendChild(row);
      });
      if (!(res.dirs || []).length) dirs.innerHTML = '<p class="field-hint" style="margin:0;">此目录下没有子文件夹</p>';
    }

    const actions = el('div', 'modal-actions');
    const skip = el('button', 'btn btn-secondary', '暂不绑定文件夹');
    skip.type = 'button';
    skip.addEventListener('click', () => submitSpace(''));
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button'; cancel.addEventListener('click', Modal.close);
    const ok = el('button', 'btn btn-primary', '创建并进入');
    ok.type = 'button';
    ok.addEventListener('click', () => submitSpace(chosenPath));
    actions.appendChild(cancel); actions.appendChild(skip); actions.appendChild(ok);
    box.appendChild(actions);

    async function submitSpace(path) {
      const name = nameInput.value.trim();
      try {
        const view = await api('POST', '/api/spaces', { name, path });
        spaceState.activeId = view.active_id || spaceState.activeId;
        spaceState.spaces = view.spaces || [];
        if (view.workspace) renderWorkspace({ workspace: view.workspace });
        Modal.close();
        await loadConvoList();
        toast(path ? `空间已创建，工作区切换到：${wsShort(path)}` : '空间已创建', 'success');
      } catch (err) { toast(`创建失败：${err.message}`, 'error'); }
    }

    browse('');
    setTimeout(() => nameInput.focus(), 50);
  });
}
$('#space-new').addEventListener('click', createSpaceDialog);


function convoItemEl(it, active) {
  const item = el('button', 'convo-item' + (active ? ' active' : ''));
  item.type = 'button';
  item.dataset.convoId = it.id;
  item.title = it.title;
  const title = el('span', 'convo-item-title', it.title || '新对话');
  const ops = el('span', 'convo-item-ops');
  const edit = el('button', 'convo-op');
  edit.type = 'button'; edit.innerHTML = CONVO_ICONS.edit; edit.title = '重命名';
  edit.addEventListener('click', (e) => { e.stopPropagation(); renameConvo(it.id, it.title); });
  const del = el('button', 'convo-op convo-op--del');
  del.type = 'button'; del.innerHTML = CONVO_ICONS.del; del.title = '删除';
  del.addEventListener('click', (e) => { e.stopPropagation(); deleteConvo(it.id, it.title); });
  ops.appendChild(edit); ops.appendChild(del);
  item.appendChild(title); item.appendChild(ops);
  item.addEventListener('click', () => openConvo(it.id));
  return item;
}

function setThreadMode(on) {
  viewingConvo = on;
  const sec = $('#view-goals');
  sec.classList.toggle('thread-mode', on);
  goalInput.placeholder = composerPlaceholder();
  $('#goal-filters').parentElement.style.display = on ? 'none' : '';
  document.querySelector('.command-deck').style.display = on ? 'none' : '';
  document.querySelector('.quick-launch').style.display = on ? 'none' : '';
  // 候补目标属于「开一个新的」那一屏：读会话时把这一坨收起，否则它和整页概览抢位置。
  // 用 display 而不是 hidden——`hidden` 归 renderCues 管（"有没有话要说"），两个开关各管一件事。
  document.querySelector('.cue-deck').style.display = on ? 'none' : '';
}

// 首页 / 空任务的插图位：Gleam 自己的折线标记，与 index.html 里的静态版保持一致
const HERO_ART = `<div class="hero-art" aria-hidden="true"><svg viewBox="0 0 96 96" width="96" height="96" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><rect x="18" y="18" width="60" height="60" rx="18" stroke-width="1.6" opacity=".35"/><g stroke-width="3"><circle cx="48" cy="48" r="7"/><path d="M48 30v6M48 60v6M30 48h6M60 48h6M35.3 35.3l4.2 4.2M56.5 56.5l4.2 4.2M35.3 60.7l4.2-4.2M56.5 39.5l4.2-4.2"/></g></svg></div>`;

function resetFeedToEmpty(title, desc, opts = {}) {
  const feed = $('#goal-feed');
  feed.innerHTML = '';
  const empty = el('div', opts.home ? 'empty home-hero' : 'empty home-hero home-hero--task');
  empty.id = 'goals-empty';
  empty.innerHTML = `${HERO_ART}<div class="empty-title">${esc(title)}</div><p class="empty-desc">${esc(desc)}</p>`
    + (opts.home ? '<section class="activity-card" aria-label="本机活动"></section>' : '');
  feed.appendChild(empty);
}

async function enterConvoView(c, opts = {}) {
  currentConvo = c;
  setThreadMode(true);
  $('#goals-title').textContent = c.title || '新对话';
  if (opts.create) {
    resetFeedToEmpty('可以直接开口说了', '说出你想推进的事，Gleam 会边听边整理这轮会话的上下文。');
  }
  await loadConvoList(c.id);
  refreshWorkbench();
}

async function startNewConvo(spaceId) {
  const running = [...tasks.values()].filter((t) => t.info.status === 'running').length;
  if (running > 0) { toast(`还有 ${running} 个目标正在执行，完成后再开始新对话`, 'info'); return; }
  try {
    // 指定了别的空间：先切换激活空间（后端联动切换绑定的文件夹）
    if (spaceId && spaceId !== spaceState.activeId) {
      const view = await api('POST', `/api/spaces/${encodeURIComponent(spaceId)}/activate`);
      spaceState.activeId = view.active_id || spaceId;
      if (view.workspace) renderWorkspace({ workspace: view.workspace });
    }
    const c = await api('POST', '/api/conversations', { space_id: spaceId || spaceState.activeId });
    currentConvo = c;
    setThreadMode(true);
    $('#goals-title').textContent = c.title || '新对话';
    resetFeedToEmpty('可以直接开口说了', '说出你想推进的事，Gleam 会边听边整理这轮会话的上下文。');
    convoLive.clear();
    await loadConvoList(c.id);
    goalInput.focus();
    toast('已开始新对话', 'success');
  } catch (err) { toast(`新建对话失败：${err.message}`, 'error'); }
}

async function openConvo(id) {
  try {
    // 载入历史到短期上下文，便于在旧会话上继续
    const c = await api('POST', `/api/conversations/${encodeURIComponent(id)}/activate`);
    currentConvo = c;
    convoLive.clear();
    setThreadMode(true);
    $('#goals-title').textContent = c.title || '新对话';
    renderConvoMessages(c);
    await loadConvoList(c.id);
    goalInput.focus();
  } catch (err) { toast(`打开对话失败：${err.message}`, 'error'); }
}

async function exitConvoToWorkbench() {
  setThreadMode(false);
  currentConvo = null;
  $('#goals-title').textContent = '目标';
  tasks.clear();
  await loadGoals();
}

function renderConvoMessages(c) {
  const feed = $('#goal-feed');
  feed.innerHTML = '';
  const msgs = c.messages || [];
  if (!msgs.length) {
    resetFeedToEmpty('这是一个空对话', '在下方输入第一条消息开始。');
    return;
  }
  // 相邻同角色消息各自成气泡；按 task_id 归组提供“查看执行详情”入口
  msgs.forEach((m) => feed.appendChild(convoBubble(m)));
  scrollToBottom(false);
  refreshWorkbench();
}

// 气泡底部操作区：历史渲染和 SSE 完成回填共用这一份。
// 以前只写在历史那半，刚跑完的回复要刷新页面才长出「反馈这条」——正是"判据对、线没接"。
function appendMsgActs(bodyEl, m) {
  const acts = el('div', 'msg-acts');
  // 「反馈这条」永远指着**这一次**运行（带 task_id），不是"最近一次"：
  // 过了三天回来抱怨刚才那个回答，反馈却不该指向昨天最后跑的那件事。
  const fb = el('button', 'msg-detail');
  fb.type = 'button';
  fb.textContent = '反馈这条';
  fb.title = '就着这次回答写一条反馈：运行现场会自动带上这一次的状态';
  fb.addEventListener('click', () => openFeedbackView(m.task_id));
  acts.appendChild(fb);
  if (m.mode && m.mode !== 'chat') {
    const more = el('button', 'msg-detail');
    more.type = 'button';
    more.textContent = '查看执行详情';
    more.addEventListener('click', () => openTaskDetail(m.task_id));
    acts.appendChild(more);
  }
  bodyEl.appendChild(acts);
}

function convoBubble(m) {
  const wrap = el('div', `msg msg--${m.role === 'user' ? 'user' : 'assistant'}`);
  const body = el('div', 'msg-body');
  const content = el('div', 'msg-content');
  content.innerHTML = renderMarkdown(m.content || '');
  body.appendChild(content);
  if (m.role === 'assistant' && m.task_id) appendMsgActs(body, m);
  if (m.role === 'assistant' && m.status && m.status !== 'success') {
    const tag = el('span', `msg-status msg-status--${esc(m.status)}`, STATUS_LABEL[m.status] || m.status);
    body.appendChild(tag);
  }
  wrap.appendChild(body);
  return wrap;
}

async function openTaskDetail(taskID) {
  try {
    const info = await api('GET', '/api/goals/' + taskID);
    Modal.open(`执行详情 · ${esc((info.goal || '').slice(0, 40))}`, (box) => {
      const meta = el('div', 'field-hint');
      // 分数在 result 里，taskInfo 顶层没有 score——读错层会让每次详情都显示「完成度 —/100」
      const score = info.result && Number.isFinite(info.result.score) ? info.result.score : '—';
      meta.textContent = `${STATUS_LABEL[info.status] || info.status} · 完成度 ${score}/100`;
      box.appendChild(meta);
      if (info.result && info.result.summary) {
        const s = el('div', 'msg-content');
        s.style.margin = '8px 0';
        s.innerHTML = renderMarkdown(info.result.summary);
        box.appendChild(s);
      }
      if (info.result && info.result.error) {
        const e = el('div', 'callout callout--error', info.result.error);
        box.appendChild(e);
      }
      const chSlot = el('div');
      renderChangesInto(chSlot, taskID, info.result && info.result.changes);
      box.appendChild(chSlot);
      const steps = (info.result && info.result.steps) || [];
      if (steps.length) {
        const list = el('div', 'timeline');
        steps.forEach((st) => {
          const it = el('div', 'timeline-item');
          // reply 步骤的 .text 是人话，直接展示；其余对象输出折进「查看原始输出」，
          // 不再把 JSON.stringify 的整坨怼到时间线上（2026-09-23 QA 报告 M2）。
          let readable = '';
          if (typeof st.output === 'string') readable = st.output;
          else if (st.output && typeof st.output === 'object' && typeof st.output.text === 'string') readable = st.output.text;
          it.innerHTML = `<span class="tl-kind">${esc(st.tool || '步骤')}</span>${esc(readable || st.error || st.status || '')}`;
          if (st.output != null && typeof st.output === 'object') {
            const d = document.createElement('details');
            d.className = 'tl-raw';
            d.innerHTML = `<summary>查看原始输出</summary><pre>${esc(JSON.stringify(st.output, null, 2))}</pre>`;
            it.appendChild(d);
          }
          list.appendChild(it);
        });
        box.appendChild(list);
      }
    });
  } catch { toast('该任务详情已不在内存（重启后仅保留对话记录）', 'info'); }
}

// 提交后即时渲染“用户气泡 + 进行中的助手气泡”，完成/流式由 SSE 回填
function appendLiveConvoTurn(goal, taskID, taskMode) {
  const feed = $('#goal-feed');
  const empty = $('#goals-empty');
  if (empty) empty.remove();
  feed.appendChild(convoBubble({ role: 'user', content: goal }));
  const aWrap = el('div', 'msg msg--assistant');
  const aBody = el('div', 'msg-body');
  const aContent = el('div', 'msg-content streaming');
  aContent.textContent = taskMode === 'chat' ? '思考中…' : '执行中…';
  aBody.appendChild(aContent);
  aWrap.appendChild(aBody);
  feed.appendChild(aWrap);
  // 会话模式下卡片是隐藏的，停止入口必须长在气泡上，否则运行中的对话无从打断
  mountStopButton(aBody, taskID, 'running');
  convoLive.set(taskID, { assistantEl: aContent, bodyEl: aBody, buf: '', wrap: aWrap, approvalEl: null, taskMode: taskMode });
  scrollToBottom(false);
}

// 会话视图内联渲染审批卡（批准/拒绝复用统一裁决接口）
function convoApproval(ap) {
  const live = convoLive.get(ap.task_id);
  if (!live || live.approvalEl) return;
  const riskCls = ap.risk === 'high' ? 'risk-high' : 'risk-medium';
  const box = el('div', 'approval-card convo-approval');
  box.dataset.approvalId = ap.id;
  box.innerHTML = `
    <div class="approval-head">${ICONS.alert}<span class="approval-title">需要你的批准</span><span class="badge badge--${riskCls}">${ap.risk === 'high' ? '高风险' : '中风险'}</span></div>
    <ul class="approval-list">${(ap.plan || []).map((p) => `<li>${esc(p)}</li>`).join('')}</ul>
    ${ap.reason ? `<div class="approval-reason">${esc(ap.reason)}</div>` : ''}
    <div class="approval-actions">
      <button class="btn btn-primary btn-sm" data-act="approve">${ICONS.check} 批准执行</button>
      <button class="btn btn-secondary btn-sm" data-act="deny">${ICONS.x} 拒绝</button>
    </div>`;
  box.querySelector('[data-act="approve"]').addEventListener('click', () => resolveApproval(ap.id, true, box, null));
  box.querySelector('[data-act="deny"]').addEventListener('click', () => resolveApproval(ap.id, false, box, null));
  live.wrap.after(box);
  live.approvalEl = box;
  scrollToBottom();
}

function convoStream(taskID, delta) {
  const live = convoLive.get(taskID);
  if (!live) return;
  live.buf += delta;
  live.assistantEl.classList.add('streaming');
  live.assistantEl.innerHTML = renderMarkdown(live.buf);
  scrollToBottom();
}

function convoComplete(taskID, result) {
  const live = convoLive.get(taskID);
  const text = (result && (result.summary || result.error)) || '';
  if (live) {
    live.assistantEl.classList.remove('streaming');
    live.assistantEl.innerHTML = renderMarkdown(text || '（无回复内容）');
    if (live.approvalEl) live.approvalEl.remove();
    appendMsgActs(live.bodyEl, { task_id: taskID, mode: live.taskMode });
    // 跑完了就没有可停止的东西：按钮必须跟着走，留下它只会让人点了个空
    mountStopButton(live.bodyEl, taskID, 'done');
    convoLive.delete(taskID);
  } else if (viewingConvo && currentConvo) {
    // 非实时（重连/晚到完成事件）：回源刷新整段会话保证一致
    api('GET', `/api/conversations/${encodeURIComponent(currentConvo.id)}`)
      .then(renderConvoMessages).catch(() => {});
  }
  if (viewingConvo && currentConvo) {
    loadConvoList(currentConvo.id);
    // 首条消息后后端会按内容生成标题，回源同步顶部标题
    api('GET', `/api/conversations/${encodeURIComponent(currentConvo.id)}`)
      .then((c) => {
        if (c.title && c.title !== currentConvo.title) {
          currentConvo.title = c.title;
          $('#goals-title').textContent = c.title;
        }
      })
      .catch(() => {});
  }
  scrollToBottom();
}

async function renameConvo(id, oldTitle) {
  const title = await promptModal('给这个对话起个名字', oldTitle || '', '重命名对话');
  if (title === null) return;
  try {
    const c = await api('PATCH', `/api/conversations/${encodeURIComponent(id)}`, { title });
    if (currentConvo && currentConvo.id === id) { currentConvo.title = c.title; $('#goals-title').textContent = c.title; }
    await loadConvoList(currentConvo && currentConvo.id);
  } catch (err) { toast(`重命名失败：${err.message}`, 'error'); }
}

async function deleteConvo(id, title) {
  if (!await confirmModal(`删除对话「${title || '新对话'}」？此操作不可恢复。`, '删除对话', { okText: '删除', danger: true })) return;
  try {
    await api('DELETE', `/api/conversations/${encodeURIComponent(id)}`);
    if (currentConvo && currentConvo.id === id) await exitConvoToWorkbench();
    else await loadConvoList(currentConvo && currentConvo.id);
    toast('对话已删除', 'success');
  } catch (err) { toast(`删除失败：${err.message}`, 'error'); }
}

// 必须包一层：直接交 startNewConvo 给 addEventListener，第一个实参就是 MouseEvent，
// 它被当成 spaceId 送去 activate —— POST /api/spaces/[object Object]/activate 必然失败，
// 「新对话」于是每次弹"新建对话失败"，按钮从来没通过。
$('#convo-new').addEventListener('click', () => startNewConvo());
// 顶部“新对话”同样创建持久化会话
$('#new-chat-btn').addEventListener('click', () => startNewConvo());

function openWorkspaceDialog() {
  Modal.open('选择任务工作区', (box) => {
    const wrap = el('div');
    wrap.innerHTML = `
      <p class="field-hint" style="margin: 0 0 var(--space-3);">目标中的文件操作将限定在工作区内。切换立即生效，写入不重启。</p>
      <div class="field">
        <label class="field-label">最近使用</label>
        <div id="ws-recents"></div>
      </div>
      <div class="field">
        <label class="field-label">浏览文件夹</label>
        <div class="ws-crumb" id="ws-crumb"></div>
        <div id="ws-dirs" style="max-height: 260px; overflow: auto;"></div>
      </div>`;
    box.appendChild(wrap);

    let current = '';

    async function refreshView() {
      const rec = $('#ws-recents');
      let ws;
      // 这些浏览函数由点击直接调用：不接住 rejection 就只剩一个永远转不完的骨架屏
      try { ws = await api('GET', '/api/workspace'); }
      catch (err) { rec.innerHTML = `<p class="field-hint" style="margin:0;color:var(--color-destructive);">${esc(err.message)}</p>`; return; }
      rec.innerHTML = '';
      (ws.recents || []).forEach((r) => {
        const row = el('div', 'ws-row');
        row.innerHTML = `<div class="row-main"><div class="row-title">${esc(wsShort(r))}</div><div class="row-sub">${esc(r)}</div></div>`;
        row.addEventListener('click', () => pick(r));
        rec.appendChild(row);
      });
      if (!(ws.recents || []).length) rec.innerHTML = '<p class="field-hint" style="margin:0;">暂无记录</p>';
    }

    async function browse(path) {
      const dirs = $('#ws-dirs');
      let res;
      try { res = await api('GET', '/api/fs?path=' + encodeURIComponent(path || '')); }
      catch (err) { dirs.innerHTML = `<p class="field-hint" style="margin:0;color:var(--color-destructive);">${esc(err.message)}</p>`; return; }
      current = res.current || path || '';
      const crumb = $('#ws-crumb');
      crumb.innerHTML = '';
      if (res.parent !== undefined && res.parent !== null && res.parent !== '') {
        const up = el('button', 'btn btn-secondary btn-sm', '上一级');
        up.addEventListener('click', () => browse(res.parent));
        crumb.appendChild(up);
      }
      crumb.appendChild(el('span', null, res.current || (path ? path : '根目录')));
      dirs.innerHTML = '';
      (res.dirs || []).forEach((d) => {
        const row = el('div', 'ws-row');
        row.innerHTML = `<div class="row-main"><div class="row-title">${esc(d.name)}</div><div class="row-sub">${esc(d.path)}</div></div>`;
        row.addEventListener('click', () => browse(d.path));
        dirs.appendChild(row);
      });
      if (!(res.dirs || []).length) dirs.innerHTML = '<p class="field-hint" style="margin:0;">此目录下没有子文件夹</p>';
    }

    async function pick(path) {
      try {
        const ws = await api('POST', '/api/workspace', { path });
        Modal.close();
        toast(`工作区已切换：${wsShort(ws.workspace)}`, 'success');
        renderWorkspace(ws);
      } catch (err) {
        toast(err.message, 'error');
      }
    }

    const actions = el('div', 'modal-actions');
    const chooseBtn = el('button', 'btn btn-primary', '选择当前文件夹');
    chooseBtn.type = 'button';
    chooseBtn.addEventListener('click', () => { if (current) pick(current); else toast('请先进入一个文件夹', 'info'); });
    const closeBtn = el('button', 'btn btn-secondary', '取消');
    closeBtn.type = 'button';
    closeBtn.addEventListener('click', Modal.close);
    actions.appendChild(closeBtn);
    actions.appendChild(chooseBtn);
    box.appendChild(actions);

    refreshView();
    browse('');
  });
}

/* ---------- 权限模式切换警告 ---------- */
const PermWarning = (() => {
  let overlay = null;
  let resolveCb = null;

  function createOverlay() {
    if (overlay) return overlay;
    overlay = document.createElement('div');
    overlay.className = 'perm-warning-overlay';
    overlay.innerHTML = `
      <div class="perm-warning" role="alertdialog" aria-labelledby="perm-warning-title" aria-describedby="perm-warning-desc">
        <div class="perm-warning-icon">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/>
            <path d="M12 9v4M12 17h.01"/>
          </svg>
        </div>
        <h3 id="perm-warning-title">切换权限前，想先和你说几句</h3>
        <p id="perm-warning-desc">你正在把 Gleam 切换到<strong>"完全访问"</strong>模式，这意味着：</p>
        <ul>
          <li>允许使用当前工作区内已授权的全部工具</li>
          <li>文件删除、命令执行等<strong>高危操作</strong>仍然必须经过你的批准</li>
          <li>普通步骤会自动推进，遇到风险边界时会停下来询问你</li>
        </ul>
        <p style="font-size: var(--fs-xs); color: var(--color-fg-muted); margin-bottom: var(--space-4);">
          小提示：如果你不确定，可以先试试“请我批准”模式。
        </p>
        <div class="perm-warning-actions">
          <button class="btn btn-secondary" id="perm-warning-cancel" type="button">暂不切换，我再想想</button>
          <button class="btn btn-primary" id="perm-warning-confirm" type="button">我已了解风险，确认切换</button>
        </div>
      </div>
    `;
    document.body.appendChild(overlay);

    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) close(false);
    });
    overlay.querySelector('#perm-warning-cancel').addEventListener('click', () => close(false));
    overlay.querySelector('#perm-warning-confirm').addEventListener('click', () => close(true));

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && overlay && overlay.classList.contains('active')) {
        close(false);
      }
    });

    return overlay;
  }

  function close(confirmed) {
    const ov = createOverlay();
    ov.classList.remove('active');
    if (resolveCb) {
      resolveCb(confirmed);
      resolveCb = null;
    }
  }

  function show() {
    return new Promise((resolve) => {
      resolveCb = resolve;
      const ov = createOverlay();
      ov.classList.add('active');
    });
  }

  return { show };
})();

function _doSetPerm(mode, persist = true) {
  const prev = currentMode;
  currentMode = mode;
  const display = mode === 'interactive' ? 'plan_first' : mode;
  document.querySelectorAll('#perm-seg button').forEach((b) =>
    b.setAttribute('aria-pressed', String(b.dataset.perm === display)));
  const sub = document.getElementById('plus-plan-sub');
  // L7：菜单副标题写「下一步动作」而不是「上次切换的结果」——过去时状态放这迟早过时
  if (sub) sub.textContent = (mode === 'plan_first' || mode === 'interactive') ? '切换「完全访问」' : '切换「请我批准」';
  if (!persist) return;
  // 后端不答应就必须把芯片拨回去：挂着「完全访问」却按旧模式跑，
  // 下一次目标要么该批的没批、要么不该批的悄悄跑完，两种都比报错更糟。
  api('POST', '/api/settings', { safety: { mode } }).catch((err) => {
    _doSetPerm(prev, false);
    toast(`权限模式未切换：${err.message}`, 'error');
  });
}

/* ---------- 专家角色 ---------- */
const roleSelect = document.getElementById('role-select');
// 变更监听只绑一次：绑在拉取成功的分支里，等于每次刷新都多挂一个，
// 而 /api/roles 失败那一次之后它干脆就不绑了——选角色静默失效。
if (roleSelect) roleSelect.addEventListener('change', () => { currentRole = roleSelect.value; });

async function loadRoles() {
  const sel = roleSelect;
  if (!sel) return;
  try {
    const data = await api('GET', '/api/roles');
    const roles = data.roles || [];
    sel.innerHTML = '';
    roles.forEach((r) => {
      const opt = document.createElement('option');
      opt.value = r.id;
      opt.textContent = r.name;
      opt.title = r.description || '';
      sel.appendChild(opt);
    });
    sel.value = currentRole;
  } catch { /* 静默失败，角色选择不影响核心功能 */ }
}

/* ---------- 成长日志 ---------- */
async function loadGrowth() {
  await Promise.all([loadGrowthStats(), loadGrowthTimeline()]);
}

async function loadGrowthStats() {
  try {
    const data = await api('GET', '/api/growth');
    const stats = data.stats;
    if (!stats) {
      document.getElementById('gs-level').textContent = '萌新微光';
      return;
    }
    document.getElementById('gs-level').textContent = stats.level || '萌新微光';
    document.getElementById('gs-tasks').textContent = stats.total_tasks || 0;
    document.getElementById('gs-skills').textContent = stats.total_skills || 0;
    document.getElementById('gs-avg').textContent = stats.avg_score ? Math.round(stats.avg_score) : '—';
    document.getElementById('gs-streak').textContent = stats.recent_streak || 0;
    const dur = stats.total_duration_seconds || 0;
    document.getElementById('gs-duration').textContent = dur > 0 ? formatDuration(dur) : '—';
    const tok = stats.total_tokens || 0;
    document.getElementById('gs-tokens').textContent = tok > 0 ? formatTokens(tok) : '—';
    document.getElementById('gs-week-tokens').textContent = stats.week_tokens ? formatTokens(stats.week_tokens) : '—';
    const progress = Math.round((stats.level_progress || 0) * 100);
    document.getElementById('gs-progress').style.setProperty('--progress', String(progress / 100));
  } catch { /* 静默 */ }
}

async function loadGrowthTimeline() {
  const wrap = document.getElementById('growth-timeline');
  if (!wrap) return;
  try {
    const data = await api('GET', '/api/growth/recent?n=30');
    const entries = data.entries || [];
    if (!entries.length) { wrap.innerHTML = '<p class="empty-hint">尚无记录，完成第一个任务即可开始成长。</p>'; return; }
    wrap.innerHTML = '';
    entries.forEach((e) => {
      const item = document.createElement('div');
      item.className = 'growth-item';
      const icon = e.type === 'skill_created' ? 'sparkle' : (e.type === 'skill_used' ? 'bolt' : 'check');
      const time = new Date(e.time).toLocaleString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
      const scoreBadge = e.score ? ` <span class="growth-score">${e.score}/100</span>` : '';
      const desc = e.goal ? shorten(e.goal, 60) : (e.skill_name || e.detail || '');
      item.innerHTML = '<span class="growth-item-icon growth-icon-' + icon + '"></span>' +
        '<div class="growth-item-body">' +
        '<div class="growth-item-title">' + esc(desc) + scoreBadge + '</div>' +
        '<div class="growth-item-meta">' + esc(typeLabel(e.type)) + ' · ' + esc(time) + '</div>' +
        '</div>';
      wrap.appendChild(item);
    });
  } catch { /* 静默 */ }
}

function typeLabel(type) {
  // skill_created 不只来自"把做法固化成技能"，市场安装与手动新建也记同一条
  // （入库事件只有一个出口，见 Agent.SkillSave），所以标签不能说"固化"。
  const labels = { task_completed: '任务完成', skill_created: '技能新增', skill_used: '技能使用', milestone: '里程碑', efficiency: '效率提升' };
  return labels[type] || type;
}

// formatTokens 把 token 数压成人类可读的量级（12345 -> 12.3k）。
function formatTokens(n) {
  const v = Number(n) || 0;
  if (v >= 1000000) return (v / 1000000).toFixed(1) + 'M';
  if (v >= 1000) return (v / 1000).toFixed(1) + 'k';
  return String(v);
}

// formatTiers / parseTiers 模型档位表与「一行一条」文本之间的互转。
// 档位是个开放的小映射，用文本框比动态增删行更省事，也便于直接粘贴一段配置。
function formatTiers(tiers) {
  if (!tiers || typeof tiers !== 'object') return '';
  return Object.keys(tiers).sort().map((k) => `${k}: ${tiers[k]}`).join('\n');
}

// parseTiers 返回 {tiers, bad}：bad 是畸形行的「第 N 行：原文」列表（L12）。
// 静默丢弃最坑——用户粘贴了 5 行只生效 2 行，还以为都存上了。
function parseTiers(text) {
  const tiers = {};
  const bad = [];
  String(text || '').split('\n').forEach((line, idx) => {
    if (!line.trim()) return;
    const i = line.indexOf(':');
    const name = i < 0 ? '' : line.slice(0, i).trim();
    const model = i < 0 ? '' : line.slice(i + 1).trim();
    if (name && model) tiers[name] = model;
    else bad.push(`第 ${idx + 1} 行「${line.trim().slice(0, 30)}」${i < 0 ? '缺少冒号' : !name ? '档位名为空' : '模型名为空'}（格式：档位: 模型ID）`);
  });
  return { tiers, bad };
}

function formatDuration(sec) {
  if (sec < 60) return Math.round(sec) + '秒';
  if (sec < 3600) return Math.round(sec / 60) + '分钟';
  return (sec / 3600).toFixed(1) + '小时';
}

/* ---------- GEO（生成式引擎优化） ---------- */
let geoSwitchBound = false;

async function loadGEO() {
  bindGEOControls();
  try {
    const data = await api('GET', '/api/geo?n=50');
    renderGEOStats(data.stats || {});
    const pr = $('#geo-principles');
    if (pr) pr.textContent = String(data.principles || '').trim();
    const cb = $('#geo-enabled');
    if (cb && document.activeElement !== cb) {
      cb.checked = data.enabled !== false;
      if (!geoSwitchBound) {
        geoSwitchBound = true;
        cb.addEventListener('change', async () => {
          const on = cb.checked;
          try {
            await api('POST', '/api/settings', { agent: { geo_enabled: on } });
            toast(on ? '已开启：创作完成后自动给出 GEO 建议' : '已关闭创作自动分析', 'success');
          } catch (err) {
            cb.checked = !on;
            toast('设置保存失败：' + err.message, 'error');
          }
        });
      }
    }
    renderGEOList(data.records || []);
  } catch { /* 静默：GEO 板块不可用不影响主流程 */ }
}

function renderGEOStats(stats) {
  const total = $('#geo-total'); if (!total) return;
  total.textContent = stats.total || 0;
  $('#geo-avg').textContent = stats.avg_score ? Math.round(stats.avg_score) : '—';
  $('#geo-best').textContent = stats.best_score ? stats.best_score : '—';
}

function renderGEOList(records) {
  const wrap = $('#geo-list');
  if (!wrap) return;
  wrap.innerHTML = '';
  if (!records.length) {
    wrap.innerHTML = '<p class="empty-hint">还没有分析记录。用「内容创作者」角色写点东西，或在这里手动分析一段文字。</p>';
    return;
  }
  records.forEach((r) => wrap.appendChild(geoCard(r)));
}

function geoCard(r) {
  const item = el('div', 'geo-item');
  const score = Number(r.score) || 0;
  const cls = score >= 80 ? 'geo-score--high' : score >= 60 ? 'geo-score--mid' : 'geo-score--low';
  const time = r.created_at ? new Date(r.created_at).toLocaleString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '';
  const src = r.source === 'manual' ? '手动分析' : '创作自动分析';
  let html = '<div class="geo-item-head">' +
    '<span class="geo-score ' + cls + '">' + score + '</span>' +
    '<div class="geo-item-meta"><div class="geo-item-title">' + esc(r.goal || '未命名内容') + '</div>' +
    '<div class="geo-item-sub">' + esc(src) + (time ? ' · ' + esc(time) : '') + '</div></div></div>';
  if (r.summary) html += '<p class="geo-summary">' + esc(r.summary) + '</p>';
  html += geoPairs('已做好', r.strengths) + geoPairs('待优化', r.weaknesses);
  if (Array.isArray(r.actionables) && r.actionables.length) {
    html += '<div class="geo-sub-title">可以这样改</div><ol class="geo-actions">' +
      r.actionables.map((a) => '<li><span class="geo-pri geo-pri--' + prioKey(a.priority) + '">' + prioLabel(a.priority) +
        '</span><span class="geo-cat">' + esc(a.category || '语义') + '</span>' + esc(a.description) + '</li>').join('') +
      '</ol>';
  }
  item.innerHTML = html;
  return item;
}

function geoPairs(title, list) {
  if (!Array.isArray(list) || !list.length) return '';
  return '<div class="geo-sub-title">' + title + '</div><ul class="geo-pairs">' +
    list.map((v) => '<li>' + esc(v) + '</li>').join('') + '</ul>';
}

function prioKey(p) {
  const k = String(p || '').toLowerCase();
  return k === 'high' ? 'high' : k === 'low' ? 'low' : 'mid';
}

function prioLabel(p) {
  return prioKey(p) === 'high' ? '高' : prioKey(p) === 'low' ? '低' : '中';
}

function bindGEOControls() {
  const btn = $('#geo-analyze');
  if (btn && !btn.dataset.bound) {
    btn.dataset.bound = '1';
    btn.addEventListener('click', runGEOAnalyze);
  }
  const clear = $('#geo-clear');
  if (clear && !clear.dataset.bound) {
    clear.dataset.bound = '1';
    clear.addEventListener('click', async () => {
      try {
        await api('DELETE', '/api/geo/history');
        renderGEOList([]);
        renderGEOStats({});
        toast('已清空 GEO 历史', 'success');
      } catch (err) { toast('清空失败：' + err.message, 'error'); }
    });
  }
}

async function runGEOAnalyze() {
  const content = ($('#geo-content').value || '').trim();
  const goal = ($('#geo-goal').value || '').trim();
  const btn = $('#geo-analyze');
  const hint = $('#geo-analyze-hint');
  if (content.length < 10) { toast('请至少输入 10 个字再分析', 'warning'); return; }
  btn.disabled = true;
  hint.textContent = '分析中…';
  try {
    const data = await api('POST', '/api/geo/analyze', { content, goal });
    const box = $('#geo-result');
    box.hidden = false;
    box.replaceChildren(geoCard(data.record || data.suggestion || {}));
    hint.textContent = '分析完成，已存入历史';
    await loadGEO();
  } catch (err) {
    hint.textContent = '分析失败';
    toast('分析失败：' + err.message, 'error');
  } finally {
    btn.disabled = false;
  }
}

/* ============================================================
 * 皮肤主题：深浅（跟随系统）+ 强调色，本地持久化
 * ============================================================ */
const UIPrefs = (() => {
  const KEY = 'gleam-ui';
  let p = Object.assign({ themeMode: 'light', accent: 'emerald', lang: 'zh', font: 'sans', text: 's', zoom: 'm', width: 'standard' }, load());
  function load() { try { return JSON.parse(localStorage.getItem(KEY) || '{}'); } catch { return {}; } }
  function save() { try { localStorage.setItem(KEY, JSON.stringify(p)); } catch { /* 隐私模式 */ } }
  function get() { return p; }
  function set(patch) { p = Object.assign({}, p, patch); save(); applyTheme(); }
  return { get, set };
})();

const darkMedia = window.matchMedia('(prefers-color-scheme: dark)');
function effectiveDark() {
  const mode = UIPrefs.get().themeMode;
  return mode === 'dark' || (mode === 'system' && darkMedia.matches);
}
function applyTheme() {
  const { themeMode, accent } = UIPrefs.get();
  const dark = effectiveDark();
  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light');
  document.documentElement.setAttribute('data-accent', accent || 'emerald');
  const mc = document.querySelector('meta[name="theme-color"]');
  if (mc) {
    // 取当前主题的窗框色：每个主题各自定义 --frame，这里不再写死两种
    const frame = getComputedStyle(document.documentElement).getPropertyValue('--frame').trim();
    if (frame) mc.setAttribute('content', frame);
  }
  const cs = document.querySelector('meta[name="color-scheme"]');
  if (cs) cs.setAttribute('content', dark ? 'dark' : 'light');
  document.querySelectorAll('.theme-mode-btn').forEach((b) =>
    b.setAttribute('aria-pressed', String(b.dataset.mode === themeMode)));
  document.querySelectorAll('.accent-swatch').forEach((b) =>
    b.setAttribute('aria-pressed', String(b.dataset.accent === accent)));
  // 纯前端外观偏好：字体 / 字号 / 缩放 / 内容宽度 / 语言，全部落到 <html> 属性上由 CSS 接管
  const prefs = UIPrefs.get();
  const de = document.documentElement;
  de.setAttribute('data-font', prefs.font || 'sans');
  de.setAttribute('data-text', prefs.text || 's');
  de.setAttribute('data-zoom', prefs.zoom || 'm');
  de.setAttribute('data-width', prefs.width || 'standard');
  de.setAttribute('data-sidebar', prefs.sidebarCollapsed ? 'collapsed' : 'open');
  document.querySelectorAll('[data-pref]').forEach((b) => {
    const on = String(prefs[b.dataset.pref]) === b.dataset.val;
    if (b.getAttribute('role') === 'menuitemradio') b.setAttribute('aria-checked', String(on));
    else b.setAttribute('aria-pressed', String(on));
  });
  applyLang(prefs.lang || 'zh');
}

/* ---------- 界面语言：壳层文案的中英切换（动态内容与后端消息保持中文） ---------- */
const I18N_EN = {
  '目标': 'Goals', '定时任务': 'Schedules', '技能': 'Skills', '工具': 'Tools', '更多': 'More', '记忆': 'Memory',
  '市场': 'Market', '成长': 'Growth', '就绪体检': 'Readiness', '反馈与建议': 'Feedback', '设置': 'Settings', '我的': 'Me',
  '新任务': 'New task', '不止于对话，把事做完': 'Beyond chat, get it done', '上下文留在本机，Gleam 帮你一步步推进': 'Context stays on this machine while Gleam moves the work forward',
  '会话': 'Chats', '任务': 'Tasks', '收起侧栏': 'Collapse sidebar', '展开侧栏': 'Expand sidebar', '展开现场栏': 'Open live panel', '收起现场栏': 'Close live panel', '搜索': 'Search', '工作区': 'Workspaces', '微光 · 本地智能体': 'Gleam · local agent',
  '新对话': 'New chat', '选择工作区': 'Choose workspace', '选择工作区（可选）': 'Choose workspace (optional)',
  '准备就绪': 'Ready', '可以开始一个新目标': 'Start a new goal', '全部': 'All', '进行中': 'Active', '已完成': 'Done', '需处理': 'Needs you',
  '本地工作台': 'Local workbench', '不止于对话，': 'Beyond chat, ', '把事做完。': 'get it done.',
  '描述一个目标，或从下方快捷入口开始。上下文留在本机，持续推进。': 'Describe a goal or start from a shortcut below. Context stays on this machine.',
  '本周完成': 'Done this week', '条记忆': 'memories', '整理工作区': 'Tidy workspace', '项目复盘': 'Project review', '快速研究': 'Quick research',
  '文件归档 · 生成索引': 'Archive files · build index', '进度分析 · 风险提醒': 'Progress · risks', '阅读文档 · 输出简报': 'Read docs · brief',
  '从一个小目标开始': 'Start with a small goal', '可以直接开口说了': 'Go ahead and say it',
  '模式': 'Mode', '权限': 'Access', '对话': 'Chat', '工作': 'Work', '编程': 'Code', '批准': 'Approve', '全自动': 'Auto', '执行前询问': 'Ask first', '自动执行': 'Auto-run', '仅对话': 'Chat only', '通用': 'General', '执行权限': 'Access', '任务档位': 'Task mode', '专家角色': 'Expert role', '演示模型': 'Demo model', '本机': 'Local', '继续当前会话…': 'Continue this chat…', '执行方式': 'Run mode', '工作模式': 'Work mode', '添加文件': 'Add file',
  'Enter 发送 · Shift+Enter 换行': 'Enter to send · Shift+Enter for newline',
  '一切从这里开始… 描述任务，或输入 @ 引用': 'Start here… describe a task, or type @ to reference',
  '添加文件': 'Add file', '添加文件夹': 'Add folder', '添加目标': 'Add goal', '计划模式': 'Plan mode', '添加插件': 'Add plugin', '@ 引用': '@ mention', '浏览器预览': 'Browser preview',
  '上下文水位': 'Context usage', '立即压缩': 'Compress now', '去设置查看': 'Open settings', '当前模型': 'Current model',
  '长期记忆': 'Long-term memory', '写入记忆': 'Save a memory', '保存': 'Save', '检索你的记忆': 'Search your memory', '写一条记忆': 'Write a memory',
  '新建定时任务': 'New schedule', '创建': 'Create', '从市场安装': 'Install from market',
  'MCP 服务器': 'MCP servers', '技能模板': 'Skill templates', '已安装': 'Installed', '自定义接入': 'Custom',
  '个人': 'Personal', '智能体': 'Agent', '安全': 'Safety', '开发': 'Developer', '协作': 'Collaboration', '外观': 'Appearance',
  '模型': 'Model', '引擎': 'Engine', 'Go 工具链': 'Go toolchain', '账号与登录': 'Account', '累计任务': 'Tasks', '连续成功': 'Streak', '本周 tokens': 'Tokens this week',
  '语言': 'Language', '明暗模式': 'Mode', '主题': 'Theme', '字体风格': 'Font', '文字大小': 'Text size', '界面缩放': 'Zoom', '内容宽度': 'Content width',
  '系统': 'System', '浅色': 'Light', '深色': 'Dark', '跟随系统': 'System', '森林': 'Forest', '薄荷': 'Mint', '蜜蜂': 'Bee', '羊皮纸': 'Parchment',
  '无衬线': 'Sans', '衬线': 'Serif', '小': 'S', '中': 'M', '大': 'L', '标准': 'Standard', '宽': 'Wide',
  '使用统计与成长': 'Usage & growth', '检查更新': 'Check for updates', '帮助与反馈': 'Help & feedback', '账号与本地数据': 'Account & local data', '退出登录': 'Sign out',
  '所有任务': 'All tasks', '选择': 'select', '打开': 'open', '个': '',
  '搜索会话标题、内容摘要或目标…': 'Search chats, previews or goals…', '搜索设置…': 'Search settings…',
  '现场': 'Live', '空闲': 'Idle', '外观皮肤': 'Appearance', '深浅模式': 'Mode', '强调色': 'Accent',
};
const i18nOrig = new WeakMap();
const I18N_ATTRS = ['placeholder', 'title', 'aria-label'];
function applyLang(lang) {
  const en = lang === 'en';
  document.documentElement.setAttribute('lang', en ? 'en' : 'zh-CN');
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  let n;
  while ((n = walker.nextNode())) {
    const raw = i18nOrig.has(n) ? i18nOrig.get(n) : n.nodeValue;
    const key = raw.trim();
    if (!key || !(key in I18N_EN)) continue;
    if (!i18nOrig.has(n)) i18nOrig.set(n, raw);
    n.nodeValue = en ? raw.replace(key, I18N_EN[key]) : raw;
  }
  document.querySelectorAll('[placeholder],[title],[aria-label]').forEach((elx) => {
    I18N_ATTRS.forEach((a) => {
      const store = 'i18n' + a.replace(/-./g, (m) => m[1].toUpperCase());
      const cur = elx.getAttribute(a);
      if (cur == null) return;
      const orig = elx.dataset[store] != null ? elx.dataset[store] : cur;
      if (!(orig in I18N_EN)) return;
      elx.dataset[store] = orig;
      elx.setAttribute(a, en ? I18N_EN[orig] : orig);
    });
  });
}
if (darkMedia.addEventListener) darkMedia.addEventListener('change', () => { if (UIPrefs.get().themeMode === 'system') applyTheme(); });

document.querySelectorAll('.theme-mode-btn').forEach((b) => {
  b.addEventListener('click', () => { UIPrefs.set({ themeMode: b.dataset.mode }); toast('外观已更新', 'success', 1600); });
});
document.querySelectorAll('.accent-swatch').forEach((b) => {
  b.addEventListener('click', () => { UIPrefs.set({ accent: b.dataset.accent }); });
});
// 设置页里的外观分段（语言 / 字体 / 字号 / 缩放 / 宽度）与头像菜单共用同一份偏好
document.querySelectorAll('.settings-panel [data-pref]').forEach((b) => {
  b.addEventListener('click', () => UIPrefs.set({ [b.dataset.pref]: b.dataset.val }));
});

/* ============================================================
 * 「我的」抽屉
 * ============================================================ */
const MeDrawer = (() => {
  const overlay = $('#me-overlay');
  function open() { overlay.hidden = false; loadAccount(); }
  function close() { overlay.hidden = true; }
  $('#me-close').addEventListener('click', close);
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !overlay.hidden) close(); });
  return { open, close };
})();

/* ---------- 账号 ---------- */
let authMode = 'signin';
document.querySelectorAll('#me-auth-tabs button').forEach((b) => {
  b.addEventListener('click', () => {
    authMode = b.dataset.atab;
    document.querySelectorAll('#me-auth-tabs button').forEach((x) => x.setAttribute('aria-pressed', String(x === b)));
    $('#me-auth-submit').textContent = authMode === 'signup' ? '注册并登录' : '登录';
    // 注册时密码必须是"新密码"，否则浏览器会把已有口令填回去并拒绝自动保存
    $('#me-password').autocomplete = authMode === 'signup' ? 'new-password' : 'current-password';
  });
});

async function loadAccount() {
  try {
    const s = await api('GET', '/api/account');
    const signedIn = !!s.signed_in;
    $('#me-guest').hidden = signedIn;
    $('#me-user').hidden = !signedIn;
    if (signedIn) {
      const name = s.display_name || s.email || '云端用户';
      $('#me-account-info').innerHTML =
        '<strong>' + esc(name) + '</strong><small>' + esc(s.email || '') + ' · 云端账号</small>';
      $('#me-name').textContent = name;
      $('#me-sub').textContent = '云端已登录';
      $('#me-cloud-provider').textContent = '已登录 · ' + (s.email || s.provider || '账号');
    } else {
      $('#me-account-info').innerHTML =
        '<strong>本地模式</strong><small>密钥、对话、技能、工具都只存在这台电脑</small>';
      $('#me-name').textContent = '我的';
      // L7：本地优先产品里「未登录」会被读成功能受限——说清本地模式就是完整形态
      $('#me-sub').textContent = '本地模式 · 功能完整';
      if (s.supabase_url) { $('#me-sb-url').value = s.supabase_url; $('#me-sb-anon').value = s.supabase_anon_key || ''; }
    }
    // 侧栏账户入口在图标轨道里只剩一个头像，可访问名要跟着账户走
    const meLabel = signedIn ? '我的 · ' + (s.display_name || s.email || '云端用户') : '我的';
    $('#open-me').setAttribute('aria-label', meLabel);
    $('#open-me').title = meLabel;
  } catch {
    $('#me-name').textContent = '我的';
    $('#me-sub').textContent = '本地模式';
  }
}

// 绑在 form 的 submit 上，而不是按钮的 click 上：那样回车走到 submit 事件时
// 没人 preventDefault，页面会带着邮箱密码整体刷新。
$('#me-auth-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const email = $('#me-email').value.trim();
  const password = $('#me-password').value;
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) { toast('邮箱格式看起来不对', 'error'); $('#me-email').focus(); return; }
  if (password.length < 6) { toast('密码至少 6 位', 'error'); $('#me-password').focus(); return; }
  const btn = $('#me-auth-submit');
  btn.disabled = true;
  try {
    if (authMode === 'signup') {
      await api('POST', '/api/account/signup', { email, password });
      toast('注册成功，如项目开启了邮箱确认，请先到邮箱确认', 'success', 5000);
    } else {
      await api('POST', '/api/account/signin', { email, password });
      toast('登录成功', 'success');
    }
    await loadAccount();
    $('#me-password').value = ''; // 密码不留在输入框里等着被下一次误提交
  } catch (err) { toast(err.message, 'error'); }
  finally { btn.disabled = false; }
});

async function oauthLogin(provider) {
  try {
    await api('POST', '/api/account/oauth', { provider });
    toast('已在浏览器打开授权页，完成后自动登录…', 'info', 5000);
    let tries = 0;
    const timer = setInterval(async () => {
      tries++;
      try {
        const s = await api('GET', '/api/account');
        if (s.signed_in) { clearInterval(timer); toast('登录成功', 'success'); loadAccount(); }
      } catch { /* 等待中 */ }
      if (tries > 60) clearInterval(timer);
    }, 3000);
  } catch (err) { toast(err.message, 'error'); }
}
$('#me-oauth-github').addEventListener('click', () => oauthLogin('github'));
$('#me-oauth-google').addEventListener('click', () => oauthLogin('google'));

$('#me-sb-save').addEventListener('click', async () => {
  const supabase_url = $('#me-sb-url').value.trim();
  const supabase_anon_key = $('#me-sb-anon').value.trim();
  if (!supabase_url || !supabase_anon_key) { toast('项目地址和 anon key 都要填', 'error'); return; }
  try {
    await api('POST', '/api/account/configure', { supabase_url, supabase_anon_key });
    toast('云端连接已保存', 'success');
    loadAccount();
  } catch (err) { toast(err.message, 'error'); }
});

$('#me-signout').addEventListener('click', async () => {
  const ok = await confirmModal('退出后仅清除本机登录状态，本地的密钥与数据都不会删除。确定退出？', '退出登录', { okText: '退出' });
  if (!ok) return;
  try {
    await api('POST', '/api/account/signout', {});
    toast('已退出登录', 'success');
    $('#me-email').value = ''; $('#me-password').value = '';
    loadAccount();
  } catch (err) { toast(err.message, 'error'); }
});

/* ---------- 抽屉入口 ---------- */
$('#me-go-settings').addEventListener('click', () => { MeDrawer.close(); showView('settings'); });
$('#me-go-growth').addEventListener('click', () => { MeDrawer.close(); showView('growth'); });

$('#me-data').addEventListener('click', async () => {
  let d;
  try { d = await api('GET', '/api/local-data'); } catch { toast('读取本地数据信息失败', 'error'); return; }
  const sizeKB = (d.total_bytes / 1024).toFixed(0);
  Modal.open('本地数据', (box) => {
    box.innerHTML =
      '<p class="modal-text">你的所有数据都保存在本机，不会上传云端。可随时复制路径去文件管理器查看或备份。</p>' +
      '<div class="field"><label class="field-label">数据目录</label><input class="input" readonly value="' + esc(d.data_dir) + '"></div>' +
      '<div class="field"><label class="field-label">密钥文件（仅本机，权限仅自己可读）</label><input class="input" readonly value="' + esc(d.credentials_file) + '"></div>' +
      '<p class="field-hint">共 ' + d.file_count + ' 个本地文件，约 ' + sizeKB + ' KB。包含密钥、对话记录、技能、记忆、工具配置等。</p>' +
      '<div class="modal-actions"><button class="btn btn-primary" id="me-data-ok">知道了</button></div>';
    box.querySelector('#me-data-ok').addEventListener('click', Modal.close);
  });
});

$('#me-update').addEventListener('click', async () => {
  let info;
  try {
    info = await api('GET', '/api/info');
  } catch {
    toast('读取本机版本失败', 'error');
    return;
  }
  const ver = info.version ? 'v' + info.version : '未知';
  $('#me-version').textContent = ver;
  // 没有联网更新源，所以"有没有新版"这件事在本机**问不出来**。以前这里给出的是一个完成时态的结论，
  // 等于把"我不知道"讲成了答案。版本号只从 /api/info 取，owner 是 internal/buildinfo。
  await alertModal('本机版本 ' + ver + '。Gleam 没有联网更新源：安装包只落在本机，所以这一行只能说出版本号，' +
    '说不出有没有新版。要升级就替换程序本身——对话、技能、密钥都存在「本地数据」那个目录里，换程序不影响它们。', '检查更新');
});

$('#me-help').addEventListener('click', () => {
  Modal.open('帮助与反馈', (box) => {
    box.innerHTML =
      '<p class="modal-text">Gleam 完全在本机运行，你的数据不会离开这台电脑。</p>' +
      '<div class="field"><label class="field-label">快速上手</label>' +
      '<p class="field-hint" style="margin-top:4px;">· 在底部输入框直接说要做什么，例如“整理当前文件夹”<br>· 「定时任务」用中文说时间即可自动执行，无需任何技术语法<br>· 模型密钥在「全部设置 → 模型」填写，只存本机<br>· 高风险操作（删除/执行命令）会先征求你的同意</p></div>' +
      '<div class="modal-actions"><button class="btn btn-secondary" id="me-help-gofb">写一条反馈</button><button class="btn btn-primary" id="me-help-ok">知道了</button></div>';
    box.querySelector('#me-help-ok').addEventListener('click', Modal.close);
    // 反馈视图藏在「更多」里，从这条人人都找得到的入口过去一次
    box.querySelector('#me-help-gofb').addEventListener('click', () => { Modal.close(); openFeedbackView(); });
  });
});

/* ---------- 反馈与建议 ---------- */
// 一条反馈**先落本机、再谈远端**，所以界面上那句"已提交"永远指的是本机那份。
// 现场字段一个都不在这儿拼：GET /api/feedback/context 回什么就显示什么——
// 后端改白名单时，抄一份字段名的预览会变成一句谎报（判据见 TestFeedbackContextPreview）。
const FB_MAX_SHOTS = 5;
const FB_MAX_TEXT = 20000;
const FB_MAX_SHOT_BYTES = 5 * 1024 * 1024;
const fbDraft = { kind: 'bug', shots: [], taskID: '' };

const FB_DELIVERY = {
  local_only: { text: '只存本机', cls: 'badge--mode', title: '没配远端，这条就在你这台电脑上；不是失败' },
  sent: { text: '已送达远端', cls: 'badge--success', title: '脱敏后的副本已送到远端' },
  failed: { text: '未送达', cls: 'badge--failed', title: '本机这份还在，重投会再试一次' },
};

function openFeedbackView(taskID) {
  if (taskID !== undefined) fbDraft.taskID = taskID;
  // 不在这儿自己拉数据：showView 会走 VIEW_LOADERS，再拉一遍就是两次请求。
  showView('feedback');
  setTimeout(() => $('#fb-text').focus(), 60);
}

function fbCount() {
  // 括号位置错过一次：[...值].length 才对，写成 [...(值.length)] 是把数字摊开，
  // 每次输入都抛 "is not iterable"，计数器永远停在 0。
  const n = [...$('#fb-text').value].length;
  $('#fb-count').textContent = n + ' / ' + FB_MAX_TEXT + ' 字';
  $('#fb-count').classList.toggle('fb-over', n > FB_MAX_TEXT);
}

// 缩略图用 blob URL：还没提交的东西不该绕一趟后端。
function fbAddShot(file) {
  if (!file || !/^image\//.test(file.type)) { toast('只收图片文件', 'warning'); return; }
  if (fbDraft.shots.length >= FB_MAX_SHOTS) { toast('截图最多 ' + FB_MAX_SHOTS + ' 张', 'warning'); return; }
  if (file.size > FB_MAX_SHOT_BYTES) { toast('这张 ' + Math.round(file.size / 1048576) + 'MB，超过 5MB 上限', 'warning'); return; }
  const reader = new FileReader();
  reader.onload = () => {
    // 真实类型由后端按文件头判，这里只留展示用的名字与大小
    fbDraft.shots.push({ data: String(reader.result), name: file.name || '粘贴的截图', bytes: file.size });
    fbRenderShots();
  };
  reader.onerror = () => toast('这张读不出来', 'error');
  reader.readAsDataURL(file);
}

function fbRenderShots() {
  const box = $('#fb-shots');
  box.innerHTML = '';
  fbDraft.shots.forEach((s, i) => {
    const item = el('div', 'fb-shot');
    const img = el('img', 'fb-shot-img');
    img.src = s.data;
    img.alt = '待提交的截图 ' + (i + 1);
    const meta = el('span', 'fb-shot-meta', s.name + ' · ' + Math.max(1, Math.round(s.bytes / 1024)) + 'KB');
    const rm = el('button', 'fb-shot-rm');
    rm.type = 'button';
    rm.innerHTML = ICONS.x;
    rm.title = '不要这张了（只从这次提交里移除，本机文件不动）';
    rm.setAttribute('aria-label', '移除截图 ' + (i + 1));
    rm.addEventListener('click', () => { fbDraft.shots.splice(i, 1); fbRenderShots(); });
    item.append(img, meta, rm);
    box.appendChild(item);
  });
}

async function fbRefreshContext() {
  const box = $('#fb-context');
  const dest = $('#fb-dest');
  try {
    const q = fbDraft.taskID ? '?task_id=' + encodeURIComponent(fbDraft.taskID) : '';
    const { context: c, remote } = await api('GET', '/api/feedback/context' + q);
    const bits = [];
    if (c.app_version) bits.push('v' + c.app_version);
    if (c.model) bits.push('模型 ' + c.model);
    if (c.llm_host) bits.push('接入 ' + c.llm_host);
    if (c.os) bits.push(c.os + ' / ' + c.go_version);
    // 状态本身已经说了"已完成"，再补一句就成了「那次运行 已完成 · 已完成」。只有停在具体工具上才值得多说半句。
    if (c.task_id) bits.push('那次运行 ' + (STATUS_LABEL[c.task_status] || c.task_status || '未知状态') + (c.failed_tool ? ' · 停在 ' + c.failed_tool : ''));
    box.innerHTML = '<span class="fb-context-k">会一并带上的运行现场</span>' +
      '<div class="fb-context-v">' + bits.map(esc).join('<span class="fb-dot">·</span>') + '</div>' +
      '<p class="field-hint">只有这些：密钥、工作区路径、日志正文都不出去。描述里如果粘了绝对路径，发出去的副本会被换成「已脱敏」，本机这份保持原样。</p>';
    $('#fb-ref-slot').innerHTML = c.task_id
      ? '<button type="button" class="fb-ref" id="fb-drop-ref">指着那次运行 ' + esc(shorten(c.task_id, 14)) + ' ✕</button>'
      : '';
    const drop = $('#fb-drop-ref');
    if (drop) drop.addEventListener('click', () => { fbDraft.taskID = ''; fbRefreshContext(); });
    dest.textContent = remote ? ('本机一份，另送一份到 ' + remote) : '只存本机（还没配远端，不是失败）';
  } catch (err) {
    box.innerHTML = '<p class="field-hint">现场读不出来：' + esc(err.message) + '</p>';
    dest.textContent = '远端状态暂时读不出来';
  }
}

async function loadFeedbackView() { await fbRefreshContext(); loadFeedback(); }

async function loadFeedback() {
  const list = $('#fb-list');
  list.innerHTML = '<div class="skeleton" style="height:56px"></div>';
  try {
    const { feedback: items, skipped, remote } = await api('GET', '/api/feedback');
    list.innerHTML = '';
    $('#fb-empty').hidden = !!(items && items.length);
    const broken = $('#fb-broken');
    broken.hidden = !skipped;
    broken.textContent = '有 ' + skipped + ' 条读不动（文件坏了），已跳过——它们仍在 feedback/ 目录里。';
    (items || []).forEach((f) => list.appendChild(fbRow(f)));
    if (remote) $('#fb-dest').textContent = '本机一份，另送一份到 ' + remote;
  } catch (err) {
    loadError(list, err, loadFeedback);
  }
}

function fbRow(f) {
  const row = el('div', 'card row');
  const main = el('div', 'row-main');
  const d = FB_DELIVERY[f.delivery] || FB_DELIVERY.local_only;
  const shots = (f.attachments || []).length;
  main.innerHTML = `<div class="row-title"><span class="badge badge--${f.kind === 'bug' ? 'failed' : 'partial'}">${f.kind === 'bug' ? '问题' : '建议'}</span>` +
    `<span class="badge ${d.cls}">${d.text}</span></div>` +
    `<div class="fb-row-text">${esc(f.text)}</div>` +
    `<div class="row-sub">${esc(new Date(f.created_at).toLocaleString())} · 只存在本机${shots ? ' · ' + shots + ' 张截图' : ''}</div>` +
    (f.delivery_note ? `<div class="row-sub fb-note" title="${esc(d.title)}">${esc(f.delivery_note)}</div>` : '');
  const actions = el('div', 'row-actions');
  if (shots) {
    const see = el('button', 'btn btn-secondary btn-sm');
    see.type = 'button';
    see.textContent = '看截图';
    see.title = '从本机读回这几张图（截图不外发，所以只能在这台电脑上看）';
    see.addEventListener('click', () => fbViewShots(f));
    actions.appendChild(see);
  }
  if (f.delivery === 'failed') {
    const retry = el('button', 'btn btn-secondary btn-sm');
    retry.type = 'button';
    retry.textContent = '重新投递';
    retry.title = d.title;
    retry.addEventListener('click', async () => {
      retry.disabled = true;
      try {
        const out = await api('POST', '/api/feedback/' + encodeURIComponent(f.id) + '/resend');
        const nd = FB_DELIVERY[out.delivery] || FB_DELIVERY.local_only;
        toast(out.delivery_note ? '重投结果：' + nd.text + '（' + out.delivery_note + '）' : '重投结果：' + nd.text,
          out.delivery === 'sent' ? 'success' : 'error', 7000);
        loadFeedback();
      } catch (err) { retry.disabled = false; toast(err.message, 'error', 7000); }
    });
    actions.appendChild(retry);
  }
  const del = el('button', 'btn btn-danger btn-sm');
  del.type = 'button';
  del.innerHTML = ICONS.trash;
  del.title = '删掉这条反馈，连同它在本机的截图';
  del.setAttribute('aria-label', '删除这条反馈：' + shorten(f.text, 20));
  del.addEventListener('click', async () => {
    if (!await confirmModal('删掉这条反馈？\n\n「' + shorten(f.text, 60) + '」\n\n本机归档与截图一起删除；已经送到远端的那份副本删不掉。', '删除反馈', { okText: '删除', danger: true })) return;
    try { await api('DELETE', '/api/feedback/' + encodeURIComponent(f.id)); toast('已删除', 'success'); loadFeedback(); }
    catch (err) { toast(err.message, 'error'); }
  });
  actions.appendChild(del);
  row.append(main, actions);
  return row;
}

// 看截图走后端读回，不是留 blob URL：历史条目是上一次会话写的，内存里没有那份图。
function fbViewShots(f) {
  Modal.open(esc('这条反馈的截图（' + (f.attachments || []).length + ' 张，只在本机）'), (box) => {
    (f.attachments || []).forEach((a) => {
      const img = el('img', 'fb-modal-shot');
      img.src = withToken('/api/feedback/attachment?name=' + encodeURIComponent(a.name));
      img.alt = '反馈截图 ' + a.name;
      box.appendChild(img);
      box.appendChild(el('p', 'field-hint', a.name + ' · ' + Math.max(1, Math.round(a.bytes / 1024)) + 'KB · ' + a.mime));
    });
  });
}

async function fbSubmit() {
  const btn = $('#fb-submit');
  const text = $('#fb-text').value.trim();
  if (!text) { toast('先写一句描述：没有文字的截图，收到也不知道该改什么', 'warning'); $('#fb-text').focus(); return; }
  if ([...text].length > FB_MAX_TEXT) { toast('描述超过 ' + FB_MAX_TEXT + ' 字，删减一点再提交', 'warning'); return; }
  btn.disabled = true;
  btn.textContent = '提交中…';
  try {
    const out = await api('POST', '/api/feedback', {
      kind: fbDraft.kind,
      text,
      task_id: fbDraft.taskID || undefined,
      attachments: fbDraft.shots.map((s) => ({ data: s.data })),
    });
    const d = FB_DELIVERY[out.delivery] || FB_DELIVERY.local_only;
    toast('已存进本机。' + d.text + (out.delivery_note ? '：' + out.delivery_note : ''),
      out.delivery === 'failed' ? 'warning' : 'success', 7000);
    fbDraft.shots = []; fbDraft.taskID = '';
    $('#fb-text').value = '';
    $('#fb-file').value = '';
    fbRenderShots(); fbCount(); fbRefreshContext(); loadFeedback();
  } catch (err) { toast(err.message, 'error', 7000); }
  btn.disabled = false;
  btn.textContent = '提交';
}

$('#fb-kind').addEventListener('click', (e) => {
  const b = e.target.closest('button[data-kind]');
  if (!b) return;
  fbDraft.kind = b.dataset.kind;
  document.querySelectorAll('#fb-kind button').forEach((x) => x.setAttribute('aria-pressed', String(x === b)));
});
$('#fb-text').addEventListener('input', fbCount);
$('#fb-pick').addEventListener('click', () => $('#fb-file').click());
$('#fb-file').addEventListener('change', (e) => { [...e.target.files].forEach(fbAddShot); });
$('#fb-submit').addEventListener('click', fbSubmit);
$('#fb-refresh').addEventListener('click', () => { fbRefreshContext(); loadFeedback(); });
// 粘贴截图：只在这个视图里认，否则等于在别的页面上偷偷吃剪贴板
document.addEventListener('paste', (e) => {
  if (!$('#view-feedback').classList.contains('active')) return;
  const files = [...(e.clipboardData ? e.clipboardData.files : [])].filter((f) => /^image\//.test(f.type));
  if (!files.length) return;
  e.preventDefault();
  files.forEach(fbAddShot);
});

/* ---------- 就绪体检（九坑自检） ---------- */
const READINESS_LABEL = { pass: '通过', warn: '待改进', fail: '不合格' };
const READINESS_VERDICT = {
  ready: { text: '可以上生产', badge: 'badge--success' },
  needs_work: { text: '基本可用 · 有待改进', badge: 'badge--partial' },
  not_ready: { text: '暂不建议上生产', badge: 'badge--failed' },
};

async function loadReadiness() {
  const wrap = $('#readiness-items');
  if (!wrap) return;
  wrap.innerHTML = '<p class="empty-hint">正在体检…</p>';
  try {
    const data = await api('GET', '/api/readiness');
    renderReadiness(data.report || {});
  } catch (e) {
    wrap.innerHTML = `<p class="empty-hint">体检失败：${esc(e.message || String(e))}</p>`;
  }
}

function renderReadiness(rep) {
  const items = rep.items || [];
  const v = READINESS_VERDICT[rep.verdict] || { text: '未知', badge: 'badge--mode' };
  const badge = $('#ready-verdict');
  if (badge) { badge.textContent = v.text; badge.className = 'badge ' + v.badge; }

  const pass = rep.passed || 0, warn = rep.warned || 0, fail = rep.failed || 0;
  $('#ready-pass').textContent = pass;
  $('#ready-warn').textContent = warn;
  $('#ready-fail').textContent = fail;

  const parts = [`共 ${rep.total || 0} 项检查：${pass} 项通过`];
  if (warn) parts.push(`${warn} 项待改进`);
  if (fail) parts.push(`${fail} 项不合格`);
  $('#ready-summary').textContent = parts.join('、') + '。';
  if (fail) {
    $('#ready-summary').textContent += '不合格项属于结构性缺口，建议优先补齐。';
  } else if (warn) {
    $('#ready-summary').textContent += '待改进项不影响使用，补上能让 Agent 更稳。';
  }

  const wrap = $('#readiness-items');
  wrap.textContent = '';
  for (const it of items) wrap.appendChild(readinessCard(it));
}

function readinessCard(it) {
  const card = el('div', `card card--glass readiness-item readiness-item--${it.status}`);
  const head = el('div', 'readiness-item-head');
  head.appendChild(el('span', `readiness-mark readiness-mark--${it.status}`, String(it.index)));
  head.appendChild(el('h3', 'readiness-item-title', it.title));
  head.appendChild(el('span', `badge readiness-badge--${it.status}`, READINESS_LABEL[it.status] || it.status));
  card.appendChild(head);
  card.appendChild(el('p', 'readiness-item-summary', it.summary));

  const ev = it.evidence || [];
  if (ev.length) {
    const ul = el('ul', 'readiness-evidence');
    for (const line of ev) ul.appendChild(el('li', null, line));
    card.appendChild(ul);
  }
  if (it.fix) card.appendChild(el('p', 'readiness-fix', '→ ' + it.fix));
  return card;
}

const readyRefresh = $('#ready-refresh');
if (readyRefresh) readyRefresh.addEventListener('click', loadReadiness);

/* ============================================================
 * 现场栏（Live Rail）：循环轨 / 事件流水 / 本机读数
 *
 * 为什么单独一列而不是把过程塞进任务卡：任务卡讲"这一次跑得怎么样"，
 * 现场栏讲"引擎此刻在干什么"。后者要在跑的过程中一直看得见，而卡片里的
 * 时间线收在折叠块内，看一眼要点开一次。
 *
 * 三条口径：
 *  1. 只画引擎真会发的阶段（plan / execute / reflect，外加 budget 作为一盏
 *     "闸"灯）。发明一个永不亮起的格子比不画更糟——用户会以为它坏了，然后不再看这一列。
 *  2. 读数没有生产者时显示「—」而不是 0。0 是一个看起来合理的假答案，"—" 不是。
 *  3. 流水是只读旁路：它不决定任何事实，只是把已经发生的事摊开。所以它出错
 *     不能连累任务卡——每个入口都兜住，但要把错误写进 console 而不是咽下去。
 * ============================================================ */
const LiveRail = (() => {
  const STAGES = ['plan', 'execute', 'reflect'];
  const STAGE_CN = { plan: '规划', execute: '执行', reflect: '复盘', budget: '预算闸', chat: '直聊' };
  const FLOW_MAX = 40;

  const railEl = $('#rail');
  const foldBtn = $('#rail-fold');
  const stripBtn = $('#rail-strip');
  const pulse = $('#rail-pulse');
  const stripDot = $('#rail-strip-dot');
  const stripStage = $('#rail-strip-stage');
  const flowBox = $('#rail-flow');
  const flowCount = $('#rail-flow-count');
  const loopRead = $('#rail-loop-read');
  const budgetEl = $('#rail-budget');
  const loopEl = $('#rail-loop');
  const stageEls = {};
  const noteEls = {};
  STAGES.forEach((s) => {
    stageEls[s] = loopEl.querySelector(`.loop-step[data-stage="${s}"]`);
    noteEls[s] = loopEl.querySelector(`[data-note="${s}"]`);
  });
  const ro = {
    model: $('#ro-model'), host: $('#ro-host'), tools: $('#ro-tools'),
    approvals: $('#ro-approvals'), schedules: $('#ro-schedules'),
    memory: $('#ro-memory'), version: $('#ro-version'),
  };

  const narrow = () => window.matchMedia('(max-width: 900px)').matches;
  const calm = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const midMQ = window.matchMedia('(max-width: 1240px)');
  const narrowMQ = window.matchMedia('(max-width: 900px)');
  // 中屏默认收成竖签，但**用户一旦自己点过就以他的选择为准**——自动兜底不能盖过显式意愿，
  // 否则你刚展开的东西会在下一次 resize 时被"贴心地"收回去。
  const pref = UIPrefs.get();
  let userChose = pref.railFolded != null;
  let choice = !!pref.railFolded;
  // 用户没点过收展 = 默认跟着视口走，**每次现读**：init 时快照一份宽度，遇到布局还没
  // 落定的场合（内嵌帧、刚创建就 maximise 的窗口）会把按钮文案和画面锁成反的——
  // 第一下点击因此"看起来没反应"。点过了就只认他点的。
  const folded = () => (userChose ? choice : true);
  // 窄屏浮层是**临时**状态，不进偏好：没人希望"下次打开窗口默认挡住对话"。
  let overlayOpen = false;
  let rows = 0;
  let cur = null;
  let ticker = null;

  /* ---------- 收展 ---------- */
  function applyFold() {
    // 窄屏没有"常驻右栏"这回事：那一列本来就是浮层，收展只由 data-overlay 说话，
    // data-rail 让位给 CSS 的宽屏分支，两套状态不打架。
    document.documentElement.dataset.rail = (!narrow() && folded()) ? 'folded' : 'open';
    railEl.dataset.overlay = (narrow() && overlayOpen) ? 'open' : 'closed';
    const expanded = narrow() ? overlayOpen : !folded();
    foldBtn.title = expanded ? '收起现场栏' : '展开现场栏';
    foldBtn.setAttribute('aria-expanded', String(expanded));
    const openBtn = $('#rail-open');
    if (openBtn) {
      openBtn.setAttribute('aria-expanded', String(expanded));
      openBtn.title = expanded ? '收起现场栏' : '展开现场栏';
      openBtn.setAttribute('aria-label', openBtn.title);
    }
  }
  function setFolded(next) {
    userChose = true;
    choice = next;
    UIPrefs.set({ railFolded: next });
    applyFold();
  }
  function setOverlay(next) {
    overlayOpen = next;
    applyFold();
    // 竖签展开后自己就隐藏了：焦点得跟着送进面板，否则键盘用户按 Tab 会掉回页面顶部
    (next ? foldBtn : stripBtn).focus?.();
  }
  // 一条竖签在两种视口下含义不同：宽屏是"把栏放回来"，窄屏是"把浮层掀开"。
  foldBtn.addEventListener('click', () => (narrow() ? setOverlay(!overlayOpen) : setFolded(!folded())));
  stripBtn.addEventListener('click', () => (narrow() ? setOverlay(true) : setFolded(false)));
  $('#rail-open').addEventListener('click', (e) => {
    e.stopPropagation();
    if (narrow()) setOverlay(!overlayOpen); else setFolded(!folded());
  });
  if (narrowMQ.addEventListener) {
    narrowMQ.addEventListener('change', () => { overlayOpen = false; applyFold(); });
  }
  if (midMQ.addEventListener) midMQ.addEventListener('change', applyFold);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && narrow() && overlayOpen) setOverlay(false);
  });
  // 浮层盖在主区上，点它外面就该散开——不然用户只能找到那个小箭头才能关。
  document.addEventListener('click', (e) => {
    if (overlayOpen && narrow() && !railEl.contains(e.target)) setOverlay(false);
  }, true);
  applyFold();

  /* ---------- 小工具 ---------- */
  function clock(d) {
    const t = d || new Date();
    const p = (n) => String(n).padStart(2, '0');
    return `${p(t.getHours())}:${p(t.getMinutes())}:${p(t.getSeconds())}`;
  }
  function clip(s, n) { return s.length > n ? s.slice(0, n) + '…' : s; }
  function setRead(el, text, tone) {
    if (!el) return;
    el.textContent = text;
    if (tone) el.dataset.tone = tone; else delete el.dataset.tone;
  }

  /* ---------- 事件流水 ---------- */
  // 近底才自动跟随：用户在翻历史时把面板拽回底部，等于不让他看。
  // （主区对话此前踩过同一个坑，这里不再犯第二次。）
  function nearBottom() { return flowBox.scrollHeight - flowBox.scrollTop - flowBox.clientHeight < 80; }
  function addRow(taskID, tag, text, state) {
    const stick = nearBottom();
    const empty = flowBox.querySelector('.flow-empty');
    if (empty) empty.remove();
    const row = el('button', 'flow-row flow-row--new');
    row.type = 'button';
    if (taskID) row.dataset.task = taskID;
    row.innerHTML = `<span class="flow-dot" data-state="${esc(state)}"></span>`
      + `<span class="flow-main"><span class="flow-text">${esc(text)}</span>`
      + `<span class="flow-meta"><span class="flow-tag">${esc(tag)}</span><span>${clock()}</span></span></span>`;
    if (!taskID) row.style.cursor = 'default';
    else row.title = '跳到这个任务';
    row.addEventListener('click', () => focusTask(taskID));
    row.addEventListener('animationend', () => row.classList.remove('flow-row--new'), { once: true });
    flowBox.appendChild(row);
    while (flowBox.children.length > FLOW_MAX) flowBox.firstChild.remove();
    rows = flowBox.children.length;
    flowCount.textContent = rows;
    if (stick) flowBox.scrollTop = flowBox.scrollHeight;
  }
  function focusTask(taskID) {
    if (!taskID) return;
    showView('goals');
    const t = tasks.get(taskID);
    if (!t || !t.card.isConnected) return;
    t.card.hidden = false;
    t.card.scrollIntoView({ block: 'center', behavior: calm() ? 'auto' : 'smooth' });
    t.card.classList.remove('goal-card--flash');
    void t.card.offsetWidth;
    t.card.classList.add('goal-card--flash');
    // 用定时器而不是 animationend 摘掉高亮：reduced-motion 下动画不跑，
    // 事件也就不会来，那张卡会一直亮着。
    setTimeout(() => t.card.classList.remove('goal-card--flash'), 1300);
  }

  /* ---------- 循环轨 ---------- */
  // 「等你批准」是这一列最该抢镜的一条事实：引擎已经停了，但阶段格还停在"规划中"，
  // 只把状态渲成琥珀色——颜色不是事实，用户看得见颜色也读不出为什么。
  let waiting = 0;
  function resetTrack(taskID) {
    cur = { taskID, stage: '', since: Date.now(), stageSince: Date.now(), calls: 0, fails: 0, settled: false };
    STAGES.forEach((s) => { stageEls[s].dataset.state = 'idle'; noteEls[s].textContent = '—'; });
    budgetEl.dataset.hit = 'false';
    budgetEl.textContent = '预算闸 · 未触发';
  }
  function clearTrack(text) {
    STAGES.forEach((s) => { if (stageEls[s].dataset.state === 'active') stageEls[s].dataset.state = 'done'; });
    cur = null;
    waiting = 0;
    stopTicker();
    setRead(loopRead, text);
    delete loopEl.dataset.wait;
    stripStage.textContent = '空闲';
    paintPulse('idle');
  }
  function paintRead() {
    if (!cur) return;
    // 计时按"这一段多久"，不按"这个任务多久"：停在复盘格上显示整任务的秒数，
    // 用户会以为复盘跑了那么久。等批准时数的是"从哪一刻起就没动了"，即任务起点。
    const secs = Math.round((Date.now() - (waiting ? cur.since : cur.stageSince)) / 1000);
    if (waiting) {
      setRead(loopRead, `等你批准 · ${secs}s`, 'warn');
      loopEl.dataset.wait = 'true';
      stripStage.textContent = '待批准';
      paintPulse('attention');
    } else {
      setRead(loopRead, `${STAGE_CN[cur.stage] || '循环'}中 · ${secs}s`);
      stripStage.textContent = STAGE_CN[cur.stage] || '循环';
      paintPulse('running');
    }
  }
  function markStage(taskID, phase) {
    if (!STAGES.includes(phase)) return;
    const sameStage = cur && cur.taskID === taskID && cur.stage === phase;
    if (!cur || cur.taskID !== taskID) resetTrack(taskID);
    const idx = STAGES.indexOf(phase);
    STAGES.forEach((s, i) => {
      if (i < idx) stageEls[s].dataset.state = 'done';
      if (i === idx) stageEls[s].dataset.state = cur && cur.settled ? 'done' : 'active';
    });
    if (!sameStage) cur.stageSince = Date.now();   // 换段才重新计时，同段内增量事件不重置
    cur.stage = phase;
    cur.since = cur.since || Date.now();
    noteEls[phase].textContent = clock();
    paintRead();
    startTicker();
  }
  function markError(taskID, phase) {
    if (!STAGES.includes(phase)) return;
    stageEls[phase].dataset.state = 'error';
    paintPulse('failed');
  }
  function startTicker() {
    if (ticker) return;
    ticker = setInterval(() => {
      if (!cur || cur.settled) { stopTicker(); return; }
      paintRead();
    }, 1000);
  }
  function stopTicker() { if (ticker) { clearInterval(ticker); ticker = null; } }

  function paintPulse(state) {
    pulse.dataset.state = state;
    stripDot.dataset.state = state;
  }

  /* ---------- 对外入口（都兜住：旁路面板不能连累主流程） ---------- */
  function guard(fn) {
    return (...args) => {
      try { fn(...args); } catch (e) { console.warn('现场栏读数失败（不影响任务本身）：', e); }
    };
  }

  const observe = guard((d) => {
    if (!d) return;
    const msg = (d.message || '').trim();
    if (d.phase === 'budget') {
      if (!cur) resetTrack(d.task_id || '');
      budgetEl.dataset.hit = 'true';
      budgetEl.textContent = '预算闸 · ' + (clip(msg || '已触发', 22));
      paintPulse('attention');
    }
    if (d.phase === 'chat' && d.kind === 'llm') return; // 增量片段：进流水就是噪音
    if (d.phase === 'execute' && msg) {
      if (!cur) resetTrack(d.task_id);
      if (msg.startsWith('✅')) cur.calls++;
      else if (msg.startsWith('❌')) { cur.calls++; cur.fails++; }
      if (cur.calls) noteEls.execute.textContent = `${cur.calls}次${cur.fails ? '·红' + cur.fails : ''}`;
    }
    if (!msg) return;
    markStage(d.task_id, d.phase);
    if (d.kind === 'error') markError(d.task_id, d.phase);
    addRow(d.task_id, STAGE_CN[d.phase] || '事件', msg, d.kind === 'error' ? 'error' : d.kind === 'warn' ? 'warn' : 'active');
  });

  const approval = guard((ap) => {
    if (!ap) return;
    const plan = (ap.plan || [])[0] || '';
    addRow(ap.task_id, '待批准', plan ? clip(plan, 46) : '有一个操作在等你决定', 'warn');
    waiting++;
    if (cur) paintRead();
    else setRead(loopRead, '等你批准', 'warn');
    paintPulse('attention');
  });

  const result = guard((r) => {
    if (!r) return;
    const label = STATUS_LABEL[r.status] || r.status;
    if (r.task_id && cur && cur.taskID !== r.task_id) { /* 别的任务的终态，不动轨 */ }
    else if (cur) cur.settled = true;
    addRow(r.task_id, '终态', `${label}${typeof r.score === 'number' ? `（${r.score}/100）` : ''}`,
      r.status === 'success' ? 'done' : r.status === 'failed' ? 'error' : 'warn');
    if (!cur || cur.taskID === r.task_id) {
      clearTrack(r.status === 'success' ? `上一轮：已完成（${r.score}/100）` : `上一轮：${label}`);
    }
    // 一轮跑完可能长出新的记忆/技能：读数跟着刷一次，不然这一列会停在旧数字上，
    // 而"面板上的数字是旧的"比"没有数字"更难被怀疑。
    api('GET', '/api/info').then(facts).catch(() => {});
  });

  // attention 是"在等谁"的唯一算法（工作台数出来的），这里只跟着它同步，不再自己数一遍。
  const summary = guard((counts, attention, weekDone) => {
    setRead(ro.approvals, String(attention || 0), attention ? 'warn' : null);
    setRead($('#metric-active'), String(counts.active || 0));
    setRead($('#metric-done'), String(weekDone || 0));
    waiting = attention || 0;
    if (cur) paintRead();
    if (!cur) {
      paintPulse(attention ? 'attention' : counts.active ? 'running' : 'idle');
      if (attention) setRead(loopRead, `等你批准 · ${attention} 项`, 'warn');
      else if (counts.active) loopRead.textContent = `${counts.active} 个目标在推进`;
    }
  });

  const facts = guard((info) => {
    if (!info) return;
    if (info.model) {
      setRead(ro.model, clip(info.model, 22), 'ok');
      // 同一个字段喂两处：现场栏只读，输入区那块还能点着切。两处各拉一次
      // /api/info 就会一个先动一个后动，看着像两个模型名。
      ComposerMeta.live(info.model);
    }
    if (typeof info.tools === 'number') setRead(ro.tools, String(info.tools));
    if (typeof info.memory === 'number') setRead(ro.memory, String(info.memory));
    if (info.version) setRead(ro.version, 'v' + info.version);
  });

  const runtime = guard((s) => {
    if (!s || !s.llm) return;
    const host = s.llm.api_key_host_cur || '';
    if (s.llm.model) setRead(ro.model, clip(s.llm.model, 22), 'ok');
    if (!host) return;
    setRead(ro.host, s.llm.api_key_set ? host : host + ' · 无密钥', s.llm.api_key_set ? 'ok' : 'warn');
  });

  const schedules = guard((n) => { if (typeof n === 'number') setRead(ro.schedules, String(n)); });

  const memory = guard((n) => { if (typeof n === 'number') setRead(ro.memory, String(n)); });

  return { observe, approval, result, summary, facts, runtime, schedules, memory };
})();

/* ============================================================
 * 浏览器预览面板（批次 F13）
 *
 * 三条判断，写下来免得下一位当成随手可改的细节：
 *  1. **非模态**。它是"边干活边看本机跑起来的样子"，所以没有遮罩、不锁焦点，
 *     开着它照样能发消息、看进度、点现场栏。
 *  2. **前进/后退走我们自己的地址栈**。被嵌的页面是跨源的，它的 history 我们
 *     既读不到也按不动——假装能控制就是骗人。所以只在"我们主动换地址"时入栈。
 *  3. **地址在入口校验**。这是一行用户输入的 URL，直接塞进 iframe.src 等于把
 *     `javascript:` / `data:` 也收下来。只认 http/https，别的红那一格。
 *
 * iframe 的 sandbox 写在 HTML 里（owner 在那儿）：不给 allow-top-navigation，
 * 所以被嵌的页面不能把整个操作台拽走。刷新走"换一个新节点"而不是原地重载——
 * 跨源时 `contentWindow.location.reload()` 会抛，而复制旧节点能带上那串属性，
 * 不必在这里再抄一份。
 * ============================================================ */
const BrowserPane = (() => {
  const pane = $('#browser-pane');
  const urlIn = $('#bp-url');
  const form = $('#bp-form');
  const empty = $('#bp-empty');
  let frame = $('#bp-frame');
  if (!pane || !urlIn || !form || !frame || !empty) return { toggle: () => {}, isOpen: () => false };

  const status = $('#bp-status');
  const backBtn = $('#bp-back');
  const fwdBtn = $('#bp-fwd');
  const quickBox = $('#bp-quick');

  // 常用端口：本机起服务翻来覆去就是这几个，写出来比让用户记地址有用。
  const QUICK = [
    { url: 'http://127.0.0.1:5173', label: 'Vite 5173' },
    { url: 'http://127.0.0.1:8080', label: 'Vue CLI 8080' },
    { url: 'http://127.0.0.1:5000', label: 'Kestrel 5000' },
    { url: 'http://127.0.0.1:8798', label: 'Gleam 自己' },
  ];

  const hist = [];
  let hi = -1;
  let opener = null;   // 谁把它打开的，关掉时把焦点还回去

  // normalizeURL 只放行 http/https；没写协议就补 http://（本机地址不必每次手打协议）。
  function normalizeURL(raw) {
    let s = String(raw || '').trim();
    if (!s) return null;
    if (s.startsWith('//')) s = 'http:' + s;
    else if (!/^[a-z][a-z0-9+.-]*:/i.test(s)) s = 'http://' + s;
    let u;
    try { u = new URL(s); } catch { return null; }
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return null;
    return u.href;
  }

  const current = () => (hi >= 0 ? hist[hi] : '');

  function setStatus(text, bad) {
    status.textContent = text;
    if (bad) status.dataset.bad = 'true'; else delete status.dataset.bad;
  }

  function paintNav() {
    backBtn.disabled = hi <= 0;
    fwdBtn.disabled = hi < 0 || hi >= hist.length - 1;
    quickBox.querySelectorAll('.bp-chip').forEach((c) => {
      const on = c.dataset.url === current();
      if (on) c.setAttribute('aria-current', 'true'); else c.removeAttribute('aria-current');
    });
  }

  // show 只做"把这一格画成这个地址"，入不入栈由调用方决定。
  function show(url) {
    urlIn.value = url;
    delete urlIn.dataset.bad;
    frame.src = url;
    frame.hidden = false;
    empty.hidden = true;
    setStatus('正在载入 ' + url + ' · 一直空白多半是它拒绝被嵌，用「在系统浏览器打开」');
    UIPrefs.set({ browserLast: url });
    paintQuick();
    paintNav();
  }

  function openURL(raw) {
    const url = normalizeURL(raw);
    if (!url) {
      urlIn.dataset.bad = 'true';
      urlIn.focus();
      urlIn.select();
      setStatus('这个地址打不开：只接受 http / https。', true);
      return false;
    }
    if (url !== current()) {
      hist.splice(hi + 1);        // 从中间地址再打开时，后面的"未来"作废——和浏览器一样
      hist.push(url);
      hi = hist.length - 1;
    }
    show(url);
    return true;
  }

  function step(delta) {
    const next = hi + delta;
    if (next < 0 || next >= hist.length) return;
    hi = next;
    show(hist[hi]);
  }

  // 原地重载跨源会抛，所以换节点：clone 带齐 sandbox / referrerpolicy，
  // 但要先摘掉 src，否则插进去就按旧地址自己加载一遍。
  function reload() {
    const url = current();
    if (!url) return;
    const fresh = frame.cloneNode();
    fresh.removeAttribute('src');
    frame.replaceWith(fresh);
    frame = fresh;
    show(url);
  }

  function paintQuick() {
    quickBox.replaceChildren();
    const last = UIPrefs.get().browserLast;
    const items = [];
    if (last) items.push({ url: last, label: '上次看的' });
    QUICK.forEach((q) => { if (q.url !== last) items.push(q); });
    if (!items.length) return;
    quickBox.appendChild(el('span', 'bp-quick-label', '常用'));
    items.forEach((it) => {
      const b = el('button', 'bp-chip', it.label);
      b.type = 'button';
      b.dataset.url = it.url;
      b.title = it.url;
      b.addEventListener('click', () => openURL(it.url));
      quickBox.appendChild(b);
    });
  }

  function open() {
    if (!pane.hidden) return;
    opener = document.activeElement;
    pane.hidden = false;
    // 同一会话里再打开回来，应该还是刚才那一页（close 只摘 src，不动地址栈）。
    // 新会话则不自动加载——只给「上次看的」那颗芯片，本机服务可能已经停了。
    if (current()) show(current());
    paintQuick();
    paintNav();
    urlIn.focus();
    urlIn.select();
  }

  function close() {
    if (pane.hidden) return;
    pane.hidden = true;
    frame.src = 'about:blank';   // 关了就断掉里面的请求，别让它继续在后台跑
    frame.hidden = true;
    empty.hidden = false;
    setStatus('');
    if (opener && document.contains(opener)) opener.focus();
    opener = null;
  }

  function toggle() { if (pane.hidden) open(); else close(); }

  form.addEventListener('submit', (e) => { e.preventDefault(); openURL(urlIn.value); });
  backBtn.addEventListener('click', () => step(-1));
  fwdBtn.addEventListener('click', () => step(1));
  $('#bp-reload').addEventListener('click', reload);
  $('#bp-close').addEventListener('click', close);
  $('#bp-external').addEventListener('click', () => {
    const url = current();
    if (!url) { setStatus('还没有地址，先打开一个再谈外部浏览器。', true); return; }
    // 这里不能图省事写 'noopener'：按规范那样传第三参，window.open 成功时也返回
    // null，于是"被拦了"这句永远报得出来——是假警报。改为拿到新窗口后亲手摘掉
    // opener（等价于 noopener，挡住反向 tabnabbing），返回值才真的能用来判成败。
    const w = window.open(url, '_blank');
    if (w) { try { w.opener = null; } catch { /* 跨源时摘不动，也不影响 */ } }
    setStatus(w ? '已在系统浏览器打开。' : '没弹出来：浏览器拦了弹窗，请在地址栏那里放行。', !w);
  });
  // 用捕获阶段：一次 Esc 只该撤掉一层。模态自己的处理器在冒泡阶段跑，若我们也排在
  // 冒泡里，抽屉刚被它关掉、我们再查就查不到"有模态开着"了——两层一起消失。
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || pane.hidden) return;
    // 有模态开着时 Esc 归模态管，面板不该跟着一起消失。
    // 问的是「真渲染出来没有」而不是「有没有 hidden 属性」：hidden 挂在外层遮罩
    // （#me-overlay / #modal-overlay）上，对话节点自己永远不带，按属性筛会永远判成
    // 「有模态开着」，于是 Esc 再也关不掉这个面板。
    const modalOpen = [...document.querySelectorAll('[aria-modal="true"]')]
      .some((n) => n.getClientRects().length > 0);
    if (modalOpen) return;
    close();
  }, { capture: true });

  return { toggle, open, close, isOpen: () => !pane.hidden, normalizeURL };
})();

/* ---------- 初始化 ---------- */
applyTheme();
api('GET', '/api/info').then((info) => { const v = $('#me-version'); if (v && info.version) v.textContent = 'v' + info.version; LiveRail.facts(info); }).catch(() => {});

/* ---------- 首次引导 ---------- */
const Onboarding = (function () {
  let providers = [];
  let selectedProvider = null;
  let selectedPlan = null;

  function show(step) {
    const overlay = $('#onboarding-overlay');
    overlay.hidden = false;
    for (let i = 0; i < 3; i++) {
      const page = $(`#ob-page-${i}`);
      page.hidden = i !== step;
      const dot = overlay.querySelector(`.onboarding-dot[data-step="${i}"]`);
      dot.classList.toggle('active', i === step);
      dot.classList.toggle('done', i < step);
    }
    if (step === 1) renderProviders();
    if (step === 2) {
      const p = providers.find((x) => x.id === selectedProvider);
      if (p) $('#ob-key-desc').textContent = `${p.name} 的 API Key，填好后测试连通性。`;
      $('#ob-api-key').value = '';
      $('#ob-test-result').hidden = true;
    }
  }

  function hide() {
    $('#onboarding-overlay').hidden = true;
    localStorage.setItem('gleam-onboarding-done', '1');
  }

  function renderProviders() {
    const box = $('#ob-providers');
    box.innerHTML = '';
    providers.forEach((p) => {
      const card = document.createElement('button');
      card.type = 'button';
      card.className = 'onboarding-provider';
      card.dataset.id = p.id;
      card.innerHTML = `<span class="ob-p-name">${esc(p.name)}</span><span class="ob-p-id">${esc(p.id)}</span>`;
      card.addEventListener('click', () => {
        box.querySelectorAll('.onboarding-provider').forEach((c) => c.classList.remove('selected'));
        card.classList.add('selected');
        selectedProvider = p.id;
        selectedPlan = p.plans && p.plans[0] ? p.plans[0].kind : 'token';
        $('#ob-next-1').disabled = false;
      });
      box.appendChild(card);
    });
  }

  async function testConnection() {
    const btn = $('#ob-test-btn');
    const out = $('#ob-test-result');
    btn.disabled = true;
    out.hidden = false;
    out.className = 'onboarding-test-result testing';
    out.textContent = '正在探测…';
    const key = $('#ob-api-key').value.trim();
    try {
      const r = await api('POST', '/api/llm/test', {
        provider_id: selectedProvider,
        api_key: key,
      });
      if (r.ok) {
        const lat = Number.isFinite(r.latency_ms) ? ` · ${r.latency_ms}ms` : '';
        out.textContent = r.kind === 'mock' ? 'Mock 模型' : `连接成功${lat}`;
        out.className = 'onboarding-test-result ok';
      } else {
        const hints = { auth: 'Key 无效或已过期', timeout: '连接超时', network: '网络不通', provider: '厂商服务异常', api: '接口返回错误', not_found: '接口不存在', rate_limited: '请求过于频繁', config: '配置有误' };
        out.textContent = (hints[r.kind] || '连接失败') + (r.message ? `（${r.message}）` : '');
        out.className = 'onboarding-test-result fail';
      }
    } catch (err) {
      out.textContent = `测试失败：${err.message}`;
      out.className = 'onboarding-test-result fail';
    } finally {
      btn.disabled = false;
    }
  }

  async function finish() {
    const key = $('#ob-api-key').value.trim();
    if (!key) { toast('请先填写 API Key', 'error'); return; }
    const btn = $('#ob-finish');
    btn.disabled = true;
    btn.textContent = '保存中…';
    try {
      const r = await api('POST', '/api/onboarding', {
        provider_id: selectedProvider,
        plan: selectedPlan || 'token',
        api_key: key,
      });
      if (r.test_result && r.test_result.ok) {
        toast('配置成功，开始使用吧', 'success');
      } else {
        toast('已保存，但连接测试未通过，可在设置页调整', 'warning');
      }
      hide();
      loadSettings();
    } catch (err) {
      toast(`保存失败：${err.message}`, 'error');
    } finally {
      btn.disabled = false;
      btn.textContent = '完成';
    }
  }

  async function start() {
    if (localStorage.getItem('gleam-onboarding-done')) return;
    try {
      const data = await api('GET', '/api/onboarding');
      if (!data.needs_onboarding) { hide(); return; }
      providers = data.providers || [];
      show(0);

      $('#ob-next-0').onclick = () => show(1);
      $('#ob-skip').onclick = () => hide();
      $('#ob-back-1').onclick = () => show(0);
      $('#ob-next-1').onclick = () => { if (selectedProvider) show(2); };
      $('#ob-back-2').onclick = () => show(1);
      $('#ob-test-btn').onclick = () => testConnection();
      $('#ob-finish').onclick = () => finish();
      $('#ob-api-key').addEventListener('keydown', (e) => { if (e.key === 'Enter') finish(); });
    } catch { /* 引导加载失败不阻塞主界面 */ }
  }

  return { start };
})();


/* ---------- 头像菜单 + 外观子菜单 ---------- */
const MeMenu = (() => {
  const menu = $('#me-menu');
  const btn = $('#open-me');
  const sub = $('#me-appearance');
  function syncChecks() {
    applyTheme();
    const signedIn = !$('#me-user').hidden;
    menu.querySelector('[data-me="signout"]').hidden = !signedIn;
    menu.querySelector('[data-me-signout-sep]').hidden = !signedIn;
  }
  // 菜单贴在用户行正上方、与侧栏左缘对齐；侧栏收起时退回按钮右侧
  function place() {
    const row = btn.closest('.user-row') || btn;
    const r = row.getBoundingClientRect();
    const collapsed = r.width === 0;
    const anchor = collapsed ? $('#sidebar-toggle').getBoundingClientRect() : r;
    menu.style.left = Math.round(collapsed ? anchor.left : r.left + 12) + 'px';
    menu.style.bottom = Math.max(8, Math.round(window.innerHeight - r.top + 11)) + 'px';
  }
  // 子菜单顶边对齐触发项；超出视口底部时整体上移，留 16px 余量
  function clampFlyout(fly) {
    if (!fly) return;
    fly.style.top = '';
    const rect = fly.getBoundingClientRect();
    const over = rect.bottom - (window.innerHeight - 16);
    if (over > 0) fly.style.top = -Math.round(over) + 'px';
  }
  function setSub(open) {
    sub.classList.toggle('open', open);
    sub.querySelector('[data-me="appearance"]').setAttribute('aria-expanded', String(open));
    if (open) clampFlyout(sub.querySelector(':scope > .menu-flyout'));
    if (!open) openLeaf(null);
  }
  // 三级子菜单：同一时刻只开一个
  function openLeaf(leaf) {
    sub.querySelectorAll('.menu-flyout .menu-sub').forEach((s) => {
      const on = s === leaf;
      s.classList.toggle('open', on);
      s.querySelector('[data-sub]').setAttribute('aria-expanded', String(on));
      if (on) clampFlyout(s.querySelector(':scope > .menu-flyout'));
    });
  }
  sub.querySelectorAll('.menu-flyout .menu-sub').forEach((s) => s.addEventListener('mouseenter', () => openLeaf(s)));
  function open() { syncChecks(); place(); menu.hidden = false; btn.setAttribute('aria-expanded', 'true'); menu.querySelector('.menu-item').focus(); }
  function close() { if (menu.hidden) return; menu.hidden = true; setSub(false); btn.setAttribute('aria-expanded', 'false'); }
  btn.setAttribute('aria-haspopup', 'menu');
  btn.setAttribute('aria-expanded', 'false');
  btn.addEventListener('click', (e) => { e.stopPropagation(); if (menu.hidden) open(); else close(); });
  menu.addEventListener('click', (e) => e.stopPropagation());
  document.addEventListener('click', close);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') close(); });
  window.addEventListener('resize', close);
  sub.addEventListener('mouseenter', () => setSub(true));
  sub.addEventListener('mouseleave', () => setSub(false));
  menu.addEventListener('click', async (e) => {
    const pr = e.target.closest('[data-pref]');
    if (pr) { UIPrefs.set({ [pr.dataset.pref]: pr.dataset.val }); return; }
    const leaf = e.target.closest('[data-sub]');
    if (leaf) { const s = leaf.closest('.menu-sub'); openLeaf(s.classList.contains('open') ? null : s); return; }
    const it = e.target.closest('[data-me]');
    if (!it) return;
    const act = it.dataset.me;
    if (act === 'appearance') { setSub(!sub.classList.contains('open')); return; }
    close();
    if (act === 'settings') showView('settings');
    else if (act === 'growth') showView('growth');
    else if (act === 'update') $('#me-update').click();
    else if (act === 'help') $('#me-help').click();
    else if (act === 'account') MeDrawer.open();
    else if (act === 'signout') $('#me-signout').click();
  });
  return { open, close };
})();
$('#sp-account').addEventListener('click', () => MeDrawer.open());
document.addEventListener('keydown', (e) => {
  // Ctrl/⌘ + , 打开设置（输入法组字时不抢）
  if ((e.ctrlKey || e.metaKey) && e.key === ',' && !e.isComposing) { e.preventDefault(); showView('settings'); }
});
$('#memory-empty-write').addEventListener('click', () => $('#memory-content').focus());

/* ---------- 全局任务搜索（Ctrl+K）：本机会话 + 当前已载入的目标 ---------- */
const TaskSearch = (() => {
  const overlay = $('#task-search');
  const input = $('#task-search-input');
  const list = $('#task-search-list');
  const count = $('#task-search-count');
  let items = [];
  let shown = [];
  let sel = 0;
  let lastFocus = null;
  const fmtTime = (s) => {
    if (!s) return '';
    const d = new Date(s);
    if (isNaN(d)) return '';
    const today = new Date();
    return d.toDateString() === today.toDateString()
      ? d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
      : d.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' });
  };
  async function collect() {
    const spaceName = new Map((spaceState.spaces || []).map((sp) => [sp.id, sp.name]));
    let convos = [];
    try { convos = (await api('GET', '/api/conversations')).conversations || []; } catch { convos = []; }
    const out = convos.map((c) => ({
      kind: 'convo', id: c.id, title: c.title || '新对话',
      sub: [spaceName.get(c.space_id || 'default') || '默认空间', c.preview || ''].filter(Boolean).join(' · '),
      time: c.updated_at, hay: `${c.title} ${c.preview || ''} ${c.id}`.toLowerCase(),
    }));
    [...tasks.values()].forEach((t) => {
      const i = t.info || {};
      out.push({ kind: 'goal', id: i.task_id, title: i.goal || '目标', sub: '目标 · ' + (STATUS_LABEL[i.status] || i.status || ''),
        time: i.created_at || i.started_at, hay: `${i.goal || ''} ${i.task_id || ''}`.toLowerCase() });
    });
    out.sort((a, b) => (new Date(b.time || 0)) - (new Date(a.time || 0)));
    return out;
  }
  function render() {
    const q = input.value.trim().toLowerCase();
    shown = q ? items.filter((it) => it.hay.includes(q)) : items;
    sel = Math.min(sel, Math.max(0, shown.length - 1));
    count.textContent = String(shown.length);
    list.innerHTML = '';
    if (!shown.length) { list.appendChild(el('div', 'search-empty', q ? '没有匹配的任务' : '还没有任务')); return; }
    shown.slice(0, 200).forEach((it, i) => {
      const row = el('button', 'search-row' + (i === sel ? ' active' : ''));
      row.type = 'button';
      row.setAttribute('role', 'option');
      row.setAttribute('aria-selected', String(i === sel));
      row.innerHTML = `<span class="search-row-title">${esc(it.title)}</span><span class="search-row-sub">${esc(it.sub)}</span><span class="search-row-time">${esc(fmtTime(it.time))}</span>`;
      row.addEventListener('mousemove', () => { if (sel !== i) { sel = i; paintSel(); } });
      row.addEventListener('click', () => choose(it));
      list.appendChild(row);
    });
  }
  function paintSel() {
    [...list.children].forEach((r, i) => { r.classList.toggle('active', i === sel); r.setAttribute('aria-selected', String(i === sel)); });
    const cur = list.children[sel];
    if (cur && cur.scrollIntoView) cur.scrollIntoView({ block: 'nearest' });
  }
  function choose(it) {
    close();
    if (it.kind === 'convo') { showView('goals'); openConvo(it.id); return; }
    showView('goals');
    const t = tasks.get(it.id);
    if (t && t.card && t.card.isConnected) t.card.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }
  async function open() {
    lastFocus = document.activeElement;
    overlay.hidden = false;
    input.value = '';
    sel = 0;
    list.innerHTML = '<div class="search-empty">加载中…</div>';
    input.focus();
    items = await collect();
    render();
  }
  function close() { if (overlay.hidden) return; overlay.hidden = true; if (lastFocus && lastFocus.focus) lastFocus.focus(); }
  input.addEventListener('input', () => { sel = 0; render(); });
  input.addEventListener('keydown', (e) => {
    if (e.isComposing) return;
    if (e.key === 'ArrowDown') { e.preventDefault(); if (shown.length) { sel = (sel + 1) % Math.min(shown.length, 200); paintSel(); } }
    else if (e.key === 'ArrowUp') { e.preventDefault(); if (shown.length) { sel = (sel - 1 + Math.min(shown.length, 200)) % Math.min(shown.length, 200); paintSel(); } }
    else if (e.key === 'Enter') { e.preventDefault(); if (shown[sel]) choose(shown[sel]); }
  });
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !overlay.hidden) { close(); return; }
    if ((e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K') && !e.isComposing) { e.preventDefault(); if (overlay.hidden) open(); else close(); }
  });
  $('#task-search-open').addEventListener('click', open);
  return { open, close };
})();

// 设置面板：分区标题挪到卡片上方，卡片只装设置行
document.querySelectorAll('.settings-panel > .card > .section-title:first-child').forEach((h) => {
  h.classList.add('settings-section-label');
  h.parentElement.before(h);
});

/* ---------- 壳层：侧栏收展 / 底部快捷跳转 / 首页态 / 本机活动卡 ---------- */
const ShellLayout = (() => {
  const toggle = $('#sidebar-toggle');
  function syncToggle() {
    const collapsed = !!UIPrefs.get().sidebarCollapsed;
    toggle.setAttribute('aria-expanded', String(!collapsed));
    toggle.title = collapsed ? '展开侧栏' : '收起侧栏';
    toggle.setAttribute('aria-label', toggle.title);
  }
  toggle.addEventListener('click', () => {
    UIPrefs.set({ sidebarCollapsed: !UIPrefs.get().sidebarCollapsed });
    syncToggle();
  });
  syncToggle();
  document.querySelectorAll('[data-goto]').forEach((b) => b.addEventListener('click', () => showView(b.dataset.goto)));

  // 本机活动：只用本机真实数据（会话最后活跃日、成长日志里完成的任务），没有就保持全空格子
  const WEEKS = 52;
  let tab = 'convo';
  let cache = null;
  const dayKey = (d) => `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
  async function fetchData() {
    const [cv, gr] = await Promise.all([
      api('GET', '/api/conversations').catch(() => ({})),
      api('GET', '/api/growth/recent?n=200').catch(() => ({})),
    ]);
    const convo = new Map();
    (cv.conversations || []).forEach((c) => {
      if (!c.count || !c.updated_at) return;
      const k = dayKey(new Date(c.updated_at));
      convo.set(k, (convo.get(k) || 0) + 1);
    });
    const task = new Map();
    (gr.entries || []).forEach((e) => {
      if (e.type !== 'task_completed' || !e.time) return;
      const k = dayKey(new Date(e.time));
      task.set(k, (task.get(k) || 0) + 1);
    });
    return { convo, task };
  }
  function level(n, max) {
    if (!n) return 0;
    if (max <= 1) return 4;
    return Math.min(4, 1 + Math.floor((n - 1) / Math.max(1, max / 4)));
  }
  function render(card) {
    const data = cache ? cache[tab] : new Map();
    const today = new Date(); today.setHours(0, 0, 0, 0);
    const start = new Date(today);
    start.setDate(start.getDate() - (WEEKS - 1) * 7 - today.getDay());
    const max = Math.max(0, ...data.values());
    let total = 0;
    const cells = [];
    const months = [];
    let lastMonth = -1;
    for (let w = 0; w < WEEKS; w++) {
      for (let d = 0; d < 7; d++) {
        const day = new Date(start); day.setDate(start.getDate() + w * 7 + d);
        if (d === 0 && day.getMonth() !== lastMonth) {
          if (w > 0) months.push(`<span style="grid-column:${w + 1}">${day.getMonth() + 1}月</span>`);
          lastMonth = day.getMonth();
        }
        if (day > today) { cells.push('<i class="heat-cell" data-future></i>'); continue; }
        const n = data.get(dayKey(day)) || 0;
        total += n;
        cells.push(`<i class="heat-cell" data-l="${level(n, max)}" title="${day.getMonth() + 1}/${day.getDate()} · ${n}"></i>`);
      }
    }
    card.innerHTML = `<div class="activity-tabs" role="tablist">
        <button type="button" role="tab" data-act="convo" aria-selected="${tab === 'convo'}">会话</button>
        <button type="button" role="tab" data-act="task" aria-selected="${tab === 'task'}">任务</button>
      </div>
      <div class="heat-grid" aria-label="过去一年的本机活动，共 ${total} 次">${cells.join('')}</div>
      <div class="heat-months" aria-hidden="true">${months.join('')}</div>`;
    card.querySelectorAll('[data-act]').forEach((b) => b.addEventListener('click', () => { tab = b.dataset.act; render(card); }));
  }
  async function paintActivity() {
    const card = document.querySelector('#goals-empty .activity-card');
    if (!card) return;
    if (!card.childElementCount) render(card);
    try { cache = await fetchData(); } catch { cache = null; }
    if (card.isConnected) render(card);
  }

  // 首页态：会话流里只有空态时，主区换成首页布局（插图位 + 活动卡 + 底部输入框）
  const view = $('#view-goals');
  const feed = $('#goal-feed');
  function syncHome() {
    const empty = $('#goals-empty');
    const home = !!empty && feed.children.length === 1;
    view.classList.toggle('is-home', home);
    if (home && empty.querySelector('.activity-card') && !empty.querySelector('.activity-card').childElementCount) paintActivity();
  }
  new MutationObserver(syncHome).observe(feed, { childList: true });
  syncHome();
  return { paintActivity, syncHome };
})();

/* ---------- 输入框工具条：执行方式下拉 + 侧栏模式切换 + 附件 ----------
 * 下拉和侧栏开关都不存状态：真实状态仍在 #perm-seg / #task-seg 的 aria-pressed 上，
 * 这里只读它们来画按钮，改动也一律转成对那两组按钮的点击（同一条保存路径）。 */
const ComposerControls = (() => {
  const permBtn = $('#cp-perm');
  const permPop = $('#cp-perm-pop');
  const permLabel = $('#cp-perm-label');
  const permIcon = $('#cp-perm-icon');
  const toggle = $('#mode-toggle');
  if (!permBtn || !permPop || !toggle) return {};
  const ICON = {
    auto: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8.5"/><path d="M10.2 8.8v6.4l5-3.2z"/></svg>',
    plan_first: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 5 5.8v5.4c0 4.4 3 7.9 7 9.3 4-1.4 7-4.9 7-9.3V5.8z"/><path d="m9.3 12 1.9 1.9 3.6-3.7"/></svg>',
    chat: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11.5a7.5 7.5 0 0 1-7.5 7.5H8l-4 3v-4.6A7.5 7.5 0 1 1 20 11.5z"/></svg>',
  };
  const LABEL = { auto: '自动执行', plan_first: '执行前询问', chat: '仅对话' };
  const pressed = (sel) => {
    const b = document.querySelector(sel + ' button[aria-pressed="true"]');
    return b ? (b.dataset.perm || b.dataset.task) : '';
  };
  function paint() {
    const task = pressed('#task-seg') || 'work';
    const perm = pressed('#perm-seg') || 'auto';
    const key = task === 'chat' ? 'chat' : (perm === 'plan_first' ? 'plan_first' : 'auto');
    permLabel.textContent = LABEL[key];
    permIcon.innerHTML = ICON[key];
    permBtn.dataset.mode = key;
    permBtn.title = key === 'auto' ? '自动执行：仅高风险操作才征求批准（点击切换）'
      : key === 'plan_first' ? '执行前先给出计划，征求你的批准（点击切换）'
        : '只聊天，不规划执行（点击切换）';
    const code = task === 'code';
    toggle.querySelectorAll('button[data-mode]').forEach((b) => {
      b.setAttribute('aria-checked', String((b.dataset.mode === 'code') === code));
    });
  }
  const mo = new MutationObserver(paint);
  ['#task-seg', '#perm-seg'].forEach((sel) => {
    const n = $(sel);
    if (n) mo.observe(n, { subtree: true, attributes: true, attributeFilter: ['aria-pressed'] });
  });
  paint();

  const setOpen = (open) => {
    permPop.hidden = !open;
    permBtn.setAttribute('aria-expanded', String(open));
  };
  permBtn.addEventListener('click', () => {
    const open = permPop.hidden;
    // 让其它输入框弹层先按自己的规矩收起（它们监听 document 点击），再开这一个
    setTimeout(() => setOpen(open), 0);
  });
  document.addEventListener('click', (e) => {
    if (!permPop.hidden && !e.target.closest('#cp-perm-pop') && !e.target.closest('#cp-perm')) setOpen(false);
  });
  ['#cp-model', '#cp-context', '#plus-btn'].forEach((sel) => {
    const n = $(sel);
    if (n) n.addEventListener('click', () => setOpen(false), true);
  });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !permPop.hidden) setOpen(false); });

  toggle.addEventListener('click', (e) => {
    const b = e.target.closest('button[data-mode]');
    if (!b) return;
    const cur = pressed('#task-seg') || 'work';
    const want = b.dataset.mode;
    if (want === 'code' && cur !== 'code') document.querySelector('#task-seg button[data-task="code"]').click();
    if (want === 'work' && cur === 'code') document.querySelector('#task-seg button[data-task="work"]').click();
  });

  const attach = $('#cp-attach');
  if (attach) {
    attach.addEventListener('click', (e) => {
      e.stopPropagation();
      const item = document.querySelector('#plus-menu button[data-plus="file"]');
      if (item) item.click();
    });
  }
  return { paint };
})();

/* ---------- 启动（必须是本文件的最后一段，判据见 scripts/check-app-startup.py） ---------- */
(async function init() {
  Onboarding.start();
  fetch('/api/heartbeat', { method: 'POST' }).catch(() => {});
  connectSSE();
  setConn('up');
  loadGoals();
  loadConvoList();
  loadWorkspace();
  loadGoStatus();
  loadRoles();
  loadContext(); // 输入区的水位条：首屏就得有数，否则那一格永远停在「—」
  loadCues();    // 候补目标同理：它只在有话说时露出来，首屏不拉就没人知道它存在
  try { const s = await api('GET', '/api/settings'); if (s.safety && s.safety.mode) _doSetPerm(s.safety.mode, false); syncRuntimeState(s); } catch {}
  // 现场栏的「定时任务」不等用户打开定时任务视图才有数：这一列的价值就是常驻
  api('GET', '/api/schedules').then((r) => LiveRail.schedules((r.jobs || []).length)).catch(() => {});
  try {
    const { approvals } = await api('GET', '/api/approvals');
    pendingApprovals = (approvals || []).length;
    updateApprovalBadge(0);
  } catch { /* 忽略 */ }
})();
