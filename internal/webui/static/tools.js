// Tools：从 app.js 拆出来的独立一屏。
//
// 依赖 app.js 的 $ / api / toast / el / esc 等，所以必须排在 app.js 之后；
// app.js 的 VIEW_LOADERS 用 `tools: () => loadTools()` 惰性引用（那个 const 在 app.js 解析期就求值，
// 直接写 `tools: loadTools` 会 ReferenceError）。

/* ---------- 工具 ---------- */
async function loadTools() {
  const list = $('#tools-list');
  list.innerHTML = '<div class="skeleton" style="height:56px"></div>';
  try {
    const { tools } = await api('GET', '/api/tools');
    list.innerHTML = '';
    tools.forEach((t) => {
      const row = el('div', 'card row');
      const main = el('div', 'row-main');
      main.innerHTML = `<div class="row-title"><code class="tool-name">${esc(t.name)}</code>
        <select class="input select-sm perm-select" data-tool="${esc(t.name)}" title="权限级别：只读自动放行 / 需我批准 / 完全访问（始终审批）">
          <option value="readonly">只读放行</option>
          <option value="user_approved">需我批准</option>
          <option value="full_access">完全访问</option>
          <option value="default">内置默认${t.overridden ? '（当前 ' + esc(PERM_LABELS[t.permission] || t.permission) + '）' : ''}</option>
        </select>
        ${t.overridden ? '<span class="badge badge--mode">已覆盖</span>' : ''}</div>
        <div class="row-sub">${esc(t.description)}</div>`;
      const details = el('details', 'schema');
      details.innerHTML = `<summary>参数说明</summary>${schemaSummary(t.schema)}
        <details class="tl-raw"><summary>查看原始 schema</summary><pre>${esc(JSON.stringify(t.schema, null, 2))}</pre></details>`;
      main.appendChild(details);
      const callBtn = el('button', 'btn btn-secondary btn-sm', '调用');
      callBtn.addEventListener('click', () => openToolCallDialog(t));
      row.appendChild(main);
      row.appendChild(callBtn);
      list.appendChild(row);
      const sel = row.querySelector('.perm-select');
      sel.value = t.overridden ? t.permission : 'default';
      sel.addEventListener('change', async () => {
        try {
          await api('POST', '/api/tools/permission', { name: t.name, permission: sel.value });
          toast(`工具 ${t.name} 权限已更新（${sel.value === 'default' ? '恢复内置默认' : (PERM_LABELS[sel.value] || sel.value)}）`, 'success');
          loadTools();
        } catch (err) {
          toast(err.message, 'error');
          loadTools();
        }
      });
    });
  } catch (err) { loadError(list, err, loadTools); }
}

// schemaSummary（L6，2026-09-23 QA）：JSON Schema 直 dump 对不写代码的人是天书，
// 先渲染「参数名 · 类型 · 必填 · 一句话说明」表，原始 JSON 收进二级折叠。
function schemaSummary(schema) {
  const props = schema && schema.properties;
  if (!props || !Object.keys(props).length) return '<div class="row-sub">此工具不需要参数。</div>';
  const required = new Set((schema.required || []));
  const rows = Object.entries(props).map(([k, v]) => {
    const type = v.type || (v.enum ? '枚举' : '任意');
    const req = required.has(k) ? '<span class="badge badge--warn">必填</span>' : '<span class="row-sub">可选</span>';
    const desc = esc(v.description || '—');
    const enm = v.enum ? `<div class="row-sub">可选值：${v.enum.map((x) => esc(String(x))).join(' · ')}</div>` : '';
    const dft = v.default !== undefined ? `<div class="row-sub">默认：${esc(JSON.stringify(v.default))}</div>` : '';
    return `<tr><td><code>${esc(k)}</code></td><td>${esc(type)}</td><td>${req}</td><td>${desc}${enm}${dft}</td></tr>`;
  }).join('');
  return `<table class="schema-table"><thead><tr><th>参数</th><th>类型</th><th></th><th>说明</th></tr></thead><tbody>${rows}</tbody></table>`;
}

function openToolCallDialog(t) {
  Modal.open(`调用 <code style="font-family:var(--font-mono);font-size:var(--fs-md)">${esc(t.name)}</code>`, (box) => {
    const form = el('form');
    const f = el('div', 'field');
    f.innerHTML = `<label class="field-label" for="tool-args">参数（JSON）</label>`;
    const ta = el('textarea', 'textarea');
    ta.id = 'tool-args';
    ta.rows = 6;
    ta.value = '{}';
    if (t.schema && t.schema.properties) {
      const sample = {};
      Object.entries(t.schema.properties).forEach(([k, v]) => { sample[k] = v.type === 'integer' || v.type === 'number' ? 0 : v.type === 'boolean' ? false : ''; });
      ta.value = JSON.stringify(sample, null, 2);
    }
    f.appendChild(ta);
    form.appendChild(f);
    const actions = el('div', 'modal-actions');
    const cancel = el('button', 'btn btn-secondary', '取消');
    cancel.type = 'button';
    cancel.addEventListener('click', Modal.close);
    const run = el('button', 'btn btn-primary', '执行');
    actions.appendChild(cancel);
    actions.appendChild(run);
    form.appendChild(actions);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      let args;
      try { args = JSON.parse(ta.value || '{}'); }
      catch { toast('参数不是合法 JSON', 'error'); return; }
      run.disabled = true;
      run.innerHTML = ICONS.spinner + ' 执行中';
      try {
        const out = await api('POST', '/api/tools/call', { name: t.name, args });
        Modal.close();
        Modal.open(`执行结果 · ${esc(t.name)}`, (b) => {
          const pre = el('pre');
          pre.textContent = JSON.stringify(out.output, null, 2);
          b.appendChild(pre);
          const act = el('div', 'modal-actions');
          const close = el('button', 'btn btn-secondary', '关闭');
          close.addEventListener('click', Modal.close);
          act.appendChild(close);
          b.appendChild(act);
        });
      } catch (err) {
        toast(`调用失败：${err.message}`, 'error', 6500);
        run.disabled = false;
        run.textContent = '执行';
      }
    });
    box.appendChild(form);
  });
}
