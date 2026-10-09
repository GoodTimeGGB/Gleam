/* 设置 v2 · 其余分区：记忆 / 扩展管理 / 钩子 / 电脑操控 / Git / Worktrees / 工作区索引 /
 * 连接 / 安全 / Go 工具链 / 引擎 / 已归档 / 实验功能 / 网络。
 * 已有表单的分区（记忆、安全、Go、引擎）只加统一页头和新卡片，旧表单原样保留在下面，
 * 绑定与保存逻辑都不动——那些控件本来就是真的。 */
'use strict';

(() => {
  const { h, row, card, label, head, toggle, select, btn, badge, empty, ico, settings, save } = S2;

  /** 给已有表单的分区插一次统一页头 + 页面容器（返回容器，供各页往里放新卡片）。 */
  function prelude(stab, title, desc, right) {
    const panel = document.querySelector(`.settings-panel[data-stab="${stab}"]`);
    let pre = panel.querySelector(':scope > .s2-page');
    if (!pre) {
      pre = h('div', { class: 's2-page s2-page--pre' });
      panel.prepend(pre);
      panel.classList.add('s2-legacy');
    }
    pre.replaceChildren(head(title, desc, right));
    return pre;
  }
  const fmtTime = (t) => {
    const d = new Date(t);
    if (isNaN(d)) return '';
    const pad = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  };
  const copy = async (text) => {
    try { await navigator.clipboard.writeText(text); toast('已复制', 'success', 1500); }
    catch { toast('复制失败：浏览器不允许访问剪贴板', 'warning'); }
  };

  /* ============================== 记忆 ============================== */
  S2.register('memory', async () => {
    const pre = prelude('memory', '记忆', 'Gleam 的长期记忆与会话上下文都保存在本机数据目录里。');
    const [info, s, local] = await Promise.all([api('GET', '/api/info'), settings(true), api('GET', '/api/local-data').catch(() => ({}))]);
    pre.append(
      label('记忆开关'),
      card(
        row({ icon: 'compress', title: '会话上下文自动压缩', desc: '窗口外的旧对话自动摘要成长期上下文，省 token 不丢前情。',
          control: toggle(s.agent.context_compress, (v) => save({ agent: { context_compress: v } })) }),
        row({ icon: 'memory', title: '项目级记忆',
          desc: '打开后，任务沉淀的记忆带上当时的工作区，检索只回本工作区 + 全局的；关着就是全局共享（历史行为）。立即生效，不用重启。',
          control: toggle(s.memory.project_scope, (v) => save({ memory: { project_scope: v } }), { labelText: '项目级记忆' }) }),
      ),
      label('记忆文件'),
      card(
        row({ icon: 'memory', title: '长期记忆', desc: `${info.memory || 0} 条 · 可在记忆页检索、写入和删除`,
          control: btn('打开记忆页', () => showView('memory')) }),
        row({ icon: 'folder', title: '数据目录', desc: local.data_dir ? `${local.data_dir} · ${local.file_count || 0} 个文件` : '—',
          control: local.data_dir ? h('button', { type: 'button', class: 's2-icon-btn', title: '复制路径', 'aria-label': '复制路径', html: ico('copy', 15), onclick: () => copy(local.data_dir) }) : null }),
      ));
    try { loadContext(); } catch { /* 记忆系统未启用 */ }
  });

  /* ============================== 扩展管理 ============================== */
  let extTab = 'mcp';
  let extQ = '';
  S2.register('ext', async () => {
    const root = $('#s2-ext');
    const [mcp, skills, roles] = await Promise.all([
      api('GET', '/api/mcp').catch(() => ({ mcp: [] })), api('GET', '/api/skills').catch(() => ({ skills: [] })), api('GET', '/api/roles').catch(() => ({ roles: [] }))]);
    const data = { mcp: mcp.mcp || [], skills: skills.skills || [], roles: roles.roles || [] };
    const tabs = [['mcp', '插件', 'MCP 服务器'], ['skills', '技能', ''], ['roles', '智能体', '专家角色']];
    const market = btn('', () => showView('market'), 'btn btn-secondary btn-sm');
    market.innerHTML = ico('ext', 14) + '<span>市场</span>';
    const search = h('input', { class: 'input s2-search-input', type: 'search', placeholder: '搜索已安装的扩展', value: extQ, 'aria-label': '搜索扩展' });
    const list = h('div', { class: 's2-card s2-card--list' });
    const tabBar = h('div', { class: 's2-tabs', role: 'tablist' }, ...tabs.map(([k, name]) => h('button', {
      type: 'button', role: 'tab', class: 's2-tab', 'aria-selected': String(extTab === k),
      onclick: () => { extTab = k; S2.show('ext'); } }, name, h('span', { class: 's2-tab-count', text: String(data[k].length) }))));
    const addBtn = btn('', () => (extTab === 'roles' ? toast('自定义智能体暂不支持，可在任务输入框里选择内置专家角色', 'info') : showView('market')), 'btn btn-secondary btn-sm');
    addBtn.innerHTML = ico('plus', 14) + '<span>添加</span>';
    function paintList() {
      const q = extQ.trim().toLowerCase();
      const items = data[extTab].filter((x) => !q || `${x.name} ${x.description || ''} ${x.id || ''}`.toLowerCase().includes(q));
      if (!items.length) {
        list.replaceChildren(empty(extTab === 'roles' ? 'robot' : 'ext', q ? '没有匹配的扩展' : (extTab === 'mcp' ? '还没有安装插件' : extTab === 'skills' ? '还没有技能' : '没有智能体'),
          q ? '' : (extTab === 'roles' ? '' : '从市场安装，或在对话里让 Gleam 把常用流程固化成技能。'),
          !q && extTab !== 'roles' ? btn('去市场看看', () => showView('market')) : null));
        return;
      }
      list.replaceChildren(...items.map((x) => {
        let control = null;
        if (extTab === 'mcp') {
          control = toggle(x.enabled, async (v) => { await api('POST', `/api/mcp/${encodeURIComponent(x.name)}/enabled`, { enabled: v }); x.enabled = v; }, { labelText: `启用 ${x.name}` });
        } else if (extTab === 'skills') {
          control = toggle(!x.disabled, async (v) => { await api('POST', `/api/skills/${encodeURIComponent(x.name)}/enabled`, { enabled: v }); x.disabled = !v; }, { labelText: `启用 ${x.name}` });
        } else control = badge('内置', 'muted');
        const sub = extTab === 'mcp' ? [x.transport, x.status || (x.connected ? '已连接' : ''), x.tools != null ? `${Array.isArray(x.tools) ? x.tools.length : x.tools} 个工具` : ''].filter(Boolean).join(' · ') : (x.description || '');
        return row({ icon: extTab === 'mcp' ? 'plug' : extTab === 'skills' ? 'wand' : 'robot', title: x.name || x.id, desc: sub || x.description || '', control });
      }));
    }
    search.addEventListener('input', () => { extQ = search.value; paintList(); });
    root.replaceChildren(
      head('扩展管理', '插件（MCP 服务器）、技能和智能体角色。安装与发现在市场里完成。', market),
      h('div', { class: 's2-toolbar' }, tabBar, h('div', { class: 's2-toolbar-right' }, h('div', { class: 's2-search' }, h('span', { html: ico('search', 14) }), search), addBtn)),
      list);
    paintList();
  });

  /* ============================== 钩子 ============================== */
  S2.register('hooks', async () => {
    const root = $('#s2-hooks');
    const [{ jobs = [] }, s] = await Promise.all([api('GET', '/api/schedules').catch(() => ({ jobs: [] })), settings()]);
    const base = location.origin;
    const listCard = jobs.length
      ? h('div', { class: 's2-card s2-card--list' }, ...jobs.map((j) => row({
        icon: 'hooks', title: j.name, desc: `POST ${base}/api/hooks/${encodeURIComponent(j.name)}${j.enabled ? '' : ' · 任务已暂停'}`,
        control: h('div', { class: 's2-icon-btns' },
          h('button', { type: 'button', class: 's2-icon-btn', title: '复制 curl 命令', 'aria-label': '复制 curl 命令', html: ico('copy', 15),
            onclick: () => copy(`curl -X POST -H "X-Gleam-Token: $(cat ~/.gleam/webui.token)" ${base}/api/hooks/${encodeURIComponent(j.name)}`) }),
          btn('触发一次', async () => {
            const r = await api('POST', `/api/hooks/${encodeURIComponent(j.name)}`).catch((e) => ({ error: e.message }));
            toast(r.error ? `触发失败：${r.error}` : (r.triggered ? `已触发「${j.name}」` : `「${j.name}」没有触发（可能正在运行）`), r.error ? 'error' : 'success');
          })) })))
      : card(empty('hooks', '还没有可触发的钩子', '每个定时任务都会自动得到一个 HTTP 钩子；先创建定时任务。', btn('去定时任务', () => showView('schedules'))));
    root.replaceChildren(
      head('钩子', '外部脚本、CI 或其他程序可以通过 HTTP 请求立刻触发某个定时任务。'),
      label('配置来源'),
      card(row({ icon: 'calendar', title: '定时任务', desc: `钩子来自定时任务列表（保存在数据目录 ${s.data_dir || ''}）。调度开关：${s.scheduler && s.scheduler.enabled ? '已开启' : '已关闭'}`,
        control: btn('管理', () => showView('schedules')) })),
      h('div', { class: 's2-notice s2-notice--warn' }, h('span', { html: ico('warn', 14) }),
        h('span', { text: `钩子没有单独的口令：任何能访问 ${location.host} 的程序都能触发它。Gleam 默认只监听本机地址，不要把这个端口暴露到局域网或公网。` })),
      label(`钩子（${jobs.length}）`),
      listCard);
  });

  /* ============================== 电脑操控 ============================== */
  const DESKTOP_ICONS = { 'desktop.clipboard.read': 'clipboard', 'desktop.clipboard.write': 'clipboard', 'desktop.notify': 'bell', 'desktop.screenshot': 'camera', 'desktop.snippets': 'snippet' };
  const PERM_OPTS = [{ value: 'default', label: '默认' }, { value: 'readonly', label: '只读放行' }, { value: 'user_approved', label: '需我批准' }, { value: 'full_access', label: '完全访问' }];
  // 全局唤起快捷键：桌面壳专属（浏览器里没有 globalShortcut）。
  // 浏览器里那一行照实说"只在桌面版可用"，而不是摆一个按了没反应的按钮。
  function globalShortcutRow() {
    const desk = window.gleamDesktop;
    if (!desk || typeof desk.globalShortcut !== 'function') {
      return row({ icon: 'shortcuts', title: '全局唤起快捷键', desc: '在任何应用里按快捷键唤出 Gleam（只在桌面版可用）。', disabled: true });
    }
    let cur = '';
    const val = h('span', { class: 's2-keys' });
    const paint = () => { val.textContent = cur || '未设置'; val.dataset.empty = String(!cur); };
    const rec = btn('录制', () => recordShortcut(val, (accel) => { cur = accel; paint(); }), 'btn btn-secondary btn-sm');
    const clr = btn('清除', async () => {
      const r = await desk.globalShortcut('set', '').catch((e) => ({ ok: false, error: e.message }));
      if (!r.ok) { toast(r.error || '清除失败', 'error'); return; }
      cur = ''; paint(); toast('已清除', 'success');
    }, 'btn btn-ghost btn-sm');
    paint();
    desk.globalShortcut('get').then((r) => { cur = (r && r.accelerator) || ''; paint(); }).catch(() => {});
    return row({
      icon: 'shortcuts', title: '全局唤起快捷键',
      desc: '在任何应用里按这个组合把 Gleam 拉到前台。只在本机注册，不联网。',
      control: h('div', { class: 's2-shortcut-ctl' }, val, rec, clr),
    });
  }

  // recordShortcut 录一次组合键。**必须带修饰键**：单键抢全局（比如字母 A）会非常讨厌。
  function recordShortcut(labelEl, done) {
    const before = labelEl.textContent;
    labelEl.textContent = '按下组合键…（Esc 取消）';
    labelEl.dataset.empty = 'false';
    const onKey = (e) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'Escape') {
        document.removeEventListener('keydown', onKey, true);
        labelEl.textContent = before;
        return;
      }
      const accel = toAccelerator(e);
      if (!accel) { toast('至少要带一个修饰键（Ctrl / Alt / Shift / Win）', 'info'); return; }
      document.removeEventListener('keydown', onKey, true);
      labelEl.textContent = before;
      window.gleamDesktop.globalShortcut('set', accel).then((r) => {
        if (!r.ok) { toast(r.error || '注册失败（可能被别的程序占用了）', 'error'); return; }
        done(r.accelerator);
        toast('已设置：' + r.accelerator, 'success');
      }).catch((err) => toast(err.message || '注册失败', 'error'));
    };
    document.addEventListener('keydown', onKey, true);
  }

  // toAccelerator 把一次 keydown 转成 Electron 认的组合串；只有修饰键或没修饰键时返回空。
  function toAccelerator(e) {
    const mods = [];
    if (e.ctrlKey) mods.push('CommandOrControl');
    if (e.altKey) mods.push('Alt');
    if (e.shiftKey) mods.push('Shift');
    if (e.metaKey) mods.push('Super');
    if (['Control', 'Alt', 'Shift', 'Meta'].includes(e.key) || !mods.length) return '';
    const k = e.key;
    let key = k;
    if (k === ' ') key = 'Space';
    else if (k.length === 1) key = k.toUpperCase();
    else if (!/^F\d{1,2}$/.test(k)) key = k.length <= 12 ? k[0].toUpperCase() + k.slice(1) : '';
    return key ? mods.join('+') + '+' + key : '';
  }

  S2.register('computer', async () => {
    const root = $('#s2-computer');
    const { tools = [] } = await api('GET', '/api/tools').catch(() => ({ tools: [] }));
    const desk = tools.filter((t) => t.name.startsWith('desktop.'));
    root.replaceChildren(
      head('电脑操控', 'Gleam 能在这台电脑上做的桌面动作：读写剪贴板、截屏、发系统通知、插入文本片段。每个动作的放行档位都可以单独调。'),
      h('div', { class: 's2-illus', 'aria-hidden': 'true', html: KEYBOARD_SVG }),
      label('桌面工具'),
      desk.length
        ? h('div', { class: 's2-card s2-card--list' }, ...desk.map((t) => row({
          icon: DESKTOP_ICONS[t.name] || 'computer', title: t.name, desc: t.description,
          control: select(PERM_OPTS, t.overridden ? t.permission : 'default', async (v) => {
            const r = await api('POST', '/api/tools/permission', { name: t.name, permission: v });
            toast(`${t.name}：${PERM_LABELS[r.permission] || r.permission}`, 'success', 1800);
          }, { labelText: `${t.name} 权限` }) })))
        : card(empty('computer', '这台电脑没有可用的桌面工具', '')),
      label('更多操控'),
      card(
        row({ icon: 'globe', title: '浏览器操控', desc: '让 Gleam 驱动浏览器打开网页、点击和填写表单。', disabled: true }),
        row({ icon: 'mouse', title: '鼠标与键盘操控', desc: '模拟点击与键盘输入来操作其他应用。', disabled: true }),
        globalShortcutRow(),
      ));
  });
  // 自绘插画：一块键盘 + 一个屏幕，用当前主题的线条色和强调色
  const KEYBOARD_SVG = `<svg viewBox="0 0 400 150" width="100%" height="150" fill="none" stroke-linecap="round" stroke-linejoin="round">
    <defs><pattern id="s2dots" width="12" height="12" patternUnits="userSpaceOnUse"><circle cx="1.5" cy="1.5" r="1" fill="var(--line)"/></pattern></defs>
    <rect x="0" y="0" width="400" height="150" rx="12" fill="url(#s2dots)"/>
    <rect x="236" y="20" width="120" height="78" rx="8" fill="var(--panel)" stroke="var(--color-fg-faint)" stroke-width="1.5"/>
    <path d="M276 108h40M296 98v10" stroke="var(--color-fg-faint)" stroke-width="1.5"/>
    <rect x="250" y="34" width="56" height="8" rx="4" fill="var(--accent-fill)"/>
    <rect x="250" y="50" width="88" height="6" rx="3" fill="var(--line)"/><rect x="250" y="62" width="70" height="6" rx="3" fill="var(--line)"/>
    <rect x="44" y="52" width="170" height="74" rx="10" fill="var(--panel)" stroke="var(--color-fg-faint)" stroke-width="1.5"/>
    ${Array.from({ length: 3 }, (_, r) => Array.from({ length: 8 }, (_, c) => `<rect x="${56 + c * 19}" y="${62 + r * 16}" width="14" height="11" rx="2.5" fill="${r === 1 && c === 5 ? 'var(--accent-fill)' : 'none'}" stroke="var(--color-fg-faint)" stroke-width="1.2"/>`).join('')).join('')}
    <rect x="94" y="110" width="70" height="9" rx="3" stroke="var(--color-fg-faint)" stroke-width="1.2"/>
    <path d="M214 82c12 0 14-20 22-24" stroke="var(--color-accent)" stroke-width="1.5" stroke-dasharray="3 4"/>
  </svg>`;

  /* ============================== Git / Worktrees ============================== */
  S2.register('git', async () => {
    const s = await settings(true);
    const g = s.git || {};
    // 这三条只作用于 Gleam 自己的 git.branch / git.commit / git.push 工具；
    // 模型在 shell.exec 里自己拼的 git 命令不归这里管，界面上要说清楚，别让人以为它是全局开关。
    const prefix = h('input', { class: 'input s2-input', value: g.branch_prefix || '', 'aria-label': '分支前缀', placeholder: 'gleam/' });
    prefix.addEventListener('change', () => save({ git: { branch_prefix: prefix.value.trim() } }, '已保存，下次建分支生效'));
    const instr = h('textarea', { class: 'input s2-textarea', rows: 4, 'aria-label': '提交说明指令', placeholder: '例如：使用 Conventional Commits，说明写中文。' });
    instr.value = g.commit_instructions || '';
    instr.addEventListener('change', () => save({ git: { commit_instructions: instr.value.trim() } }, '已保存，会附在编程任务的指引里'));
    $('#s2-git').replaceChildren(
      head('Git', '由 Gleam 自己执行的分支、提交与推送（走安全门控；推送每次都要你批准）。模型在 shell.exec 里自己拼的 git 命令不归这里管。'),
      label('分支'),
      card(
        row({ icon: 'branch', title: '分支前缀', desc: '由 Gleam 创建的分支统一加上这个前缀；已经有了就不重复加。', control: prefix }),
        row({ icon: 'git', title: '始终强制推送', desc: '推送时使用 --force-with-lease——比裸 --force 安全：远端有新提交就拒绝，不会把别人的活覆盖掉。',
          control: toggle(!!g.force_push, (v) => save({ git: { force_push: v } }), { labelText: '始终强制推送' }) }),
      ),
      label('提交说明'),
      card(row({ icon: 'edit', title: '提交说明指令', desc: '写提交说明时的写法要求，会附在编程任务的指引里，由模型按它写。' }), h('div', { class: 's2-card-pad' }, instr)));
  });
  S2.register('worktrees', () => {
    const limit = h('select', { class: 's2-select', disabled: true, 'aria-label': '数量上限' }, h('option', { text: '15 个' }));
    $('#s2-worktrees').replaceChildren(
      head('Worktrees', '为并行任务各开一个 git worktree，互不干扰。Gleam 目前在当前工作区里直接执行，还不会创建 worktree。'),
      label('创建与清理'),
      card(
        row({ icon: 'refresh', title: '创建前先 fetch', desc: '新建 worktree 之前先同步远端。', control: toggle(false, () => {}, { disabled: true }), disabled: true }),
        row({ icon: 'trash', title: '自动删除', desc: '任务归档后自动删除对应的 worktree。', control: toggle(false, () => {}, { disabled: true }), disabled: true }),
        row({ icon: 'layers', title: '数量上限', desc: '超过上限时最旧的 worktree 会被清理。', control: limit, disabled: true }),
      ),
      label('由 Gleam 管理的 Worktrees'),
      card(empty('worktrees', '没有由 Gleam 管理的 worktree', '')));
  });

  /* ============================== 工作区索引 ============================== */
  S2.register('index', async () => {
    const { spaces = [] } = await api('GET', '/api/spaces').catch(() => ({ spaces: [] }));
    const ws = spaces.filter((s) => s.path || s.workspace);
    $('#s2-index').replaceChildren(
      head('工作区索引', 'Gleam 不预先给代码建索引：需要时用 file.search / file.list 在当前工作区里现查，所以没有索引要等、也没有索引要清。'),
      h('div', { class: 's2-card s2-index-empty' },
        h('div', { class: 's2-illus s2-illus--sm', 'aria-hidden': 'true', html: INDEX_SVG }),
        h('strong', { text: '无需建立索引' }),
        h('small', { text: '打开工作区后即可在任务里引用其中的文件（输入 @）。' })),
      ws.length ? label('已打开的工作区') : null,
      ws.length ? h('div', { class: 's2-card s2-card--list' }, ...ws.map((s) => row({ icon: 'folder', title: s.name || s.title || '工作区', desc: s.path || s.workspace, control: badge('按需检索', 'muted') }))) : null);
  });
  const INDEX_SVG = `<svg viewBox="0 0 220 110" width="220" height="110" fill="none" stroke-linecap="round" stroke-linejoin="round">
    <ellipse cx="80" cy="30" rx="44" ry="13" fill="var(--panel)" stroke="var(--color-fg-faint)" stroke-width="1.5"/>
    <path d="M36 30v44c0 7 20 13 44 13s44-6 44-13V30" stroke="var(--color-fg-faint)" stroke-width="1.5"/>
    <path d="M36 52c0 7 20 13 44 13s44-6 44-13" stroke="var(--color-fg-faint)" stroke-width="1.5" stroke-dasharray="3 4"/>
    <circle cx="158" cy="62" r="20" fill="var(--panel)" stroke="var(--color-accent)" stroke-width="2"/>
    <path d="m172 76 16 16" stroke="var(--color-accent)" stroke-width="3"/>
    <path d="M150 62h16M158 54v16" stroke="var(--accent-fill)" stroke-width="2.5"/>
  </svg>`;

  /* ============================== 连接 ============================== */
  S2.register('conn', async () => {
    const root = $('#s2-conn');
    const hosts = UIPrefs.get().sshHosts || [];
    const addBtn = btn('', () => sshDialog(), 'btn btn-secondary btn-sm');
    addBtn.innerHTML = ico('plus', 14) + '<span>添加</span>';
    const sshCard = hosts.length
      ? h('div', { class: 's2-card s2-card--list' }, ...hosts.map((n) => row({ icon: 'server', title: n, desc: '来自 ~/.ssh/config',
        control: h('button', { type: 'button', class: 's2-icon-btn s2-icon-btn--danger', title: '移除', 'aria-label': `移除 ${n}`, html: ico('trash', 15),
          onclick: () => { UIPrefs.set({ sshHosts: (UIPrefs.get().sshHosts || []).filter((x) => x !== n) }); S2.show('conn'); } }) })))
      : card(empty('server', '暂无 SSH 连接', '从 ~/.ssh/config 里挑选主机，记在这里。', btn('添加', () => sshDialog(), 'btn btn-primary btn-sm')));
    const kids = [
      head('连接', '远程主机与 Gleam 的出网边界。'),
      h('div', { class: 's2-label-row' }, label('SSH'), hosts.length ? addBtn : null),
      sshCard,
      h('p', { class: 's2-foot', text: '远程 SSH 工作区暂不支持：这里只保存主机别名，Gleam 不会连接这些主机，也不会读取任何密钥文件。' }),
      label('连接与出网'),
    ];
    root.replaceChildren(...kids);
    try { loadConnections(); } catch { /* 接口不可用 */ }
  });
  async function sshDialog() {
    const overlay = h('div', { class: 's2-dialog-overlay' });
    const dlg = h('div', { class: 's2-dialog', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 's2-ssh-t' });
    overlay.append(dlg);
    const list = h('div', { class: 's2-ssh-list' });
    const all = h('input', { type: 'checkbox', id: 's2-ssh-all' });
    const add = h('button', { type: 'button', class: 'btn btn-primary btn-sm', text: '添加', disabled: true });
    const pathNote = h('small', { class: 's2-field-hint' });
    const close = () => { overlay.remove(); document.removeEventListener('keydown', onKey, true); };
    const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); close(); } };
    document.addEventListener('keydown', onKey, true);
    overlay.addEventListener('mousedown', (e) => { if (e.target === overlay) close(); });
    const saved = new Set(UIPrefs.get().sshHosts || []);
    function sync() {
      const boxes = [...list.querySelectorAll('input[type=checkbox]:not(:disabled)')];
      const n = boxes.filter((b) => b.checked).length;
      add.disabled = n === 0;
      add.textContent = n ? `添加（${n}）` : '添加';
      all.checked = boxes.length > 0 && n === boxes.length;
      all.indeterminate = n > 0 && n < boxes.length;
    }
    async function load() {
      list.replaceChildren(h('div', { class: 's2-field-hint', text: '正在读取…' }));
      const r = await api('GET', '/api/ssh/hosts').catch((e) => ({ error: e.message, hosts: [] }));
      pathNote.textContent = r.path ? `读取自 ${r.path}（只解析 Host 名称）` : '';
      if (!r.hosts.length) {
        list.replaceChildren(empty('server', r.exists ? '配置文件里没有具体的 Host' : '没有找到 SSH 配置文件', r.exists ? '通配符（* ?）条目不会列出。' : `在 ${r.path || '~/.ssh/config'} 写好 Host 后点「重新读取」。`));
      } else {
        list.replaceChildren(...r.hosts.map((n) => {
          const cb = h('input', { type: 'checkbox', value: n, disabled: saved.has(n) ? true : null });
          if (saved.has(n)) cb.checked = true;
          cb.addEventListener('change', sync);
          return h('label', { class: 's2-ssh-item' }, cb, h('span', { class: 's2-glyph', text: n[0].toUpperCase() }), h('span', { class: 's2-ssh-name', text: n }), saved.has(n) ? badge('已添加', 'muted') : null);
        }));
      }
      sync();
    }
    all.addEventListener('change', () => { list.querySelectorAll('input[type=checkbox]:not(:disabled)').forEach((b) => { b.checked = all.checked; }); sync(); });
    add.addEventListener('click', () => {
      const pick = [...list.querySelectorAll('input[type=checkbox]:checked:not(:disabled)')].map((b) => b.value);
      UIPrefs.set({ sshHosts: [...saved, ...pick] });
      close();
      toast(`已记下 ${pick.length} 台主机`, 'success', 1800);
      S2.show('conn');
    });
    const reload = h('button', { type: 'button', class: 's2-link' }, h('span', { html: ico('refresh', 12) }), '重新读取');
    reload.addEventListener('click', load);
    dlg.append(
      h('div', { class: 's2-dialog-head' }, h('strong', { id: 's2-ssh-t', text: '添加 SSH 连接' }),
        h('button', { type: 'button', class: 's2-icon-btn', 'aria-label': '关闭', title: '关闭', html: ico('x', 15), onclick: close })),
      h('div', { class: 's2-dialog-body' },
        h('p', { class: 's2-dialog-desc', text: '从你的 SSH 配置里挑选主机。Gleam 只读 Host 名称，不读取 HostName、用户名或任何密钥。' }),
        h('div', { class: 's2-ssh-tools' }, h('label', { class: 's2-ssh-all' }, all, '全选'), reload),
        list, pathNote),
      h('div', { class: 's2-dialog-foot' }, h('button', { type: 'button', class: 'btn btn-ghost btn-sm', text: '取消', onclick: close }), add));
    document.body.append(overlay);
    load();
  }

  /* ============================== 安全 ============================== */
  S2.register('safety', async () => {
    const pre = prelude('safety', '安全', '每一个会写文件、跑命令或出网的动作，执行前都要过 Gleam 的安全门控。');
    const s = await settings(true);
    pre.append(
      label('扫描层级'),
      card(
        row({ icon: 'scan', title: '静态检查', desc: '按规则判风险：路径越界、危险命令、敏感文件。所有动作都会经过，不能关闭。',
          control: h('span', { class: 's2-always' }, toggle(true, () => {}, { disabled: true, labelText: '静态检查' }), h('small', { text: '始终开启' })) }),
        row({ icon: 'shield', title: '轻量扫描', desc: '对本来会自动放行的中高风险动作，先用辅助模型快筛一遍，被标记才交给你确认。',
          control: toggle(s.safety.ai_review, (v) => save({ safety: { ai_review: v } }), { labelText: '轻量扫描' }) }),
        row({ icon: 'search', title: '深度扫描',
          desc: '执行前把整段计划交给主模型审一遍。逐动作的轻量扫描看不见"每步都正常、连起来却在做另一件事"；这一步看的是全局。它否决时会停下来问你，你说继续就继续。',
          control: toggle(s.safety.deep_review, (v) => save({ safety: { deep_review: v } }), { labelText: '深度扫描' }) }),
      ));
  });

  /* ============================== Go 工具链 / 引擎 / 快捷键：统一页头 ============================== */
  S2.register('go', () => { prelude('go', 'Go 工具链', '编程任务里构建、测试 Go 项目要用到的工具链。'); });
  S2.register('engine', () => { prelude('engine', '引擎', '规划与执行的上限、预算熔断和循环治理。'); });

  /* ============================== 已归档 ============================== */
  let arQ = '';
  let arKind = '';
  S2.register('archived', async () => {
    const root = $('#s2-archived');
    const { goals = [] } = await api('GET', '/api/goals').catch(() => ({ goals: [] }));
    const done = goals.filter((g) => g.status !== 'running');
    const KIND = { code: '编程', work: '通用', chat: '对话' };
    const STATUS = { success: '已完成', partial: '部分完成', failed: '失败', cancelled: '已取消' };
    const list = h('div', { class: 's2-card s2-card--list' });
    const search = h('input', { class: 'input s2-search-input', type: 'search', placeholder: '搜索任务', value: arQ, 'aria-label': '搜索已归档任务' });
    const kinds = [{ value: '', label: '所有类型' }].concat(Object.entries(KIND).map(([k, v]) => ({ value: k, label: v })));
    const kindSel = select(kinds, arKind, (v) => { arKind = v; paint(); }, { labelText: '任务类型' });
    const delAll = btn('全部删除', async () => {
      if (!done.length) return;
      if (!await confirmModal(`将永久删除 ${done.length} 条已结束任务的记录（盘上的归档文件一并删除），无法恢复。`, '全部删除？', { okText: '全部删除', danger: true })) return;
      let ok = 0;
      for (const g of done) { try { await api('DELETE', `/api/goals/${encodeURIComponent(g.task_id)}`); ok++; } catch { /* 跳过删不掉的 */ } }
      toast(`已删除 ${ok} 条记录`, 'success');
      try { loadGoals(); } catch { /* 目标页未初始化 */ }
      S2.show('archived');
    }, 'btn btn-danger-soft btn-sm s2-danger', { disabled: done.length ? null : true });
    function paint() {
      const q = arQ.trim().toLowerCase();
      const items = done.filter((g) => (!arKind || (g.task_mode || 'work') === arKind) && (!q || (g.goal || '').toLowerCase().includes(q)));
      if (!items.length) {
        list.replaceChildren(empty('archived', q || arKind ? '没有匹配的任务' : '暂无已归档任务', q || arKind ? '' : '任务结束后会出现在这里，重启后也读得回来。'));
        return;
      }
      list.replaceChildren(...items.map((g) => row({
        icon: g.status === 'success' ? 'check' : 'archived', title: shorten(g.goal || g.task_id, 60),
        desc: `${STATUS[g.status] || g.status} · ${KIND[g.task_mode] || '通用'} · ${fmtTime(g.started_at)}`,
        control: h('div', { class: 's2-icon-btns' },
          btn('打开', () => { showView('goals'); openTaskDetail(g.task_id); }),
          h('button', { type: 'button', class: 's2-icon-btn s2-icon-btn--danger', title: '删除', 'aria-label': '删除', html: ico('trash', 15),
            onclick: async () => {
              if (!await confirmModal(`「${shorten(g.goal, 40)}」的记录和归档文件会被永久删除。`, '删除这条任务？', { okText: '删除', danger: true })) return;
              try { await api('DELETE', `/api/goals/${encodeURIComponent(g.task_id)}`); toast('已删除', 'success', 1500); try { loadGoals(); } catch { /* */ } S2.show('archived'); }
              catch (err) { toast(err.message, 'error'); }
            } })) })));
    }
    search.addEventListener('input', () => { arQ = search.value; paint(); });
    root.replaceChildren(
      head('已归档', '已经结束的任务记录（保存在数据目录 tasks/ 下）。打开可查看过程和结果。', delAll),
      h('div', { class: 's2-toolbar' },
        h('div', { class: 's2-tabs', role: 'tablist' }, h('button', { type: 'button', role: 'tab', class: 's2-tab', 'aria-selected': 'true' }, '任务', h('span', { class: 's2-tab-count', text: String(done.length) }))),
        h('div', { class: 's2-toolbar-right' }, h('div', { class: 's2-search' }, h('span', { html: ico('search', 14) }), search), kindSel)),
      list);
    paint();
  });

  /* ============================== 实验功能 ============================== */
  S2.register('labs', () => {
    const root = $('#s2-labs');
    const ack = !!UIPrefs.get().labsAck;
    const box = h('input', { type: 'checkbox', id: 's2-labs-ack' });
    box.checked = ack;
    box.addEventListener('change', () => { UIPrefs.set({ labsAck: box.checked || undefined }); S2.show('labs'); });
    const kids = [
      head('实验功能', '还在打磨中的能力，可能不稳定，也可能在之后的版本里改动或移除。'),
      card(
        row({ icon: 'warn', title: '实验功能可能不稳定', desc: '可能出现异常结果、额外的模型调用或更高的资源占用。' }),
        row({ icon: 'labs', title: '随时可能变化', desc: '实验功能不保证向后兼容，正式发布前行为可能调整。' }),
        h('label', { class: 's2-ack', for: 's2-labs-ack' }, box, h('span', { text: '我了解风险，显示实验功能' }))),
    ];
    if (ack) {
      kids.push(label('实验功能'), card(empty('labs', '这个版本没有可开关的实验功能', '有新的实验功能时会出现在这里。')));
    }
    root.replaceChildren(...kids);
  });

  /* ============================== 网络 ============================== */
  S2.register('network', async () => {
    const root = $('#s2-network');
    const [s, net] = await Promise.all([settings(true), api('GET', '/api/network').catch(() => ({ proxy_env: {}, proxy_keys: [] }))]);
    const L = s.llm;
    const targets = [{ name: '主模型', model: L.model, body: {} }];
    if (L.fast_model) targets.push({ name: '辅助模型', model: L.fast_model, body: { model: L.fast_model, base_url: L.base_url, protocol: L.protocol, provider_id: L.provider_id, plan: L.plan } });
    const results = new Map();
    const resultEl = (t) => {
      const r = results.get(t.name);
      if (!r) return h('small', { class: 's2-row-desc', text: `${L.base_url ? new URL(L.base_url, location.href).host : '—'} · ${t.model || '未配置模型'}` });
      return h('small', { class: 's2-row-desc ' + (r.ok ? 's2-ok' : 's2-warn'), text: r.text });
    };
    const list = h('div', { class: 's2-card s2-card--list' });
    function paint() {
      list.replaceChildren(...targets.map((t) => row({ icon: 'network', title: t.name, note: resultEl(t) })));
    }
    const run = btn('开始检测', async () => {
      run.disabled = true;
      for (const t of targets) {
        results.set(t.name, { ok: true, text: '检测中…' }); paint();
        const t0 = performance.now();
        const r = await api('POST', '/api/llm/test', t.body).catch((e) => ({ ok: false, message: e.message }));
        const ms = Number.isFinite(r.latency_ms) ? r.latency_ms : Math.round(performance.now() - t0);
        results.set(t.name, r.kind === 'mock'
          ? { ok: true, text: '离线演示模型：没有发起网络请求' }
          : r.ok ? { ok: true, text: `连通 · ${ms} ms · ${r.base_url ? new URL(r.base_url).host : ''}` }
            : { ok: false, text: `不通：${r.message || r.kind}${r.http_status ? `（HTTP ${r.http_status}）` : ''}` });
        paint();
      }
      run.disabled = false;
    }, 'btn btn-secondary btn-sm');
    const keys = net.proxy_keys || [];
    const proxyDesc = keys.length
      ? keys.map((k) => `${k}=${net.proxy_env[k]}`).join(' · ')
      : '没有检测到 HTTPS_PROXY / HTTP_PROXY 环境变量：直接连接。';

    // 代理方式：三选一，选中「手动」时才露出地址输入框。
    // 改完要重启才切换——传输层是带连接池复用的，中途换等于每次请求重新握手。
    const curMode = net.proxy_mode || 'system';
    const modeSel = h('select', { class: 's2-select', 'aria-label': '代理方式' },
      h('option', { value: 'system', text: '跟随系统' }),
      h('option', { value: 'manual', text: '手动' }),
      h('option', { value: 'none', text: '不使用' }));
    modeSel.value = curMode;
    const urlIn = h('input', {
      class: 'input s2-input', placeholder: 'http://127.0.0.1:7890',
      'aria-label': '代理地址', value: net.proxy_url || '',
    });
    urlIn.hidden = curMode !== 'manual';
    modeSel.addEventListener('change', async () => {
      const v = modeSel.value;
      urlIn.hidden = v !== 'manual';
      if (v === 'manual') { urlIn.focus(); return; } // 手动要等地址填好，那一步再存
      await save({ network: { proxy_mode: v } }, '已保存，重启 Gleam 后生效');
      S2.show('network');
    });
    const commitURL = async () => {
      const raw = urlIn.value.trim();
      if (!raw) { toast('先填代理地址，例如 http://127.0.0.1:7890', 'info'); urlIn.focus(); return; }
      try {
        await save({ network: { proxy_mode: 'manual', proxy_url: raw } }, '已保存，重启 Gleam 后生效');
        S2.show('network');
      } catch (err) { toast(err.message, 'error'); }
    };
    urlIn.addEventListener('change', commitURL);
    urlIn.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); commitURL(); } });
    // 注意：这里用 settings2 的 h()，第二参是属性对象——传字符串会被当成 Object.entries 展开，
    // 逐个 setAttribute('0'|'o'…) 直接抛「'o' is not a valid attribute name」。
    const proxyCtl = h('div', { class: 's2-proxy-ctl' }, modeSel, urlIn);
    const proxyModeDesc = '跟随系统：按启动 Gleam 时的 HTTPS_PROXY / HTTP_PROXY / NO_PROXY 环境变量；'
      + '手动：只走下面这一条地址；不使用：直连，环境变量一律忽略。改完重启 Gleam 才切换。';
    const effDesc = curMode === 'none'
      ? '直连：代理已关闭，环境变量不再生效。'
      : curMode === 'manual'
        ? `手动：${net.proxy_url || '（还没填地址，保存后才生效）'}`
        : proxyDesc;
    root.replaceChildren(
      head('网络', 'Gleam 访问模型服务时的连通情况与代理方式。'),
      h('div', { class: 's2-label-row' }, label('连接检测'), run),
      list,
      label('代理'),
      card(
        row({ icon: 'globe', title: '代理方式', desc: proxyModeDesc,
          control: proxyCtl }),
        row({ icon: 'link', title: '当前生效', desc: effDesc }),
      ));
    paint();
  });

  // 「连接与出网」台账原来挂在安全页底部，整体搬到「连接」页（节点搬家，原有绑定不变）
  const ledger = $('#cx-conn-list') && $('#cx-conn-list').closest('.card');
  const connPanel = document.querySelector('.settings-panel[data-stab="conn"]');
  if (ledger && connPanel) { ledger.classList.add('s2-ledger'); connPanel.classList.add('s2-legacy'); connPanel.append(ledger); }

  S2.init();
})();
