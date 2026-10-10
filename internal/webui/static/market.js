// Market：从 app.js 拆出来的独立一屏。
//
// 依赖 app.js 的 $ / api / toast / el / esc 等，所以必须排在 app.js 之后；
// app.js 的 VIEW_LOADERS 用 `market: () => loadMarket()` 惰性引用（那个 const 在 app.js 解析期就求值，
// 直接写 `market: loadMarket` 会 ReferenceError）。

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
  LocalImport.renderLocal(); // 「本机检测」页签：扫本机别的工具配过的 MCP / 技能
}

async function loadMCPMarket() {
  const wrap = $('#mcp-presets');
  wrap.innerHTML = '<div class="skeleton" style="height:64px"></div>';
  try {
    const res = await api('GET', '/api/market/mcp?q=' + encodeURIComponent($('#mcp-search').value.trim()));
    const presets = res.presets || [];
    wrap.innerHTML = '';
    // 日常那句「实测最快：npm 国内镜像…」不在这里显示了（列表页要的是干净）。
    // 但 `source_note` **必须留**：它说的是"目录源连不上，这份来自缓存，可能不全"——
    // 把残缺说成完整，比不显示更坏。所以只隐常规信息，异常说明照旧显示。
    if (res.source_note) wrap.appendChild(el('div', 'market-note', res.source_note));
    if (!presets.length) {
      wrap.innerHTML += `<div class="empty">${ICONS.search}<div class="empty-title">没有匹配的 MCP 服务器</div></div>`;
      return;
    }
    presets.forEach((p) => wrap.appendChild(mcpPresetCard(p)));
    renderMCPTags(presets);
  } catch (err) { loadError(wrap, err, loadMCPMarket); }
}

// 市场条目图标：自绘 24 栅格线条图，**不引外部品牌图**（与设置页同一套约束）。
//
// 为什么按关键词猜而不是每条手写：内置目录只有十几条，远端目录一次就能拉回来几十条，
// 手写跟不上；而抓 favicon 要引外部品牌资源、还多一条出网路径。猜不到就退回字母方块——
// 朴素比来路不明的图标诚实。
const MKT_ICON = {
  folder: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.7 2.5 15.3 0 18M12 3c-2.5 2.7-2.5 15.3 0 18"/>',
  brain: '<path d="M9 4a3 3 0 0 0-3 3 3 3 0 0 0-2 5 3 3 0 0 0 2 5 3 3 0 0 0 3 3 3 3 0 0 0 3-3V7a3 3 0 0 0-3-3z"/><path d="M15 4a3 3 0 0 1 3 3 3 3 0 0 1 2 5 3 3 0 0 1-2 5 3 3 0 0 1-3 3 3 3 0 0 1-3-3"/>',
  browser: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18"/><circle cx="7" cy="6.5" r=".5"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  branch: '<circle cx="6" cy="6" r="2"/><circle cx="6" cy="18" r="2"/><circle cx="18" cy="8" r="2"/><path d="M6 8v8M18 10c0 4-6 3-12 6"/>',
  db: '<ellipse cx="12" cy="6" rx="7" ry="3"/><path d="M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6"/>',
  terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 10 2.5 2L7 14M12.5 14H17"/>',
  robot: '<rect x="5" y="8" width="14" height="10" rx="2"/><path d="M12 4v4M9 13h.01M15 13h.01M9 18v2M15 18v2"/>',
  plug: '<path d="M9 3v6M15 3v6M6 9h12v3a6 6 0 0 1-12 0z"/><path d="M12 18v3"/>',
  book: '<path d="M4 5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2z"/><path d="M4 19a2 2 0 0 1 2-2h13"/>',
  calendar: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 10h18M8 3v4M16 3v4"/>',
  code: '<path d="m9 8-4 4 4 4M15 8l4 4-4 4"/>',
  chart: '<path d="M4 19V5M4 19h16M8 16v-5M12 16V8M16 16v-3"/>',
  flask: '<path d="M9 3h6M10 3v6L4.5 18.5A1.7 1.7 0 0 0 6 21h12a1.7 1.7 0 0 0 1.5-2.5L14 9V3"/><path d="M7 15h10"/>',
  shield: '<path d="M12 3 5 6v5c0 4.6 3 8.4 7 10 4-1.6 7-5.4 7-10V6z"/>',
  mail: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="m3.5 7 8.5 6 8.5-6"/>',
  cloud: '<path d="M7 18h10a4 4 0 0 0 .6-8 6 6 0 0 0-11.4 1.6A3.5 3.5 0 0 0 7 18z"/>',
  image: '<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="m4 18 5-5 4 4 3-3 4 4"/>',
  pdf: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13h6M9 17h4"/>',
};
// 关键词 → 图标。**顺序有意义**：先命中的赢，所以把更具体的词排在前面。
const MKT_ICON_RULES = [
  [/filesystem|文件|folder/, 'folder'],
  [/git|版本管理|仓库/, 'branch'],
  [/sqlite|postgres|mysql|mongo|redis|duckdb|数据库|database|\bdb\b/, 'db'],
  [/puppeteer|playwright|browser|浏览器|chrome|selenium/, 'browser'],
  [/memory|记忆|knowledge/, 'brain'],
  [/time|时间|时区|clock/, 'clock'],
  [/fetch|web|网页|http|crawl|scrap/, 'globe'],
  [/thinking|推理|reason/, 'brain'],
  [/shell|terminal|command|终端/, 'terminal'],
  [/postman|api|graphql|openapi/, 'plug'],
  [/pdf|doc|报表|文档|report/, 'pdf'],
  [/image|图|设计|design|ui|ux/, 'image'],
  [/mail|邮件|email|smtp/, 'mail'],
  [/cloud|vercel|netlify|部署|deploy|k8s|docker/, 'cloud'],
  [/chart|数据|分析|analytics|metric|统计/, 'chart'],
  [/test|测试|review|评审|安全|security|secure/, 'shield'],
  [/book|知识|note|笔记|文档库/, 'book'],
  [/calendar|日程|定时|schedule/, 'calendar'],
  [/code|开发|dev|lint|build/, 'code'],
  [/实验|labs|flask|sandbox/, 'flask'],
  [/robot|agent|自动化|automation/, 'robot'],
];

// marketIcon 挑一个自绘图标：命中关键词用它，命中不了返回空串（调用方退回字母方块）。
function marketIcon(p) {
  const hay = `${p.id || ''} ${p.name || ''} ${p.desc || ''} ${(p.tags || []).join(' ')}`.toLowerCase();
  for (const [re, key] of MKT_ICON_RULES) {
    if (re.test(hay)) return MKT_ICON[key] || '';
  }
  return '';
}

// marketHue 按名字取一个稳定的色相：同一台服务器每次都是同一个颜色，
// 而不同条目能看出区别——列表读起来才不会糊成一片灰方块。
function marketHue(name) {
  let h = 0;
  for (const ch of String(name || '?')) h = (h * 31 + ch.codePointAt(0)) % 360;
  return h;
}

// 市场条目：语义图标（自绘）或字母方块 · 名称 · 一行说明
function marketTile(p) {
  const svg = marketIcon(p);
  const t = el('span', 'market-icon' + (svg ? ' market-icon--glyph' : ''));
  t.style.setProperty('--mkt-h', String(marketHue(p.name)));
  if (svg) {
    t.innerHTML = `<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${svg}</svg>`;
  } else {
    t.textContent = ([...String(p.name || '?')][0] || '?').toUpperCase();
  }
  t.setAttribute('aria-hidden', 'true');
  return t;
}
function mcpPresetCard(p) {
  const card = el('div', 'market-item');
  card.dataset.tags = (p.tags || []).join('|');
  card.appendChild(marketTile(p));
  const main = el('div', 'market-main');
  // 来源与「装不了」都要在按下之前就说：点完才报错，观感上跟"根本不在目录里"分不出来。
  const src = p.remote ? '<span class="badge badge--mode">官方注册表</span>' : '';
  // 「缺运行时」与「装不了」是两件事：前者是这台机器缺东西（能补），后者是这条本机跑不了（补不了）。
  const noRT = p.runtime_found === false && p.installable !== false;
  const blocked = p.installable === false
    ? '<span class="badge badge--warn">装不了</span>'
    : (noRT ? `<span class="badge badge--warn">缺 ${esc(p.runtime)}</span>` : '');
  // 名字单独包一层：**只有它能被截断**。名字和徽标挤在同一行又不截断时，
  // 长名字会把徽标顶出卡片、压在右边那颗按钮上——那正是之前看到的重叠。
  const badges = `${p.installed ? '<span class="badge badge--success">已安装</span>' : ''}`
    + `${p.params && p.params.length ? '<span class="badge badge--mode">需配置</span>' : ''}${src}${blocked}`;
  main.innerHTML = `<div class="market-name"><span class="market-title-text">${esc(p.name)}</span>${badges}</div>
    <div class="market-desc">${esc(p.installable === false && p.unsupported ? p.unsupported : p.desc)}</div>`;
  main.title = `${p.desc || ''}\n${p.command} · 信任 ${PERM_LABELS[p.trust] || p.trust}${p.tags && p.tags.length ? ' · ' + p.tags.join(' / ') : ''}`;
  card.appendChild(main);
  // 装不了就不给按钮：徽标已经把话说完了，再放一颗写着「装不了」的灰按钮
  // 等于同一句话说两遍，还把名字挤得更短。
  if (p.installable !== false) {
    const actions = el('div', 'row-actions');
    const btn = el('button', 'btn btn-secondary btn-sm', p.installed ? '重装' : '安装');
    if (noRT) {
      // 缺运行时：该给的是"怎么补"，所以按钮说的是下一步，而不是重复徽标那句话
      btn.textContent = '如何补齐';
      btn.title = p.runtime_why || ('本机没有 ' + p.runtime);
      btn.addEventListener('click', () => toast(p.runtime_why || ('本机没有 ' + p.runtime), 'info', 8000));
    } else {
      btn.addEventListener('click', () => openMCPInstallDialog(p));
    }
    actions.appendChild(btn);
    card.appendChild(actions);
  }
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

// 技能页的分类筛选：与 MCP 页同一套交互（点一下筛、再点取消），判据从 tags 换成
// 目录给的 category——分类是单一归属，不会出现"一条技能同时属于三格"。
let skillCatActive = '';
function renderSkillCats(cats) {
  const row = $('#skill-cats');
  if (!row) return;
  row.replaceChildren();
  const mk = (label, val) => {
    const b = el('button', 'chip' + (skillCatActive === val ? ' active' : ''), label);
    b.type = 'button';
    b.setAttribute('aria-pressed', String(skillCatActive === val));
    b.addEventListener('click', () => {
      skillCatActive = skillCatActive === val ? '' : val;
      renderSkillCats(cats);
      applySkillCatFilter();
    });
    return b;
  };
  row.appendChild(mk('全部', ''));
  cats.forEach((c) => row.appendChild(mk(c, c)));
}
function applySkillCatFilter() {
  document.querySelectorAll('#skill-presets .market-item').forEach((c) => {
    c.hidden = !!skillCatActive && c.dataset.category !== skillCatActive;
  });
}

async function loadSkillMarket() {
  const wrap = $('#skill-presets');
  wrap.innerHTML = '<div class="skeleton" style="height:64px"></div>';
  try {
    const res = await api('GET', '/api/market/skills?q=' + encodeURIComponent($('#skill-search').value.trim()));
    const presets = res.presets || [];
    // 分类表由后端给：界面自己维护一份就会漂，而漂的方向是"筛选栏有一格点了是空的"
    renderSkillCats(res.categories || []);
    wrap.innerHTML = '';
    if (!presets.length) {
      wrap.innerHTML = `<div class="empty">${ICONS.search}<div class="empty-title">没有匹配的技能模板</div></div>`;
      return;
    }
    presets.forEach((s) => {
      const card = el('div', 'market-item');
      card.dataset.category = s.category || '';
      card.appendChild(marketTile({ name: s.name, desc: s.description, tags: s.tags }));
      const main = el('div', 'market-main');
      const cat = s.category ? `<span class="badge badge--mode">${esc(s.category)}</span>` : '';
      main.innerHTML = `<div class="market-name"><span class="market-title-text">${esc(s.name)}</span>${s.installed ? '<span class="badge badge--success">已安装</span>' : ''}${cat}</div>
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
    applySkillCatFilter();
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
