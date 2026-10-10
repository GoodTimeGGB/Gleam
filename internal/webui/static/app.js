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

/* ---------- 输入框弹层协调 ----------
 * 以前每个弹层各管各的：点加号开一个，再点曲别针又开一个，两个会叠在一起；
 * 位置也各自按卡片的左/右边写死，按钮换行后弹层就和按钮错位。
 * 这里收成一处：任何弹层打开前先关掉别的，并且一律贴在触发按钮的正上方。
 */
const Popovers = (() => {
  const reg = new Map(); // id -> 收起函数
  function register(id, close) { reg.set(id, close); }
  function closeOthers(id) { reg.forEach((close, k) => { if (k !== id) close(); }); }
  function closeAll() { reg.forEach((close) => close()); }
  return { register, closeOthers, closeAll };
})();
// 弹层是固定定位的：窗口一改尺寸，它们和按钮的对应关系就旧了，直接收起最省事
window.addEventListener('resize', () => Popovers.closeAll());

// 把弹层摆到按钮正上方：底边离按钮顶边 gap，左缘与按钮对齐，并夹在视口内；上方放不下就翻到下方。
//
// 用**绝对定位 + 参照块坐标**而不是固定定位：外壳里带 transform / backdrop-filter 的祖先会变成
// 固定定位的包含块，那时 `top` 就不再是「距视口」，算出来的位置会整体偏掉。绝对定位只认最近的
// 定位祖先（弹层各自的 offsetParent），把视口边界换算进这套坐标即可，两种祖先都算得对。
function anchorPopover(pop, btn, gap = 8) {
  pop.hidden = false; // 先显示才量得到尺寸
  pop.style.position = 'absolute';
  pop.style.right = 'auto';
  pop.style.bottom = 'auto';
  const w = pop.offsetWidth, h = pop.offsetHeight;
  const cb = pop.offsetParent || document.body;
  const cbr = cb.getBoundingClientRect();
  const b = btn.getBoundingClientRect();
  const left = Math.max(4 - cbr.left, Math.min(b.left - cbr.left, window.innerWidth - w - 4 - cbr.left));
  let top = b.top - gap - h - cbr.top;
  if (b.top - h - gap < 4) top = b.bottom + gap - cbr.top; // 上方放不下 → 翻到下方
  top = Math.max(4 - cbr.top, Math.min(top, window.innerHeight - h - 4 - cbr.top));
  pop.style.left = Math.round(left) + 'px';
  pop.style.top = Math.round(top) + 'px';
}

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
const VIEW_LOADERS = { goals: loadGoals, skills: () => loadSkills(), memory: () => { initMemoryOnce(); LocalImport.renderMemory(); }, schedules: () => loadSchedules(), tools: () => loadTools(), settings: () => { loadSettingsProfile(); return loadSettings(); }, market: () => loadMarket(), sites: loadSites, growth: () => loadGrowth(), geo: () => loadGEO() };

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

const PERM_FRIENDLY = { auto: '完全访问', plan_first: '请我批准', interactive: '请我批准', full_access: '完全访问' };
let currentPermLabel = 'auto'; // 前端展示用的权限标签：plan_first | auto | full_access
// 任务档位的人读名：与 #task-seg 的三个按钮一致。卡片徽标以前直接印枚举值，
// 刷新后从"完全访问 · 编程"变成 auto/plan_first，同一件事两种说法。
const TASK_LABEL = { chat: '对话', work: '工作', code: '编程' };

function setPerm(mode, label) {
  if (mode !== 'auto') {
    _doSetPerm(mode, label);
    return;
  }
  PermWarning.show().then((confirmed) => {
    if (!confirmed) {
      _doSetPerm(currentMode, currentPermLabel, false);
      return;
    }
    _doSetPerm(mode, label);
    toast('已切换到“完全访问”，高风险操作仍会请你批准', 'warning', 5000);
  });
}

$('#perm-list').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-perm]');
  if (!btn) return;
  setPerm(btn.dataset.perm, btn.dataset.permLabel);
});

$('#task-seg').addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-task]');
  if (!btn) return;
  currentTask = btn.dataset.task;
  document.querySelectorAll('#task-seg button').forEach((b) =>
    b.setAttribute('aria-pressed', String(b === btn)));
  // 对话模式与工作区无关：隐藏工作区芯片以聚焦（执行方式那颗按钮会自己显示成「仅对话」）
  $('#ws-chip').hidden = currentTask === 'chat';
  goalInput.placeholder = composerPlaceholder();
});

// 输入框占位：会话里统一写「继续当前会话…」，首页按任务档位给例子
// 「这台机器现在跑不了」：没配模型，或者配了模型但没填密钥。由 paintModelChip 维护，
// 输入框的占位文案跟着它走——摆一个能用的样子出来，比说清楚更糟。
let llmUnusable = false;

function composerPlaceholder() {
  if (llmUnusable) return '还没有可用的模型：先去「设置 → 模型」选厂商并填入密钥';
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
  // 发送键由「设置 → 快捷键 → 发送消息」决定：Enter（默认）或 Ctrl+Enter。
  // Ctrl+Enter 模式下，单按 Enter 交还给 textarea 换行。
  if (e.key !== 'Enter' || e.shiftKey) return;
  const ctrlMode = typeof Keymap !== 'undefined' && Keymap.sendMode() === 'ctrlEnter';
  if (ctrlMode && !(e.ctrlKey || e.metaKey)) return;
  e.preventDefault(); submitGoal();
});
// 输入框自适应高度
// 手动拖出来的输入区高度：0 = 跟随内容。设了之后它是下限，内容更长时继续长。
// 初值必须延到脚本末尾再读：UIPrefs 定义在文件后半，这里直接读会撞 TDZ。
// 文本框是 flex 项（flex: 1 1 0%），height 压不过 flex 布局——要改高度得同时摘掉 flex。
let composerH = 0;
function applyComposerH() {
  if (composerH > 0) {
    goalInput.style.flex = 'none';
    // 样式表里那条 max-height: 200px 是给「跟随内容」用的，手动拖的时候得撤掉，
    // 否则拖到 200 就不动了，看着像坏了
    goalInput.style.maxHeight = 'none';
    goalInput.style.height = composerH + 'px';
    goalInput.style.overflowY = 'auto';
  } else {
    goalInput.style.flex = '';
    goalInput.style.maxHeight = '';
    goalInput.style.height = '';
    goalInput.style.overflowY = '';
  }
}
function autoResize() {
  if (composerH > 0) return; // 手动定过高度就交给用户，不再跟内容走
  goalInput.style.height = 'auto';
  goalInput.style.height = Math.min(goalInput.scrollHeight, 200) + 'px';
}
goalInput.addEventListener('input', autoResize);

// 输入框上沿那枚把手：按住上下拖改高度，双击回到跟随内容
(() => {
  const grip = $('#composer-grip');
  if (!grip) return;
  let drag = null;
  grip.addEventListener('pointerdown', (e) => {
    drag = { y: e.clientY, h: goalInput.getBoundingClientRect().height };
    grip.setPointerCapture(e.pointerId);
    grip.classList.add('is-dragging');
    e.preventDefault();
  });
  grip.addEventListener('pointermove', (e) => {
    if (!drag) return;
    const max = Math.round(window.innerHeight * 0.6);
    composerH = Math.round(Math.max(56, Math.min(drag.h + (drag.y - e.clientY), max)));
    applyComposerH();
  });
  const endDrag = (e) => {
    if (!drag) return;
    drag = null;
    grip.classList.remove('is-dragging');
    try { grip.releasePointerCapture(e.pointerId); } catch { /* 已经释放 */ }
    UIPrefs.set({ composerH });
  };
  grip.addEventListener('pointerup', endDrag);
  grip.addEventListener('pointercancel', endDrag);
  grip.addEventListener('dblclick', () => { composerH = 0; UIPrefs.set({ composerH: 0 }); applyComposerH(); autoResize(); });
})();

setTimeout(() => { composerH = Number(UIPrefs.get().composerH) || 0; applyComposerH(); autoResize(); }, 0);
$('#goal-submit').addEventListener('click', submitGoal);

/* 输入框右键菜单：跟着鼠标出现（系统原生菜单的手感）。 */
const goalCtxMenu = $('#goal-ctx-menu');
function showGoalCtxMenu(x, y) {
  if (!goalCtxMenu) return;
  goalCtxMenu.hidden = false; // 先显示才能量到真实尺寸
  const w = goalCtxMenu.offsetWidth, h = goalCtxMenu.offsetHeight;
  // 贴到窗口边缘时往回缩一档，别让菜单被边缘裁掉
  const left = Math.max(8, Math.min(x, window.innerWidth - w - 8));
  const top = Math.max(8, Math.min(y, window.innerHeight - h - 8));
  goalCtxMenu.style.left = left + 'px';
  goalCtxMenu.style.top = top + 'px';
}
function hideGoalCtxMenu() { if (goalCtxMenu) goalCtxMenu.hidden = true; }
Popovers.register('ctx', hideGoalCtxMenu);
goalInput.addEventListener('contextmenu', (e) => {
  e.preventDefault();
  Popovers.closeOthers('ctx');
  showGoalCtxMenu(e.clientX, e.clientY);
});
document.addEventListener('click', (e) => {
  if (!goalCtxMenu || goalCtxMenu.hidden) return;
  if (!e.target.closest('#goal-ctx-menu')) hideGoalCtxMenu();
});
if (goalCtxMenu) {
  goalCtxMenu.addEventListener('click', (e) => {
    const item = e.target.closest('[data-ctx]');
    if (!item) return;
    const action = item.dataset.ctx;
    if (window.gleamDesktop && window.gleamDesktop.edit) {
      window.gleamDesktop.edit(action).catch(() => {});
    } else {
      try { document.execCommand(action); } catch { /* ignore */ }
    }
    hideGoalCtxMenu();
  });
}

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
    const goalText = siteMode && !/index\.html/i.test(displayGoal) ? displayGoal + SITE_SUFFIX : displayGoal;
    const submitted = await api('POST', '/api/goals', {
      goal: goalText,
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
  if (show) {
    Popovers.closeOthers('plus'); // 先收别的，再开这个
    anchorPopover(plusMenu, plusBtn);
  } else {
    plusMenu.hidden = true;
  }
  plusBtn.setAttribute('aria-expanded', String(show));
}
Popovers.register('plus', () => togglePlus(false));
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
  else if (act === 'site') { setSiteMode(!siteMode); goalInput.focus(); }
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
  refsBox.hidden = refs.length === 0 && !siteMode;
  refsBox.innerHTML = '';
  if (siteMode) refsBox.appendChild(siteChip());
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

function clearRefs() { refs.length = 0; siteMode = false; paintSiteSub(); renderRefs(); }

/* ---------- 站点模式：输入框里的「站点」芯片 ----------
 * 它不是后端意义上的引用（引用类型白名单里没有站点），而是一句写进目标的产出约定：
 * 网页放在当前工作区 sites/ 下、入口 index.html。「站点」页扫的正是这个约定，
 * 所以芯片和页面是同一条闭环，而不是一个只在界面上亮一下的标签。 */
let siteMode = false;
const SITE_SUFFIX = '\n\n（站点：产出放在当前工作区 sites/ 下的一个新文件夹里，用简短的英文文件夹名，入口是 index.html；HTML 和 CSS 写在同一个文件里，不依赖外部资源，用浏览器直接打开就能看。）';
function siteChip() {
  const chip = el('span', 'ref-chip ref-chip--site');
  chip.innerHTML = '<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18"/></svg><span class="ref-label">站点</span>';
  chip.title = '做出来的网页会放进工作区 sites/，并出现在「站点」页';
  const x = el('button', 'ref-x');
  x.type = 'button';
  x.setAttribute('aria-label', '移除站点');
  x.innerHTML = '<svg viewBox="0 0 24 24" width="10" height="10" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12"/></svg>';
  x.addEventListener('click', () => setSiteMode(false));
  chip.appendChild(x);
  return chip;
}
function setSiteMode(on) {
  const was = siteMode;
  siteMode = !!on;
  // 对话档只聊天、不动文件；要做网页至少得是「通用」档
  if (siteMode && currentTask === 'chat') document.querySelector('#task-seg button[data-task="work"]')?.click();
  paintSiteSub();
  renderRefs();
  // 站点模板只在站点模式下出现；切换时即时显隐
  const tpl = document.querySelector('#goals-empty .site-tpl');
  if (tpl) {
    if (siteMode) { if (!tpl.innerHTML.trim()) SiteTemplates.paint(tpl); else tpl.hidden = false; }
    else tpl.hidden = true;
  }
  if (siteMode && !was) goalInput.focus();
}
function paintSiteSub() {
  const sub = document.getElementById('plus-site-sub');
  if (sub) sub.textContent = siteMode ? '已开启 · 再点一次取消' : '做一个能直接打开的网页';
}

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
async function addFolderRef(absPath) {
  let label = absPath;
  try {
    const ws = await api('GET', '/api/workspace');
    if (ws.workspace && absPath.startsWith(ws.workspace)) {
      const rel = absPath.slice(ws.workspace.length).replace(/^[\/\\]+/, '');
      if (rel) label = rel;
    }
  } catch { /* 用绝对路径兜底 */ }
  addRef('folder', label, '@folder:' + absPath);
  toast('已引用文件夹：' + label, 'success', 2500);
}

/* 系统文件/文件夹选择器：桌面端走 Electron dialog，浏览器兜底用隐藏的 <input> */
function pickSystemFile() {
  if (window.gleamDesktop && window.gleamDesktop.showOpenDialog) {
    window.gleamDesktop.showOpenDialog({ properties: ['openFile'] }).then((paths) => {
      if (paths && paths[0]) addFileRef(paths[0]);
    }).catch((err) => toast('选择文件失败：' + err.message, 'error'));
    return;
  }
  // 浏览器兜底：无法拿到完整路径，但至少能引用文件名
  const input = document.createElement('input');
  input.type = 'file';
  input.style.display = 'none';
  input.addEventListener('change', () => {
    const file = input.files?.[0];
    if (file) { addRef('file', file.name, '@file:' + file.name); toast('已引用文件：' + file.name, 'success', 2500); }
    input.remove();
  });
  document.body.appendChild(input);
  input.click();
}
function pickSystemFolder() {
  if (window.gleamDesktop && window.gleamDesktop.showOpenDialog) {
    window.gleamDesktop.showOpenDialog({ properties: ['openDirectory'] }).then((paths) => {
      if (paths && paths[0]) addFolderRef(paths[0]);
    }).catch((err) => toast('选择文件夹失败：' + err.message, 'error'));
    return;
  }
  // 浏览器兜底：webkitdirectory 拿不到完整路径
  const input = document.createElement('input');
  input.type = 'file';
  input.style.display = 'none';
  input.webkitdirectory = true;
  input.addEventListener('change', () => {
    const file = input.files?.[0];
    if (file) { addRef('folder', file.name, '@folder:' + file.name); toast('已引用文件夹：' + file.name, 'success', 2500); }
    input.remove();
  });
  document.body.appendChild(input);
  input.click();
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
  if (approvalBadge) { approvalBadge.hidden = pendingApprovals === 0; approvalBadge.textContent = pendingApprovals; }
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
  // 底部终端发起的 shell.exec：审批就地画在终端里，不跳去目标视图
  if (typeof Terminal !== 'undefined' && Terminal.claimApproval(ap)) return;
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
  if (badge) { badge.hidden = pendingApprovals === 0; badge.textContent = pendingApprovals; }
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

/* ---------- 上下文窗口：预设下拉 + 自定义 ----------
 * 预设覆盖常见档（128K / 200K / 400K / 1M），另留「自动」交给内置表认（见 llm.ContextWindowFor）。
 * 认不出来的旧值（手填过 32000 这类）**原样保留成"自定义"**：一个下拉里没有这个值时，
 * 静默把它显示成第一项、保存时又写回去，就等于替用户改了他填过的数。
 * 这也是「占用百分比的分母」——分母错了，界面上那个百分比就是假的。 */
const CTX_WINDOW_PRESETS = ['0', '128000', '200000', '400000', '1000000'];
function syncCtxWindowCustom() {
  const sel = $('#set-context-window'), wrap = $('#set-context-window-custom-wrap');
  if (!sel || !wrap) return;
  wrap.hidden = sel.value !== 'custom';
}
function paintCtxWindow(value) {
  const sel = $('#set-context-window');
  if (!sel) return;
  const v = String(value || 0);
  if (CTX_WINDOW_PRESETS.includes(v)) {
    sel.value = v;
  } else {
    sel.value = 'custom';
    if ($('#set-context-window-custom')) $('#set-context-window-custom').value = v;
  }
  syncCtxWindowCustom();
}
function ctxWindowValue() {
  const sel = $('#set-context-window');
  if (!sel) return undefined;
  return sel.value === 'custom' ? numValue('set-context-window-custom') : Number(sel.value);
}
if ($('#set-context-window')) $('#set-context-window').addEventListener('change', syncCtxWindowCustom);

function fillSettingsFields(s) {
  $('#set-name').value = s.persona?.name || 'Gleam';
  setSegValue('#set-style', s.persona?.style || 'efficient');
  setSegValue('#set-safety-mode', s.safety?.mode || 'auto');
  _doSetPerm(s.safety?.mode || 'auto', s.safety?.mode || 'auto', false);
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
    if ($('#set-context-window')) paintCtxWindow(s.llm.context_window || 0);
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
  }, { label: '安全设置', numeric: ['set-approval-timeout'], onSuccess: () => _doSetPerm(mode, mode, false) });
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
  // 选了「自定义」却没填数值：拦住。否则这个键在 JSON 里整个消失，后端只收到其余字段、
  // 回 200，提示"已保存"，而窗口大小根本没变（同 saveModule 里 numeric 空值那道判断的理由）。
  if ($('#set-context-window') && $('#set-context-window').value === 'custom'
      && !($('#set-context-window-custom').value || '').trim()) {
    toast('上下文窗口：选了「自定义」，但还没填数值', 'error', 6000);
    $('#set-context-window-custom').focus();
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
      // 0 = 不填，交给后端按内置表认（认不出用兜底值）。别把 0 当成"窗口是 0"。
      context_window: ctxWindowValue(),
    },
  };
  const key = $('#set-api-key').value.trim();
  if (key) patch.llm.api_key = key;
  await saveModule('set-save-llm', patch, {
    label: '模型设置',
    numeric: ['set-temperature', 'set-max-tokens', 'set-llm-timeout', 'set-context-window-custom'],
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

  // 多模型列表（/api/models）+ 运行时状态。modelsList 是用户配置的所有模型，
  // liveModel 是当前真正在跑的模型 ID（/api/info），llmCfg 保留向后兼容（mock 检测等）。
  let modelsList = [];   // GET /api/models 返回的 ModelEntry[]
  let llmCfg = null;     // /api/settings 的 llm 段（mock 检测、旧字段兼容）
  let fetched = [];      // 最近一次「拉取厂商模型」的结果
  let liveModel = '';    // /api/info 的 model：当前**真的在跑**的模型
  let lastCtx = null;    // 最近一次 /api/context，供弹层重开时立刻画

  const TIER_CN = { economy: '经济', coding: '编程', office: '办公', reasoning: '推理' };
  const PLAN_CN = { token: '按量', coding: '编程套餐', agent: '智能体套餐' };

  /* ---------- 弹层开合：同一时刻只开一个 ---------- */
  function setOpen(which) {
    [[modelBtn, modelPop, 'model'], [ctxBtn, ctxPop, 'context']].forEach(([btn, pop, id]) => {
      const show = btn === which;
      if (show) {
        Popovers.closeOthers(id); // 连同输入框里的其它弹层一起收
        anchorPopover(pop, btn);
      } else {
        pop.hidden = true;
      }
      btn.setAttribute('aria-expanded', String(show));
    });
  }
  const closeAll = () => setOpen(null);
  Popovers.register('model', closeAll);
  Popovers.register('context', closeAll);
  document.addEventListener('click', closeAll);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeAll(); });
  [modelPop, ctxPop].forEach((pop) => pop.addEventListener('click', (e) => e.stopPropagation()));
  modelBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const show = modelPop.hidden;
    closeAll();
    if (show) {
      setOpen(modelBtn);
      // 打开时拉最新模型列表，画完再贴一次定位
      loadModels().then(() => { paintModelList(); anchorPopover(modelPop, modelBtn); });
      paintModelList();
      anchorPopover(modelPop, modelBtn);
    }
  });
  ctxBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const show = ctxPop.hidden;
    closeAll();
    if (show) { setOpen(ctxBtn); paintContextPanel(lastCtx); anchorPopover(ctxPop, ctxBtn); loadContext(); }
  });

  /* ---------- 模型 ---------- */

  // 从 /api/models 拉取完整列表，首次调用后缓存。
  async function loadModels() {
    try {
      const r = await api('GET', '/api/models');
      modelsList = (r && Array.isArray(r.models)) ? r.models : [];
    } catch { modelsList = []; }
    return modelsList;
  }

  // 当前激活的模型条目：优先 liveModel（真正在跑的），其次标记为默认的条目。
  function activeEntry() {
    const id = liveModel || '';
    if (id) {
      const m = modelsList.find((x) => x.model === id || x.id === id);
      if (m) return m;
    }
    return modelsList.find((x) => x.is_default) || modelsList[0] || null;
  }

  function paintModelChip() {
    const entry = activeEntry();
    const m = entry ? (entry.model || entry.id) : '';
    const isMock = (llmCfg && llmCfg.provider === 'mock') || PROVIDER === 'mock';
    const noKey = !isMock && entry && entry.api_key_set === false;
    const unusable = !isMock && (!m || noKey);
    if (llmUnusable !== unusable) {
      llmUnusable = unusable;
      goalInput.placeholder = composerPlaceholder();
    }
    modelName.textContent = isMock ? '演示模型' : (unusable ? '未配置模型' : shorten(m, 18));
    modelName.title = isMock ? '离线演示模型：不发起真实网络调用'
      : unusable ? '还没有可用的模型：去「设置 → 模型」选一个厂商并填入密钥'
        : (noKey ? m + ' · 没填密钥，现在跑不了' : m);
    const tagEl = $('#cp-model-tag');
    if (tagEl) {
      let tag = '';
      if (isMock) tag = 'mock';
      else if (entry && entry.is_default) tag = '默认';
      else if (entry && entry.is_fast) tag = '辅助';
      tagEl.textContent = tag;
      tagEl.hidden = !tag;
    }
    modelCur.textContent = m || '未配置';
    const bits = [];
    if (entry) {
      const pv = PROVIDERS.find((x) => x.id === entry.provider_id);
      bits.push(pv ? pv.name : (entry.provider_id || '自定义'));
    }
    if (entry && entry.plan) bits.push(PLAN_CN[entry.plan] || entry.plan);
    if (entry && entry.api_key_set === false) bits.push('无密钥');
    modelProv.textContent = bits.join(' · ');
    modelBtn.dataset.tone = unusable ? 'warn' : '';
  }

  function paintModelList() {
    modelList.replaceChildren();
    if (!modelsList.length) {
      modelList.appendChild(el('div', 'cp-pop-note', '本机还没有已配置的模型。去设置里添加一个，或拉取厂商模型列表。'));
      return;
    }
    const active = activeEntry();
    const activeId = active ? active.id : '';
    modelsList.forEach((entry) => {
      const b = el('button', 'plus-item');
      b.type = 'button';
      const isCur = entry.id === activeId;
      if (isCur) b.setAttribute('aria-current', 'true');
      const pv = PROVIDERS.find((x) => x.id === entry.provider_id);
      const vendorName = pv ? pv.name : (entry.provider_id || '自定义');
      b.appendChild(el('span', null, entry.model || entry.id));
      const subParts = [vendorName];
      if (entry.is_default) subParts.push('默认');
      if (entry.is_fast) subParts.push('辅助');
      if (entry.api_key_set === false) subParts.push('无密钥');
      b.appendChild(el('span', 'plus-sub', isCur ? '使用中' : subParts.join(' · ')));
      b.disabled = isCur;
      b.addEventListener('click', () => switchModel(entry.id));
      modelList.appendChild(b);
    });
  }

  async function switchModel(id) {
    closeAll();
    const prev = liveModel || (activeEntry() ? activeEntry().model : '');
    const entry = modelsList.find((x) => x.id === id);
    if (entry) liveModel = entry.model || id;
    paintModelChip();
    paintModelList();
    try {
      const r = await api('POST', `/api/models/${encodeURIComponent(id)}/activate`);
      const active = r && r.active;
      if (active) {
        liveModel = active.model || id;
        // 同步更新 llmCfg 的关键字段，让 PROVIDER / API_KEY_SET 等全局量跟上
        if (llmCfg) {
          llmCfg.provider = active.provider_id || llmCfg.provider;
          llmCfg.model = active.model || llmCfg.model;
          llmCfg.api_key_set = active.api_key_set;
        }
        PROVIDER = active.provider_id || PROVIDER;
        API_KEY_SET = !!active.api_key_set;
      }
      toast(`已切到 ${liveModel}，下一个任务开始用它`, 'success');
    } catch (err) {
      liveModel = prev;
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

  // 窗口大小按 k 说人话：128000 → 128k。读数里没人愿意数后面的零。
  const kTok = (n) => (n >= 1000 ? Math.round(n / 1000) + 'k' : String(n || 0));

  // 分母是怎么来的，界面必须标出来：同一个百分比，"按你手填的数算的"和"按兜底值算的"
  // 不是一个可信度。这句话由后端给（window_note），前端只在没有时兜一句。
  const windowSourceLabel = { manual: '你手填的', table: '内置表', default: '兜底值' };

  function paintMeter(ctx) {
    if (!ctx || typeof ctx.window_pct !== 'number') {
      // 字段缺失就是接线断了。宁可空着，也不在前端拿别的东西自己除一个凑数。
      pctEl.textContent = '—';
      ctxBtn.dataset.ready = 'false';
      meterFill.style.width = '0%';
      delete meterFill.dataset.level;
      badge.hidden = true;
      ctxBtn.title = '上下文窗口读数不可用';
      return;
    }
    // 读的是**模型窗口占用**：最近一次请求的输入 token ÷ 模型的上下文窗口。
    const pct = ctx.window_pct;
    ctxBtn.dataset.ready = 'true';
    meterFill.style.width = pct + '%';
    meterFill.dataset.level = pct >= 90 ? 'high' : pct >= 60 ? 'warn' : 'fresh';
    pctEl.textContent = pct + '%';
    badge.hidden = !ctx.overflow;
    badge.textContent = String(ctx.overflow || 0);
    badge.title = ctx.overflow ? `待压缩 ${ctx.overflow} 轮` : '';
    ctxBtn.dataset.full = pct >= 100 ? 'true' : 'false';
    const src = windowSourceLabel[ctx.window_source] || '来源未知';
    ctxBtn.title = (ctx.enabled ? '' : '自动压缩已关闭 · ')
      + `上下文窗口占用 ${pct}%（模型窗口 ${kTok(ctx.window_tokens)}，${src}；`
      + `最近一次请求 ${ctx.prompt_tokens} tokens${ctx.prompt_estimated ? '，估算' : ''}）`;
  }

  function paintContextPanel(ctx) {
    if (!ctx) return;
    if (ctx.enabled) delete ctxRead.dataset.off;
    else ctxRead.dataset.off = 'true';
    const saved = ctx.est_tokens_saved > 0 ? ` · 累计已省约 ${ctx.est_tokens_saved} tokens` : '';
    const bar = $('#cp-context-bar');
    const pct = ctx.window_pct || 0;
    if (bar) { bar.style.width = Math.max(0, Math.min(100, pct)) + '%'; bar.dataset.level = pct >= 90 ? 'high' : pct >= 60 ? 'warn' : 'fresh'; }
    const src = windowSourceLabel[ctx.window_source] || '来源未知';
    // 两套口径一起说：轮数回答"对话攒了多少"，窗口回答"下一轮还能塞多少"。
    ctxRead.textContent = ctx.enabled
      ? `占用 ${pct}% · 模型窗口 ${kTok(ctx.window_tokens)}（${src}，${ctx.window_note || ''}）`
        + ` · 最近一次请求 ${ctx.prompt_tokens} tokens${ctx.prompt_estimated ? '（估算）' : ''}`
        + ` · 下一轮带 ${ctx.carry_turns} 轮最近对话`
        + ` · 短期窗口 ${ctx.short_turns}/${ctx.short_cap} 轮，待压缩 ${ctx.overflow} 轮${saved}`
      : `自动压缩已关闭：短期窗口 ${ctx.short_turns}/${ctx.short_cap} 轮，窗口外的对话不会被摘要接住，水位越线时也不会自动收紧`;
    // 阈值由后端给（compress_end_pct / compress_mid_pct），前端不另写一份数字。
    const note = $('#cp-context-note');
    if (note && typeof ctx.compress_end_pct === 'number') {
      note.textContent = '占用是「最近一次请求的输入 ÷ 模型窗口」。溢出窗口的旧对话随时汇总成摘要，不会丢；'
        + `占用到 ${ctx.compress_end_pct}%（任务已结束）或 ${ctx.compress_mid_pct}%（任务进行中）时会自动收紧下一轮携带的对话，压完接着把任务做完。`;
    }
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
      // 设置变了，模型列表可能也变了——静默刷新，等列表到了再画
      loadModels().then(() => { paintModelChip(); if (!modelPop.hidden) paintModelList(); });
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
      // 如果模型列表还没加载，先拉一次再画
      if (modelsList.length === 0) {
        loadModels().then(() => { paintModelChip(); if (!modelPop.hidden) paintModelList(); });
      } else {
        paintModelChip();
        if (!modelPop.hidden) paintModelList();
      }
    },
  };
})();

/* ---------- 心跳：页面打开期间每 5 秒上报存活（桌面端 app 模式据此判断窗口是否关闭） ---------- */
setInterval(() => { fetch('/api/heartbeat', { method: 'POST' }).catch(() => {}); }, 5000);


/* ---------- Go 工具链检测 ---------- */
// 来源口径归前端一处：后端给的是机器码（PATH / gleam-managed …），直接印出来像日志。
const GO_SOURCE_LABEL = {
  'PATH': '系统 PATH',
  'common-path': '常见安装目录',
  'GOROOT': 'GOROOT 环境变量',
  'user-specified': '你指定的路径',
  'gleam-managed': 'Gleam 自带（tools/go）',
};
function goSourceText(code) {
  if (!code) return '系统 PATH';
  return GO_SOURCE_LABEL[code] || code;
}

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
      '<div class="row-sub">来源: ' + esc(goSourceText(st.source)) + ' · ' + esc(st.root || st.bin_dir || '') + '</div></div></div>';
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
      'Gleam 会依次尝试国内镜像拉取 Go 1.23.4（约 80MB），校验官方 sha256 后解压到 ' +
      'Gleam 自己的 <code class="inline-code">tools/go</code> 目录，不动机器全局 PATH。</p>' +
      '<p class="field-hint" style="margin: 0 0 var(--space-3);">也可以手动装好 Go 后，在下方填写路径检测。</p>';
    box.appendChild(wrap);
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const install = el('button', 'btn btn-primary', ICONS.zap + ' 自动下载安装');
    install.type = 'button';
    install.addEventListener('click', async () => {
      install.innerHTML = ICONS.spinner + ' 下载并解压中…';
      install.disabled = true;
      try {
        const res = await api('POST', '/api/go-status/install', {});
        if (res.found) {
          renderGoStatus(res);
          toast('Go 已就绪：' + (res.version || ''), 'success', 6000);
          Modal.close();
        } else if (res.message) {
          // 自动下载失败：先把后端给的原因原样说出来，再给手动那条路。
          wrap.innerHTML = '<div class="callout callout--warn" style="margin: 0 0 var(--space-3);">' + ICONS.alert +
            '<div><div class="row-title">自动安装没成功</div>' +
            '<div class="row-sub">' + esc(res.message) + '</div></div></div>' +
            '<ol style="margin: 0 0 var(--space-3) var(--space-4); padding-left: var(--space-4); line-height: 2;">' +
            '<li>检查网络或代理后点「重试」；也可以换一个镜像源再试。</li>' +
            '<li>或跑安装脚本：<br><code class="inline-code">powershell -ExecutionPolicy Bypass -File scripts/install.ps1</code></li>' +
            '<li>或手动下载 Go：<br><a href="' + esc(res.download || 'https://go.dev/dl/') + '" target="_blank" style="color:var(--color-accent);">' + esc(res.download || 'https://go.dev/dl/') + '</a></li>' +
            '<li>装好后在下方填路径点「检测」</li>' +
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


/* ---------- 工作区（任务文件夹） ---------- */
function wsShort(path) {
  if (!path) return '不指定工作区';
  // Windows 用反斜杠、Unix 用正斜杠：两种分隔符都要认，否则整条绝对路径会原样怼到界面上
  const parts = String(path).replace(/[\\/]+$/, '').split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

// 当前连接的 SSH 主机别名。引擎仍在这台电脑上执行，它只是「这次任务对着哪台机器」的界面标记：
// 选了 SSH 就不再显示本地文件夹，免得两个来源同时亮着，说不清文件操作到底落在哪边。
let activeSSH = '';
try { activeSSH = localStorage.getItem('gleam.workspace.ssh') || ''; } catch { /* 隐私模式 */ }
function setActiveSSH(host) {
  activeSSH = host || '';
  try { localStorage.setItem('gleam.workspace.ssh', activeSSH); } catch { /* 隐私模式 */ }
}

async function loadWorkspace() {
  try {
    const ws = await api('GET', '/api/workspace');
    renderWorkspace(ws);
  } catch { /* 静默 */ }
}

let lastWsView = { workspace: '', recents: [], git_branch: '' };
function renderWorkspace(ws) {
  if (ws) lastWsView = ws;
  const path = (ws && ws.workspace) || '';
  const branch = (ws && ws.git_branch) || '';
  const label = activeSSH ? 'SSH · ' + activeSSH : (path ? '工作区 · ' + wsShort(path) : '不指定工作区');
  const text = $('#ws-chip-text');
  if (text) { text.textContent = label; text.title = activeSSH || path || ''; }
  const foot = $('#composer-ws-text');
  if (foot) { foot.textContent = label; foot.title = activeSSH || path || ''; }
  const chipBtn = $('#composer-ws');
  if (chipBtn) chipBtn.classList.toggle('composer-foot--active', !!(path || activeSSH));
  const branchWrap = $('#composer-ws-branch');
  const branchText = $('#composer-ws-branch-text');
  if (branchWrap && branchText) {
    branchWrap.hidden = !branch || !!activeSSH; // SSH 标记优先，不再显示本地分支
    branchText.textContent = branch;
  }
}

// 选择工作区：把新路径交给引擎，清掉 SSH 标记，再让界面重建。
async function chooseWorkspace(path) {
  try {
    const ws = await api('POST', '/api/workspace', { path });
    setActiveSSH('');
    renderWorkspace(ws);
    toast(`工作区已切换：${wsShort(ws.workspace)}`, 'success');
  } catch (err) { toast(err.message, 'error'); }
}

// 不指定工作区：引擎不再绑定根目录，最近列表保留。
async function clearWorkspace() {
  try {
    const ws = await api('POST', '/api/workspace/clear');
    setActiveSSH('');
    renderWorkspace(ws);
    toast('已改为不指定工作区', 'success');
  } catch (err) { toast(err.message, 'error'); }
}

/* ---------- 工作区下拉菜单：点左下角那颗按钮弹出来 ---------- */
function closeWorkspaceMenu() {
  const m = document.getElementById('ws-menu');
  if (m) m.remove();
  const btn = $('#composer-ws');
  if (btn) btn.setAttribute('aria-expanded', 'false');
  document.removeEventListener('mousedown', onWsMenuOutside, true);
  document.removeEventListener('keydown', onWsMenuKey, true);
}
function onWsMenuOutside(e) {
  const m = document.getElementById('ws-menu');
  if (m && !m.contains(e.target) && !e.target.closest('#composer-ws')) closeWorkspaceMenu();
}
function onWsMenuKey(e) { if (e.key === 'Escape') { e.preventDefault(); closeWorkspaceMenu(); } }
Popovers.register('ws', closeWorkspaceMenu);

async function openWorkspaceMenu() {
  const btn = $('#composer-ws');
  if (!btn) return;
  if (document.getElementById('ws-menu')) { closeWorkspaceMenu(); return; } // 再点一次收起

  let ws = { workspace: '', recents: [], git_branch: '' };
  try { ws = await api('GET', '/api/workspace'); } catch { /* 离线时仍给出新建 / SSH 入口 */ }

  const menu = el('div', 'ws-menu');
  menu.id = 'ws-menu';
  menu.setAttribute('role', 'menu');

  const cur = activeSSH ? 'SSH · ' + activeSSH : (ws.workspace ? wsShort(ws.workspace) : '不指定工作区');
  const head = el('div', 'ws-menu-head');
  head.innerHTML = `<div class="ws-menu-label">当前工作区</div><div class="ws-menu-current">${esc(cur)}</div>`;
  menu.appendChild(head);

  const recents = (ws.recents || []).filter((r) => r !== ws.workspace);
  if (recents.length) {
    menu.appendChild(el('div', 'ws-menu-label ws-menu-label--sep', '历史'));
    recents.forEach((r) => {
      const row = el('button', 'ws-menu-item');
      row.type = 'button';
      row.setAttribute('role', 'menuitem');
      row.innerHTML = `<span class="ws-menu-ico">${WS_ICON.folder}</span>` +
        `<span class="ws-menu-main"><span class="ws-menu-name">${esc(wsShort(r))}</span>` +
        `<span class="ws-menu-sub">${esc(r)}</span></span>`;
      row.addEventListener('click', () => { closeWorkspaceMenu(); chooseWorkspace(r); });
      menu.appendChild(row);
    });
  }

  const mkAction = (icon, name, sub, fn) => {
    const b = el('button', 'ws-menu-item ws-menu-item--action');
    b.type = 'button';
    b.setAttribute('role', 'menuitem');
    b.innerHTML = `<span class="ws-menu-ico">${icon}</span>` +
      `<span class="ws-menu-main"><span class="ws-menu-name">${esc(name)}</span>` +
      (sub ? `<span class="ws-menu-sub">${esc(sub)}</span>` : '') + '</span>';
    b.addEventListener('click', fn);
    return b;
  };

  menu.appendChild(el('div', 'ws-menu-sep'));
  menu.appendChild(mkAction(WS_ICON.folderAdd, '新建工作区…', '选一个文件夹作为工作区', () => {
    closeWorkspaceMenu();
    openNewWorkspaceDialog();
  }));
  menu.appendChild(mkAction(WS_ICON.plug, '连接 SSH…', '自动检测本机与设置里的主机', () => {
    closeWorkspaceMenu();
    openSSHDialog();
  }));
  if (ws.workspace || activeSSH) {
    menu.appendChild(mkAction(WS_ICON.none, '不指定工作区', '文件操作不受工作区限制', () => {
      closeWorkspaceMenu();
      clearWorkspace();
    }));
  }

  document.body.appendChild(menu);
  btn.setAttribute('aria-expanded', 'true');
  Popovers.closeOthers('ws');
  anchorPopover(menu, btn, 6); // 贴在按钮正上方

  document.addEventListener('mousedown', onWsMenuOutside, true);
  document.addEventListener('keydown', onWsMenuKey, true);
}

const WS_ICON = {
  folder: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg>',
  folderAdd: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/><path d="M12 11v5M9.5 13.5h5"/></svg>',
  plug: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 3v6M15 3v6M6 9h12v3a6 6 0 0 1-6 6 6 6 0 0 1-6-6z"/><path d="M12 18v3"/></svg>',
  none: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="m8.5 8.5 7 7M15.5 8.5l-7 7"/></svg>',
  server: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="7" rx="1.6"/><rect x="3" y="13" width="18" height="7" rx="1.6"/><path d="M7 7.5h.01M7 16.5h.01"/></svg>',
};

// 新建工作区：桌面壳里走系统文件夹选择器；普通浏览器里没有系统选择器，退回内置浏览。
async function pickSystemFolder() {
  const d = window.gleamDesktop;
  if (d && typeof d.showOpenDialog === 'function') {
    let paths = [];
    try { paths = await d.showOpenDialog({ properties: ['openDirectory', 'createDirectory'], title: '选择工作区文件夹' }); }
    catch (err) { toast('打开系统选择器失败：' + err.message, 'error'); return; }
    const picked = Array.isArray(paths) ? paths[0] : paths;
    if (picked) chooseWorkspace(picked);
    return; // 用户取消：什么都不做
  }
  openWorkspaceBrowse();
}

/* ---------- 连接 SSH：独立窗口，合并本机 ssh_config 与设置里记下的主机 ---------- */
async function openSSHDialog() {
  const saved = new Set(UIPrefs.get().sshHosts || []);
  let local = [];
  let cfgPath = '';
  try {
    const r = await api('GET', '/api/ssh/hosts');
    local = r.hosts || [];
    cfgPath = r.path || '';
  } catch { /* 读不到配置文件不是失败：设置里记下的主机照样能选 */ }

  // 本机检测到的主机排在前面，设置里记下的补在后头，去重
  const hosts = [];
  local.forEach((h) => { if (!hosts.includes(h)) hosts.push(h); });
  saved.forEach((h) => { if (!hosts.includes(h)) hosts.push(h); });

  Modal.open('连接 SSH', (box) => {
    const wrap = el('div');
    wrap.innerHTML =
      '<p class="field-hint" style="margin:0 0 var(--space-3);">从本机 SSH 配置与「设置 → 连接」里挑选一台主机。Gleam 只读 Host 名称，不读取用户名、地址或任何密钥。</p>' +
      '<div class="ws-ssh-list" id="ws-ssh-list" role="listbox" aria-label="SSH 主机"></div>' +
      '<p class="field-hint" id="ws-ssh-note" style="margin:var(--space-3) 0 0;"></p>';
    box.appendChild(wrap);

    const list = $('#ws-ssh-list');
    const note = $('#ws-ssh-note');
    note.textContent = cfgPath ? `本机配置：${cfgPath}（只解析 Host 名称）` : '没有找到本机 SSH 配置文件。';

    if (!hosts.length) {
      list.innerHTML = '<div class="empty" style="padding:var(--space-4) 0;"><div class="empty-title">没有可用的主机</div>' +
        '<p class="empty-desc">在 ~/.ssh/config 里写好 Host，或到「设置 → 连接」添加 SSH 连接。</p></div>';
    } else {
      hosts.forEach((h) => {
        const row = el('button', 'ws-ssh-item');
        row.type = 'button';
        row.setAttribute('role', 'option');
        row.setAttribute('aria-selected', String(activeSSH === h));
        row.innerHTML = `<span class="ws-ssh-glyph">${esc((h[0] || '?').toUpperCase())}</span>` +
          `<span class="ws-ssh-name">${esc(h)}</span>` +
          (activeSSH === h ? '<span class="badge badge--mode">已连接</span>' : '');
        row.addEventListener('click', () => {
          setActiveSSH(h);
          Modal.close();
          renderWorkspace(lastWsView); // 保留本地工作区信息，只是让 SSH 标记盖过它
          toast(`已连接 SSH：${h}`, 'success');
        });
        list.appendChild(row);
      });
    }

    const actions = el('div', 'modal-actions');
    if (activeSSH) {
      const dc = el('button', 'btn btn-secondary', '断开连接');
      dc.type = 'button';
      dc.addEventListener('click', () => { setActiveSSH(''); Modal.close(); loadWorkspace(); toast('已断开 SSH', 'success'); });
      actions.appendChild(dc);
    }
    const cancel = el('button', 'btn btn-secondary', activeSSH ? '取消' : '关闭');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    actions.appendChild(cancel);
    box.appendChild(actions);
  });
}

/* ---------- 新建工作区：源文件夹 / 名称 / 图标 / 颜色 / 索引 ---------- */
// 图标与色板的备选清单（纯前端装饰，挑中的组合按路径记在本机）
const WS_ICON_MARKUP = [
  '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/>',
  '<path d="M4 8h13v8a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3z"/><path d="M17 10h1.5a2.5 2.5 0 0 1 0 5H17"/>',
  '<path d="M4 5h16l-6 7v6l-4 2v-8z"/>',
  '<path d="M3.5 8 12 4l8.5 4v8L12 20l-8.5-4z"/><path d="M3.5 8 12 12l8.5-4M12 12v8"/>',
  '<path d="M9 4 7 20M17 4l-2 16M4 9h16M3 15h16"/>',
  '<path d="M18 9a6 6 0 1 0-12 0c0 6-2 7-2 7h16s-2-1-2-7"/><path d="M10 20a2 2 0 0 0 4 0"/>',
  '<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="7" r="2"/><path d="M6 7v10M18 9c0 5-12 3-12 8"/>',
  '<path d="M12 20s-7-4.4-7-9a4 4 0 0 1 7-2.6A4 4 0 0 1 19 11c0 4.6-7 9-7 9z"/>',
  '<circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="3.4"/>',
  '<path d="M9.5 4h5v2.5h2.5V12h-2.5v2.5h-5V12H7V6.5h2.5z"/>',
  '<path d="M20 11.5a7.5 7.5 0 0 1-7.5 7.5H8l-4 3v-4.6A7.5 7.5 0 1 1 20 11.5z"/>',
  '<circle cx="12" cy="12" r="8.5"/><path d="M8.8 14.5a4 4 0 0 0 6.4 0"/><path d="M9.5 9.8h.01M14.5 9.8h.01"/>',
  '<path d="M12 3a9 9 0 1 0 0 18 1.7 1.7 0 0 0 1.3-2.8 1.7 1.7 0 0 1 1.3-2.8h1.8A5.6 5.6 0 0 0 22 9.8C22 6 17.5 3 12 3z"/>',
  '<path d="M9.5 14h5l.5 4H9zM10.5 14V9.5a1.5 1.5 0 0 1 3 0V14M12 4v1.5"/>',
  '<path d="M10 3v6l-4.5 8A2 2 0 0 0 7.2 20h9.6a2 2 0 0 0 1.7-3L14 9V3M9 3h6"/>',
  '<path d="m8 7-5 5 5 5M16 7l5 5-5 5"/>',
  '<path d="M4 5a2 2 0 0 1 2-2h9l5 5v11a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z"/><path d="M14 3v6h6"/>',
  '<path d="M9 18V6l10-2v12"/><circle cx="6.5" cy="18" r="2.5"/><circle cx="16.5" cy="16" r="2.5"/>',
  '<path d="M4 8.5A2.5 2.5 0 0 1 6.5 6h11A2.5 2.5 0 0 1 20 8.5v7a2.5 2.5 0 0 1-2.5 2.5h-11A2.5 2.5 0 0 1 4 15.5z"/><circle cx="12" cy="12" r="3"/>',
  '<path d="m12 3 2.6 5.6 6.1.8-4.5 4.2 1.2 6-5.4-3-5.4 3 1.2-6L3.3 9.4l6.1-.8z"/>',
];
const WS_COLORS = [
  '#2f6b4f', '#3aa46a', '#3b78d8', '#e0912f', '#e0524f', '#d5529b', '#7d5bd6', '#4a63c8',
  '#e0a52f', '#dd7a45', '#2fa79b', '#3b8fd8', '#6b7280', '#26292e', '#9a7fe0', '#4f9e6b',
];

// 图标 / 颜色 / 索引 / 自定义名称按工作区路径记在本机（localStorage）：引擎只认路径，
// 这几项是界面上的标识，不该为了它们去动引擎的配置模型。
function wsMetaStore() {
  try { return JSON.parse(localStorage.getItem('gleam.workspace.meta') || '{}') || {}; }
  catch { return {}; }
}
function wsMetaOf(path) { return wsMetaStore()[path] || {}; }
function saveWsMeta(path, patch) {
  try {
    const all = wsMetaStore();
    all[path] = Object.assign({}, all[path], patch);
    localStorage.setItem('gleam.workspace.meta', JSON.stringify(all));
  } catch { /* 隐私模式：记不住也不影响功能 */ }
}

function openNewWorkspaceDialog() {
  Modal.open('新建工作区', (box) => {
    let picked = '';
    let touchedName = false;
    let iconIdx = -1;
    let colorIdx = 0;

    const wrap = el('div', 'wsnew');
    wrap.innerHTML =
      '<div class="wsnew-field">' +
        '<div class="wsnew-label-row"><span class="wsnew-label">源文件夹</span>' +
        '<button type="button" class="wsnew-add" id="wsnew-add" hidden>+ 添加</button></div>' +
        '<div id="wsnew-src"></div>' +
      '</div>' +
      '<div class="wsnew-field">' +
        '<label class="wsnew-label" for="wsnew-name">工作区名称</label>' +
        '<input class="input wsnew-input" id="wsnew-name" type="text" placeholder="输入名称..." autocomplete="off">' +
      '</div>' +
      '<div class="wsnew-field">' +
        '<div class="wsnew-label">工作区图标</div>' +
        '<div class="wsnew-icons" id="wsnew-icons" role="radiogroup" aria-label="工作区图标"></div>' +
      '</div>' +
      '<div class="wsnew-field">' +
        '<div class="wsnew-label">工作区颜色</div>' +
        '<div class="wsnew-colors" id="wsnew-colors" role="radiogroup" aria-label="工作区颜色"></div>' +
      '</div>' +
      '<div class="wsnew-field wsnew-field--index">' +
        '<div class="wsnew-index">' +
          '<div class="wsnew-index-text">' +
            '<div class="wsnew-label">工作区索引</div>' +
            '<p class="wsnew-hint">建立工作区索引可增强上下文理解能力，从而提升智能体回复准确性。关于工作区索引数据处理方式，' +
            '<a href="#" class="wsnew-link" id="wsnew-more">了解详情</a>。</p>' +
          '</div>' +
          '<button type="button" class="wsnew-switch" id="wsnew-index" role="switch" aria-checked="true" aria-label="工作区索引"><span></span></button>' +
        '</div>' +
      '</div>';
    box.appendChild(wrap);

    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    const create = el('button', 'btn btn-primary', '创建');
    create.type = 'button';
    create.disabled = true;
    actions.appendChild(cancel);
    actions.appendChild(create);
    box.appendChild(actions);

    const srcBox = $('#wsnew-src');
    const addBtn = $('#wsnew-add');
    const nameInput = $('#wsnew-name');
    const idxBtn = $('#wsnew-index');

    // 图标
    const iconsBox = $('#wsnew-icons');
    WS_ICON_MARKUP.forEach((mk, i) => {
      const b = el('button', 'wsnew-icon');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.innerHTML = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" ' +
        'stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + mk + '</svg>';
      b.addEventListener('click', () => { iconIdx = i; paintIcons(); });
      iconsBox.appendChild(b);
    });
    function paintIcons() {
      [...iconsBox.children].forEach((b, i) => b.setAttribute('aria-checked', String(i === iconIdx)));
    }

    // 颜色
    const colorBox = $('#wsnew-colors');
    WS_COLORS.forEach((c, i) => {
      const b = el('button', 'wsnew-color');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.style.setProperty('--c', c);
      b.innerHTML = '<span></span>';
      b.addEventListener('click', () => { colorIdx = i; paintColors(); });
      colorBox.appendChild(b);
    });
    function paintColors() {
      [...colorBox.children].forEach((b, i) => b.setAttribute('aria-checked', String(i === colorIdx)));
    }

    idxBtn.addEventListener('click', () => {
      idxBtn.setAttribute('aria-checked', String(idxBtn.getAttribute('aria-checked') !== 'true'));
    });
    $('#wsnew-more').addEventListener('click', (e) => {
      e.preventDefault();
      toast('工作区索引还没上线：这里只先记住你的开关意向。', 'info');
    });

    // 源文件夹：空态是个虚线框，选中后变成带名字与「可读写」标记的一条
    function paintSrc() {
      if (!picked) {
        addBtn.hidden = true;
        srcBox.innerHTML = '<button type="button" class="wsnew-drop" id="wsnew-drop">' +
          '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.7" ' +
          'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/><path d="M12 11v5M9.5 13.5h5"/></svg>' +
          '<span>点击添加可读写文件夹</span></button>';
        srcBox.querySelector('#wsnew-drop').addEventListener('click', pickFolder);
      } else {
        addBtn.hidden = false;
        srcBox.innerHTML = '<div class="wsnew-chip">' +
          '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" ' +
          'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg>' +
          '<span class="wsnew-chip-name">' + esc(wsShort(picked)) + '</span>' +
          '<span class="wsnew-chip-badge">可读写</span>' +
          '<button type="button" class="wsnew-chip-x" id="wsnew-chip-x" aria-label="移除该文件夹">✕</button></div>';
        srcBox.querySelector('#wsnew-chip-x').addEventListener('click', () => { picked = ''; paintSrc(); });
      }
      create.disabled = !picked;
    }

    async function pickFolder() {
      const d = window.gleamDesktop;
      if (!d || typeof d.showOpenDialog !== 'function') {
        toast('这里没有系统文件夹选择器，请在桌面应用里新建工作区。', 'info');
        return;
      }
      let paths = [];
      try { paths = await d.showOpenDialog({ properties: ['openDirectory', 'createDirectory'], title: '选择工作区文件夹' }); }
      catch (err) { toast('打开系统选择器失败：' + err.message, 'error'); return; }
      const p = Array.isArray(paths) ? paths[0] : paths;
      if (!p) return; // 用户取消
      picked = p;
      if (!touchedName) nameInput.value = wsShort(p);
      const meta = wsMetaOf(p);
      if (typeof meta.icon === 'number') iconIdx = meta.icon;
      if (typeof meta.color === 'number') colorIdx = meta.color;
      if (typeof meta.index === 'boolean') idxBtn.setAttribute('aria-checked', String(meta.index));
      paintIcons();
      paintColors();
      paintSrc();
    }

    addBtn.addEventListener('click', pickFolder);
    nameInput.addEventListener('input', () => { touchedName = true; });
    cancel.addEventListener('click', () => Modal.settle(() => {}));
    create.addEventListener('click', () => {
      if (!picked) return;
      saveWsMeta(picked, {
        icon: iconIdx,
        color: colorIdx,
        index: idxBtn.getAttribute('aria-checked') === 'true',
        name: nameInput.value.trim(),
      });
      Modal.settle(() => {});
      chooseWorkspace(picked);
    });

    paintIcons();
    paintColors();
    paintSrc();
  });
}

$('#composer-ws').addEventListener('click', () => openWorkspaceMenu());
$('#ws-chip').addEventListener('click', () => openWorkspaceMenu());
// 仍然给「需要选工作区」的老入口（顶栏文件夹按钮、终端「选择工作区」）留一个名字
function openWorkspaceDialog() { openWorkspaceMenu(); }

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

// 首页 / 空任务的插图位：Gleam 软件内的线条标记（随主题强调色），与 index.html 里的静态版保持一致；
// 官方折角 logo 只用于应用图标、favicon 和官网
const HERO_ART = `<div class="hero-art" aria-hidden="true"><svg class="brand-mark" viewBox="0 0 96 96" width="96" height="96" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><rect x="18" y="18" width="60" height="60" rx="18" stroke-width="1.6" opacity=".35"/><g stroke-width="3"><circle cx="48" cy="48" r="7"/><path d="M48 30v6M48 60v6M30 48h6M60 48h6M35.3 35.3l4.2 4.2M56.5 56.5l4.2 4.2M35.3 60.7l4.2-4.2M56.5 39.5l4.2-4.2"/></g></svg></div>`;

function resetFeedToEmpty(title, desc, opts = {}) {
  const feed = $('#goal-feed');
  feed.innerHTML = '';
  const empty = el('div', opts.home ? 'empty home-hero' : 'empty home-hero home-hero--task');
  empty.id = 'goals-empty';
  empty.innerHTML = `${HERO_ART}<div class="empty-title">${esc(title)}</div><p class="empty-desc">${esc(desc)}</p>`
    + (opts.home ? '<section class="activity-card" aria-label="本机活动"></section>' : '')
    + '<section class="site-tpl" aria-label="站点模板" hidden></section>';
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
    // 不再立即创建会话：回到首页初始界面，等用户确认发送后由 ensureConvo() 按需创建，
    // 避免点一次「新任务」就生成一个空会话。
    currentConvo = null;
    convoLive.clear();
    showView('goals');
    goalInput.focus();
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
// （原先这里第一颗是「反馈这条」，指向一个早已删掉的视图——反馈功能整个撤掉了，
//  那颗按钮点下去只会抛异常，所以连同入口一起去掉。）
function appendMsgActs(bodyEl, m) {
  const acts = el('div', 'msg-acts');
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
// 顶部“新对话”同样回到首页初始界面，发送时再按需创建会话
$('#new-chat-btn').addEventListener('click', () => startNewConvo());

// 内置目录浏览：普通浏览器里没有系统文件夹选择器时的退路（桌面壳走 pickSystemFolder）。
function openWorkspaceBrowse() {
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
      Modal.close();
      await chooseWorkspace(path);
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

function _doSetPerm(mode, label, persist = true) {
  const prev = currentMode;
  const prevLabel = currentPermLabel;
  currentMode = mode;
  if (label) currentPermLabel = label;
  // 更新下拉菜单选中状态：按展示标签区分 auto / full_access
  document.querySelectorAll('#perm-list button').forEach((b) => {
    b.setAttribute('aria-checked', String(b.dataset.permLabel === currentPermLabel));
  });
  const sub = document.getElementById('plus-plan-sub');
  // L7：菜单副标题写「下一步动作」而不是「上次切换的结果」——过去时状态放这迟早过时
  if (sub) sub.textContent = (mode === 'plan_first' || mode === 'interactive') ? '切换「完全访问」' : '切换「请我批准」';
  if (!persist) return;
  // 后端不答应就必须把芯片拨回去：挂着「完全访问」却按旧模式跑，
  // 下一次目标要么该批的没批、要么不该批的悄悄跑完，两种都比报错更糟。
  api('POST', '/api/settings', { safety: { mode } }).catch((err) => {
    _doSetPerm(prev, prevLabel, false);
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

/* ============================================================
 * 皮肤主题：深浅（跟随系统）+ 强调色，本地持久化
 * ============================================================ */
const UIPrefs = (() => {
  const KEY = 'gleam-ui';
  let p = Object.assign({ themeMode: 'light', accent: 'gleam', lang: 'zh', font: 'sans', text: 's', zoom: 'm', width: 'standard' }, load());
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
  document.documentElement.setAttribute('data-accent', accent || 'gleam');
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


/* ---------- 界面语言 ---------- */
// 词表与翻译器在 i18n.js（在 app.js 之前加载）。这里只负责把当前语言应用上去。

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
  // 版本号只从 /api/info 取（owner 是 internal/buildinfo），问题交给 checkUpdate：
  // 它去问 /api/update/check，有新版、已最新、连不上三种结果各自说人话。
  let info;
  try {
    info = await api('GET', '/api/info');
  } catch (err) {
    // 这一步拿不到，后面什么都问不了：用模态说清楚，而不是丢一条会自己消失的 toast
    await alertModal('没能读到本机版本（' + err.message + '），检查更新没能开始，稍后再试一次。', '检查更新');
    return;
  }
  $('#me-version').textContent = info.version ? 'v' + info.version : '未知';
  await checkUpdate();
});

// 用户行那颗「?」和抽屉里的「帮助」是同一个入口：都开帮助，不各写一份文案
const helpBtn = $('#help-btn');
if (helpBtn) helpBtn.addEventListener('click', () => $('#me-help').click());

$('#me-help').addEventListener('click', () => {
  Modal.open('帮助', (box) => {
    box.innerHTML =
      '<p class="modal-text">Gleam 完全在本机运行，你的数据不会离开这台电脑。</p>' +
      '<div class="field"><label class="field-label">快速上手</label>' +
      '<p class="field-hint" style="margin-top:4px;">· 在底部输入框直接说要做什么，例如“整理当前文件夹”<br>· 「定时任务」用中文说时间即可自动执行，无需任何技术语法<br>· 模型密钥在「全部设置 → 模型」填写，只存本机<br>· 高风险操作（删除/执行命令）会先征求你的同意</p></div>' +
      '<div class="modal-actions"><button class="btn btn-primary" id="me-help-ok">知道了</button></div>';
    box.querySelector('#me-help-ok').addEventListener('click', Modal.close);
  });
});

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
  const stage = pane && pane.querySelector('.bp-stage');
  const tabsBox = $('#bp-tabs');
  const firstFrame = $('#bp-frame');
  if (!pane || !urlIn || !form || !stage || !tabsBox || !firstFrame || !empty) {
    return { toggle: () => {}, isOpen: () => false, newTab: () => {} };
  }

  const status = $('#bp-status');
  const backBtn = $('#bp-back');
  const fwdBtn = $('#bp-fwd');
  const quickBox = $('#bp-quick');

  // ---- 标签 ----
  // 每个标签自己带地址栈和一个独立 iframe（sandbox 属性从 HTML 里那份 clone 出来，
  // 不在这里再抄一遍）。只有一个标签时标签条整条隐藏，界面上和没有标签时一样。
  let seq = 0;
  const tabs = []; // { id, hist, hi, frame, title }
  let active = null;

  // spare 是 HTML 里那份 #bp-frame：第一个标签直接拿它用，不然后面 clone 一个、
  // 原来那个就白白留在 stage 里（一个没人管的空白 iframe）
  let spare = firstFrame;
  function ensureFrame(t) {
    if (!t.frame) {
      if (spare) { t.frame = spare; spare = null; } else {
        t.frame = firstFrame.cloneNode(); // clone 带齐 sandbox / referrerpolicy
        t.frame.removeAttribute('src');
        stage.appendChild(t.frame);
      }
    }
    return t.frame;
  }

  function activeFrame() { return ensureFrame(active); }
  const current = () => (active && active.hi >= 0 ? active.hist[active.hi] : '');

  function paintTabs() {
    tabsBox.replaceChildren();
    if (tabs.length <= 1) { tabsBox.hidden = true; return; }
    tabsBox.hidden = false;
    tabs.forEach((t) => {
      const label = t.title || (t.hi >= 0 ? t.hist[t.hi].replace(/^https?:\/\//, '') : '新标签页');
      const b = el('button', 'bp-tab' + (t === active ? ' is-active' : ''));
      b.type = 'button';
      b.setAttribute('role', 'tab');
      b.setAttribute('aria-selected', String(t === active));
      b.title = label;
      b.appendChild(el('span', 'bp-tab-label', label));
      const x = el('span', 'bp-tab-x');
      x.setAttribute('role', 'button');
      x.setAttribute('aria-label', '关闭标签');
      x.textContent = '✕';
      x.addEventListener('click', (e) => { e.stopPropagation(); closeTab(t.id); });
      b.appendChild(x);
      b.addEventListener('click', () => activate(t.id));
      tabsBox.appendChild(b);
    });
  }

  // activate 只切可见性，不重载：切回来还该是刚才那一页。
  // 唯一例外是关掉面板时被清空过的标签——那种要找地址重新载一次。
  function activate(id) {
    const t = tabs.find((x) => x.id === id);
    if (!t) return;
    active = t;
    tabs.forEach((x) => { if (x.frame) x.frame.hidden = x !== t; });
    // 关面板时会把 src 清成 about:blank 掐断请求；切回来要重新载一次。
    // 不能靠 src 属性判"是不是空的"——about:blank 也是个 src
    if (t.blanked && t.hi >= 0) show(t.hist[t.hi]);
    else paintChrome();
  }

  function newTab(rawURL) {
    const t = { id: ++seq, hist: [], hi: -1, frame: null, title: '' };
    tabs.push(t);
    ensureFrame(t);
    const url = rawURL ? normalizeURL(rawURL) : null;
    pane.hidden = false;
    active = t;
    if (url) { t.hist.push(url); t.hi = 0; show(url); } else { paintChrome(); urlIn.focus(); }
    Dock.refresh();
    return t;
  }

  function closeTab(id) {
    const i = tabs.findIndex((x) => x.id === id);
    if (i < 0) return;
    const t = tabs[i];
    if (t.frame) { t.frame.src = 'about:blank'; t.frame.remove(); }
    tabs.splice(i, 1);
    if (!tabs.length) { close(); return; }
    if (t === active) active = tabs[Math.min(i, tabs.length - 1)];
    activate(active.id);
  }

  // 常用端口：本机起服务翻来覆去就是这几个，写出来比让用户记地址有用。
  const QUICK = [
    { url: 'http://127.0.0.1:5173', label: 'Vite 5173' },
    { url: 'http://127.0.0.1:8080', label: 'Vue CLI 8080' },
    { url: 'http://127.0.0.1:5000', label: 'Kestrel 5000' },
    { url: 'http://127.0.0.1:8798', label: 'Gleam 自己' },
  ];

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

  function setStatus(text, bad) {
    status.textContent = text;
    if (bad) status.dataset.bad = 'true'; else delete status.dataset.bad;
  }

  function paintNav() {
    const t = active;
    backBtn.disabled = !t || t.hi <= 0;
    fwdBtn.disabled = !t || t.hi < 0 || t.hi >= t.hist.length - 1;
    quickBox.querySelectorAll('.bp-chip').forEach((c) => {
      const on = c.dataset.url === current();
      if (on) c.setAttribute('aria-current', 'true'); else c.removeAttribute('aria-current');
    });
  }

  function paintChrome() {
    const t = active;
    urlIn.value = t && t.hi >= 0 ? t.hist[t.hi] : '';
    empty.hidden = !!(t && t.hi >= 0);
    paintQuick();
    paintNav();
    paintTabs();
  }

  // show 只做"把这一格画成这个地址"，入不入栈由调用方决定。
  function show(url) {
    const t = active;
    if (!t) return;
    urlIn.value = url;
    delete urlIn.dataset.bad;
    const f = activeFrame();
    f.src = url;
    f.hidden = false;
    tabs.forEach((x) => { if (x !== t && x.frame) x.frame.hidden = true; });
    empty.hidden = true;
    t.title = url.replace(/^https?:\/\//, '');
    t.blanked = false;
    setStatus('正在载入 ' + url + ' · 一直空白多半是它拒绝被嵌，用「在系统浏览器打开」');
    UIPrefs.set({ browserLast: url });
    paintChrome();
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
    const t = active || newTab();
    if (url !== current()) {
      t.hist.splice(t.hi + 1);    // 从中间地址再打开时，后面的"未来"作废——和浏览器一样
      t.hist.push(url);
      t.hi = t.hist.length - 1;
    }
    show(url);
    return true;
  }

  function step(delta) {
    const t = active;
    if (!t) return;
    const next = t.hi + delta;
    if (next < 0 || next >= t.hist.length) return;
    t.hi = next;
    show(t.hist[t.hi]);
  }

  // 原地重载跨源会抛，所以换节点：clone 带齐 sandbox / referrerpolicy，
  // 但要先摘掉 src，否则插进去就按旧地址自己加载一遍。
  function reload() {
    const url = current();
    if (!url || !active) return;
    const old = activeFrame();
    const fresh = old.cloneNode();
    fresh.removeAttribute('src');
    old.replaceWith(fresh);
    active.frame = fresh;
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
    // 第一次打开给一个空标签（只有「上次看的」那颗芯片，不自动加载——本机服务可能已经停了）；
    // 再次打开则回到上次那一页（close 只清 src，不动地址栈与标签）
    if (!tabs.length) newTab();
    else if (current() && active.blanked) show(current());
    paintChrome();
    Dock.refresh();
    urlIn.focus();
    urlIn.select();
  }

  function close() {
    if (pane.hidden) return;
    pane.hidden = true;
    // 所有标签的 iframe 都摘掉 src：关了就断掉里面的请求，别让它们继续在后台跑。
    // 地址栈与标签列表留着，下次打开还是这些页
    tabs.forEach((t) => {
      if (t.frame) { t.frame.src = 'about:blank'; t.frame.hidden = true; }
      t.blanked = true;
    });
    empty.hidden = false;
    setStatus('');
    if (opener && document.contains(opener)) opener.focus();
    opener = null;
    Dock.refresh();
  }

  function toggle() { if (pane.hidden) open(); else close(); }

  form.addEventListener('submit', (e) => { e.preventDefault(); openURL(urlIn.value); });
  backBtn.addEventListener('click', () => step(-1));
  fwdBtn.addEventListener('click', () => step(1));
  $('#bp-reload').addEventListener('click', reload);
  // 关闭键已上移到坞的头部（#dock-close）：面板坞里 ✕ 应该关掉"当前这一页"，不只关浏览器

  $('#bp-external').addEventListener('click', () => {
    const url = current();
    if (!url) { setStatus('还没有地址，先打开一个再谈外部浏览器。', true); return; }
    // 这里不能图省事写 'noopener'：按规范那样传第三参，window.open 成功时也返回
    // null，于是"被拦了"这句永远报得出来——是假警报。改为拿到新窗口后亲手摘掉
    // opener（等价于 noopener，挡住反向 tabnabbing），返回值才真的能用来判成败。
    // 桌面壳里 window.open 一律被主进程拦下（返回 null），按返回值判会报一句假的「弹窗被拦」：
    // 走 preload 的 openExternal，由主进程 shell.openExternal 交给系统浏览器。
    const desk = window.gleamDesktop;
    if (desk && typeof desk.openExternal === 'function') {
      desk.openExternal(url)
        .then(() => setStatus('已在系统浏览器打开。'))
        .catch((err) => setStatus('没能交给系统浏览器：' + ((err && err.message) || err), true));
      return;
    }
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

  return { toggle, open, close, newTab, closeTab, isOpen: () => !pane.hidden, normalizeURL };
})();

/* ============================================================
 * 审阅面板（批次 A1）：先用已有的改动清单，行内 diff 与文件清单在 A3 再铺开。
 * ============================================================ */
const ReviewPane = (() => {
  const box = $('#dock-review');
  if (!box) return { open() {}, close() {}, isOpen: () => false };
  async function open() {
    box.hidden = false;
    box.innerHTML = '<div class="skeleton" style="height:64px;margin:10px 12px"></div>';
    let latest = null;
    try {
      const { goals } = await api('GET', '/api/goals');
      const list = goals || [];
      // /api/goals 按时间**倒序**回（新的在前，见 handleGoalList），所以第一条才是最新一轮。
      latest = list[0] || null;
    } catch { /* 拉不到就按空态画，不编东西出来 */ }
    box.innerHTML = '';
    box.appendChild(el('div', 'review-head', latest ? '最近一轮改动' : '还没有可审阅的改动'));
    const slot = el('div', 'review-slot');
    box.appendChild(slot);
    if (!latest) {
      slot.appendChild(el('p', 'field-hint', '跑一次任务之后，这里会列出它动了哪些文件。'));
      return;
    }
    renderChangesInto(slot, latest.task_id, latest.result && latest.result.changes);
  }
  function close() { box.hidden = true; }
  return { open, close, isOpen: () => !box.hidden };
})();

const Dock = (() => {
  const dock = $('#dock');
  if (!dock) return { open() {}, close() {}, refresh() {}, isOpen: () => false };
  const tabs = [...dock.querySelectorAll('.dock-tab')];
  const dockBody = dock.querySelector('.dock-body');
  const panes = { browser: $('#browser-pane'), review: $('#dock-review') };
  let active = UIPrefs.get().dockTab === 'review' ? 'review' : 'browser';

  function paint() {
    tabs.forEach((b) => {
      const on = b.dataset.dtab === active;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
    });
    Object.entries(panes).forEach(([k, p]) => {
      const wrap = p && p.closest('.dock-pane');
      if (wrap) wrap.hidden = k !== active;
    });
    UIPrefs.set({ dockTab: active });
  }
  // 只对齐可见性，不主动开面板：BrowserPane 自己在开合，这里跟着它走
  function refresh() {
    const anyOpen = BrowserPane.isOpen() || ReviewPane.isOpen();
    dock.hidden = !anyOpen;
    if (!anyOpen) return;
    if (active === 'review' && !ReviewPane.isOpen()) active = 'browser';
    else if (active === 'browser' && !BrowserPane.isOpen()) active = 'review';
    paint();
  }
  function show(tab) {
    active = tab;
    Popovers.closeAll(); // 坞一开，输入框那些弹层先收掉，别叠在一起
    if (tab === 'review') ReviewPane.open(); else BrowserPane.open();
    refresh();
  }
  function close() { // 头部 ✕：收掉整个坞（两页都关，不留一个空壳）
    BrowserPane.close();
    ReviewPane.close();
    refresh();
  }

  tabs.forEach((b) => b.addEventListener('click', () => show(b.dataset.dtab)));
  $('#dock-close').addEventListener('click', close);

  // 新标签：浏览器面板里开一个新标签（浏览器没开就先开起来）
  $('#dock-new').addEventListener('click', () => { BrowserPane.newTab(); });

  // 拆分：坞内左右并排显示两栏（浏览器 + 审阅）。再点一次合回去。
  const splitBtn = $('#dock-split');
  splitBtn.addEventListener('click', () => {
    const on = !dockBody.classList.contains('is-split');
    dockBody.classList.toggle('is-split', on);
    splitBtn.setAttribute('aria-pressed', String(on));
    splitBtn.title = on ? '取消拆分' : '拆分面板';
  });

  const maxBtn = $('#dock-max');
  maxBtn.addEventListener('click', () => {
    const on = !dock.classList.contains('is-max');
    dock.classList.toggle('is-max', on);
    maxBtn.setAttribute('aria-pressed', String(on));
    maxBtn.title = on ? '还原面板宽度' : '最大化面板';
  });

  // 拖左缘调宽：宽度记在本机，下次开还是这个宽度
  const handle = $('#dock-resize');
  const savedW = Number(UIPrefs.get().dockW);
  if (savedW >= 320) dock.style.width = savedW + 'px';
  let drag = null;
  handle.addEventListener('pointerdown', (e) => {
    if (dock.classList.contains('is-max')) return;
    const r = dock.getBoundingClientRect();
    drag = { x: e.clientX, w: r.width };
    handle.setPointerCapture(e.pointerId);
    e.preventDefault();
  });
  handle.addEventListener('pointermove', (e) => {
    if (!drag) return;
    const w = Math.max(320, Math.min(drag.w - (e.clientX - drag.x), window.innerWidth - 260));
    dock.style.width = Math.round(w) + 'px';
  });
  const endDrag = (e) => {
    if (!drag) return;
    drag = null;
    try { handle.releasePointerCapture(e.pointerId); } catch { /* 已经释放 */ }
    UIPrefs.set({ dockW: Math.round(dock.getBoundingClientRect().width) });
  };
  handle.addEventListener('pointerup', endDrag);
  handle.addEventListener('pointercancel', endDrag);

  // Esc 关审阅页；浏览器页的 Esc 由 BrowserPane 自己管（它还要先看有没有模态开着）
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || dock.hidden || active !== 'review') return;
    const modalOpen = [...document.querySelectorAll('[aria-modal="true"]')]
      .some((n) => n.getClientRects().length > 0);
    if (modalOpen) return;
    close();
  }, { capture: true });

  return { open: show, close, refresh, isOpen: () => !dock.hidden };
})();

/* ---------- 两侧边缘拖宽：左＝侧栏（拖到最左＝收起），右＝右侧栏（拖到最右＝关闭） ---------- */
// dir=+1：指针往右＝面板变宽（在左边那块）；dir=-1：指针往左＝面板变宽（在右边那块）
const makeSash = ({ handleSel, panelSel, dir, min, max, closeAt, apply, save, close }) => {
  const handle = $(handleSel);
  const panel = $(panelSel);
  if (!handle || !panel) return;
  const root = document.documentElement;
  let drag = null;
  const width = () => panel.getBoundingClientRect().width;
  const rawW = (d, x) => Math.round(d.w + dir * (x - d.x));

  // 辉光跟着指针在线上走：只取它在热区里的纵向位置，横向永远贴着那条线。
  // 参数就是 clientY——这里写成对象再读 .clientY 会得到 NaN，样式算不出 transform，
  // 光就死在原位不动了。
  const glowTo = (clientY) =>
    handle.style.setProperty('--drag-y', Math.round(clientY - handle.getBoundingClientRect().top) + 'px');

  handle.addEventListener('pointerdown', (e) => {
    if (panel.getClientRects().length === 0) return; // 面板收起了就别拖
    drag = { x: e.clientX, w: width(), lastX: e.clientX };
    // 钉住内容宽度：拖动期间里面那层不重排，只由外面裁切（见 .sidebar-inner）
    root.style.setProperty('--side-w-lock', Math.round(drag.w) + 'px');
    // 捕获拿不到也别散架：move/up 都挂在 window 上，捕获只是让它更跟手
    try { handle.setPointerCapture(e.pointerId); } catch { /* 指针已经不在了 */ }
    root.classList.add('sidebar-dragging');
    handle.classList.add('is-dragging');
    glowTo(e.clientY);
    e.preventDefault();
  });

  // 监听挂在 window 的捕获阶段：指针移出窗口、或捕获没拿到时，拖动照样能收尾。
  // 早先只挂在 handle 上，指针 up 丢一次就永远停在「拖动中」——那条线会一直留在界面上。
  window.addEventListener('pointermove', (e) => {
    if (!drag) return;
    // 自救：还在拖动状态、但按键已经松开了——说明 up 半路丢了，就地收尾
    if (e.pointerType === 'mouse' && e.buttons === 0) { endDrag(e); return; }
    drag.lastX = e.clientX;
    glowTo(e.clientY);
    apply(Math.max(0, Math.min(rawW(drag, e.clientX), max)));
  }, true);

  const endDrag = (e) => {
    if (!drag) return;
    const x = typeof e.clientX === 'number' ? e.clientX : drag.lastX;
    const raw = rawW(drag, x), keep = drag.w;
    drag = null;
    root.classList.remove('sidebar-dragging');
    handle.classList.remove('is-dragging');
    handle.style.removeProperty('--drag-y');
    root.style.removeProperty('--side-w-lock');
    try { handle.releasePointerCapture(e.pointerId); } catch { /* 已经释放 */ }
    // 先让过渡属性重新生效并落一次样式，否则这一帧起不了动画
    void root.offsetWidth;
    if (raw <= closeAt) {
      apply(Math.max(min, Math.min(keep, max))); // 留着收起前的宽度，再打开时还回来
      close();                                    // 收起/关闭走各自既有那条路径
      return;
    }
    const w = Math.max(min, Math.min(raw, max));
    apply(w);
    if (save) save(w);
  };
  window.addEventListener('pointerup', endDrag, true);
  window.addEventListener('pointercancel', endDrag, true);
  handle.addEventListener('lostpointercapture', endDrag);
  window.addEventListener('blur', endDrag);
};

// 左侧栏：宽度落在 --side-w；拖到最左＝收起
makeSash({
  handleSel: '#sidebar-resize', panelSel: '#sidebar', dir: 1,
  min: 200, max: 420, closeAt: 150,
  apply: (w) => document.documentElement.style.setProperty('--side-w', Math.round(w) + 'px'),
  save: (w) => UIPrefs.set({ sidebarW: w }),
  close: () => $('#sidebar-toggle').click(),
});
// 右侧栏：宽度落在它自己的 --aux-w；拖到最右＝关闭
makeSash({
  handleSel: '#aux-resize', panelSel: '#aux', dir: -1,
  min: 280, max: 560, closeAt: 200,
  apply: (w) => { const a = $('#aux'); if (a) a.style.setProperty('--aux-w', Math.round(w) + 'px'); },
  save: (w) => UIPrefs.set({ auxW: w }),
  close: () => AuxPanel.toggle(),
});
// 右侧栏宽度也记在本机：开机按上次的宽，没拖过就用 CSS 里的 clamp
(() => {
  const w = Number(UIPrefs.get().auxW), a = $('#aux');
  if (a && w >= 280) a.style.setProperty('--aux-w', Math.round(w) + 'px');
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
// Ctrl/⌘ + , 打开设置：已并入 chrome.js 的全局键位表（可在「设置 → 快捷键」里改）。
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

/* ============================================================
 * 站点：模板轮播（首页）+ 「站点」页
 *
 * 模板是 Gleam 自己写的起手式：点一下只是把一句建站目标填进输入框并挂上「站点」芯片，
 * 不会替你提交，也不会凭空生成文件。缩略图是对应骨架页面的截图（static/sites/*.webp）。
 * 「我的站点」只列真实存在的东西：各空间文件夹里 sites/ 等位置的 index.html，
 * 用与文件选择器相同的只读工具（file.search / file.read）扫出来；扫不到就是空态。
 * ============================================================ */
const SITE_CATS = [
  { id: 'landing', label: '落地页' }, { id: 'portfolio', label: '作品集' }, { id: 'blog', label: '博客与内容' },
  { id: 'dashboard', label: '数据看板' }, { id: 'internal', label: '内部工具' }, { id: 'other', label: '其他' },
];
const SITE_TEMPLATES = [
  {
    "id": "notes-app",
    "cat": "landing",
    "title": "本地笔记应用 · 产品落地页",
    "prompt": "为一款本地优先的笔记应用做产品落地页：首屏标语与两个行动按钮、三项特性介绍、价格区与常见问题。"
  },
  {
    "id": "night-ride",
    "cat": "landing",
    "title": "城市夜骑活动 · 报名页",
    "prompt": "为一场城市夜骑活动做报名落地页：活动主视觉、时间地点、路线说明、报名表单（姓名、手机、车型），以及注意事项。"
  },
  {
    "id": "street-photo",
    "cat": "portfolio",
    "title": "街头摄影 · 暗色作品集",
    "prompt": "做一个街头摄影师的暗色作品集：顶部姓名与简介、按年份筛选、瀑布流照片网格（用占位色块代替照片）、点击放大的灯箱。"
  },
  {
    "id": "illustrator",
    "cat": "portfolio",
    "title": "插画师 · 个人作品主页",
    "prompt": "做一个插画师的个人作品主页：大号姓名标题、三栏精选作品卡片（作品名 + 年份）、合作邀约区和联系方式，风格明亮活泼。"
  },
  {
    "id": "tech-blog",
    "cat": "blog",
    "title": "极简技术博客 · 首页",
    "prompt": "做一个极简技术博客：站点名与一句话简介、文章列表（标题、日期、标签、两行摘要）、标签云和订阅入口，阅读舒适、深浅两套配色。"
  },
  {
    "id": "reading-weekly",
    "cat": "blog",
    "title": "读书笔记周刊 · 归档页",
    "prompt": "做一个读书笔记周刊的归档页：刊头、按期号排列的卡片（期号、本期书名、一句摘录）、搜索框与按年份分组，排版偏杂志感。"
  },
  {
    "id": "budget",
    "cat": "dashboard",
    "title": "家庭记账 · 月度看板",
    "prompt": "做一个家庭记账的月度看板：读取同目录 data.json（先生成一份示例数据并标明是示例），展示收支概览、按分类的环形图、按周的柱状图和最近流水表。"
  },
  {
    "id": "team-weekly",
    "cat": "dashboard",
    "title": "团队周报 · 指标看板",
    "prompt": "做一个团队周报指标看板：本地 data.json 驱动（先生成示例并标注），包含本周完成事项、进行中事项、阻塞项列表和一条趋势折线，适合投屏。"
  },
  {
    "id": "room-booking",
    "cat": "internal",
    "title": "会议室预约 · 内部表单",
    "prompt": "做一个会议室预约的内部小工具：左侧房间列表、右侧按小时的时间格，点格子弹出预约表单，数据存浏览器 localStorage，不需要后端。"
  },
  {
    "id": "asset-lending",
    "cat": "internal",
    "title": "设备借用登记 · 内部工具",
    "prompt": "做一个设备借用登记台：设备表格（名称、编号、状态、借用人）、借出与归还按钮、按状态筛选，数据存 localStorage，可导出 CSV。"
  },
  {
    "id": "party-invite",
    "cat": "other",
    "title": "生日派对邀请函 · 单页",
    "prompt": "做一张生日派对的网页邀请函：大号标题、时间地点、一段手写感的邀请语、加入日历按钮和回执表单（是否出席、人数）。"
  },
  {
    "id": "countdown",
    "cat": "other",
    "title": "发布会倒计时 · 单页",
    "prompt": "做一个发布会倒计时单页：全屏深色背景、居中的天/时/分/秒倒计时（目标时间写在页面顶部的常量里）、一句标语和预约提醒按钮。"
  }
];

function startSiteFromTemplate(t) {
  if (viewingConvo || document.documentElement.dataset.view !== 'goals') showView('goals');
  goalInput.value = `在工作区新建 sites/${t.id}/index.html：${t.prompt}单文件 HTML + CSS，不依赖外部资源，桌面和手机都能看。`;
  goalInput.dispatchEvent(new Event('input', { bubbles: true }));
  setSiteMode(true);
  goalInput.focus();
  goalInput.selectionStart = goalInput.selectionEnd = goalInput.value.length;
}

const SiteTemplates = (() => {
  let cat = SITE_CATS[0].id;
  function paint(box) {
    // 站点模板仅站点模式下出现；setSiteMode 控制显隐
    if (!siteMode) { box.hidden = true; return; }
    box.hidden = false;
    box.innerHTML = `<div class="site-tpl-head">
        <div class="site-tpl-tabs" role="tablist" aria-label="模板分类">${SITE_CATS.map((c) =>
          `<button type="button" role="tab" data-cat="${c.id}" aria-selected="${c.id === cat}">${esc(c.label)}</button>`).join('')}</div>
        <div class="site-tpl-nav">
          <button type="button" class="icon-btn" data-dir="-1" aria-label="上一组"><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m15 6-6 6 6 6"/></svg></button>
          <button type="button" class="icon-btn" data-dir="1" aria-label="下一组"><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m9 6 6 6-6 6"/></svg></button>
          <button type="button" class="icon-btn" data-close aria-label="收起站点模板" title="收起（可在「站点」页重新打开）"><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12"/></svg></button>
        </div>
      </div>
      <div class="site-tpl-row" role="list"></div>`;
    const row = box.querySelector('.site-tpl-row');
    // 一条横向轨道按分类顺序排开；分类标签只是跳转锚点，滚动时高亮跟着走
    SITE_CATS.forEach((c) => SITE_TEMPLATES.filter((t) => t.cat === c.id).forEach((t) => {
      const card = el('button', 'site-tpl-card');
      card.type = 'button';
      card.dataset.cat = t.cat;
      card.setAttribute('role', 'listitem');
      card.title = '用这个模板起一个站点（只填进输入框，不会自动提交）';
      card.innerHTML = `<span class="site-tpl-thumb"><img src="/assets/sites/${esc(t.id)}.webp" alt="" loading="lazy" width="400" height="250"></span><span class="site-tpl-title">${esc(t.title)}</span>`;
      card.addEventListener('click', () => startSiteFromTemplate(t));
      row.appendChild(card);
    }));
    const tabs = [...box.querySelectorAll('.site-tpl-tabs [data-cat]')];
    const prev = box.querySelector('[data-dir="-1"]');
    const next = box.querySelector('[data-dir="1"]');
    const mark = (id) => { cat = id; tabs.forEach((x) => x.setAttribute('aria-selected', String(x.dataset.cat === id))); };
    const sync = () => {
      const max = row.scrollWidth - row.clientWidth;
      prev.disabled = row.scrollLeft <= 1;
      next.disabled = row.scrollLeft >= max - 1;
      if (jumping) return;
      const first = [...row.children].find((c) => c.offsetLeft + c.offsetWidth / 2 >= row.scrollLeft);
      if (first) mark(row.scrollLeft >= max - 1 && max > 0 ? cat : first.dataset.cat);
    };
    let jumping = null;
    tabs.forEach((b) => b.addEventListener('click', () => {
      mark(b.dataset.cat);
      const target = row.querySelector(`.site-tpl-card[data-cat="${b.dataset.cat}"]`);
      if (!target) return;
      clearTimeout(jumping);
      jumping = setTimeout(() => { jumping = null; sync(); }, 600);
      row.scrollTo({ left: target.offsetLeft, behavior: 'smooth' });
    }));
    [prev, next].forEach((b) => b.addEventListener('click', () => {
      row.scrollBy({ left: Number(b.dataset.dir) * row.clientWidth * 0.8, behavior: 'smooth' });
    }));
    row.addEventListener('scroll', sync, { passive: true });
    requestAnimationFrame(() => {
      const target = row.querySelector(`.site-tpl-card[data-cat="${cat}"]`);
      if (target && cat !== SITE_CATS[0].id) row.scrollLeft = target.offsetLeft;
      mark(cat); sync();
    });
    box.querySelector('[data-close]').addEventListener('click', () => {
      setSiteMode(false);
      toast('已收起站点模板，可在「站点」页重新打开', 'success', 2600);
    });
  }
  function restore() {
    UIPrefs.set({ siteTplHidden: false });
    const box = document.querySelector('#goals-empty .site-tpl');
    if (box) paint(box);
  }
  return { paint, restore };
})();

// 「站点」页：扫各空间文件夹里的 index.html。只读、只扫，不建也不删。
const SITE_SKIP = /[\/\\](node_modules|\.[^\/\\]+|vendor|bower_components)[\/\\]/;
async function loadSites() {
  const body = $('#sites-body');
  const note = $('#sites-note');
  const layout = UIPrefs.get().sitesLayout || 'grid';
  body.dataset.layout = layout;
  document.querySelectorAll('.sites-layout [data-layout]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.layout === layout)));
  body.innerHTML = '<div class="sites-grid">' + '<div class="site-card skeleton-card"><div class="skeleton" style="height:100%"></div></div>'.repeat(3) + '</div>';
  note.hidden = true;
  let spaces;
  try {
    const sv = await api('GET', '/api/spaces');
    spaces = (sv.spaces || []).filter((s) => s.path).map((s) => ({ name: s.name, path: s.path }));
    if (!spaces.length && sv.workspace) spaces = [{ name: '当前工作区', path: sv.workspace }];
  } catch (err) {
    try {
      const ws = await api('GET', '/api/workspace');
      spaces = ws.workspace ? [{ name: '当前工作区', path: ws.workspace }] : [];
    } catch (e2) { loadError(body, e2, loadSites); return; }
  }
  const seen = new Set();
  const found = [];
  let skipped = 0;
  for (const sp of spaces) {
    if (seen.has(sp.path)) continue;
    seen.add(sp.path);
    try {
      const res = await api('POST', '/api/tools/call', { name: 'file.search', args: { root: sp.path, pattern: 'index.html' } });
      (res.output?.files || []).forEach((f) => {
        if (SITE_SKIP.test(f)) return;
        found.push({ file: f, dir: f.replace(/[\/\\][^\/\\]+$/, ''), space: sp });
      });
    } catch { skipped++; }
  }
  // 只列 Gleam 自己生成的。「是不是这款软件做的」不能靠目录名猜：拿任务归档里的
  // 改动清单当凭据——那个 index.html 真被某次任务写过，才算数。
  let owned = null;
  try {
    const { goals } = await api('GET', '/api/goals');
    owned = new Set();
    (goals || []).forEach((g) => {
      (((g.result || {}).changes) || []).forEach((c) => { if (c && c.path) owned.add(normPath(c.path)); });
    });
  } catch { /* 归档拉不到就不过滤：宁可多列，也不凭空把列表清空 */ }
  const mine = owned ? found.filter((f) => owned.has(normPath(f.file))) : found;
  const notMine = found.length - mine.length;

  // 标题与预览都来自文件本身；最多读 24 个，避免大仓库里一口气读太多
  const sites = mine.slice(0, 24);
  await Promise.all(sites.map(async (s) => {
    try {
      const r = await api('POST', '/api/tools/call', { name: 'file.read', args: { path: s.file } });
      s.html = r.output?.content || '';
      s.size = r.output?.size || 0;
      const m = s.html.match(/<title[^>]*>([^<]{1,120})<\/title>/i);
      s.title = m ? m[1].trim() : '';
    } catch { s.html = ''; }
  }));
  const bits = [];
  if (skipped) bits.push(`有 ${skipped} 个空间不在当前工作区范围内，切到那个空间再刷新就能扫到`);
  if (notMine) bits.push(`${notMine} 个 index.html 不是 Gleam 生成的，已略过`);
  if (mine.length > sites.length) bits.push(`共 ${mine.length} 个，这里先列前 ${sites.length} 个`);
  if (bits.length) {
    note.hidden = false;
    note.textContent = bits.join('；') + '。';
  }
  renderSites(body, sites);
}

// 路径统一成小写正斜杠再比：任务归档与 file.search 给的斜杠方向、大小写都不一定一致
function normPath(p) { return String(p).replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase(); }

function renderSites(body, sites) {
  body.innerHTML = '';
  if (!sites.length) {
    const empty = el('div', 'empty sites-empty');
    empty.innerHTML = `<div class="sites-empty-art" aria-hidden="true"><svg viewBox="0 0 64 44" width="64" height="44" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="2" width="60" height="40" rx="4"/><path d="M2 10h60"/><path d="M8 6h.01M12 6h.01M16 6h.01"/><path d="M14 20h18M14 26h26M14 32h12" opacity=".6"/></svg></div>
      <div class="empty-title">还没有站点</div>
      <p class="empty-desc">让 Gleam 在工作区里做一个网页：做好的 index.html 会出现在这里。</p>`;
    const go = el('button', 'btn btn-secondary btn-sm', '去首页挑个模板');
    go.type = 'button';
    go.addEventListener('click', () => { SiteTemplates.restore(); showView('goals'); setSiteMode(true); goalInput.focus(); });
    empty.appendChild(go);
    body.appendChild(empty);
    return;
  }
  const grid = el('div', 'sites-grid');
  // 缩略图是把 1280×800 的页面按卡片实际宽度等比缩小；宽度随窗口变，比例也跟着变
  const fit = new ResizeObserver((entries) => entries.forEach((e) => e.target.style.setProperty('--s', String(e.contentRect.width / 1280))));
  sites.forEach((s) => {
    const name = s.title || s.dir.split(/[\/\\]/).pop();
    const rel = relPath(s.file, s.space.path);
    const card = el('article', 'site-card');
    const thumb = el('div', 'site-thumb');
    if (s.html) {
      // 只读预览：sandbox 不给脚本、不给同源，外链资源加载不到时就是骨架样子——这是真实文件的样子，不做美化
      const fr = document.createElement('iframe');
      fr.setAttribute('sandbox', '');
      fr.setAttribute('loading', 'lazy');
      fr.setAttribute('tabindex', '-1');
      fr.setAttribute('aria-hidden', 'true');
      fr.srcdoc = s.html;
      thumb.appendChild(fr);
    } else {
      thumb.appendChild(el('span', 'site-thumb-miss', '读不到内容'));
    }
    const meta = el('div', 'site-meta');
    meta.innerHTML = `<div class="site-name" title="${esc(name)}">${esc(name)}</div><div class="site-path" title="${esc(s.file)}">${esc(s.space.name)} · ${esc(rel)}</div>`;
    const acts = el('div', 'site-acts');
    const pv = el('button', 'btn btn-secondary btn-sm', '预览');
    pv.type = 'button';
    pv.disabled = !s.html;
    pv.addEventListener('click', () => openSitePreview(name, s));
    const more = el('button', 'btn btn-ghost btn-sm', '继续完善');
    more.type = 'button';
    more.addEventListener('click', () => {
      showView('goals');
      goalInput.value = `继续完善 ${rel}：`;
      goalInput.dispatchEvent(new Event('input', { bubbles: true }));
      addRef('file', rel, '@' + s.file);
      setSiteMode(true);
      goalInput.focus();
    });
    const cp = el('button', 'btn btn-ghost btn-sm', '复制路径');
    cp.type = 'button';
    cp.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(s.file); toast('已复制路径', 'success', 1600); }
      catch { toast('复制失败，路径是：' + s.file, 'error', 6000); }
    });
    acts.append(pv, more, cp);
    fit.observe(thumb);
    card.append(thumb, meta, acts);
    grid.appendChild(card);
  });
  body.appendChild(grid);
}

function openSitePreview(name, s) {
  Modal.open(esc(name), (box) => {
    box.appendChild(el('p', 'field-hint', `${s.file} · 只读预览：脚本和外链资源不会运行，完整效果请用浏览器直接打开这个文件。`));
    const fr = document.createElement('iframe');
    fr.className = 'site-preview-frame';
    fr.setAttribute('sandbox', '');
    fr.srcdoc = s.html;
    box.appendChild(fr);
  });
}

document.getElementById('sites-refresh')?.addEventListener('click', () => loadSites());
document.getElementById('sites-add')?.addEventListener('click', () => {
  SiteTemplates.restore();
  showView('goals');
  setSiteMode(true);
  goalInput.focus();
});
document.querySelectorAll('.sites-layout [data-layout]').forEach((b) => b.addEventListener('click', () => {
  UIPrefs.set({ sitesLayout: b.dataset.layout });
  $('#sites-body').dataset.layout = b.dataset.layout;
  document.querySelectorAll('.sites-layout [data-layout]').forEach((x) => x.setAttribute('aria-pressed', String(x === b)));
}));

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
        // 日期与次数挂到 data-* 上，交给下面的卡片提示；原生 title 太慢也太丑
        cells.push(`<i class="heat-cell" data-l="${level(n, max)}" data-ts="${day.getTime()}" data-n="${n}"></i>`);
      }
    }
    hideHeatTip();
    card.innerHTML = `<div class="activity-tabs" role="tablist">
        <button type="button" role="tab" data-act="convo" aria-selected="${tab === 'convo'}">会话</button>
        <button type="button" role="tab" data-act="task" aria-selected="${tab === 'task'}">任务</button>
      </div>
      <div class="heat-grid" aria-label="过去一年的本机活动，共 ${total} 次">${cells.join('')}</div>
      <div class="heat-months" aria-hidden="true">${months.join('')}</div>`;
    card.querySelectorAll('[data-act]').forEach((b) => b.addEventListener('click', () => { tab = b.dataset.act; render(card); }));
    bindHeatTip(card.querySelector('.heat-grid'), () => tab);
  }

  // 悬停格子时弹一张日期卡片：12月4日周四 / 单日活跃任务 / N 个任务
  let heatTip = null;
  function hideHeatTip() { if (heatTip) heatTip.hidden = true; }
  function bindHeatTip(grid, tabOf) {
    if (!grid) return;
    if (!heatTip) { heatTip = el('div', 'heat-tip'); heatTip.hidden = true; document.body.appendChild(heatTip); }
    grid.addEventListener('mouseover', (e) => {
      const cell = e.target.closest('.heat-cell');
      if (!cell || cell.hasAttribute('data-future') || !cell.dataset.ts) return;
      const d = new Date(Number(cell.dataset.ts));
      const n = Number(cell.dataset.n) || 0;
      const wd = '日一二三四五六'[d.getDay()];
      const noun = tabOf() === 'task' ? '任务' : '会话';
      heatTip.innerHTML = `<div class="heat-tip-head"><span class="heat-tip-date">${d.getMonth() + 1}月${d.getDate()}日周${wd}</span>` +
        `<span class="heat-tip-count">${n} 个${noun}</span></div>` +
        `<div class="heat-tip-sub">单日活跃${noun}</div>`;
      heatTip.hidden = false;
      const r = cell.getBoundingClientRect();
      const tw = heatTip.offsetWidth, th = heatTip.offsetHeight;
      const left = Math.max(8, Math.min(r.left + r.width / 2 - tw / 2, window.innerWidth - tw - 8));
      let top = r.top - th - 8;
      if (top < 8) top = r.bottom + 8;
      heatTip.style.left = Math.round(left) + 'px';
      heatTip.style.top = Math.round(top) + 'px';
    });
    grid.addEventListener('mouseleave', hideHeatTip);
    grid.addEventListener('mouseout', (e) => { if (!e.relatedTarget || !e.relatedTarget.closest('.heat-grid')) hideHeatTip(); });
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
    const tpl = empty && empty.querySelector('.site-tpl');
    if (home && tpl && siteMode) { if (!tpl.childElementCount) SiteTemplates.paint(tpl); else tpl.hidden = false; }
    else if (tpl) tpl.hidden = true;
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
  const LABEL = {
    auto: '自动审批',
    full_access: '完全访问',
    plan_first: '询问审批',
    chat: '仅对话',
  };
  function paint() {
    const taskBtn = document.querySelector('#task-seg button[aria-pressed="true"]');
    const task = taskBtn ? (taskBtn.dataset.task || 'work') : 'work';
    const permLabelBtn = document.querySelector('#perm-list button[aria-checked="true"]');
    const perm = permLabelBtn ? (permLabelBtn.dataset.permLabel || 'auto') : currentPermLabel;
    const key = task === 'chat' ? 'chat' : perm;
    permLabel.textContent = LABEL[key] || LABEL.auto;
    permIcon.innerHTML = ICON[key === 'plan_first' ? 'plan_first' : 'auto'];
    permBtn.dataset.mode = key;
    permBtn.title = key === 'plan_first' ? '执行前始终询问（点击切换）'
      : key === 'full_access' ? '完全访问：高风险操作仍会询问（点击切换）'
        : '自动审批：仅高风险操作才征求批准（点击切换）';
    const code = task === 'code';
    toggle.querySelectorAll('button[data-mode]').forEach((b) => {
      b.setAttribute('aria-checked', String((b.dataset.mode === 'code') === code));
    });
    updateModePill();
  }
  function updateModePill() {
    const pill = toggle.querySelector('.mode-toggle-pill');
    const active = toggle.querySelector('button[aria-checked="true"]');
    if (!pill || !active) return;
    const rect = active.getBoundingClientRect();
    const parent = toggle.getBoundingClientRect();
    pill.style.left = (rect.left - parent.left) + 'px';
    pill.style.width = rect.width + 'px';
  }
  const mo = new MutationObserver(paint);
  ['#task-seg', '#perm-list'].forEach((sel) => {
    const n = $(sel);
    if (n) mo.observe(n, { subtree: true, attributes: true, attributeFilter: ['aria-pressed', 'aria-checked'] });
  });
  paint();

  const setOpen = (open) => {
    if (open) {
      Popovers.closeOthers('perm');
      anchorPopover(permPop, permBtn);
    } else {
      permPop.hidden = true;
    }
    permBtn.setAttribute('aria-expanded', String(open));
  };
  Popovers.register('perm', () => setOpen(false));
  permBtn.addEventListener('click', () => setOpen(permPop.hidden));
  document.addEventListener('click', (e) => {
    if (!permPop.hidden && !e.target.closest('#cp-perm-pop') && !e.target.closest('#cp-perm')) setOpen(false);
  });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !permPop.hidden) setOpen(false); });

  toggle.addEventListener('click', (e) => {
    const b = e.target.closest('button[data-mode]');
    if (!b) return;
    // 注意别用 segValue：它读的是 data-val，而 #task-seg 的按钮用的是 data-task，
    // 拿回来永远是空串 → 兜底成 'work' → 从「编程」切回「通用」时判定永远不成立。
    const taskBtn = document.querySelector('#task-seg button[aria-pressed="true"]');
    const cur = taskBtn ? (taskBtn.dataset.task || 'work') : 'work';
    const want = b.dataset.mode;
    if (want === 'code' && cur !== 'code') document.querySelector('#task-seg button[data-task="code"]').click();
    if (want === 'work' && cur === 'code') document.querySelector('#task-seg button[data-task="work"]').click();
  });

  const attach = $('#cp-attach');
  const attachMenu = $('#attach-menu');
  if (attach && attachMenu) {
    const setAttachOpen = (open) => {
      if (open) {
        Popovers.closeOthers('attach');
        anchorPopover(attachMenu, attach);
      } else {
        attachMenu.hidden = true;
      }
      attach.setAttribute('aria-expanded', String(open));
    };
    Popovers.register('attach', () => setAttachOpen(false));
    attach.addEventListener('click', (e) => {
      e.stopPropagation();
      setAttachOpen(attachMenu.hidden);
    });
    attachMenu.addEventListener('click', (e) => {
      const item = e.target.closest('[data-attach]');
      if (!item) return;
      const kind = item.dataset.attach;
      setAttachOpen(false);
      if (kind === 'file') pickSystemFile();
      else if (kind === 'folder') pickSystemFolder();
    });
    document.addEventListener('click', (e) => {
      if (!attachMenu.hidden && !e.target.closest('#attach-menu') && !e.target.closest('#cp-attach')) setAttachOpen(false);
    });
  }
  return { paint };
})();

/* ---------- 自动更新 ---------- */
const Updater = (() => {
  const desk = window.gleamDesktop;
  if (!desk?.update) return { init() {} };

  const btn = document.getElementById('cp-update-btn');
  const btnText = document.getElementById('cp-update-text');
  let state = 'idle'; // idle | available | downloading | downloaded
  let version = '';

  function showBtn() { if (btn) btn.hidden = false; }
  function hideBtn() { if (btn) btn.hidden = true; }

  function paintBtn(text, tone) {
    if (!btnText) return;
    btnText.textContent = text;
    btn.dataset.tone = tone;
    btn.title = tone === 'ready' ? `点击安装 Gleam ${version}` : `正在下载 Gleam ${version}`;
  }

  function openInstallDialog() {
    if (state !== 'downloaded') return;
    const overlay = document.createElement('div');
    overlay.className = 'upd-overlay';
    const dlg = document.createElement('div');
    dlg.className = 'upd-dialog';
    dlg.setAttribute('role', 'dialog');
    dlg.setAttribute('aria-modal', 'true');
    dlg.setAttribute('aria-labelledby', 'upd-dlg-title');
    dlg.innerHTML = `
      <h3 id="upd-dlg-title">安装 Gleam ${esc(version)}</h3>
      <p>更新已经下载并验证。安装会退出并重新打开 Gleam。</p>
      <div class="upd-actions">
        <button type="button" class="btn btn-ghost btn-sm" data-act="later">稍后更新</button>
        <button type="button" class="btn btn-primary btn-sm" data-act="restart">安装并重启应用</button>
      </div>`;
    overlay.appendChild(dlg);
    document.body.appendChild(overlay);

    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) { close(); return; }
      const act = e.target.closest('[data-act]')?.dataset.act;
      if (act === 'later') close();
      else if (act === 'restart') desk.update.restart();
    });
    dlg.querySelector('[data-act="restart"]').focus();

    function close() { overlay.remove(); }
  }

  function init() {
    if (!btn) return;
    btn.addEventListener('click', () => {
      if (state === 'downloaded') openInstallDialog();
    });

    desk.update.onStatus((s) => {
      if (!s || !s.kind) return;
      switch (s.kind) {
        case 'checking':
          break;
        case 'available':
          state = 'available'; version = s.version;
          showBtn(); paintBtn('更新', 'available');
          break;
        case 'downloading':
          state = 'downloading';
          showBtn(); paintBtn(`更新 ${s.percent}%`, 'downloading');
          break;
        case 'downloaded':
          state = 'downloaded'; version = s.version;
          showBtn(); paintBtn('重启', 'ready');
          break;
        case 'not-available':
          state = 'idle'; version = '';
          hideBtn();
          break;
        case 'error':
          state = 'idle'; version = '';
          hideBtn();
          break;
      }
    });
  }

  return { init };
})();

/* ---------- 启动（必须是本文件的最后一段，判据见 scripts/check-app-startup.py） ---------- */
(async function init() {
  Onboarding.start();
  Updater.init();
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
  try { const s = await api('GET', '/api/settings'); if (s.safety && s.safety.mode) _doSetPerm(s.safety.mode, s.safety.mode, false); syncRuntimeState(s); } catch {}
  // 现场栏的「定时任务」不等用户打开定时任务视图才有数：这一列的价值就是常驻
  api('GET', '/api/schedules').then((r) => LiveRail.schedules((r.jobs || []).length)).catch(() => {});
  try {
    const { approvals } = await api('GET', '/api/approvals');
    pendingApprovals = (approvals || []).length;
    updateApprovalBadge(0);
  } catch { /* 忽略 */ }
})();
