// Memory：从 app.js 拆出来的独立一屏。
//
// 依赖 app.js 的 $ / api / toast / el / esc 等，所以必须排在 app.js 之后；
// app.js 的 VIEW_LOADERS 用 `memory: () => initMemoryOnce()` 惰性引用（那个 const 在 app.js 解析期就求值，
// 直接写 `memory: initMemoryOnce` 会 ReferenceError）。

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
