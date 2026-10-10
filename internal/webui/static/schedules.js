// Schedules：从 app.js 拆出来的独立一屏。
//
// 依赖 app.js 的 $ / api / toast / el / esc 等，所以必须排在 app.js 之后；
// app.js 的 VIEW_LOADERS 用 `schedules: () => loadSchedules()` 惰性引用（那个 const 在 app.js 解析期就求值，
// 直接写 `schedules: loadSchedules` 会 ReferenceError）。

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
      return; // 新建表单默认收起，点右上角「新建定时任务」才展开
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
