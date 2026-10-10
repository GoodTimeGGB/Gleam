// Skills：从 app.js 拆出来的独立一屏。
//
// 依赖 app.js 的 $ / api / toast / el / esc 等，所以必须排在 app.js 之后；
// app.js 的 VIEW_LOADERS 用 `skills: () => loadSkills()` 惰性引用（那个 const 在 app.js 解析期就求值，
// 直接写 `skills: loadSkills` 会 ReferenceError）。

/* ---------- 技能 ---------- */
async function loadSkills() {
  const grid = $('#skills-grid');
  grid.innerHTML = '<div class="skeleton" style="height:80px"></div><div class="skeleton" style="height:80px"></div>';
  try {
    const { skills } = await api('GET', '/api/skills');
    grid.innerHTML = '';
    if (!skills || !skills.length) {
      grid.innerHTML = `<div class="empty">${ICONS.zap}<div class="empty-title">还没有技能</div><p class="empty-desc">完成一次多步骤任务后，Gleam 会主动建议把流程固化为技能；也可以去市场直接安装。</p><button class="btn btn-secondary btn-sm" id="skills-empty-market">去市场看看</button></div>`;
      const go = grid.querySelector('#skills-empty-market');
      if (go) go.addEventListener('click', () => showView('market'));
      return;
    }
    skills.forEach((sk) => grid.appendChild(skillCard(sk)));
  } catch (err) {
    loadError(grid, err, loadSkills);
  }
}

function skillCard(sk) {
  const card = el('div', 'card');
  const head = el('div', 'row');
  head.style.padding = '0';
  const main = el('div', 'row-main');
  main.innerHTML = `<div class="row-title">${esc(sk.name)} <span class="badge badge--version">v${sk.version}</span>
    ${sk.disabled ? '<span class="badge badge--cancelled">已停用</span>' : ''}</div>
    <div class="row-sub">${esc(sk.description || '')}</div>
    <div class="stat">运行 ${sk.runs} 次 · 成功 ${sk.successes} · ${(sk.steps || []).length} 步${sk.params && sk.params.length ? ' · 参数: ' + esc(sk.params.join(', ')) : ''}</div>`;
  const actions = el('div', 'row-actions');
  if (!sk.disabled) {
    const runBtn = el('button', 'btn btn-primary btn-sm');
    runBtn.innerHTML = ICONS.play + ' 运行';
    runBtn.addEventListener('click', () => openSkillRunDialog(sk));
    actions.appendChild(runBtn);
  }
  // 停用而不是删除：技能是用户攒下来的做法，临时不想让它被引用时，不该连步骤一起扔
  const toggle = el('button', 'btn btn-ghost btn-sm');
  toggle.type = 'button';
  toggle.textContent = sk.disabled ? '启用' : '停用';
  toggle.title = sk.disabled ? '启用后重新进入技能清单，可被引用与运行' : '停用后保留内容与统计，但不再进技能清单';
  toggle.setAttribute('aria-label', `${sk.disabled ? '启用' : '停用'}技能 ${sk.name}`);
  toggle.addEventListener('click', async () => {
    toggle.disabled = true;
    try {
      await api('POST', `/api/skills/${encodeURIComponent(sk.name)}/enabled`, { enabled: sk.disabled });
      toast(sk.disabled ? `技能「${sk.name}」已启用` : `技能「${sk.name}」已停用`, 'success');
      loadSkills();
    } catch (err) {
      toast(err.message, 'error');
      toggle.disabled = false;
    }
  });
  actions.appendChild(toggle);
  const delBtn = el('button', 'btn btn-danger btn-sm');
  delBtn.innerHTML = ICONS.trash;
  delBtn.setAttribute('aria-label', `删除技能 ${sk.name}`);
  delBtn.addEventListener('click', async () => {
    if (!await confirmModal(`删除技能「${sk.name}」？此操作不可恢复。`, '删除技能', { okText: '删除', danger: true })) return;
    try { await api('DELETE', `/api/skills/${encodeURIComponent(sk.name)}`); toast('技能已删除', 'success'); loadSkills(); }
    catch (err) { toast(err.message, 'error'); }
  });
  actions.appendChild(delBtn);
  head.appendChild(main);
  head.appendChild(actions);
  card.appendChild(head);
  return card;
}

function openSkillRunDialog(sk) {
  Modal.open(`运行技能「${esc(sk.name)}」`, (box) => {
    const form = el('form');
    (sk.params || []).forEach((p) => {
      const f = el('div', 'field');
      f.innerHTML = `<label class="field-label" for="p-${esc(p)}">${esc(p)}</label>`;
      const input = el('input', 'input');
      input.id = 'p-' + p;
      input.name = p;
      f.appendChild(input);
      form.appendChild(f);
    });
    if (!(sk.params || []).length) form.appendChild(el('p', 'field-hint', '该技能无需参数。'));
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const run = el('button', 'btn btn-primary', '执行');
    run.innerHTML = ICONS.play + ' 执行';
    actions.appendChild(cancel);
    actions.appendChild(run);
    form.appendChild(actions);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      run.disabled = true;
      run.innerHTML = ICONS.spinner + ' 执行中';
      const params = {};
      (sk.params || []).forEach((p) => { params[p] = form.elements[p].value; });
      try {
        const out = await api('POST', `/api/skills/${encodeURIComponent(sk.name)}/run`, { params });
        Modal.close();
        toast(`技能执行${out.status === 'success' ? '成功' : '结束'}（${out.summary || ''}）`, out.status === 'success' ? 'success' : 'info', 6000);
      } catch (err) {
        toast(`执行失败：${err.message}`, 'error');
        run.disabled = false;
        run.innerHTML = ICONS.play + ' 执行';
      }
    });
    box.appendChild(form);
  });
}

function openSkillSaveDialog(sk) {
  Modal.open(`保存技能「${esc(sk.name)}」`, (box) => {
    const form = el('form');
    const nameF = el('div', 'field');
    nameF.innerHTML = `<label class="field-label" for="sk-name">技能名</label>`;
    const nameInput = el('input', 'input');
    nameInput.id = 'sk-name';
    nameInput.value = sk.name;
    nameF.appendChild(nameInput);
    form.appendChild(nameF);
    const descF = el('div', 'field');
    descF.innerHTML = `<label class="field-label" for="sk-desc">描述</label>`;
    const descInput = el('input', 'input');
    descInput.id = 'sk-desc';
    descInput.value = sk.description || '';
    descF.appendChild(descInput);
    form.appendChild(descF);
    const preview = el('pre');
    preview.textContent = JSON.stringify(sk.steps, null, 2);
    form.appendChild(preview);
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const save = el('button', 'btn btn-primary', '保存');
    actions.appendChild(cancel);
    actions.appendChild(save);
    form.appendChild(actions);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const skillName = nameInput.value.trim();
      if (!skillName) { toast('技能名不能为空', 'error'); nameInput.focus(); return; }
      if (save.disabled) return; // 连点两次会覆盖保存或报重名
      save.disabled = true;
      try {
        await api('POST', '/api/skills', {
          name: skillName, description: descInput.value.trim(), steps: sk.steps,
        });
        Modal.close();
        toast(`技能「${skillName}」已保存`, 'success');
      } catch (err) {
        toast(`保存失败：${err.message}`, 'error');
      } finally {
        save.disabled = false;
      }
    });
    box.appendChild(form);
  });
}
