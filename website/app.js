/* Gleam 官网交互：滚动显现 + 目标模式演示（纯前端模拟真实引擎时序） */
'use strict';

/* ---------- 滚动显现 ---------- */
(function () {
  const els = document.querySelectorAll('.reveal');
  if (!('IntersectionObserver' in window) ||
      window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    els.forEach((e) => e.classList.add('in'));
    return;
  }
  const io = new IntersectionObserver((entries) => {
    entries.forEach((en) => {
      if (en.isIntersecting) {
        en.target.classList.add('in');
        io.unobserve(en.target);
      }
    });
  }, { threshold: 0.12 });
  els.forEach((e) => io.observe(e));
})();

/* ---------- 目标模式演示引擎 ---------- */
const $ = (s) => document.querySelector(s);

const SCENARIOS = {
  organize: {
    goal: '整理桌面文件并按日期归档',
    script: [
      { d: 300, phase: 'plan', text: '正在理解目标并规划步骤…', pct: 3 },
      { d: 500, phase: 'plan', text: '已制定 3 步计划（1 个并行）', pct: 8 },
      { d: 400, phase: 'execute', text: '✅ s1 file.list 扫描桌面文件（1/3）', pct: 40 },
      { d: 500, phase: 'execute', text: '✅ s2 file.move 归档 12 张图片 → 图片/2026-09（2/3）', pct: 70 },
      { d: 450, phase: 'execute', text: '✅ s3 file.move 归档 5 份文档 → 文档/2026-09（3/3）', pct: 90 },
      { d: 500, phase: 'reflect', text: '完成度评估：全部步骤成功', pct: 96 },
    ],
    result: { score: 93, summary: '已归档 12 张图片与 5 份文档，桌面已清爽。' },
    suggest: '可以把"桌面归档"固化为技能，每周自动执行一次？',
  },
  briefing: {
    goal: '生成今日简报并写入 daily.md',
    script: [
      { d: 300, phase: 'plan', text: '正在理解目标并规划步骤…', pct: 3 },
      { d: 450, phase: 'plan', text: '已制定 3 步计划', pct: 8 },
      { d: 600, phase: 'execute', text: '✅ s1 web.fetch 抓取今日要闻（1/3）', pct: 40 },
      { d: 450, phase: 'execute', text: '✅ s2 memory.search 回顾你关注的话题（2/3）', pct: 70 },
      { d: 550, phase: 'execute', text: '✅ s3 file.write 写入 daily.md（3/3）', pct: 90 },
      { d: 500, phase: 'reflect', text: '完成度评估：简报结构完整', pct: 96 },
    ],
    result: { score: 90, summary: 'daily.md 已生成：今日要闻 8 条，含你关注的前端与 AI 板块。' },
    suggest: '需要每天早上 9 点自动生成简报吗？一句话即可创建定时任务。',
  },
  cleanup: {
    goal: '清理临时目录中的 .tmp 文件',
    script: [
      { d: 300, phase: 'plan', text: '正在理解目标并规划步骤…', pct: 3 },
      { d: 450, phase: 'plan', text: '已制定 2 步计划（含高风险操作）', pct: 8 },
      { d: 450, phase: 'execute', text: '✅ s1 file.search 找到 23 个 .tmp 文件（1/2）', pct: 45 },
      { approval: true }, // 高风险审批：等待用户决策
      { d: 500, phase: 'execute', text: '✅ s2 file.delete 删除 23 个 .tmp 文件（2/2）', pct: 90, when: 'approved' },
      { d: 500, phase: 'reflect', text: '完成度评估：全部删除成功', pct: 96, when: 'approved' },
    ],
    result: { score: 95, summary: '23 个临时文件已删除，释放 156MB 空间。' },
    approveResult: { score: 0, summary: '你拒绝了删除操作，任务已取消。临时文件原样保留。' },
  },
};

let current = 'organize';
let runToken = 0; // 递增令牌：新一轮运行使旧运行失效

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function setChip(key) {
  current = key;
  $('#demo-goal').textContent = SCENARIOS[key].goal;
  document.querySelectorAll('.chip').forEach((c) =>
    c.setAttribute('aria-pressed', String(c.dataset.demo === key)));
}

document.querySelectorAll('.chip').forEach((chip) => {
  chip.addEventListener('click', () => {
    if ($('#demo-run').disabled) return;
    setChip(chip.dataset.demo);
    resetDemo();
  });
});

function resetDemo() {
  runToken++;
  $('#demo-timeline').innerHTML = '';
  const result = $('#demo-result');
  result.hidden = true;
  $('#demo-progress-bar').style.width = '0%';
  $('#demo-run').disabled = false;
  $('#demo-run').innerHTML = icon('play') + ' 执行';
}

function icon(name) {
  const icons = {
    play: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 4.5v15l13-7.5-13-7.5z"/></svg>',
    stop: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>',
  };
  return icons[name] || '';
}

function addTimelineItem(phase, text, state) {
  const tl = $('#demo-timeline');
  const item = document.createElement('div');
  item.className = `timeline-item${state ? ' timeline-item--' + state : ''}`;
  const kindLabel = phase === 'execute' ? '执行' : phase === 'reflect' ? '反思' : '规划';
  const kind = `<span class="tl-kind tl-kind--${phase}">${kindLabel}</span>`;
  item.innerHTML = kind + text.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
  tl.appendChild(item);
  while (tl.children.length > 14) tl.firstChild.remove();
  return item;
}

function showResult(score, summary) {
  const box = $('#demo-result');
  const ring = box.querySelector('.ring-fg');
  const num = box.querySelector('text');
  box.hidden = false;
  // 重新触发过渡动画
  ring.style.strokeDashoffset = '100';
  requestAnimationFrame(() => requestAnimationFrame(() => {
    ring.style.strokeDashoffset = String(100 - Math.max(0, Math.min(100, score)));
  }));
  num.textContent = String(score);
  $('#demo-summary').textContent = summary;
}

function showApproval(scenario, token) {
  return new Promise((resolve) => {
    const item = addTimelineItem('execute', '', 'active');
    const card = document.createElement('div');
    card.className = 'demo-approval';
    card.innerHTML = `
      <div class="demo-approval-head">⚠ 需要你的批准 <span class="badge badge--high">高风险</span></div>
      <div>即将删除临时目录下 23 个 .tmp 文件（file.delete）</div>
      <div class="demo-approval-actions">
        <button class="btn btn-primary" data-act="ok">✓ 批准执行</button>
        <button class="btn btn-danger" data-act="no">✗ 拒绝</button>
      </div>`;
    item.appendChild(card);
    card.querySelector('[data-act="ok"]').addEventListener('click', () => {
      item.className = 'timeline-item timeline-item--done';
      card.remove();
      addTimelineItem('execute', '已批准 ✓', 'done');
      resolve('approved');
    });
    card.querySelector('[data-act="no"]').addEventListener('click', () => {
      card.querySelector('.demo-approval-actions').remove();
      card.insertAdjacentHTML('beforeend', '<div style="margin-top:8px;color:var(--color-fg-muted);font-size:13px;">✗ 已拒绝</div>');
      resolve('denied');
    });
  });
}

async function runDemo() {
  resetDemo();              // 先重置（内部会递增令牌）
  const token = runToken;   // 再捕获当前令牌，避免立即失效
  const sc = SCENARIOS[current];
  const btn = $('#demo-run');
  btn.disabled = true;

  let approved = null;
  for (const step of sc.script) {
    if (step.approval) {
      approved = await showApproval(sc, token);
      if (token !== runToken) return;
      continue;
    }
    if (step.when === 'approved' && approved !== 'approved') continue;
    await sleep(step.d);
    if (token !== runToken) return; // 用户切换了场景
    addTimelineItem(step.phase, `${step.pct}% · ${step.text}`, step.phase === 'reflect' ? 'done' : 'active');
    $('#demo-progress-bar').style.width = step.pct + '%';
  }
  if (token !== runToken) return;

  await sleep(400);
  if (token !== runToken) return;
  const result = approved === 'denied' ? sc.approveResult : sc.result;
  showResult(result.score, result.summary);
  if (approved !== 'denied' && sc.suggest) {
    addTimelineItem('reflect', '💡 主动提议：' + sc.suggest, 'done');
  }
  $('#demo-progress-bar').style.width = '100%';
  btn.disabled = false;
  btn.innerHTML = icon('play') + ' 再跑一次';
}

$('#demo-run').addEventListener('click', runDemo);
setChip('organize');


/* ---------- 下载区平台自动检测 ---------- */
(function detectPlatform() {
  const ua = navigator.userAgent;
  const tips = {
    windows: document.getElementById("dl-tip-windows"),
    "macos-arm": document.getElementById("dl-tip-macos-arm"),
    "macos-intel": document.getElementById("dl-tip-macos-intel"),
    linux: document.getElementById("dl-tip-linux"),
  };
  const show = (key, text) => {
    const el = tips[key];
    if (!el) return;
    el.textContent = text;
    el.classList.add("dl-tip--show");
  };

  let recommended = null;

  if (/Windows/.test(ua)) {
    recommended = document.querySelector('[data-platform="windows"]');
    show("windows", "✅ 检测到你在使用 Windows，直接下载即可");
  } else if (/Macintosh|MacIntel/.test(ua)) {
    // Apple Silicon 上 navigator.platform 照样报 "MacIntel"，浏览器侧分不出芯片，
    // 所以两张卡都给提示，让用户按「关于本机」的芯片字段自己挑。
    recommended = document.querySelector('[data-platform="macos-arm"]');
    show("macos-arm", "✅ 检测到你在使用 macOS — Apple Silicon (M 系列) 用户请下载此版本");
    show("macos-intel", "Intel 芯片的 Mac 请下载此版本（点左上角 Apple 菜单 → 关于本机 确认）");
  } else if (!/Android/.test(ua) && /Linux|X11/.test(ua)) {
    // 安卓的 UA 里也带 "Linux"，而这里没有安卓产物：不提示好过指一个跑不起来的文件。
    recommended = document.querySelector('[data-platform="linux"]');
    show("linux", "✅ 检测到你在使用 Linux，下载后 chmod +x 即可运行");
  }

  // Highlight recommended card
  if (recommended) {
    recommended.classList.add("dl-card--recommended");
    // Scroll the recommended card into view on the download section
    const dlLink = document.querySelector('a[href="#download"]');
    if (dlLink) {
      dlLink.addEventListener("click", () => {
        setTimeout(() => recommended.scrollIntoView({ behavior: "smooth", block: "center" }), 300);
      });
    }
  }
})();
