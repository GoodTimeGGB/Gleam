/* Gleam Web UI — 窗口外壳（chrome）：应用菜单、前进后退、终端面板、右侧栏、
 * 全局键位表与「设置 → 快捷键」页。依赖 app.js 里的全局（$ / el / esc / api / toast /
 * Modal / UIPrefs / showView / BrowserPane / TaskSearch …），所以必须在它之后加载。
 *
 * 桌面壳（Electron）通过 preload 暴露 window.gleamDesktop；没有它就是普通浏览器，
 * 菜单里做不到的项直接隐藏，而不是摆着一个点了没反应的按钮。 */
'use strict';

const Desk = (window.gleamDesktop && window.gleamDesktop.isDesktop) ? window.gleamDesktop : null;
const deskHas = (fn) => !!(Desk && typeof Desk[fn] === 'function');
document.documentElement.dataset.shell = Desk ? 'desktop' : 'browser';
if (Desk && Desk.platform) document.documentElement.dataset.platform = Desk.platform;

/* ============================================================
 * 键位表：动作 → 组合键。默认值写在这里，用户改过的存 localStorage('gleam-keys')。
 * ============================================================ */
const Keymap = (() => {
  const KEY = 'gleam-keys';
  const IS_MAC = /Mac/i.test(navigator.platform || '');
  // run 在 init 之后才会被调用，所以这里可以放心引用下面定义的各个模块。
  const ACTIONS = [
    { id: 'nav.settings', group: 'nav', title: '设置', desc: '打开 Gleam 设置。', def: 'Ctrl+,', run: () => showView('settings') },
    { id: 'nav.back', group: 'nav', title: '返回', desc: '在页面导航历史中返回上一页。', def: 'Ctrl+[', run: () => NavHistory.back() },
    { id: 'nav.forward', group: 'nav', title: '前进', desc: '在页面导航历史中前进到下一页。', def: 'Ctrl+]', run: () => NavHistory.forward() },
    { id: 'nav.mode', group: 'nav', title: '切换工作模式', desc: '在编程模式和通用模式之间切换。', def: 'Ctrl+Tab', run: toggleWorkMode, browserReserved: true },
    { id: 'nav.switchTask', group: 'nav', title: '切换任务', desc: '搜索并切换到另一个任务。', def: 'Ctrl+G', run: () => TaskSearch.open() },
    { id: 'nav.toggleLeft', group: 'nav', title: '打开或关闭左侧栏', desc: '切换左侧导航栏的展开状态。', def: 'Ctrl+B', run: () => $('#sidebar-toggle').click() },
    { id: 'task.new', group: 'task', title: '新任务', desc: '打开新的任务输入页。', def: 'Ctrl+N', run: () => $('#convo-new').click(), browserReserved: true },
    { id: 'task.find', group: 'task', title: '搜索当前任务', desc: '在当前可见的任务内容中查找文字。', def: 'Ctrl+F', run: () => FindBar.open() },
    { id: 'task.toggleRight', group: 'task', title: '打开或关闭右侧栏', desc: '切换右侧入口栏（工作区文件 / 内置浏览器 / 终端）。', def: 'Ctrl+Shift+B', run: () => AuxPanel.toggle() },
    { id: 'aux.files', group: 'panel', title: '打开工作区文件', desc: '从当前工作区选择文件，引用到输入框。', def: 'Ctrl+Alt+E', run: () => AuxPanel.files() },
    { id: 'aux.browser', group: 'panel', title: '打开内置浏览器', desc: '打开浏览器预览面板，查看本机服务。', def: 'Ctrl+T', run: () => BrowserPane.open(), browserReserved: true },
    { id: 'aux.terminal', group: 'panel', title: '打开终端', desc: '打开底部终端面板并聚焦命令行。', def: 'Ctrl+Shift+J', run: () => Terminal.open(true) },
    { id: 'terminal.toggle', group: 'system', title: '打开或关闭终端面板', desc: '切换底部终端面板。', def: 'Ctrl+J', run: () => Terminal.toggle() },
    { id: 'system.newWindow', group: 'system', title: '新建窗口', desc: '打开一个新的 Gleam 窗口。', def: 'Ctrl+Shift+N', run: newWindow, browserReserved: true },
  ];
  const GROUPS = [
    ['nav', '导航'], ['task', '任务'], ['panel', '侧栏入口'], ['system', '系统'],
  ];
  // 菜单里那几项（关闭窗口 / 重新载入 / 缩放 / 全屏）跟着桌面应用的惯例走、不可改：
  // 桌面壳没有原生菜单，加速键得由我们自己接。浏览器里这些键本来就归浏览器。
  const FIXED_DESKTOP = {
    'Ctrl+W': () => Desk.window('close'),
    'Ctrl+R': () => Desk.view('reload'),
    'Ctrl+Shift+R': () => Desk.view('forceReload'),
    'Ctrl+0': () => Desk.view('resetZoom'),
    'Ctrl+=': () => Desk.view('zoomIn'),
    'Ctrl+Shift+=': () => Desk.view('zoomIn'),
    'Ctrl+-': () => Desk.view('zoomOut'),
    'F11': () => Desk.window('fullscreen'),
  };

  let st = load();
  let recording = null;
  const listeners = new Set();

  function load() {
    try {
      const v = JSON.parse(localStorage.getItem(KEY) || '{}');
      return { bindings: v.bindings || {}, send: v.send === 'ctrlEnter' ? 'ctrlEnter' : 'enter' };
    } catch { return { bindings: {}, send: 'enter' }; }
  }
  function save() {
    try { localStorage.setItem(KEY, JSON.stringify(st)); } catch { /* 隐私模式：本次会话内仍生效 */ }
    listeners.forEach((fn) => fn());
  }
  const action = (id) => ACTIONS.find((a) => a.id === id);
  // 用户显式清空过的绑定存为 ''，与「没改过」(undefined) 区分开
  function binding(id) {
    const a = action(id);
    if (!a) return '';
    return Object.prototype.hasOwnProperty.call(st.bindings, id) ? st.bindings[id] : a.def;
  }
  const isCustom = (id) => Object.prototype.hasOwnProperty.call(st.bindings, id) && st.bindings[id] !== action(id).def;
  function setBinding(id, combo) {
    const a = action(id);
    if (combo === a.def) delete st.bindings[id]; else st.bindings[id] = combo;
    save();
  }
  function reset(id) { delete st.bindings[id]; save(); }
  function resetAll() { st.bindings = {}; st.send = 'enter'; save(); }
  function owner(combo, except) {
    if (!combo) return null;
    return ACTIONS.find((a) => a.id !== except && binding(a.id) === combo) || null;
  }

  const CODE_KEYS = {
    Comma: ',', Period: '.', Slash: '/', Semicolon: ';', Quote: "'", BracketLeft: '[', BracketRight: ']',
    Backslash: '\\', Minus: '-', Equal: '=', Backquote: '`', Tab: 'Tab', Enter: 'Enter', Space: 'Space',
    ArrowUp: 'Up', ArrowDown: 'Down', ArrowLeft: 'Left', ArrowRight: 'Right', Backspace: 'Backspace', Delete: 'Delete',
    Home: 'Home', End: 'End', PageUp: 'PageUp', PageDown: 'PageDown', Insert: 'Insert',
  };
  // 用 e.code 而不是 e.key：Shift 会把 [ 变成 {、把 = 变成 +，按 key 记下来的组合就对不上了
  function comboOf(e) {
    let k = '';
    const c = e.code || '';
    if (/^Key[A-Z]$/.test(c)) k = c.slice(3);
    else if (/^Digit[0-9]$/.test(c)) k = c.slice(5);
    else if (/^Numpad[0-9]$/.test(c)) k = c.slice(6);
    else if (/^F([1-9]|1[0-2])$/.test(c)) k = c;
    else if (c === 'NumpadAdd') k = '=';
    else if (c === 'NumpadSubtract') k = '-';
    else if (CODE_KEYS[c]) k = CODE_KEYS[c];
    if (!k) return '';
    const parts = [];
    if (e.ctrlKey || (IS_MAC && e.metaKey)) parts.push('Ctrl');
    if (e.altKey) parts.push('Alt');
    if (e.shiftKey) parts.push('Shift');
    parts.push(k);
    return parts.join('+');
  }
  const KEY_LABEL = { ',': '逗号' };
  function parts(combo) { return combo ? combo.split('+').map((p, i, arr) => (p === '' && i === arr.length - 1 ? '+' : p)).filter(Boolean) : []; }
  function label(combo, { words = false } = {}) {
    if (!combo) return '';
    return parts(combo).map((p) => (words && KEY_LABEL[p]) || p).join('+');
  }
  function kbdHTML(combo) {
    if (!combo) return '<span class="kb-none">未设置</span>';
    return parts(combo).map((p) => `<kbd>${esc(p)}</kbd>`).join('');
  }
  // 一个可接受的组合：F 键可以单按；其余至少要带 Ctrl 或 Alt，否则打字就会被吃掉
  function acceptable(combo) {
    if (!combo) return false;
    const ps = combo.split('+');
    const key = ps[ps.length - 1];
    if (/^F\d+$/.test(key)) return true;
    return ps.includes('Ctrl') || ps.includes('Alt');
  }

  function onKey(e) {
    if (recording || e.isComposing || e.keyCode === 229) return;
    const combo = comboOf(e);
    if (!combo) return;
    if (Desk && FIXED_DESKTOP[combo]) { e.preventDefault(); FIXED_DESKTOP[combo](); return; }
    const a = ACTIONS.find((x) => binding(x.id) === combo);
    if (!a) return;
    // 模态开着时，只放行反馈键本身以外的全局键会把界面叠成好几层——一律让路
    if (document.querySelector('#modal-overlay:not([hidden]), #onboarding-overlay:not([hidden])') && a.id !== 'nav.settings') return;
    e.preventDefault();
    e.stopPropagation();
    try { a.run(); } catch (err) { console.error(err); }
  }

  function init() {
    document.addEventListener('keydown', onKey, true);
  }

  return {
    ACTIONS, GROUPS, binding, isCustom, setBinding, reset, resetAll, owner, comboOf, label, kbdHTML, acceptable,
    sendMode: () => st.send,
    setSendMode: (m) => { st.send = m === 'ctrlEnter' ? 'ctrlEnter' : 'enter'; save(); },
    customCount: () => ACTIONS.filter((a) => isCustom(a.id)).length + (st.send !== 'enter' ? 1 : 0),
    total: () => ACTIONS.length + 1,
    setRecording: (v) => { recording = v; },
    onChange: (fn) => listeners.add(fn),
    action, init, IS_MAC,
  };
})();

function toggleWorkMode() {
  const btns = [...document.querySelectorAll('#mode-toggle button[data-mode]')];
  const off = btns.find((b) => b.getAttribute('aria-checked') !== 'true');
  if (off) off.click();
}

function newWindow() {
  if (deskHas('newWindow')) { Desk.newWindow(); return; }
  const w = window.open(location.origin + '/', '_blank');
  if (!w) toast('浏览器拦下了新窗口，请允许本页弹出窗口', 'warning');
}

/* ============================================================
 * 前进 / 后退：记录视图切换的历史（只记 showView，不记滚动）
 * ============================================================ */
const NavHistory = (() => {
  const stack = [document.documentElement.dataset.view || 'goals'];
  let idx = 0;
  let replaying = false;
  const back = $('#nav-back');
  const fwd = $('#nav-fwd');
  const orig = showView;
  function paint() {
    back.disabled = idx <= 0;
    fwd.disabled = idx >= stack.length - 1;
    const b = Keymap.label(Keymap.binding('nav.back'));
    const f = Keymap.label(Keymap.binding('nav.forward'));
    back.title = '返回' + (b ? `（${b}）` : '');
    fwd.title = '前进' + (f ? `（${f}）` : '');
  }
  // showView 是 app.js 顶层的函数声明（全局对象属性），在这里包一层，app.js 里的调用也会走到
  // eslint-disable-next-line no-global-assign
  showView = function (name, ...rest) {
    const r = orig.call(this, name, ...rest);
    if (!replaying && stack[idx] !== name) {
      stack.splice(idx + 1);
      stack.push(name);
      if (stack.length > 50) stack.shift();
      idx = stack.length - 1;
    }
    paint();
    return r;
  };
  function go(d) {
    const n = idx + d;
    if (n < 0 || n >= stack.length) return;
    idx = n;
    replaying = true;
    try { showView(stack[idx]); } finally { replaying = false; }
    paint();
  }
  back.addEventListener('click', () => go(-1));
  fwd.addEventListener('click', () => go(1));
  Keymap.onChange(paint);
  paint();
  return { back: () => go(-1), forward: () => go(1) };
})();

/* ============================================================
 * 应用菜单：文件 / 编辑 / 视图 / 帮助
 * ============================================================ */
const MenuBar = (() => {
  const bar = $('#menubar');
  const pop = $('#menubar-pop');
  let openId = null;
  let focusBefore = null;
  let byKeyboard = false;   // 鼠标打开的菜单按 Esc 关掉时，不把焦点环留在菜单按钮上

  // 浏览器里没有 webContents：编辑项退回 execCommand，剪贴板读取走 Clipboard API
  function browserEdit(act) {
    const t = focusBefore && document.contains(focusBefore) ? focusBefore : document.activeElement;
    if (t && t.focus) t.focus();
    if (act === 'paste' || act === 'pasteAndMatchStyle') {
      if (!navigator.clipboard || !navigator.clipboard.readText) { toast('浏览器不允许网页读剪贴板，请直接按 Ctrl+V', 'warning'); return; }
      navigator.clipboard.readText()
        .then((txt) => { if (!document.execCommand('insertText', false, txt)) toast('这里不能粘贴', 'info'); })
        .catch(() => toast('浏览器没给剪贴板权限，请直接按 Ctrl+V', 'warning'));
      return;
    }
    const ok = document.execCommand(act === 'selectAll' ? 'selectAll' : act);
    if (!ok && act !== 'selectAll') toast('浏览器拒绝了这一步，请改用键盘快捷键', 'info');
  }
  function edit(act) {
    if (deskHas('edit')) {
      const t = focusBefore && document.contains(focusBefore) ? focusBefore : null;
      if (t && t.focus) t.focus();
      Desk.edit(act);
    } else browserEdit(act);
  }
  function fullscreen() {
    if (deskHas('window')) { Desk.window('fullscreen'); return; }
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    else document.documentElement.requestFullscreen().catch(() => toast('浏览器拒绝进入全屏', 'warning'));
  }
  const settingsKey = () => Keymap.label(Keymap.binding('nav.settings'), { words: true });
  const MENUS = {
    file: () => [
      { label: '设置…', accel: settingsKey(), run: () => showView('settings') },
      '-',
      { label: '关闭窗口', accel: 'Ctrl+W', run: () => Desk.window('close'), show: deskHas('window') },
    ],
    edit: () => [
      { label: '撤销', accel: 'Ctrl+Z', run: () => edit('undo') },
      { label: '重做', accel: 'Ctrl+Y', run: () => edit('redo') },
      '-',
      { label: '剪切', accel: 'Ctrl+X', run: () => edit('cut') },
      { label: '复制', accel: 'Ctrl+C', run: () => edit('copy') },
      { label: '粘贴', accel: 'Ctrl+V', run: () => edit('paste') },
      { label: '粘贴并匹配样式', accel: 'Ctrl+Shift+V', run: () => edit('pasteAndMatchStyle') },
      { label: '删除', accel: '', run: () => edit('delete') },
      { label: '全选', accel: 'Ctrl+A', run: () => edit('selectAll') },
    ],
    view: () => [
      { label: '重新载入', accel: 'Ctrl+R', run: () => (deskHas('view') ? Desk.view('reload') : location.reload()) },
      { label: '强制重新载入', accel: 'Ctrl+Shift+R', run: () => Desk.view('forceReload'), show: deskHas('view') },
      '-',
      // 网页没法改浏览器自己的缩放；浏览器里这三项交给浏览器（Ctrl+0 / Ctrl+= / Ctrl+-），不在菜单里装样子
      { label: '实际大小', accel: 'Ctrl+0', run: () => Desk.view('resetZoom'), show: deskHas('view') },
      { label: '放大', accel: 'Ctrl+=', run: () => Desk.view('zoomIn'), show: deskHas('view') },
      { label: '缩小', accel: 'Ctrl+-', run: () => Desk.view('zoomOut'), show: deskHas('view') },
      '-',
      { label: '切换全屏', accel: 'F11', run: fullscreen },
    ],
    help: () => [
      { label: '检查更新', accel: '', run: checkUpdate },
      '-',
      { label: '关于 Gleam', accel: '', run: About.open },
    ],
  };
  function items(id) {
    const raw = MENUS[id]().filter((it) => it === '-' || it.show !== false);
    // 去掉首尾与相邻的分隔线（某些项被隐藏后）
    const out = [];
    raw.forEach((it) => { if (it === '-' && (!out.length || out[out.length - 1] === '-')) return; out.push(it); });
    while (out[out.length - 1] === '-') out.pop();
    return out;
  }
  function render(id) {
    pop.replaceChildren();
    items(id).forEach((it) => {
      if (it === '-') { pop.appendChild(el('div', 'mb-sep')); return; }
      const b = el('button', 'mb-item');
      b.type = 'button';
      b.setAttribute('role', 'menuitem');
      b.innerHTML = `<span class="mb-label">${esc(it.label)}</span><span class="mb-accel">${esc(it.accel || '')}</span>`;
      b.addEventListener('mousedown', (e) => e.preventDefault());
      b.addEventListener('click', () => { close(); it.run(); });
      pop.appendChild(b);
    });
  }
  function open(id, focusFirst) {
    const btn = bar.querySelector(`[data-menu="${id}"]`);
    if (!btn) return;
    if (!openId) focusBefore = document.activeElement;
    byKeyboard = !!focusFirst;
    openId = id;
    bar.querySelectorAll('.menubar-item').forEach((b) => b.setAttribute('aria-expanded', String(b === btn)));
    render(id);
    pop.hidden = false;
    pop.setAttribute('aria-label', btn.textContent);
    const r = btn.getBoundingClientRect();
    pop.style.left = Math.round(r.left) + 'px';
    pop.style.top = Math.round(r.bottom + 4) + 'px';
    if (focusFirst) { const f = pop.querySelector('.mb-item'); if (f) f.focus(); }
  }
  function close() {
    if (!openId) return;
    openId = null;
    pop.hidden = true;
    bar.querySelectorAll('.menubar-item').forEach((b) => b.setAttribute('aria-expanded', 'false'));
  }
  bar.querySelectorAll('.menubar-item').forEach((b) => {
    // mousedown 不抢焦点：编辑菜单要作用在原来那个输入框上
    b.addEventListener('mousedown', (e) => e.preventDefault());
    b.addEventListener('click', () => (openId === b.dataset.menu ? close() : open(b.dataset.menu)));
    b.addEventListener('mouseenter', () => { if (openId && openId !== b.dataset.menu) open(b.dataset.menu); });
    b.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(b.dataset.menu, true); }
    });
  });
  const order = () => [...bar.querySelectorAll('.menubar-item')].map((b) => b.dataset.menu);
  pop.addEventListener('keydown', (e) => {
    const list = [...pop.querySelectorAll('.mb-item')];
    const i = list.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') { e.preventDefault(); (list[(i + 1) % list.length] || list[0]).focus(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); (list[(i - 1 + list.length) % list.length] || list[0]).focus(); }
    else if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      const o = order(); const k = o.indexOf(openId);
      open(o[(k + (e.key === 'ArrowRight' ? 1 : -1) + o.length) % o.length], true);
    }
  });
  document.addEventListener('mousedown', (e) => {
    if (openId && !pop.contains(e.target) && !bar.contains(e.target)) close();
  }, true);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && openId) { e.preventDefault(); e.stopPropagation(); const b = bar.querySelector(`[data-menu="${openId}"]`); const kb = byKeyboard; close(); if (b && kb) b.focus(); }
  }, true);
  window.addEventListener('blur', close);
  window.addEventListener('resize', close);
  return { open, close };
})();

/* ============================================================
 * 检查更新：本机服务端去问 GitHub 的 release，前端只把三种结果说人话。
 *   · 有新版   → 摆出要下载的资产名与大小，用户点「立即更新」才下载并替换
 *   · 已是最新 → 直接说（这句现在是问出来的结论，不是替本机下判断）
 *   · 连不上   → 说清是网络/代理，别让人以为是版本问题
 * 下载与替换都在服务端做（见 internal/webui/update.go），前端递不进地址。
 * ============================================================ */
const UpdateCheck = (() => {
  const RELEASES = 'https://github.com/gleam-ai/Gleam/releases';

  function openPage(url) {
    const d = window.gleamDesktop;
    if (d && typeof d.openExternal === 'function') { d.openExternal(url).catch(() => {}); return; }
    window.open(url, '_blank', 'noopener');
  }
  function sizeMB(n) { return n > 0 ? (n / 1048576).toFixed(1) + ' MB' : '大小未知'; }

  async function run() {
    let r;
    try { r = await api('GET', '/api/update/check'); }
    catch (err) { return alertModal('没能问到更新情况：' + err.message + '。稍后再试一次。', '检查更新'); }
    const cur = r.current ? 'v' + r.current : '未知版本';
    if (r.error) return offline(r, cur);
    if (!r.has_update) {
      return alertModal('已是最新版本 ' + cur + '。\n\n发布页上还没有比它更新的版本，不用动它。', '检查更新');
    }
    newer(r, cur);
  }

  function offline(r, cur) {
    const why = r.error === 'rate_limit'
      ? '更新源说我们问得太频繁了，过几分钟再试。'
      : r.error === 'no_release'
        ? '发布页上还没有正式版本，暂时无从比较。'
        : '没能连上更新源。检查一下网络；如果走代理，确认 Gleam 能出网。';
    alertModal('本机版本 ' + cur + '。\n\n' + why, '检查更新');
  }

  function newer(r, cur) {
    Modal.open('检查更新', (box) => {
      const p = el('p', 'modal-text');
      p.textContent = '发现新版本 v' + r.latest + '，你现在是 ' + cur + '。';
      box.appendChild(p);

      if (r.notes) {
        box.appendChild(el('div', 'field-label', '这次更新了什么'));
        const pre = el('div', 'update-notes');
        pre.textContent = r.notes;
        box.appendChild(pre);
      }

      const hint = el('p', 'field-hint');
      hint.textContent = r.asset
        ? '将下载 ' + r.asset.name + '（' + sizeMB(r.asset.size) + '）并覆盖当前程序本身。对话、技能、密钥都存在「本地数据」目录里，换程序不影响它们；旧版本会留一份 .old 备份，想退回去改回扩展名即可。'
        : '最新版里没有这个平台的安装包，去发布页手动下载。';
      box.appendChild(hint);

      const actions = el('div', 'modal-actions');
      const page = el('button', 'btn btn-secondary', '去发布页');
      page.type = 'button';
      page.addEventListener('click', () => openPage(r.notes_url || RELEASES));
      const later = el('button', 'btn btn-secondary', r.asset ? '以后再说' : '知道了');
      later.type = 'button';
      later.addEventListener('click', Modal.close);
      actions.appendChild(page);
      actions.appendChild(later);
      if (r.asset) {
        const now = el('button', 'btn btn-primary', '立即更新');
        now.type = 'button';
        now.addEventListener('click', () => applyUpdate(now, r));
        actions.appendChild(now);
      }
      box.appendChild(actions);
    });
  }

  async function applyUpdate(btn, r) {
    btn.disabled = true;
    btn.textContent = '正在下载…';
    try {
      const res = await api('POST', '/api/update/apply', {});
      Modal.close();
      await alertModal('已更新到 v' + r.latest + '（下载的是 ' + (res.to || r.asset.name) + '）。\n\n' +
        '把 Gleam 关掉再打开，新版本就生效了。原文件留在 ' + (res.backup || '同目录的 .old 文件') +
        '，要退回旧版就把它的扩展名改回去。', '更新完成');
    } catch (err) {
      btn.disabled = false;
      btn.textContent = '立即更新';
      toast('更新失败：' + err.message, 'error');
    }
  }

  return { run };
})();

async function checkUpdate() { return UpdateCheck.run(); }

/* ============================================================
 * 关于 Gleam
 * ============================================================ */
const About = (() => {
  async function open() {
    let info = {};
    let desk = null;
    try { info = await api('GET', '/api/info'); } catch { /* 下方显示未知 */ }
    if (deskHas('info')) { try { desk = await Desk.info(); } catch { desk = null; } }
    Modal.open('关于 Gleam', (box) => {
      const rows = [
        ['版本', info.version ? 'v' + info.version : '未知'],
        ['运行方式', Desk ? '桌面版（Electron 外壳 + 本机服务）' : '浏览器（本机服务 ' + location.host + '）'],
      ];
      if (desk) {
        if (desk.electron) rows.push(['Electron', String(desk.electron)]);
        if (desk.chrome) rows.push(['Chromium', String(desk.chrome)]);
        if (desk.platform) rows.push(['平台', String(desk.platform) + (desk.arch ? ' / ' + desk.arch : '')]);
      }
      if (info.model) rows.push(['当前模型', String(info.model)]);
      const wrap = el('div', 'about');
      wrap.innerHTML = `
        <div class="about-head">
          <span class="about-mark" aria-hidden="true"><svg viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.6 5.6l2.2 2.2M16.2 16.2l2.2 2.2M5.6 18.4l2.2-2.2M16.2 7.8l2.2-2.2"/><circle cx="12" cy="12" r="3"/></svg></span>
          <div><strong>Gleam · 微光</strong><small>在本机运行的智能体工作台</small></div>
        </div>
        <dl class="about-rows">${rows.map(([k, v]) => `<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`).join('')}</dl>
        <p class="field-hint">对话、技能、记忆与密钥都存在这台电脑上。</p>
        <div class="modal-actions"><button class="btn btn-primary" type="button" id="about-ok">好</button></div>`;
      box.appendChild(wrap);
      wrap.querySelector('#about-ok').addEventListener('click', () => Modal.close());
    });
  }
  return { open };
})();

/* ============================================================
 * 窗口按钮（只在桌面壳的无边框窗口里出现；macOS 用系统红绿灯）
 * ============================================================ */
(function WindowControls() {
  const box = $('#win-controls');
  if (!deskHas('window') || Desk.platform === 'darwin') return;
  box.hidden = false;
  box.querySelectorAll('[data-win]').forEach((b) => b.addEventListener('click', () => Desk.window(b.dataset.win)));
  const paint = (s) => {
    const max = !!(s && s.maximized);
    document.documentElement.dataset.winMax = max ? 'true' : 'false';
    const b = box.querySelector('[data-win="maximize"]');
    b.title = max ? '向下还原' : '最大化';
    b.setAttribute('aria-label', b.title);
  };
  if (deskHas('onWindowState')) Desk.onWindowState(paint);
  if (deskHas('windowState')) Desk.windowState().then(paint).catch(() => {});
  // 双击拖拽区最大化 / 还原，与系统标题栏一致
  $('.topbar-drag').addEventListener('dblclick', () => Desk.window('maximize'));
})();

/* ============================================================
 * 悬浮提示：data-tip（文字）+ data-tip-key（键位表里的动作）
 * ============================================================ */
const ChromeTip = (() => {
  const tip = $('#chrome-tip');
  let timer = null;
  function show(t) {
    const key = t.dataset.tipKey ? Keymap.label(Keymap.binding(t.dataset.tipKey)) : '';
    tip.innerHTML = `<span>${esc(t.dataset.tip || '')}</span>${key ? `<span class="tip-key">${esc(key)}</span>` : ''}`;
    tip.hidden = false;
    const r = t.getBoundingClientRect();
    const tr = tip.getBoundingClientRect();
    let left; let top;
    if (t.dataset.tipSide === 'left') { left = r.left - tr.width - 8; top = r.top + (r.height - tr.height) / 2; }
    else { left = r.left + r.width / 2 - tr.width / 2; top = r.bottom + 6; }
    left = Math.max(6, Math.min(left, window.innerWidth - tr.width - 6));
    tip.style.left = Math.round(left) + 'px';
    tip.style.top = Math.round(top) + 'px';
  }
  function hide() { clearTimeout(timer); tip.hidden = true; }
  function bind(t) {
    t.addEventListener('mouseenter', () => { clearTimeout(timer); timer = setTimeout(() => show(t), 350); });
    t.addEventListener('mouseleave', hide);
    t.addEventListener('focus', () => show(t));
    t.addEventListener('blur', hide);
    t.addEventListener('click', hide);
  }
  document.querySelectorAll('[data-tip]').forEach(bind);
  return { refresh: (t) => { if (!tip.hidden && t && t.matches(':hover')) show(t); } };
})();

/* ============================================================
 * 右侧栏：三张入口卡
 * ============================================================ */
const AuxPanel = (() => {
  const aux = $('#aux');
  const btn = $('#aux-toggle');
  let workspace = '';
  function paint() {
    const open = !aux.hidden;
    document.documentElement.dataset.aux = open ? 'open' : 'closed';
    btn.setAttribute('aria-expanded', String(open));
    btn.dataset.tip = open ? '隐藏侧边栏' : '显示侧边栏';
    btn.setAttribute('aria-label', btn.dataset.tip);
    ChromeTip.refresh(btn);
    const files = $('#aux-files');
    files.disabled = !workspace;
    files.title = workspace ? '工作区：' + workspace : '先选择一个工作区';
    document.querySelectorAll('[data-kbd-for]').forEach((s) => {
      s.textContent = Keymap.label(Keymap.binding(s.dataset.kbdFor)).replace(/\+/g, ' ');
    });
  }
  function set(open) {
    aux.hidden = !open;
    UIPrefs.set({ auxOpen: open });
    paint();
  }
  function toggle() { set(aux.hidden); }
  function setWorkspace(ws) { workspace = ws || ''; paint(); }
  function files() {
    if (!workspace) { toast('先选择一个工作区，才能打开里面的文件', 'info'); openWorkspaceDialog(); return; }
    showView('goals');
    openFilePicker();
  }
  btn.addEventListener('click', toggle);
  $('#aux-files').addEventListener('click', files);
  $('#aux-browser').addEventListener('click', () => BrowserPane.open());
  $('#aux-term').addEventListener('click', () => Terminal.open(true));
  Keymap.onChange(paint);
  aux.hidden = !UIPrefs.get().auxOpen;
  paint();
  return { toggle, set, files, setWorkspace };
})();

/* ============================================================
 * 底部终端：一条命令 = 一次 shell.exec（经 POST /api/tools/call，走同一套安全门控）。
 * 不是交互式 PTY：没有 vim / top 这类全屏程序，长时间运行的命令受工具超时约束。
 * ============================================================ */
const Terminal = (() => {
  const panel = $('#term');
  const btn = $('#term-toggle');
  const log = $('#term-log');
  const input = $('#term-input');
  const cwdEl = $('#term-cwd');
  const note = $('#term-note');
  let workspace = '';
  let cwd = '';
  let busy = null;          // { cmd, approvalBox }
  const hist = [];
  let hi = 0;
  const MIN_H = 140;

  function paint() {
    const open = !panel.hidden;
    document.documentElement.dataset.term = open ? 'open' : 'closed';
    btn.setAttribute('aria-expanded', String(open));
    btn.dataset.tip = open ? '关闭终端面板' : '打开终端面板';
    btn.setAttribute('aria-label', btn.dataset.tip);
    ChromeTip.refresh(btn);
    const has = !!workspace;
    $('#term-empty').hidden = has;
    $('#term-body').hidden = !has;
    $('#term-clear').hidden = !has;
    cwdEl.textContent = has ? cwd : '';
    cwdEl.title = has ? cwd : '';
    note.textContent = has ? '每条命令经 Gleam 安全门控执行 · 非交互式' : '';
  }
  function applyHeight(h) {
    const max = Math.max(MIN_H, Math.round(window.innerHeight * 0.7));
    const v = Math.max(MIN_H, Math.min(max, Math.round(h)));
    document.documentElement.style.setProperty('--term-h', v + 'px');
    return v;
  }
  async function refreshWorkspace() {
    try {
      const ws = await api('GET', '/api/workspace');
      setWorkspace(ws.workspace || '');
    } catch { /* 连不上服务：保持现状 */ }
  }
  function setWorkspace(ws) {
    if (ws !== workspace) { workspace = ws; cwd = ws; }
    AuxPanel.setWorkspace(ws);
    paint();
  }
  function open(focus) {
    if (panel.hidden) {
      panel.hidden = false;
      UIPrefs.set({ termOpen: true });
      refreshWorkspace();
    }
    paint();
    if (focus) setTimeout(() => (workspace ? input.focus() : $('#term-pick-ws').focus()), 0);
  }
  function close() {
    if (panel.hidden) return;
    panel.hidden = true;
    UIPrefs.set({ termOpen: false });
    paint();
  }
  function toggle() { if (panel.hidden) open(true); else close(); }

  function line(cls, text) {
    const d = el('div', 'term-row ' + cls);
    d.textContent = text;
    log.appendChild(d);
    log.scrollTop = log.scrollHeight;
    return d;
  }
  async function exec(command, dir) {
    const res = await api('POST', '/api/tools/call', { name: 'shell.exec', args: { command, cwd: dir } });
    return (res && res.output) || {};
  }
  async function run(raw) {
    const cmd = raw.trim();
    if (!cmd || busy) return;
    hist.push(cmd); hi = hist.length;
    if (cmd === 'clear' || cmd === 'cls') { log.replaceChildren(); return; }
    line('term-cmd', '$ ' + cmd);
    busy = { cmd };
    input.disabled = true;
    const pending = line('term-pending', '运行中…');
    try {
      const cd = /^cd(?:\s+(.*))?$/.exec(cmd);
      if (cd) {
        // cd 在一次性的 shell 里不会留下来：让 shell 自己解析目标目录，再把结果当作下一条命令的 cwd
        const target = (cd[1] || '').trim() || workspace;
        const isWin = /^[A-Za-z]:\\/.test(workspace);
        const q = isWin ? `"${target.replace(/"/g, '')}"` : `'${target.replace(/'/g, `'\\''`)}'`;
        const out = await exec(isWin ? `cd /d ${q} && cd` : `cd ${q} && pwd`, cwd);
        pending.remove();
        if (out.exit_code === 0 && out.stdout) cwd = String(out.stdout).trim().split(/\r?\n/).pop();
        else line('term-err', (out.stderr || out.error || '目录切换失败').trim());
      } else {
        const out = await exec(cmd, cwd);
        pending.remove();
        if (out.stdout) line('term-out', String(out.stdout).replace(/\n$/, ''));
        if (out.stderr) line('term-err', String(out.stderr).replace(/\n$/, ''));
        if (out.exit_code) line('term-meta', '退出码 ' + out.exit_code);
        if (out.output_incomplete) line('term-meta', '输出可能不完整：有子进程仍持有输出管道');
      }
    } catch (err) {
      pending.remove();
      line('term-err', (err && err.message) || String(err));
    } finally {
      if (busy && busy.approvalBox) busy.approvalBox.remove();
      busy = null;
      input.disabled = false;
      paint();
      input.focus();
    }
  }
  // 终端发起的 shell.exec 需要批准时，审批就地出现在终端里
  function claimApproval(ap) {
    if (!busy || panel.hidden) return false;
    const plan = (ap.plan || []).join(' ');
    if (!/shell\.exec/.test(plan)) return false;
    const box = el('div', 'term-row term-approval');
    box.innerHTML = `<span>这条命令需要你的批准${ap.reason ? '：' + esc(ap.reason) : ''}</span>`;
    const yes = el('button', 'btn btn-primary btn-sm', '批准执行');
    const no = el('button', 'btn btn-secondary btn-sm', '拒绝');
    yes.type = 'button'; no.type = 'button';
    yes.addEventListener('click', () => resolveApproval(ap.id, true, box, null));
    no.addEventListener('click', () => resolveApproval(ap.id, false, box, null));
    box.append(yes, no);
    log.appendChild(box);
    log.scrollTop = log.scrollHeight;
    busy.approvalBox = box;
    yes.focus();
    return true;
  }

  $('#term-form').addEventListener('submit', (e) => { e.preventDefault(); const v = input.value; input.value = ''; run(v); });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowUp') { if (hi > 0) { hi--; input.value = hist[hi]; e.preventDefault(); } }
    else if (e.key === 'ArrowDown') { if (hi < hist.length) { hi++; input.value = hist[hi] || ''; e.preventDefault(); } }
  });
  btn.addEventListener('click', toggle);
  $('#term-close').addEventListener('click', close);
  $('#term-clear').addEventListener('click', () => log.replaceChildren());
  $('#term-pick-ws').addEventListener('click', () => openWorkspaceDialog());

  // 拖动顶边改高度（指针 + 键盘上下键）
  const grip = $('#term-resize');
  grip.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    grip.setPointerCapture(e.pointerId);
    const startY = e.clientY;
    const startH = panel.getBoundingClientRect().height;
    document.documentElement.classList.add('is-resizing');
    const move = (ev) => applyHeight(startH + (startY - ev.clientY));
    const up = () => {
      grip.removeEventListener('pointermove', move);
      grip.removeEventListener('pointerup', up);
      document.documentElement.classList.remove('is-resizing');
      UIPrefs.set({ termH: Math.round(panel.getBoundingClientRect().height) });
    };
    grip.addEventListener('pointermove', move);
    grip.addEventListener('pointerup', up);
  });
  grip.addEventListener('keydown', (e) => {
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return;
    e.preventDefault();
    const h = applyHeight(panel.getBoundingClientRect().height + (e.key === 'ArrowUp' ? 24 : -24));
    UIPrefs.set({ termH: h });
  });

  // 工作区在别处被切换时（输入框下方 / 弹窗），终端跟着换主文件夹
  const origRender = renderWorkspace;
  // eslint-disable-next-line no-global-assign
  renderWorkspace = function (ws) { origRender(ws); setWorkspace((ws && ws.workspace) || ''); };

  applyHeight(UIPrefs.get().termH || 260);
  panel.hidden = !UIPrefs.get().termOpen;
  refreshWorkspace();
  paint();
  return { open, close, toggle, claimApproval };
})();

/* ============================================================
 * 页内查找（搜索当前任务）：Chromium 的 window.find，命中项由浏览器高亮
 * ============================================================ */
const FindBar = (() => {
  const bar = $('#findbar');
  const input = $('#findbar-input');
  const count = $('#findbar-count');
  function open() {
    bar.hidden = false;
    input.focus();
    input.select();
  }
  function close() {
    bar.hidden = true;
    const s = window.getSelection && window.getSelection();
    if (s) s.removeAllRanges();
  }
  function find(back) {
    const q = input.value;
    if (!q) { count.textContent = ''; return; }
    if (typeof window.find !== 'function') { count.textContent = '当前环境不支持页内查找'; return; }
    // 先把焦点让给页面，否则 find 会在输入框自己里面找
    const ok = window.find(q, false, !!back, true, false, false, false);
    count.textContent = ok ? '' : '没有匹配';
    input.focus();
  }
  input.addEventListener('keydown', (e) => {
    if (e.isComposing) return;
    if (e.key === 'Enter') { e.preventDefault(); find(e.shiftKey); }
    else if (e.key === 'Escape') { e.preventDefault(); close(); }
  });
  $('#findbar-next').addEventListener('click', () => find(false));
  $('#findbar-prev').addEventListener('click', () => find(true));
  $('#findbar-close').addEventListener('click', close);
  return { open, close };
})();

/* ============================================================
 * 设置 → 快捷键
 * ============================================================ */
const ShortcutsPage = (() => {
  const root = $('#kb-page');
  let query = '';
  let editing = null;   // { id, combo, conflict }
  const PEN = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>';
  const UNDO = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7v6h6"/><path d="M21 17a9 9 0 0 0-15-6.7L3 13"/></svg>';

  function hay(a) {
    return [a.title, a.desc, Keymap.binding(a.id), Keymap.label(Keymap.binding(a.id))].join(' ').toLowerCase();
  }
  function render() {
    if (!root) return;
    const q = query.trim().toLowerCase();
    const total = Keymap.total();
    const custom = Keymap.customCount();
    const sendHit = !q || '发送消息 enter ctrl 换行 输入'.includes(q) || q.includes('enter');
    let html = `
      <header class="kb-head">
        <h2 class="kb-title">快捷键</h2>
        <p class="kb-desc">搜索、查看并修改 Gleam 内置命令的快捷键。修改后会立即保存在当前设备。</p>
      </header>
      <div class="kb-section-label">应用快捷键</div>
      <div class="kb-card kb-search-card">
        <div class="kb-row-text"><strong>搜索快捷键</strong><small>共 ${total} 个快捷键，${custom} 个已自定义。</small></div>
        <div class="kb-search">
          <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>
          <input type="search" id="kb-q" placeholder="搜索命令、说明或组合键" value="${esc(query)}" autocomplete="off" spellcheck="false" aria-label="搜索快捷键">
        </div>
        ${custom ? '<button class="btn btn-ghost btn-sm kb-reset-all" type="button" id="kb-reset-all">全部恢复默认</button>' : ''}
      </div>`;
    if (sendHit) {
      html += `
      <div class="kb-section-label">输入</div>
      <div class="kb-card">
        <div class="kb-row">
          <div class="kb-row-text"><strong>发送消息</strong><small>${Keymap.sendMode() === 'enter' ? '在任务输入框按 Enter 发送，Shift+Enter 换行。' : '在任务输入框按 Ctrl+Enter 发送，Enter 换行。'}</small></div>
          <label class="kb-select"><span class="sr-only">发送键</span>
            <select id="kb-send">
              <option value="enter"${Keymap.sendMode() === 'enter' ? ' selected' : ''}>Enter</option>
              <option value="ctrlEnter"${Keymap.sendMode() === 'ctrlEnter' ? ' selected' : ''}>Ctrl+Enter</option>
            </select>
          </label>
        </div>
      </div>`;
    }
    let shown = sendHit ? 1 : 0;
    Keymap.GROUPS.forEach(([gid, gname]) => {
      const list = Keymap.ACTIONS.filter((a) => a.group === gid && (!q || hay(a).includes(q)));
      if (!list.length) return;
      shown += list.length;
      html += `<div class="kb-section-label">${esc(gname)}</div><div class="kb-card">`;
      list.forEach((a) => {
        const combo = Keymap.binding(a.id);
        const isEdit = editing && editing.id === a.id;
        const reserved = a.browserReserved && !Desk ? '<span class="kb-flag" title="浏览器会先拦下这个组合键；在桌面版里生效">浏览器内被占用</span>' : '';
        html += `<div class="kb-row${isEdit ? ' is-editing' : ''}" data-id="${a.id}">
          <div class="kb-row-text"><strong>${esc(a.title)}${reserved}</strong><small>${esc(a.desc)}</small></div>
          <div class="kb-ctl">`;
        if (isEdit) {
          const c = editing.combo;
          html += `<span class="kb-rec" tabindex="0" id="kb-rec" aria-live="polite">${c ? Keymap.kbdHTML(c) : '<span class="kb-hint">按下新的组合键…</span>'}</span>`;
          if (editing.error) html += `<span class="kb-warn">${esc(editing.error)}</span>`;
          if (editing.conflict) html += `<button class="btn btn-secondary btn-sm" type="button" data-act="swap">替换</button>`;
          html += `<button class="btn btn-primary btn-sm" type="button" data-act="save"${c && !editing.conflict && !editing.error ? '' : ' disabled'}>保存</button>
            <button class="btn btn-ghost btn-sm" type="button" data-act="clear">清除</button>
            <button class="btn btn-ghost btn-sm" type="button" data-act="cancel">取消</button>`;
        } else {
          html += `<span class="kb-keys">${Keymap.kbdHTML(combo)}</span>`;
          if (Keymap.isCustom(a.id)) html += `<button class="kb-icon" type="button" data-act="reset" title="恢复默认（${esc(Keymap.label(a.def))}）" aria-label="恢复默认">${UNDO}</button>`;
          html += `<button class="kb-icon" type="button" data-act="edit" title="修改快捷键" aria-label="修改「${esc(a.title)}」的快捷键">${PEN}</button>`;
        }
        html += '</div></div>';
      });
      html += '</div>';
    });
    if (!shown) html += '<p class="kb-empty">没有匹配的快捷键</p>';
    html += `<p class="kb-foot">${Desk ? '桌面版里所有快捷键都由 Gleam 接管。' : '在浏览器里，Ctrl+N / Ctrl+T / Ctrl+Shift+N 这类组合键会先被浏览器拦下；换一个组合，或使用桌面版。'}</p>`;
    root.innerHTML = html;
    const qi = $('#kb-q', root);
    qi.addEventListener('input', () => { query = qi.value; const pos = qi.selectionStart; render(); const n = $('#kb-q', root); n.focus(); n.setSelectionRange(pos, pos); });
    const send = $('#kb-send', root);
    if (send) send.addEventListener('change', () => Keymap.setSendMode(send.value));
    const ra = $('#kb-reset-all', root);
    if (ra) ra.addEventListener('click', async () => { if (await confirmModal('把所有快捷键恢复为默认值？', '恢复默认')) Keymap.resetAll(); });
    root.querySelectorAll('.kb-row[data-id] [data-act]').forEach((b) => b.addEventListener('click', () => act(b.closest('.kb-row').dataset.id, b.dataset.act)));
    const rec = $('#kb-rec', root);
    if (rec) { rec.addEventListener('keydown', onRecordKey); rec.focus(); }
  }
  function check(id, combo) {
    editing.combo = combo;
    editing.conflict = null;
    editing.error = '';
    if (!combo) return;
    if (!Keymap.acceptable(combo)) { editing.error = '至少要带 Ctrl 或 Alt（F1–F12 除外）'; return; }
    const o = Keymap.owner(combo, id);
    if (o) { editing.conflict = o.id; editing.error = `已被「${o.title}」使用`; }
  }
  function onRecordKey(e) {
    if (['Control', 'Shift', 'Alt', 'Meta'].includes(e.key)) return;
    e.preventDefault(); e.stopPropagation();
    if (e.key === 'Escape' && !e.ctrlKey && !e.altKey) { act(editing.id, 'cancel'); return; }
    if (e.key === 'Enter' && !e.ctrlKey && !e.altKey && !e.shiftKey && editing.combo && !editing.conflict && !editing.error) { act(editing.id, 'save'); return; }
    check(editing.id, Keymap.comboOf(e));
    render();
  }
  function act(id, what) {
    if (what === 'edit') { editing = { id, combo: '', conflict: null, error: '' }; Keymap.setRecording(true); render(); return; }
    if (what === 'reset') { Keymap.reset(id); return; }
    if (what === 'cancel') { editing = null; Keymap.setRecording(false); render(); return; }
    if (what === 'clear') { editing = null; Keymap.setRecording(false); Keymap.setBinding(id, ''); return; }
    if (what === 'save' && editing && editing.combo) {
      const c = editing.combo; editing = null; Keymap.setRecording(false); Keymap.setBinding(id, c); return;
    }
    if (what === 'swap' && editing && editing.conflict) {
      // 替换：把冲突那一项清空（显示「未设置」，随时可恢复默认），再绑定当前这一项
      const c = editing.combo; const other = editing.conflict;
      editing = null; Keymap.setRecording(false);
      Keymap.setBinding(other, '');
      Keymap.setBinding(id, c);
      toast(`「${Keymap.action(other).title}」的快捷键已清除`, 'info');
    }
  }
  Keymap.onChange(render);
  // 离开快捷键页时不留在录制状态
  document.addEventListener('click', (e) => {
    // 点击的按钮可能已在重绘时被换掉（isConnected=false），那不算「点到外面」
    if (editing && e.target.isConnected && !root.contains(e.target)) { editing = null; Keymap.setRecording(false); render(); }
  });
  render();
  return { render };
})();

Keymap.init();
