/* 设置 v2 · 模型：已配置模型卡片 + 「添加模型」对话框。
 * Gleam 只有一套主模型接入（+ 可选辅助模型 fast_model + 档位映射），所以这里的「模型列表」
 * 如实就是这两张卡；校验是一次真实的最小请求（POST /api/llm/test，带表单覆盖值）；
 * API Key 只往后端写，页面上永远只显示「已设置 / 未设置」，不回显、不打日志。 */
'use strict';

(() => {
  const { h, row, card, label, head, btn, badge, empty, ico, settings, save, openLink } = S2;

  // 厂商控制台的密钥页（公开地址；没有把握的厂商就不给链接）
  const KEY_PAGES = {
    zhipu: 'https://open.bigmodel.cn/usercenter/apikeys',
    deepseek: 'https://platform.deepseek.com/api_keys',
    moonshot: 'https://platform.moonshot.cn/console/api-keys',
    qwen: 'https://bailian.console.aliyun.com/?apiKey=1',
    volc: 'https://console.volcengine.com/ark/region:ark+cn-beijing/apiKey',
    minimax: 'https://platform.minimaxi.com/user-center/basic-information/interface-key',
    openai: 'https://platform.openai.com/api-keys',
    anthropic: 'https://console.anthropic.com/settings/keys',
    openrouter: 'https://openrouter.ai/keys',
  };
  const CUSTOM = {
    'custom-openai': { name: 'OpenAI Compatible', types: [{ value: 'openai_chat', label: 'Chat Completions API' }, { value: 'openai_responses', label: 'Responses API' }], base: 'https://api.example.com/v1' },
    'custom-anthropic': { name: 'Anthropic Compatible', types: [{ value: 'anthropic', label: 'Messages API' }], base: 'https://api.example.com' },
  };
  const PROTO_LABEL = { openai_chat: 'Chat Completions', openai_responses: 'Responses', anthropic: 'Messages' };
  const TEST_HINTS = {
    auth: '鉴权失败：API Key 不正确或已过期', not_found: '找不到地址或模型：核对 Base URL 与 Model ID',
    rate_limited: '被限流（429），稍后再试', provider: '厂商服务暂不可用（5xx）', api: '接口返回错误',
    timeout: '连接超时（15 秒）：检查网络或 Base URL', network: '网络不通：检查 Base URL、代理或防火墙', config: '配置不完整',
  };

  let PROVS = null;
  async function providers() {
    if (!PROVS) PROVS = (await api('GET', '/api/providers')).providers || [];
    return PROVS;
  }
  const glyph = (name, cls = '') => h('span', { class: 's2-glyph ' + cls, 'aria-hidden': 'true', text: ([...String(name || '?').replace(/^[^\p{L}\p{N}]+/u, '')][0] || '?').toUpperCase() });
  const hostOf = (u) => { try { return new URL(u).host; } catch { return u || ''; } };

  function ensureShell() {
    const panel = document.querySelector('.settings-panel[data-stab="llm"]');
    let root = panel.querySelector('.s2-page');
    if (root) return root;
    root = h('div', { class: 's2-page', id: 's2-llm' });
    const adv = h('details', { class: 's2-adv' }, h('summary', {}, h('span', { html: ico('chevron', 14) }), '高级：接入参数、生成参数与模型档位'));
    [...panel.children].forEach((c) => adv.append(c));
    panel.append(root, adv);
    return root;
  }

  function describe(s, provs) {
    const L = s.llm;
    const p = provs.find((x) => x.id === L.provider_id);
    if (p) {
      const plan = (p.plans || []).find((x) => x.kind === L.plan) || (p.plans || [])[0];
      return { name: p.name, sub: plan ? plan.label : '', id: p.id };
    }
    const proto = L.protocol || 'openai_chat';
    return { name: proto === 'anthropic' ? 'Anthropic Compatible' : 'OpenAI Compatible', sub: (PROTO_LABEL[proto] || proto) + ' API', id: proto === 'anthropic' ? 'custom-anthropic' : 'custom-openai' };
  }

  const isConfigured = (L) => !!(L.api_key_set || L.provider_id || L.protocol);
  async function render() {
    const root = ensureShell();
    const [s, provs] = await Promise.all([settings(true), providers()]);
    const L = s.llm;
    const configured = isConfigured(L);
    const add = btn('', () => openDialog(null), 'btn btn-primary btn-sm s2-add', {});
    add.innerHTML = ico('plus', 14) + `<span>${configured ? '更换模型' : '添加模型'}</span>`;
    const kids = [head('模型', 'Gleam 用一套主模型执行任务，可另配一个更快更便宜的辅助模型处理压缩、复核等高频小调用。', add)];
    if (L.provider === 'mock') {
      kids.push(h('div', { class: 's2-notice' }, h('span', { html: ico('warn', 14) }),
        h('span', { text: '当前以离线演示模型运行（启动参数 --mock-llm）。这里保存的配置会写入本机，正常启动时生效；校验仍是真实网络请求。' })));
    }
    if (!configured) {
      kids.push(card(empty('llm', '暂未配置模型', '添加一个模型后，Gleam 才能真正执行任务。', btn('添加模型', () => openDialog(null), 'btn btn-primary btn-sm'))));
    } else {
      const d = describe(s, provs);
      const keyState = L.api_key_set ? `密钥已设置 · 仅发往 ${L.api_key_host_cur || hostOf(L.base_url)}` : (L.api_key_host ? `已存的密钥属于 ${L.api_key_host}，需重填` : '未设置密钥');
      const actions = (onEdit, onDel, delTitle) => h('div', { class: 's2-icon-btns' },
        h('button', { type: 'button', class: 's2-icon-btn', title: '编辑', 'aria-label': '编辑', html: ico('edit', 15), onclick: onEdit }),
        h('button', { type: 'button', class: 's2-icon-btn s2-icon-btn--danger', title: delTitle, 'aria-label': delTitle, html: ico('trash', 15), onclick: onDel }));
      const mainCard = h('div', { class: 's2-model' },
        glyph(d.name),
        h('div', { class: 's2-model-text' },
          h('strong', {}, L.model || '（未填模型）', badge('主模型', 'accent')),
          h('small', { text: `${d.name}${d.sub ? ' · ' + d.sub : ''}` }),
          h('small', { class: L.api_key_set ? 's2-ok' : 's2-warn', text: `${hostOf(L.base_url)} · ${keyState}` })),
        actions(() => openDialog(s), async () => {
          if (!await confirmModal('删除后会清除这套接入的密钥与厂商选择，Gleam 需要重新添加模型才能执行任务。', '删除模型配置？', { okText: '删除', danger: true })) return;
          await save({ llm: { clear_api_key: true, provider_id: '', plan: '', protocol: '', fast_model: '' } }, '模型配置已删除');
          render();
        }, '删除'));
      const cards = [mainCard];
      if (L.fast_model) {
        cards.push(h('div', { class: 's2-model' }, glyph(d.name, 's2-glyph--soft'),
          h('div', { class: 's2-model-text' },
            h('strong', {}, L.fast_model, badge('辅助模型', 'muted')),
            h('small', { text: `与主模型同一接入（${hostOf(L.base_url)}），用于上下文压缩、AI 复核、技能优化等辅助调用。` })),
          actions(() => openDialog(s), async () => {
            if (!await confirmModal('移除后辅助调用改用主模型。', '移除辅助模型？', { okText: '移除', danger: true })) return;
            await save({ llm: { fast_model: '' } }, '已移除辅助模型');
            render();
          }, '移除')));
      }
      kids.push(label('已配置'), h('div', { class: 's2-card s2-card--list' }, ...cards));
      const tiers = Object.entries(L.tiers || {});
      if (tiers.length) {
        kids.push(label('模型档位'), card(...tiers.map(([k, v]) => row({ icon: 'layers', title: k, desc: v }))));
      }
    }
    root.replaceChildren(...kids);
  }

  /* ---------------- 添加 / 编辑对话框 ---------------- */
  async function openDialog(cur) {
    const provs = await providers();
    const SETL = (await settings()).llm;
    const L = cur ? cur.llm : null;
    const st = {
      prov: L ? describe(cur, provs).id : (provs[0] ? provs[0].id : 'custom-openai'),
      plan: L ? L.plan : '', apiType: L ? (L.protocol || 'openai_chat') : 'openai_chat',
      base: L ? L.base_url : '', key: '', models: L ? [L.model, L.fast_model].filter(Boolean) : [''],
      editing: !!L, keySet: !!(L && L.api_key_set),
    };
    if (!st.models.length) st.models = [''];
    let dirty = false;
    let busy = false;
    const touch = () => { dirty = true; };

    const overlay = h('div', { class: 's2-dialog-overlay', role: 'presentation' });
    const dlg = h('div', { class: 's2-dialog', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 's2-dlg-title' });
    overlay.append(dlg);
    const body = h('div', { class: 's2-dialog-body' });
    const result = h('div', { class: 's2-dialog-result', role: 'status', 'aria-live': 'polite', hidden: true });
    const submit = h('button', { type: 'button', class: 'btn btn-primary btn-sm' }, '校验并添加');
    const cancel = h('button', { type: 'button', class: 'btn btn-ghost btn-sm', text: '取消' });
    dlg.append(
      h('div', { class: 's2-dialog-head' },
        h('strong', { id: 's2-dlg-title', text: st.editing ? '编辑模型' : '添加模型' }),
        h('div', { class: 's2-dialog-head-btns' },
          h('button', { type: 'button', class: 's2-icon-btn', title: '接入说明', 'aria-label': '接入说明', html: ico('book', 15), onclick: () => openLink('https://github.com/gleam-ai/Gleam#readme') }),
          h('button', { type: 'button', class: 's2-icon-btn', title: '关闭', 'aria-label': '关闭', html: ico('x', 15), onclick: () => tryClose() }))),
      body, result,
      h('div', { class: 's2-dialog-foot' }, cancel, submit));

    function isCustom() { return st.prov.startsWith('custom-'); }
    function curProv() { return provs.find((p) => p.id === st.prov); }
    function curPlan() { const p = curProv(); return p ? ((p.plans || []).find((x) => x.kind === st.plan) || p.plans[0]) : null; }

    function providerPicker() {
      const wrap = h('div', { class: 's2-picker' });
      const p = curProv();
      const name = p ? p.name : CUSTOM[st.prov].name;
      const trigger = h('button', { type: 'button', class: 's2-picker-btn', 'aria-haspopup': 'listbox', 'aria-expanded': 'false', id: 's2-dlg-prov' },
        glyph(name), h('span', { class: 's2-picker-name', text: name }), h('span', { class: 's2-picker-chev', html: ico('chevron', 14) }));
      const list = h('div', { class: 's2-picker-list', role: 'listbox', hidden: true, 'aria-label': '供应商' });
      const opt = (id, nm) => h('button', { type: 'button', role: 'option', class: 's2-picker-opt', 'aria-selected': String(id === st.prov),
        onclick: () => {
          if (id !== st.prov) {
            st.prov = id; st.plan = ''; touch();
            if (CUSTOM[id]) { st.apiType = CUSTOM[id].types[0].value; st.base = ''; st.models = ['']; }
            else { const pl = curPlan(); st.base = pl ? pl.base_url : ''; st.models = [pl ? pl.model : '']; }
          }
          close(); paint();
        } }, glyph(nm), h('span', { text: nm }), id === st.prov ? h('span', { class: 's2-picker-check', html: ico('check', 14) }) : null);
      list.append(h('div', { class: 's2-picker-group', text: '厂商' }), ...provs.map((x) => opt(x.id, x.name)),
        h('div', { class: 's2-picker-group', text: '自定义' }), opt('custom-openai', CUSTOM['custom-openai'].name), opt('custom-anthropic', CUSTOM['custom-anthropic'].name));
      function close() { list.hidden = true; trigger.setAttribute('aria-expanded', 'false'); }
      trigger.addEventListener('click', () => { list.hidden = !list.hidden; trigger.setAttribute('aria-expanded', String(!list.hidden)); if (!list.hidden) (list.querySelector('[aria-selected="true"]') || list.querySelector('.s2-picker-opt')).focus(); });
      list.addEventListener('keydown', (e) => {
        const opts = [...list.querySelectorAll('.s2-picker-opt')];
        const i = opts.indexOf(document.activeElement);
        if (e.key === 'ArrowDown') { e.preventDefault(); (opts[i + 1] || opts[0]).focus(); }
        if (e.key === 'ArrowUp') { e.preventDefault(); (opts[i - 1] || opts[opts.length - 1]).focus(); }
        if (e.key === 'Escape') { e.stopPropagation(); close(); trigger.focus(); }
      });
      wrap.append(trigger, list);
      return wrap;
    }
    const field = (lab, ctl, extra) => h('div', { class: 's2-field' }, h('div', { class: 's2-field-label' }, h('label', { text: lab, for: ctl.id || null }), extra || null), ctl);
    const sel = (id, opts, val, on) => {
      const s = h('select', { class: 's2-select s2-select--block', id });
      opts.forEach((o) => s.append(h('option', { value: o.value, text: o.label })));
      s.value = val;
      s.addEventListener('change', () => { touch(); on(s.value); });
      return s;
    };
    const input = (id, val, ph, on, type = 'text') => {
      const i = h('input', { class: 'input s2-dlg-input', id, type, value: val || '', placeholder: ph, autocomplete: 'off', spellcheck: 'false' });
      i.addEventListener('input', () => { touch(); on(i.value); result.hidden = true; validate(); });
      return i;
    };
    function keyField() {
      const i = input('s2-dlg-key', st.key, st.keySet ? '已设置，留空表示不修改' : '请输入 API Key', (v) => { st.key = v; }, 'password');
      const eye = h('button', { type: 'button', class: 's2-eye', 'aria-label': '显示密钥', title: '显示密钥', html: ico('eye', 15) });
      eye.addEventListener('click', () => {
        const show = i.type === 'password';
        i.type = show ? 'text' : 'password';
        eye.innerHTML = ico(show ? 'eyeOff' : 'eye', 15);
        eye.title = eye.ariaLabel = show ? '隐藏密钥' : '显示密钥';
      });
      const link = !isCustom() && KEY_PAGES[st.prov]
        ? h('button', { type: 'button', class: 's2-link', onclick: () => openLink(KEY_PAGES[st.prov]) }, '获取 API Key', h('span', { html: ico('out', 12) }))
        : null;
      return field('API Key', h('div', { class: 's2-input-wrap' }, i, eye), link);
    }
    function modelIdsField() {
      const rows = h('div', { class: 's2-model-ids' });
      st.models.forEach((m, idx) => {
        const i = input('s2-dlg-model-' + idx, m, idx === 0 ? '例如 qwen3-max' : '辅助模型，例如一个更快的小模型', (v) => { st.models[idx] = v; });
        const del = h('button', { type: 'button', class: 's2-icon-btn', title: '删除', 'aria-label': '删除这个 Model ID', html: ico('trash', 14), disabled: st.models.length === 1 ? true : null,
          onclick: () => { st.models.splice(idx, 1); touch(); paint(); } });
        rows.append(h('div', { class: 's2-model-id-row' }, idx === 1 ? h('span', { class: 's2-model-id-tag', text: '辅助' }) : null, i, del));
      });
      const addId = st.models.length < 2
        ? h('button', { type: 'button', class: 's2-link', onclick: () => { st.models.push(''); touch(); paint(); } }, h('span', { html: ico('plus', 12) }), '添加 Model ID')
        : h('span', { class: 's2-field-hint', text: '第二个作为辅助模型' });
      return field('Model ID', rows, addId);
    }
    function namedModelField() {
      const pl = curPlan();
      const listId = 's2-dlg-models-list';
      const i = input('s2-dlg-model-0', st.models[0] || (pl ? pl.model : ''), pl ? pl.model : '', (v) => { st.models[0] = v; });
      i.setAttribute('list', listId);
      if (!st.models[0] && pl) st.models[0] = pl.model;
      const dl = h('datalist', { id: listId }, pl ? h('option', { value: pl.model }) : null);
      const fetchBtn = h('button', { type: 'button', class: 's2-link' }, h('span', { html: ico('refresh', 12) }), '拉取可用模型');
      fetchBtn.addEventListener('click', async () => {
        fetchBtn.disabled = true;
        showResult('pending', '正在向厂商拉取模型列表…');
        try {
          const r = await api('POST', '/api/llm/models', target(st.models[0]));
          if (r.ok && Array.isArray(r.models) && r.models.length) {
            dl.replaceChildren(...r.models.map((m) => h('option', { value: m.id })));
            showResult('ok', `拉到 ${r.models.length} 个模型，点模型框即可选择`);
          } else showResult('fail', (TEST_HINTS[r.kind] || '拉取失败') + '：这个入口可能不提供模型列表，直接手填即可');
        } catch (err) { showResult('fail', err.message); }
        fetchBtn.disabled = false;
      });
      return field('模型', h('div', {}, i, dl), fetchBtn);
    }
    function target(model) {
      const b = isCustom()
        ? { provider_id: '', plan: '', protocol: st.apiType, base_url: st.base.trim(), model: (model || '').trim() }
        : { provider_id: st.prov, plan: (curPlan() || {}).kind || '', protocol: (curPlan() || {}).protocol || '', base_url: (curPlan() || {}).base_url || '', model: (model || '').trim() };
      if (st.key.trim()) b.api_key = st.key.trim();
      return b;
    }
    function validate() {
      const needKey = !st.keySet && !st.key.trim();
      const ok = (isCustom() ? !!st.base.trim() : true) && !!(st.models[0] || '').trim() && !needKey;
      submit.disabled = busy || !ok;
      return ok;
    }
    function showResult(kind, text) {
      result.hidden = false;
      result.className = 's2-dialog-result is-' + kind;
      result.textContent = text;
    }
    const replacing = !st.editing && SETL && isConfigured(SETL) ? SETL.model : '';
    if (replacing) dlg.querySelector('#s2-dlg-title').textContent = '更换模型';
    function paint() {
      const kids = [];
      if (replacing) kids.push(h('p', { class: 's2-dialog-desc', text: `Gleam 只有一套主模型接入：校验通过并保存后，会替换当前的 ${replacing}。` }));
      kids.push(field('供应商', providerPicker()));
      if (isCustom()) {
        const c = CUSTOM[st.prov];
        kids.push(field('API 类型', sel('s2-dlg-type', c.types, st.apiType, (v) => { st.apiType = v; })));
        kids.push(field('接口地址（Base URL）', input('s2-dlg-base', st.base, c.base, (v) => { st.base = v; })));
        kids.push(keyField(), modelIdsField());
      } else {
        const p = curProv();
        if (!st.plan) st.plan = (p.plans[0] || {}).kind || '';
        kids.push(field('类型', sel('s2-dlg-plan', p.plans.map((x) => ({ value: x.kind, label: x.label })), st.plan, (v) => {
          st.plan = v; const pl = curPlan(); st.models[0] = pl ? pl.model : ''; paint();
        })));
        kids.push(namedModelField(), keyField());
        kids.push(modelIdsFieldNamed());
      }
      body.replaceChildren(...kids);
      validate();
    }
    function modelIdsFieldNamed() {
      const i = input('s2-dlg-model-1', st.models[1] || '', '留空表示辅助调用也用主模型', (v) => { st.models[1] = v; });
      return field('辅助模型（可选）', i);
    }

    async function submitNow() {
      if (!validate()) return;
      busy = true; validate();
      const models = st.models.map((m) => (m || '').trim()).filter(Boolean);
      try {
        for (let i = 0; i < models.length; i++) {
          showResult('pending', `正在校验 ${models[i]}（一次真实的最小请求）…`);
          const r = await api('POST', '/api/llm/test', target(models[i]));
          if (!r.ok) {
            showResult('fail', `${models[i]}：${TEST_HINTS[r.kind] || '校验失败'}${r.http_status ? `（HTTP ${r.http_status}）` : ''}`);
            return;
          }
        }
        const t = target(models[0]);
        const patch = { llm: Object.assign({}, t, { fast_model: models[1] || '' }) };
        await save(patch, null);
        dirty = false;
        toast(`已添加模型 ${models[0]}`, 'success', 2200);
        close();
        render();
      } catch (err) {
        showResult('fail', err.message || String(err));
      } finally {
        busy = false; validate();
      }
    }
    submit.addEventListener('click', submitNow);
    cancel.addEventListener('click', () => tryClose());
    overlay.addEventListener('mousedown', (e) => { if (e.target === overlay) tryClose(); });
    const onKey = (e) => { if (e.key === 'Escape' && !overlay.querySelector('.s2-confirm')) { e.preventDefault(); tryClose(); } };
    document.addEventListener('keydown', onKey, true);
    function close() {
      document.removeEventListener('keydown', onKey, true);
      overlay.remove();
    }
    function tryClose() {
      if (!dirty) { close(); return; }
      const box = h('div', { class: 's2-confirm', role: 'alertdialog', 'aria-modal': 'true', 'aria-labelledby': 's2-cf-t' });
      const keep = h('button', { type: 'button', class: 'btn btn-secondary btn-sm', text: '继续编辑' });
      const drop = h('button', { type: 'button', class: 'btn btn-danger btn-sm', text: '放弃更改' });
      box.append(h('strong', { id: 's2-cf-t', text: '放弃未保存的模型配置？' }),
        h('p', { text: '已填写的供应商、地址和密钥都不会保存。' }),
        h('div', { class: 's2-confirm-btns' }, keep, drop));
      const layer = h('div', { class: 's2-confirm-layer' }, box);
      overlay.append(layer);
      keep.focus();
      const off = (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); layer.remove(); document.removeEventListener('keydown', off, true); } };
      document.addEventListener('keydown', off, true);
      keep.addEventListener('click', () => { layer.remove(); document.removeEventListener('keydown', off, true); });
      drop.addEventListener('click', () => { document.removeEventListener('keydown', off, true); close(); });
    }
    if (!st.editing && !isCustom()) { const pl = curPlan(); st.base = pl ? pl.base_url : ''; st.models = [pl ? pl.model : '']; }
    paint();
    document.body.append(overlay);
    setTimeout(() => $('#s2-dlg-prov') && $('#s2-dlg-prov').focus(), 0);
  }

  S2.register('llm', render);
  S2.openModelDialog = openDialog;
})();
