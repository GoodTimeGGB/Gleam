// Growth：从 app.js 拆出来的独立一屏。
//
// 依赖 app.js 的 $ / api / toast / el / esc 等，所以必须排在 app.js 之后；
// app.js 的 VIEW_LOADERS 用 `growth: () => loadGrowth()` 惰性引用（那个 const 在 app.js 解析期就求值，
// 直接写 `growth: loadGrowth` 会 ReferenceError）。

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

