/* Gleam 设置 v2：整页设置的分组导航、行式卡片与各分区页面。
 * 规则：页面上的每个开关都接到真实设置（/api/settings 等）或会真的生效的本机偏好；
 * Gleam 没有的能力要么不出现，要么灰掉并标「暂不支持」——不放假数据、不放摆设。
 * 依赖 app.js（api / toast / esc / UIPrefs / loadSettings …）与 chrome.js（Keymap / Desk）的全局。 */
'use strict';

const S2 = (() => {
  /* ---------- 图标：线性 24 栅格，自绘，不引外部品牌图 ---------- */
  const P = {
    profile: '<circle cx="12" cy="12" r="9"/><circle cx="12" cy="10" r="3"/><path d="M6.5 18.5c1.3-2 3.2-3 5.5-3s4.2 1 5.5 3"/>',
    general: '<circle cx="12" cy="12" r="3"/><path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M5.3 18.7l2.1-2.1M16.6 7.4l2.1-2.1"/>',
    modes: '<path d="M4 7h10M4 17h6"/><circle cx="17" cy="7" r="3"/><circle cx="13" cy="17" r="3"/><path d="M20 17h0"/>',
    monitor: '<path d="M4 6h3M4 12h3M4 18h3M10 6h10M10 12h10M10 18h10"/>',
    shortcuts: '<rect x="2.5" y="6" width="19" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10"/>',
    appearance: '<path d="M12 3a9 9 0 1 0 0 18c1.1 0 1.5-.8 1.5-1.6 0-1.3-1-1.6-1-2.9 0-1 .8-1.5 1.8-1.5H17a4 4 0 0 0 4-4C21 6.6 17 3 12 3z"/><circle cx="7.5" cy="11" r="1"/><circle cx="10" cy="7" r="1"/><circle cx="15" cy="7.5" r="1"/>',
    llm: '<path d="M12 3 4 7.5v9L12 21l8-4.5v-9z"/><path d="M4 7.5 12 12l8-4.5M12 12v9"/>',
    memory: '<path d="M9 4a3 3 0 0 0-3 3 3 3 0 0 0-2 5 3 3 0 0 0 2 5 3 3 0 0 0 3 3h0a3 3 0 0 0 3-3V7a3 3 0 0 0-3-3z"/><path d="M15 4a3 3 0 0 1 3 3 3 3 0 0 1 2 5 3 3 0 0 1-2 5 3 3 0 0 1-3 3h0a3 3 0 0 1-3-3"/>',
    ext: '<rect x="4" y="4" width="6" height="6" rx="1.5"/><rect x="14" y="4" width="6" height="6" rx="1.5"/><rect x="4" y="14" width="6" height="6" rx="1.5"/><path d="M17 14v6M14 17h6"/>',
    hooks: '<circle cx="12" cy="5" r="2"/><path d="M12 7v9a4 4 0 0 1-8 0v-2M4 14l-1.5 1.5M4 14l1.5 1.5"/>',
    computer: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
    git: '<circle cx="6" cy="6" r="2"/><circle cx="6" cy="18" r="2"/><circle cx="18" cy="8" r="2"/><path d="M6 8v8M18 10c0 4-6 3-12 6"/>',
    worktrees: '<path d="M12 21V11M12 11 7 6M12 11l5-5M7 6V3M17 6V3"/><circle cx="12" cy="21" r="0.5"/>',
    index: '<ellipse cx="12" cy="6" rx="7" ry="3"/><path d="M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6"/>',
    conn: '<path d="m13 3-8 11h7l-1 7 8-11h-7z"/>',
    safety: '<path d="M12 3 5 6v5c0 4.6 3 8.4 7 10 4-1.6 7-5.4 7-10V6z"/>',
    go: '<path d="M4 17 10 11 4 5M12 19h8"/>',
    engine: '<rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4"/>',
    archived: '<rect x="3" y="4" width="18" height="5" rx="1.5"/><path d="M5 9v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9M10 13h4"/>',
    labs: '<path d="M9 3h6M10 3v6L4.5 18.5A1.7 1.7 0 0 0 6 21h12a1.7 1.7 0 0 0 1.5-2.5L14 9V3"/><path d="M7 15h10"/>',
    network: '<path d="M3 12h4l2.5-6 5 12 2.5-6h4"/>',
    // 行内图标
    send: '<path d="M21 3 10 14M21 3l-7 18-4-7-7-4z"/>',
    check: '<path d="M20 6 9 17l-5-5"/>',
    compress: '<path d="M4 14h6v6M20 10h-6V4M14 10l7-7M3 21l7-7"/>',
    spark: '<path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6"/>',
    clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    calendar: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 10h18M8 3v4M16 3v4"/>',
    user: '<circle cx="12" cy="8" r="4"/><path d="M4 21c0-4 3.6-6 8-6s8 2 8 6"/>',
    chat: '<path d="M4 5h16v11H8l-4 4z"/>',
    filter: '<path d="M3 5h18l-7 8v6l-4 2v-8z"/>',
    copy: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>',
    layers: '<path d="m12 3 9 5-9 5-9-5z"/><path d="m3 13 9 5 9-5"/>',
    type: '<path d="M4 7V5h16v2M9 19h6M12 5v14"/>',
    width: '<path d="M3 12h18M7 8l-4 4 4 4M17 8l4 4-4 4"/>',
    blur: '<circle cx="12" cy="12" r="9"/><path d="M12 3v18" stroke-dasharray="2 2"/>',
    lang: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.8 3.8 5.8 3.8 9S14.5 18.2 12 21c-2.5-2.8-3.8-5.8-3.8-9S9.5 5.8 12 3z"/>',
    zoom: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5M8 11h6M11 8v6"/>',
    key: '<circle cx="8" cy="15" r="4"/><path d="m10.8 12.2 8.7-8.7M16 7l3 3M14 9l2 2"/>',
    eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z"/><circle cx="12" cy="12" r="3"/>',
    eyeOff: '<path d="M3 3l18 18M10.6 5.1A9.8 9.8 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.1 3.9M6.6 6.6C3.8 8.4 2 12 2 12s3.5 7 10 7c1.7 0 3.2-.5 4.5-1.2"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
    trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
    edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    refresh: '<path d="M20 11a8 8 0 0 0-14.5-4.5L4 8M4 4v4h4M4 13a8 8 0 0 0 14.5 4.5L20 16M20 20v-4h-4"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
    x: '<path d="M18 6 6 18M6 6l12 12"/>',
    chevron: '<path d="m6 9 6 6 6-6"/>',
    book: '<path d="M4 5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2z"/><path d="M4 19V5"/>',
    warn: '<path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><path d="M12 9v4M12 17h.01"/>',
    link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
    folder: '<path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
    server: '<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/>',
    globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.8 3.8 5.8 3.8 9S14.5 18.2 12 21"/>',
    out: '<path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>',
    logout: '<path d="M15 4h4a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-4M10 8l-4 4 4 4M6 12h10"/>',
    branch: '<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="7" r="2"/><path d="M6 7v10M18 9c0 5-12 3-12 8"/>',
    shield: '<path d="M12 3 5 6v5c0 4.6 3 8.4 7 10 4-1.6 7-5.4 7-10V6z"/><path d="m9 12 2 2 4-4"/>',
    scan: '<path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3M4 12h16"/>',
    bell: '<path d="M6 16V11a6 6 0 1 1 12 0v5l2 2H4z"/><path d="M10 21h4"/>',
    clipboard: '<rect x="6" y="4" width="12" height="17" rx="2"/><path d="M9 4h6v3H9z"/>',
    camera: '<path d="M4 8h3l2-3h6l2 3h3v11H4z"/><circle cx="12" cy="13" r="3.5"/>',
    snippet: '<path d="M8 6 3 12l5 6M16 6l5 6-5 6"/>',
    mouse: '<rect x="7" y="3" width="10" height="18" rx="5"/><path d="M12 7v3"/>',
    rail: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M15 4v16"/>',
    pulse: '<path d="M3 12h4l2.5-6 5 12 2.5-6h4"/>',
    gauge: '<path d="M4 15a8 8 0 1 1 16 0"/><path d="m12 15 4-5"/>',
    cpu: '<rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M10 3v4M14 3v4M10 17v4M14 17v4"/>',
    flask: '<path d="M9 3h6M10 3v6L4.5 18.5A1.7 1.7 0 0 0 6 21h12a1.7 1.7 0 0 0 1.5-2.5L14 9V3"/>',
    terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3M13 15h4"/>',
    robot: '<rect x="5" y="8" width="14" height="11" rx="2"/><path d="M12 4v4M9 13h.01M15 13h.01M3 13v2M21 13v2"/>',
    plug: '<path d="M9 7V3M15 7V3M6 7h12v4a6 6 0 0 1-12 0zM12 17v4"/>',
    wand: '<path d="m4 20 11-11M14 4l1.5 3L19 8.5 15.5 10 14 13l-1.5-3L9 8.5 12.5 7z"/>',
    style: '<path d="M4 6h16M4 12h10M4 18h7"/>',
    name: '<path d="M4 20h16M6 16 12 4l6 12M8.5 11h7"/>',
  };
  function ico(name, size = 15) {
    return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${P[name] || ''}</svg>`;
  }

  /* ---------- 小型 DOM 工具 ---------- */
  function h(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (v == null || v === false) continue;
        if (k === 'class') e.className = v;
        else if (k === 'html') e.innerHTML = v;
        else if (k === 'text') e.textContent = v;
        else if (k.startsWith('on') && typeof v === 'function') e.addEventListener(k.slice(2), v);
        else if (k === 'dataset') Object.assign(e.dataset, v);
        else e.setAttribute(k, v === true ? '' : v);
      }
    }
    for (const kid of kids.flat()) {
      if (kid == null || kid === false) continue;
      e.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
    }
    return e;
  }
  const badge = (text = '暂不支持', kind = '') => h('span', { class: 's2-badge' + (kind ? ' s2-badge--' + kind : ''), text });
  function head(title, desc, right) {
    return h('header', { class: 's2-head' },
      h('div', { class: 's2-head-text' }, h('h2', { class: 's2-title', text: title }), desc ? h('p', { class: 's2-desc', text: desc }) : null),
      right || null);
  }
  const label = (text) => h('div', { class: 's2-section-label', text });
  const card = (...rows) => h('div', { class: 's2-card' }, ...rows);
  /** 一行：图标 / 标题与说明 / 右侧控件。disabled 时整行灰掉并挂「暂不支持」。 */
  function row({ icon, title, desc, control, disabled, note, cls }) {
    const r = h('div', { class: 's2-row' + (disabled ? ' is-disabled' : '') + (cls ? ' ' + cls : '') },
      icon ? h('span', { class: 's2-row-ico', html: ico(icon) }) : null,
      h('div', { class: 's2-row-text' },
        h('strong', { class: 's2-row-title' }, title, disabled ? badge(typeof disabled === 'string' ? disabled : '暂不支持') : null),
        desc ? h('small', { class: 's2-row-desc' }, desc) : null,
        note || null),
      control ? h('div', { class: 's2-row-ctl' }, control) : null);
    return r;
  }
  function toggle(checked, onChange, { disabled, labelText } = {}) {
    const input = h('input', { type: 'checkbox', class: 'switch', role: 'switch', 'aria-label': labelText || '开关' });
    input.checked = !!checked;
    input.disabled = !!disabled;
    input.addEventListener('change', async () => {
      input.disabled = true;
      try { await onChange(input.checked); } catch (err) { input.checked = !input.checked; toast(err.message || String(err), 'error'); }
      finally { input.disabled = !!disabled; }
    });
    return input;
  }
  function select(options, value, onChange, { disabled, labelText } = {}) {
    const s = h('select', { class: 's2-select', 'aria-label': labelText || '选择' });
    for (const o of options) {
      const opt = h('option', { value: o.value, text: o.label });
      if (o.disabled) opt.disabled = true;
      s.append(opt);
    }
    s.value = value;
    s.disabled = !!disabled;
    s.addEventListener('change', async () => {
      const prev = value;
      try { await onChange(s.value); value = s.value; } catch (err) { s.value = prev; toast(err.message || String(err), 'error'); }
    });
    return s;
  }
  const btn = (text, onClick, cls = 'btn btn-secondary btn-sm', attrs = {}) => h('button', Object.assign({ type: 'button', class: cls, onclick: onClick }, attrs), text);
  function empty(iconName, title, desc, action) {
    return h('div', { class: 's2-empty' },
      h('span', { class: 's2-empty-ico', html: ico(iconName, 22) }),
      h('strong', { text: title }), desc ? h('small', { text: desc }) : null, action || null);
  }
  const openLink = (url) => {
    if (deskHas('openExternal')) { Desk.openExternal(url); return; }
    const w = window.open(url, '_blank', 'noopener');
    if (!w) toast('浏览器拦下了新窗口：' + url, 'warning');
  };

  /* ---------- 设置读写：一律走 /api/settings，保存后让旧表单与运行态同步 ---------- */
  let SET = null;
  async function settings(force) {
    if (!SET || force) SET = await api('GET', '/api/settings');
    return SET;
  }
  async function save(patch, okText = '已保存') {
    const s = await api('POST', '/api/settings', patch);
    SET = s;
    try { syncRuntimeState(s); } catch { /* 旧页面元素缺失时忽略 */ }
    try { loadSettings(); } catch { /* 同上 */ }
    if (okText) toast(okText, 'success', 1800);
    return s;
  }

  /* ---------- 导航：给每个分区挂图标 ---------- */
  function decorateNav() {
    document.querySelectorAll('.settings-nav .settings-tab').forEach((t) => {
      if (t.querySelector('.s2-nav-ico')) return;
      const name = t.textContent.trim();
      t.textContent = '';
      t.append(h('span', { class: 's2-nav-ico', html: ico(t.dataset.stab, 15) }), h('span', { class: 's2-nav-label', text: name }));
    });
  }

  /* ============================== 个人资料 ============================== */
  async function renderProfile() {
    const acc = await api('GET', '/api/account').catch(() => ({}));
    const pref = UIPrefs.get();
    const name = (acc.signed_in && (acc.display_name || acc.email)) || pref.localName || ($('#me-name') && $('#me-name').textContent) || '我的';
    $('#sp-name').textContent = name;
    $('#sp-avatar').textContent = initial(name);
    const tag = $('#sp-tag');
    tag.hidden = false;
    tag.textContent = acc.signed_in ? (acc.provider ? `${acc.provider} 登录` : '已登录') : '本地模式';
    $('#sp-sub').textContent = acc.signed_in ? (acc.email || '已登录') : '数据只在这台电脑';
    const box = $('#sp-account-card');
    box.replaceChildren();
    if (acc.signed_in) {
      box.append(
        row({ icon: 'user', title: '当前账户', desc: acc.email || name, control: btn('管理', () => MeDrawer.open()) }),
        row({ icon: 'logout', title: '退出登录', desc: '清除本机登录凭证；本地任务和设置不会被删除。',
          control: btn('退出登录', () => $('#me-signout').click(), 'btn btn-danger-soft btn-sm s2-danger') }));
    } else {
      box.append(
        row({ icon: 'user', title: '当前计划', desc: 'Gleam 在本机运行，没有订阅计划；模型费用由你配置的厂商直接结算。', control: badge('本地版', 'muted') }),
        row({ icon: 'logout', title: '账户', desc: acc.configured ? '登录后可同步账户信息；不登录也能使用全部本地功能。' : '这台电脑没有配置账户服务，所有数据都只保存在本机。',
          control: acc.configured ? btn('登录', () => MeDrawer.open()) : badge('未配置', 'muted') }));
    }
    const editBtn = $('#sp-edit');
    editBtn.hidden = !!acc.signed_in;
    editBtn.onclick = async () => {
      const v = await promptModal('显示名称（只保存在本机，用于侧栏和个人资料）', pref.localName || '', '编辑个人资料');
      if (v == null) return;
      UIPrefs.set({ localName: v || undefined });
      applyLocalName();
      renderProfile();
    };
  }
  function initial(name) {
    const ch = [...String(name || 'G').trim()][0] || 'G';
    return ch.toUpperCase();
  }
  function applyLocalName() {
    const n = UIPrefs.get().localName;
    const me = $('#me-name');
    const signed = $('#me-user') && !$('#me-user').hidden;
    if (me && !signed) me.textContent = n || '我的';
  }

  /* ============================== 常规 ============================== */
  async function renderGeneral() {
    const root = $('#s2-general');
    const s = await settings(true);
    const a = s.agent || {};
    const nameInput = h('input', { class: 'input s2-input', value: s.persona.name || '', 'aria-label': '助手名称', maxlength: 32 });
    const commitName = async () => {
      const v = nameInput.value.trim();
      if (!v || v === (SET.persona || {}).name) return;
      await save({ persona: { name: v } }, '助手名称已保存');
    };
    nameInput.addEventListener('change', () => commitName().catch((e) => toast(e.message, 'error')));
    const agentToggle = (key, ok) => toggle(a[key], (v) => save({ agent: { [key]: v } }, ok || null));
    root.replaceChildren(
      head('常规', '任务执行、助手风格与后台调度。改动即时生效，重启后依然保留。'),
      label('任务与工具'),
      card(
        row({ icon: 'send', title: '发送方式', desc: '在任务输入框里用哪个键发送；另一种组合用来换行。快捷键页里的同一设置会同步。',
          control: select([{ value: 'enter', label: 'Enter' }, { value: 'ctrlEnter', label: 'Ctrl+Enter' }], Keymap.sendMode(), (v) => Keymap.setSendMode(v), { labelText: '发送方式' }) }),
        row({ icon: 'check', title: '对话回答后独立自检', desc: '由辅助模型核对回答是否达成目标，未达成如实判「部分完成」。需要配置辅助模型。', control: agentToggle('chat_acceptance') }),
        row({ icon: 'filter', title: '低价值调用去重', desc: '相同参数的只读调用只跑一次，已知失败的重复调用直接跳过。', control: agentToggle('dedupe_calls') }),
        row({ icon: 'compress', title: '会话上下文自动压缩', desc: '窗口外的旧对话自动摘要，省 token 不丢上下文。', control: agentToggle('context_compress') }),
        row({ icon: 'wand', title: '技能自进化', desc: '技能运行失败后自动修订参数并保存为新版本。', control: agentToggle('skill_auto_optimize') }),
        row({ icon: 'globe', title: 'GEO 可见度分析', desc: '允许「GEO」页对你的站点做生成式搜索可见度分析（会调用模型）。', control: agentToggle('geo_enabled') }),
      ),
      label('助手'),
      card(
        row({ icon: 'name', title: '助手名称', desc: '任务回复与通知里助手的称呼。', control: nameInput }),
        row({ icon: 'style', title: '协作风格', desc: '影响回复的措辞：严谨精确、温和平实或直击要点。',
          control: select([{ value: 'rigorous', label: '严谨' }, { value: 'gentle', label: '温和' }, { value: 'efficient', label: '高效' }], s.persona.style || 'efficient', (v) => save({ persona: { style: v } }, '协作风格已保存'), { labelText: '协作风格' }) }),
      ),
      label('审批与调度'),
      card(
        row({ icon: 'clock', title: '审批超时', desc: '等待你批准的操作超过这个时间未答复，将自动拒绝。',
          control: select(timeoutOptions(s.safety.approval_timeout_seconds), String(s.safety.approval_timeout_seconds), (v) => save({ safety: { approval_timeout_seconds: Number(v) } }), { labelText: '审批超时' }) }),
        row({ icon: 'calendar', title: '定时任务调度', desc: '关闭后已创建的定时任务都不会自动触发（仍可手动运行）。', control: toggle(s.scheduler && s.scheduler.enabled, (v) => save({ scheduler: { enabled: v } })) }),
      ),
    );
  }
  function timeoutOptions(cur) {
    const base = [60, 120, 300, 600, 1800, 3600];
    if (cur && !base.includes(cur)) base.push(cur);
    return base.sort((x, y) => x - y).map((v) => ({ value: String(v), label: v < 60 ? `${v} 秒` : `${Math.round(v / 60 * 10) / 10} 分钟` }));
  }

  /* ============================== 模式配置 ============================== */
  const THEMES = [
    { id: 'gleam', name: '微光', sw: '#D7FB58' }, { id: 'emerald', name: '森林', sw: '#34D399' },
    { id: 'mint', name: '薄荷', sw: '#5FD3A6' }, { id: 'bee', name: '蜜蜂', sw: '#E8C547' },
    { id: 'parchment', name: '羊皮纸', sw: '#E5825E' },
  ];
  const EXTRA_ACCENTS = [
    { id: 'violet', name: '星云紫', sw: '#8B7CF6' }, { id: 'blue', name: '晴空蓝', sw: '#4C8DFF' },
    { id: 'amber', name: '暖阳橙', sw: '#F5A524' }, { id: 'rose', name: '樱粉', sw: '#F472B6' },
  ];
  const MODE_NAMES = { code: '编程', work: '通用' };
  const currentMode = () => {
    const b = document.querySelector('#mode-toggle button[aria-checked="true"]');
    return b ? b.dataset.mode : 'work';
  };
  function applyModeBinding(mode) {
    const bind = (UIPrefs.get().modeBind || {})[mode] || {};
    if (bind.accent && bind.accent !== UIPrefs.get().accent) UIPrefs.set({ accent: bind.accent });
    if (bind.perm) {
      const cur = document.querySelector('#perm-seg button[aria-pressed="true"]');
      if (!cur || cur.dataset.perm !== bind.perm) { try { setPerm(bind.perm); } catch { /* 权限控件不可用 */ } }
    }
  }
  function watchMode() {
    let last = currentMode();
    const mo = new MutationObserver(() => {
      const m = currentMode();
      if (m !== last) { last = m; applyModeBinding(m); }
    });
    document.querySelectorAll('#mode-toggle button[data-mode]').forEach((b) => mo.observe(b, { attributes: true, attributeFilter: ['aria-checked'] }));
  }
  function renderModes() {
    const root = $('#s2-modes');
    const binds = UIPrefs.get().modeBind || {};
    const setBind = (mode, patch) => {
      const all = Object.assign({}, UIPrefs.get().modeBind || {});
      all[mode] = Object.assign({}, all[mode] || {}, patch);
      Object.keys(all[mode]).forEach((k) => { if (!all[mode][k]) delete all[mode][k]; });
      UIPrefs.set({ modeBind: all });
      if (currentMode() === mode) applyModeBinding(mode);
    };
    const themeOpts = [{ value: '', label: '不绑定' }].concat(THEMES.concat(EXTRA_ACCENTS).map((t) => ({ value: t.id, label: t.name })));
    const permOpts = [{ value: '', label: '不改变' }, { value: 'plan_first', label: '执行前询问' }, { value: 'auto', label: '自动执行' }];
    const section = (mode, desc) => [
      label(MODE_NAMES[mode] + (currentMode() === mode ? ' · 当前' : '')),
      card(
        row({ icon: 'appearance', title: '绑定主题', desc: `切换到「${MODE_NAMES[mode]}」时自动换成这个主题色。`,
          control: select(themeOpts, (binds[mode] || {}).accent || '', (v) => setBind(mode, { accent: v }), { labelText: '绑定主题' }) }),
        row({ icon: 'shield', title: '默认执行权限', desc: desc,
          control: select(permOpts, (binds[mode] || {}).perm || '', (v) => setBind(mode, { perm: v }), { labelText: '默认执行权限' }) }),
      ),
    ];
    root.replaceChildren(
      head('模式配置', '侧栏顶部的「编程 / 通用」切换时，顺带切换的界面与权限偏好。只保存在本机。'),
      ...section('code', '进入编程模式时把输入框的执行权限切到这一档；高风险操作仍会请你批准。'),
      ...section('work', '进入通用模式时把输入框的执行权限切到这一档。'),
    );
  }

  /* ============================== 任务监控（现场栏） ============================== */
  const RAIL_PARTS = [
    { id: 'loop', title: '微光循环', desc: '规划 → 执行 → 验收的阶段轨与当前步骤。', icon: 'gauge' },
    { id: 'flow', title: '事件流水', desc: '引擎实时发出的事件列表与计数。', icon: 'pulse' },
    { id: 'local', title: '本机', desc: '模型、工作区与本机资源读数。', icon: 'cpu' },
  ];
  function applyRailParts() {
    const hide = UIPrefs.get().railHide || [];
    document.documentElement.dataset.railHide = hide.join(' ');
  }
  function renderMonitor() {
    const root = $('#s2-monitor');
    const p = UIPrefs.get();
    const state = p.railFolded == null ? 'auto' : (p.railFolded ? 'folded' : 'open');
    const hide = new Set(p.railHide || []);
    root.replaceChildren(
      head('任务监控', '右侧「现场」栏：任务运行时引擎在做什么，摊开在一列仪表里。'),
      label('展示方式'),
      card(
        row({ icon: 'rail', title: '现场栏默认状态', desc: '跟随窗口宽度：宽屏展开、中屏收成竖签。选「展开 / 收起」会立刻应用并记住。',
          control: select([{ value: 'auto', label: '跟随窗口宽度' }, { value: 'open', label: '展开' }, { value: 'folded', label: '收起' }], state, (v) => {
            if (v === 'auto') { UIPrefs.set({ railFolded: undefined }); toast('下次打开 Gleam 时按窗口宽度决定', 'info', 2500); return; }
            const want = v === 'folded';
            const now = document.documentElement.dataset.rail === 'folded';
            if (want !== now && $('#rail-fold')) $('#rail-fold').click();
            UIPrefs.set({ railFolded: want });
          }, { labelText: '现场栏默认状态' }) }),
      ),
      label('显示的模块'),
      card(...RAIL_PARTS.map((part) => row({ icon: part.icon, title: part.title, desc: part.desc,
        control: toggle(!hide.has(part.id), (on) => {
          const s = new Set(UIPrefs.get().railHide || []);
          if (on) s.delete(part.id); else s.add(part.id);
          UIPrefs.set({ railHide: [...s] });
          applyRailParts();
        }, { labelText: part.title }) }))),
    );
  }

  /* ============================== 外观 ============================== */
  function renderAppearance() {
    const panel = document.querySelector('.settings-panel[data-stab="appearance"]');
    let root = panel.querySelector('.s2-page');
    if (!root) { root = h('div', { class: 's2-page' }); panel.replaceChildren(root); }
    const p = UIPrefs.get();
    const cards = h('div', { class: 's2-theme-grid', role: 'radiogroup', 'aria-label': '主题' },
      ...THEMES.map((t) => {
        const b = h('button', { type: 'button', class: 's2-theme', role: 'radio', 'aria-checked': String(p.accent === t.id), dataset: { accent: t.id }, style: `--sw:${t.sw}`,
          onclick: () => { UIPrefs.set({ accent: t.id }); renderAppearance(); } },
          h('span', { class: 's2-theme-prev', 'aria-hidden': 'true' },
            h('span', { class: 's2-theme-bar' }), h('span', { class: 's2-theme-line' }), h('span', { class: 's2-theme-line s2-theme-line--short' }), h('span', { class: 's2-theme-dot' })),
          h('span', { class: 's2-theme-name', text: t.name }));
        return b;
      }));
    const swatches = h('div', { class: 's2-swatches' }, ...EXTRA_ACCENTS.map((t) => h('button', {
      type: 'button', class: 's2-swatch', title: t.name, 'aria-label': t.name, 'aria-pressed': String(p.accent === t.id), style: `--sw:${t.sw}`,
      onclick: () => { UIPrefs.set({ accent: t.id }); renderAppearance(); } })));
    const pref = (key, opts, title) => select(opts, String(p[key] || opts[0].value), (v) => { UIPrefs.set({ [key]: v }); }, { labelText: title });
    root.replaceChildren(
      head('外观', '主题、颜色模式与排版。外观偏好只保存在本机。'),
      label('主题'),
      card(h('div', { class: 's2-theme-wrap' }, cards),
        row({ icon: 'appearance', title: '其他强调色', desc: '不想用整套主题时，只换强调色。', control: swatches })),
      label('显示'),
      card(
        row({ icon: 'blur', title: '颜色模式', desc: '跟随系统时随操作系统的深浅色自动切换。',
          control: select([{ value: 'system', label: '跟随系统' }, { value: 'light', label: '浅色' }, { value: 'dark', label: '深色' }], p.themeMode || 'light', (v) => UIPrefs.set({ themeMode: v }), { labelText: '颜色模式' }) }),
        row({ icon: 'lang', title: '界面语言', desc: '切换界面外壳的文案；任务内容与后端消息保持原文。', control: pref('lang', [{ value: 'zh', label: '简体中文' }, { value: 'en', label: 'English' }], '界面语言') }),
        row({ icon: 'type', title: '字体风格', control: pref('font', [{ value: 'sans', label: '无衬线' }, { value: 'serif', label: '衬线' }], '字体风格') }),
        row({ icon: 'style', title: '文字大小', control: pref('text', [{ value: 's', label: '小' }, { value: 'm', label: '中' }, { value: 'l', label: '大' }], '文字大小') }),
        row({ icon: 'zoom', title: '界面缩放', control: pref('zoom', [{ value: 'm', label: '中' }, { value: 'l', label: '大' }], '界面缩放') }),
        row({ icon: 'width', title: '内容宽度', desc: '对话与页面内容的最大宽度。', control: pref('width', [{ value: 'standard', label: '标准' }, { value: 'wide', label: '宽' }], '内容宽度') }),
        row({ icon: 'layers', title: '毛玻璃效果', desc: '弹层与遮罩的背景模糊。关闭可在低配机器上更流畅。',
          control: toggle(p.blur !== 'off', (on) => { UIPrefs.set({ blur: on ? undefined : 'off' }); applyBlur(); }, { labelText: '毛玻璃效果' }) }),
      ),
      label('应用图标'),
      card(row({ icon: 'computer', title: '应用图标', desc: 'Gleam 目前只有这一款图标。',
        control: h('span', { class: 's2-appicon', 'aria-label': 'Gleam 图标' }, h('img', { src: '/assets/gleam-logo.svg', alt: '', width: 28, height: 28 })) })),
    );
  }
  function applyBlur() { document.documentElement.dataset.blur = UIPrefs.get().blur === 'off' ? 'off' : 'on'; }

  /* ---------- 页面注册 ---------- */
  const PAGES = {
    profile: renderProfile, general: renderGeneral, modes: renderModes, monitor: renderMonitor, appearance: renderAppearance,
  };
  function register(name, fn) { PAGES[name] = fn; }
  function show(stab) {
    const fn = PAGES[stab];
    if (!fn) return;
    Promise.resolve().then(fn).catch((err) => toast(`设置页加载失败：${err.message || err}`, 'error'));
  }

  function init() {
    decorateNav();
    applyRailParts();
    applyBlur();
    watchMode();
    document.querySelectorAll('.settings-nav .settings-tab').forEach((t) => t.addEventListener('click', () => show(t.dataset.stab)));
    // 进入设置页时渲染当前分区（app.js 的 settings 加载器只管旧表单）
    const origShow = showView;
    showView = function (name, ...rest) {
      const r = origShow.call(this, name, ...rest);
      if (name === 'settings') {
        const cur = document.querySelector('.settings-nav .settings-tab.active');
        show(cur ? cur.dataset.stab : 'profile');
      }
      return r;
    };
    if (typeof loadAccount === 'function') {
      const origAcc = loadAccount;
      loadAccount = async function (...args) {
        const r = await origAcc.apply(this, args);
        applyLocalName();
        return r;
      };
    }
    applyLocalName();
    // 设置搜索按分区里的文字过滤：新分区是懒渲染的，第一次搜索前先把它们都画出来
    const search = $('#settings-search');
    let primed = false;
    if (search) search.addEventListener('input', (e) => {
      if (primed) return;
      primed = true;
      const active = document.querySelector('.settings-nav .settings-tab.active');
      Promise.allSettled(Object.keys(PAGES).filter((k) => !active || k !== active.dataset.stab).map((k) => Promise.resolve().then(PAGES[k])))
        .then(() => search.dispatchEvent(new Event('input')));
    }, true);
  }

  return { init, register, show, h, row, card, label, head, toggle, select, btn, badge, empty, ico, settings, save, openLink };
})();
