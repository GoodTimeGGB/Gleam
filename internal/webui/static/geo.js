// GEO（生成式引擎优化）：从 app.js 拆出来的独立一屏。
//
// 依赖 app.js 里的 $ / api / toast / el / esc / prioKey / prioLabel，所以必须排在 app.js 之后。
// app.js 的 VIEW_LOADERS 用 `geo: () => loadGEO()` 惰性引用——本文件在后、函数在前，
// 写成 `geo: loadGEO` 会在 app.js 解析期就去找一个还不存在的函数。
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
