/* 从本机导入：扫本机其他 AI 工具留下的记忆 / 规则、MCP 配置与技能。
 *
 * 依赖 app.js 里的全局（$ / el / esc / api / toast），所以必须在它之后加载。
 *
 * 服务端只读扫描，前端只提交候选 id——路径与命令由服务端查回，
 * 不给「填任意路径读任意文件」留口子。MCP 撞名由安装路径拒掉，这里跳过不覆盖。
 */
const LocalImport = (() => {
  let cache = null;
  const picked = { memory: new Set(), mcp: new Set() };

  async function scan(force) {
    if (cache && !force) return cache;
    cache = await api('GET', '/api/import/scan');
    return cache;
  }
  const size = (n) => (n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(1) + ' KB' : (n / 1048576).toFixed(1) + ' MB');

  /* ---------- 记忆导入（记忆页） ---------- */
  function memRow(c) {
    const row = el('label', 'import-row');
    const cb = el('input');
    cb.type = 'checkbox';
    cb.checked = picked.memory.has(c.id);
    cb.addEventListener('change', () => {
      if (cb.checked) picked.memory.add(c.id); else picked.memory.delete(c.id);
      syncMemBtn();
    });
    const main = el('div', 'import-main');
    main.innerHTML = '<div class="import-title"><span class="import-app">' + esc(c.app) + '</span>' + esc(c.title) + '</div>' +
      '<div class="import-sub">' + esc(c.path) + '</div><div class="import-preview">' + esc(c.preview) + '</div>';
    const meta = el('div', 'import-meta');
    meta.innerHTML = '<span>' + c.entries + ' 条</span><span>' + size(c.bytes) + '</span>';
    row.append(cb, main, meta);
    return row;
  }
  function syncMemBtn() {
    const btn = $('#memory-import-apply');
    if (!btn) return;
    const n = picked.memory.size;
    btn.disabled = n === 0;
    btn.textContent = n ? '导入选中（' + n + '）' : '导入选中';
  }

  async function renderMemory(force) {
    const wrap = $('#memory-import');
    if (!wrap) return;
    wrap.hidden = false;
    const list = $('#memory-import-list');
    const note = $('#memory-import-note');
    const scanBtn = $('#memory-import-scan');
    list.innerHTML = '<div class="skeleton" style="height:52px"></div>';
    let snap;
    try { snap = await scan(force); } catch (err) { list.innerHTML = '<p class="field-hint">' + esc(err.message) + '</p>'; return; }
    const items = snap.memory || [];
    scanBtn.textContent = '重新扫描';
    if (!items.length) {
      list.innerHTML = '<div class="empty"><div class="empty-title">没找到可导入的记忆</div>' +
        '<p class="empty-desc">只在本机找 Claude Code、Codex、Cursor 等工具写过的记忆与规则文件；它们没装过或没写过，这里就是空的。</p></div>';
      syncMemBtn();
      return;
    }
    note.textContent = '扫描到 ' + items.length + ' 份文件。选中后按段落写入 Gleam 的记忆库；全程只读，不改动它们的文件。';
    list.replaceChildren(...items.map(memRow));
    syncMemBtn();
  }

  async function applyMemory() {
    const ids = [...picked.memory];
    if (!ids.length) return;
    const btn = $('#memory-import-apply');
    btn.disabled = true;
    try {
      const r = await api('POST', '/api/import/apply', { memory: ids });
      const m = r.memory || {};
      toast('已导入 ' + (m.imported || 0) + ' 份记忆' + (m.skipped ? '，跳过 ' + m.skipped + ' 份' : ''), 'success');
      picked.memory.clear();
      cache = null;
      renderMemory(true);
    } catch (err) { toast(err.message, 'error'); btn.disabled = false; }
  }

  /* ---------- MCP / 技能检测（市场 → 本机检测） ---------- */
  function mcpRow(c) {
    const row = el('label', 'import-row' + (c.installed ? ' is-dim' : ''));
    const cb = el('input');
    cb.type = 'checkbox';
    cb.disabled = !!c.installed;
    cb.checked = picked.mcp.has(c.id);
    cb.addEventListener('change', () => {
      if (cb.checked) picked.mcp.add(c.id); else picked.mcp.delete(c.id);
      syncMCPBtn();
    });
    const env = c.env_keys && c.env_keys.length ? ' · 环境变量 ' + c.env_keys.map(esc).join(', ') : '';
    const main = el('div', 'import-main');
    main.innerHTML = '<div class="import-title"><span class="import-app">' + esc(c.app) + '</span>' + esc(c.name) +
      (c.installed ? '<span class="badge badge--mode">已安装</span>' : '') + '</div>' +
      '<div class="import-sub">' + esc(c.command) + ' ' + esc((c.args || []).join(' ')) + '</div>' +
      '<div class="import-sub">' + esc(c.path) + env + '</div>';
    row.append(cb, main);
    return row;
  }
  function syncMCPBtn() {
    const btn = $('#local-mcp-apply');
    if (!btn) return;
    const n = picked.mcp.size;
    btn.disabled = n === 0;
    btn.textContent = n ? '导入选中（' + n + '）' : '导入选中';
  }

  async function applyMCP() {
    const ids = [...picked.mcp];
    if (!ids.length) return;
    const btn = $('#local-mcp-apply');
    btn.disabled = true;
    try {
      const r = await api('POST', '/api/import/apply', { mcp: ids });
      const m = r.mcp || {};
      const bits = ['已导入 ' + (m.imported || 0) + ' 台'];
      if (m.already) bits.push('已存在跳过 ' + m.already + ' 台');
      if (m.failed) bits.push('失败 ' + m.failed + ' 台');
      toast(bits.join('，'), 'success');
      picked.mcp.clear();
      cache = null;
      renderLocal(true);
    } catch (err) { toast(err.message, 'error'); btn.disabled = false; }
  }

  async function renderLocal(force) {
    const body = $('#local-import-body');
    if (!body) return;
    body.innerHTML = '<div class="skeleton" style="height:72px"></div>';
    let snap;
    try { snap = await scan(force); } catch (err) { body.innerHTML = '<p class="field-hint">' + esc(err.message) + '</p>'; return; }
    const mcps = snap.mcp || [];
    const skills = snap.skills || [];
    const out = el('div', 'import-sections');

    const mcpSec = el('div', 'import-section');
    const mcpBtn = el('button', 'btn btn-primary btn-sm', '导入选中');
    mcpBtn.type = 'button';
    mcpBtn.id = 'local-mcp-apply';
    mcpBtn.disabled = true;
    mcpBtn.addEventListener('click', applyMCP);
    const mcpHead = el('div', 'import-head');
    mcpHead.innerHTML = '<h2 class="section-title">本机检测到的 MCP</h2>';
    mcpHead.appendChild(mcpBtn);
    mcpSec.appendChild(mcpHead);
    const skipped = snap.mcp_skipped || 0;
    const mcpNote = el('p', 'field-hint');
    mcpNote.textContent = '来自 Claude Code / Claude Desktop / Cursor / VS Code / Codex 的配置文件。只读命令与参数，环境变量的值不会读出来；同名服务器已安装的会跳过，不覆盖你现在的配置。'
      + (skipped ? ' 另有 ' + skipped + ' 台是 URL/SSE 型，Gleam 目前只支持 stdio 接入，没有列出。' : '');
    mcpSec.appendChild(mcpNote);
    if (mcps.length) {
      const list = el('div', 'row-list');
      list.replaceChildren(...mcps.map(mcpRow));
      mcpSec.appendChild(list);
    } else {
      mcpSec.insertAdjacentHTML('beforeend', '<div class="empty"><div class="empty-title">没检测到 MCP 配置</div><p class="empty-desc">本机这些工具都没配过 MCP 服务器。</p></div>');
    }
    out.appendChild(mcpSec);

    const skillSec = el('div', 'import-section');
    skillSec.insertAdjacentHTML('beforeend', '<div class="import-head"><h2 class="section-title">本机检测到的技能</h2></div>' +
      '<p class="field-hint">Gleam 的技能是「可执行的步骤清单」，别的工具留下的是 Markdown 说明——两者不是同一种东西，硬导进来只会得到一个空壳，所以这里只列出、不导入。</p>');
    if (skills.length) {
      const list = el('div', 'row-list');
      skills.forEach((c) => {
        const row = el('div', 'import-row is-dim');
        const main = el('div', 'import-main');
        main.innerHTML = '<div class="import-title"><span class="import-app">' + esc(c.app) + '</span>' + esc(c.name) +
          (c.installed ? '<span class="badge badge--mode">同名已存在</span>' : '') + '</div>' +
          '<div class="import-sub">' + esc(c.path) + '</div><div class="import-preview">' + esc(c.preview) + '</div>';
        row.append(main);
        list.appendChild(row);
      });
      skillSec.appendChild(list);
    } else {
      skillSec.insertAdjacentHTML('beforeend', '<div class="empty"><div class="empty-title">没检测到技能</div><p class="empty-desc">在 ~/.claude/skills 或 ~/.codex/skills 下放过 SKILL.md 才会出现在这里。</p></div>');
    }
    out.appendChild(skillSec);

    body.replaceChildren(out);
    syncMCPBtn();
  }

  const scanBtn = $('#memory-import-scan');
  if (scanBtn) scanBtn.addEventListener('click', () => renderMemory(true));
  const applyBtn = $('#memory-import-apply');
  if (applyBtn) applyBtn.addEventListener('click', applyMemory);

  return { renderMemory, renderLocal };
})();
