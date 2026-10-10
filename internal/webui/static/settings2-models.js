/* 设置 v2 · 模型：多模型列表管理 + 添加/编辑对话框。
 * Gleam 支持同时配置多个模型（不同厂商、不同协议），对话页下拉切换。
 * 每条模型是独立的 ModelEntry（厂商/协议/地址/模型/密钥），CRUD 走 /api/models。
 * API Key 只往后端写，页面上永远只显示「已设置 / 未设置」，不回显、不打日志。 */
'use strict';

(() => {
  const { h, row, card, label, head, btn, badge, empty, ico, settings, save, openLink } = S2;

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
    tokendance: 'https://tokendance.space/keys',
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
    const adv = h('details', { class: 's2-adv' }, h('summary', {}, h('span', { html: ico('chevron', 14) }), '高级：生成参数与模型档位'));
    [...panel.children].forEach((c) => adv.append(c));
    panel.append(root, adv);
    return root;
  }

  function describeEntry(entry, provs) {
    const p = provs.find((x) => x.id === entry.provider_id);
    if (p) {
      const plan = (p.plans || []).find((x) => x.kind === entry.plan) || (p.plans || [])[0];
      return { name: p.name, sub: plan ? plan.label : '', id: p.id };
    }
    const proto = entry.protocol || 'openai_chat';
    return { name: proto === 'anthropic' ? 'Anthropic Compatible' : 'OpenAI Compatible', sub: (PROTO_LABEL[proto] || proto) + ' API', id: proto === 'anthropic' ? 'custom-anthropic' : 'custom-openai' };
  }

  async function render() {
    const root = ensureShell();
    const [s, provs] = await Promise.all([settings(true), providers()]);
    const L = s.llm;
    const models = L.models || [];
    const addBtn = btn('', () => openDialog(null), 'btn btn-primary btn-sm s2-add', {});
    addBtn.innerHTML = ico('plus', 14) + `<span>添加模型</span>`;
    const kids = [head('模型', 'Gleam 支持同时配置多个模型，对话时下拉切换。可另配一个更快更便宜的辅助模型处理压缩、复核等高频小调用。', addBtn)];
    if (L.provider === 'mock') {
      kids.push(h('div', { class: 's2-notice' }, h('span', { html: ico('warn', 14) }),
        h('span', { text: '当前以离线演示模型运行（启动参数 --mock-llm）。这里保存的配置会写入本机，正常启动时生效；校验仍是真实网络请求。' })));
    }
    if (!models.length) {
      kids.push(card(empty('llm', '暂未配置模型', '添加一个模型后，Gleam 才能真正执行任务。', btn('添加模型', () => openDialog(null), 'btn btn-primary btn-sm'))));
    } else {
      const cards = models.map((m) => {
        const d = describeEntry(m, provs);
        const keyState = m.api_key_set ? '密钥已设置' : '未设置密钥';
        const badges = [];
        if (m.is_default) badges.push(badge('默认', 'accent'));
        if (m.is_fast) badges.push(badge('辅助', 'muted'));
        const actions = h('div', { class: 's2-icon-btns' },
          !m.is_default ? h('button', { type: 'button', class: 's2-icon-btn', title: '设为默认', 'aria-label': '设为默认', html: ico('check', 15),
            onclick: async () => {
              try { await api('POST', `/api/models/${encodeURIComponent(m.id)}/default`); toast('已设为默认模型', 'success', 1800); render(); }
              catch (err) { toast(`操作失败：${err.message}`, 'error'); }
            } }) : null,
          h('button', { type: 'button', class: 's2-icon-btn', title: '编辑', 'aria-label': '编辑', html: ico('edit', 15),
            onclick: () => openDialog(m) }),
          h('button', { type: 'button', class: 's2-icon-btn s2-icon-btn--danger', title: '删除', 'aria-label': '删除', html: ico('trash', 15),
            onclick: async () => {
              if (!await confirmModal(`删除「${m.name}」后会清除这套接入的密钥与配置。`, '删除模型？', { okText: '删除', danger: true })) return;
              try {
                await api('DELETE', `/api/models/${encodeURIComponent(m.id)}`);
                toast(`已删除 ${m.name}`, 'success', 1800);
                render();
              } catch (err) { toast(`删除失败：${err.message}`, 'error'); }
            } }));
        return h('div', { class: 's2-model' },
          glyph(d.name),
          h('div', { class: 's2-model-text' },
            h('strong', {}, m.model || '（未填模型）', ...badges),
            h('small', { text: `${m.name} · ${d.name}${d.sub ? ' · ' + d.sub : ''}` }),
            h('small', { class: m.api_key_set ? 's2-ok' : 's2-warn', text: `${hostOf(m.base_url)} · ${keyState}` })),
          actions);
      });
      kids.push(label('已配置'), h('div', { class: 's2-card s2-card--list' }, ...cards));
      const tiers = Object.entries(L.tiers || {});
      if (tiers.length) {
        kids.push(label('模型档位'), card(...tiers.map(([k, v]) => row({ icon: 'layers', title: k, desc: v }))));
      }
    }
    root.replaceChildren(...kids);
  }

  /* ---------------- 添加 / 编辑对话框 ---------------- */
  async function openDialog(entry) {
    const provs = await providers();
    const isEdit = !!entry;
    const st = {
      prov: isEdit ? describeEntry(entry, provs).id : (provs[0] ? provs[0].id : 'custom-openai'),
      plan: isEdit ? (entry.plan || '') : '',
      apiType: isEdit ? (entry.protocol || 'openai_chat') : 'openai_chat',
      base: isEdit ? (entry.base_url || '') : '',
      key: '',
      modelName: isEdit ? (entry.model || '') : '',
      displayName: isEdit ? (entry.name || '') : '',
      isFast: isEdit ? !!entry.is_fast : false,
      isDefault: isEdit ? !!entry.is_default : false,
      editing: isEdit,
      keySet: isEdit ? !!entry.api_key_set : false,
      entryId: isEdit ? entry.id : '',
    };
    let dirty = false;
    let busy = false;
    const touch = () => { dirty = true; };

    const overlay = h('div', { class: 's2-dialog-overlay', role: 'presentation' });
    const dlg = h('div', { class: 's2-dialog', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 's2-dlg-title' });
    overlay.append(dlg);
    const body = h('div', { class: 's2-dialog-body' });
    const result = h('div', { class: 's2-dialog-result', role: 'status', 'aria-live': 'polite', hidden: true });
    const submit = h('button', { type: 'button', class: 'btn btn-primary btn-sm' }, isEdit ? '校验并保存' : '校验并添加');
    const cancel = h('button', { type: 'button', class: 'btn btn-ghost btn-sm', text: '取消' });
    dlg.append(
      h('div', { class: 's2-dialog-head' },
        h('strong', { id: 's2-dlg-title', text: isEdit ? '编辑模型' : '添加模型' }),
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
            if (CUSTOM[id]) { st.apiType = CUSTOM[id].types[0].value; st.base = ''; st.modelName = ''; }
            else { const pl = curPlan(); st.base = pl ? pl.base_url : ''; st.modelName = pl ? pl.model : ''; }
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
    function nameField() {
      return field('显示名称', input('s2-dlg-name', st.displayName, '例如：我的 DeepSeek', (v) => { st.displayName = v; }));
    }
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
    function modelField() {
      const pl = curPlan();
      const listId = 's2-dlg-models-list';
      const i = input('s2-dlg-model', st.modelName || (pl ? pl.model : ''), pl ? pl.model : '', (v) => { st.modelName = v; });
      i.setAttribute('list', listId);
      if (!st.modelName && pl) st.modelName = pl.model;
      const dl = h('datalist', { id: listId }, pl ? h('option', { value: pl.model }) : null);
      const fetchBtn = h('button', { type: 'button', class: 's2-link' }, h('span', { html: ico('refresh', 12) }), '拉取可用模型');
      fetchBtn.addEventListener('click', async () => {
        fetchBtn.disabled = true;
        showResult('pending', '正在向厂商拉取模型列表…');
        try {
          const r = await api('POST', '/api/llm/models', target());
          if (r.ok && Array.isArray(r.models) && r.models.length) {
            dl.replaceChildren(...r.models.map((m) => h('option', { value: m.id })));
            showResult('ok', `拉到 ${r.models.length} 个模型，点模型框即可选择`);
          } else showResult('fail', (TEST_HINTS[r.kind] || '拉取失败') + '：这个入口可能不提供模型列表，直接手填即可');
        } catch (err) { showResult('fail', err.message); }
        fetchBtn.disabled = false;
      });
      return field('模型 ID', h('div', {}, i, dl), fetchBtn);
    }
    function fastToggle() {
      const cb = h('input', { type: 'checkbox', class: 'switch', role: 'switch', 'aria-label': '辅助模型' });
      cb.checked = st.isFast;
      cb.addEventListener('change', () => { st.isFast = cb.checked; touch(); });
      return field('辅助模型', h('div', { class: 's2-field-row' }, cb, h('span', { class: 's2-field-hint', text: '用于上下文压缩、AI 复核等高频小调用，选一个更快更便宜的模型' })));
    }
    function defaultToggle() {
      const cb = h('input', { type: 'checkbox', class: 'switch', role: 'switch', 'aria-label': '设为默认' });
      cb.checked = st.isDefault;
      cb.addEventListener('change', () => { st.isDefault = cb.checked; touch(); });
      return field('默认模型', h('div', { class: 's2-field-row' }, cb, h('span', { class: 's2-field-hint', text: '新对话默认使用这个模型' })));
    }
    function target() {
      const b = isCustom()
        ? { provider_id: '', plan: '', protocol: st.apiType, base_url: st.base.trim(), model: (st.modelName || '').trim() }
        : { provider_id: st.prov, plan: (curPlan() || {}).kind || '', protocol: (curPlan() || {}).protocol || '', base_url: (curPlan() || {}).base_url || '', model: (st.modelName || '').trim() };
      if (st.key.trim()) b.api_key = st.key.trim();
      b.name = (st.displayName || '').trim();
      b.is_fast = st.isFast;
      b.is_default = st.isDefault;
      if (st.editing) {
        b.id = st.entryId;
        b.model_id = st.entryId; // 用于测试时查找现有模型的密钥
      }
      return b;
    }
    function validate() {
      const needKey = !st.keySet && !st.key.trim();
      const ok = !!st.displayName.trim() && (isCustom() ? !!st.base.trim() : true) && !!(st.modelName || '').trim() && !needKey;
      submit.disabled = busy || !ok;
      return ok;
    }
    function showResult(kind, text) {
      result.hidden = false;
      result.className = 's2-dialog-result is-' + kind;
      result.textContent = text;
    }
    function paint() {
      const kids = [];
      kids.push(field('供应商', providerPicker()));
      if (isCustom()) {
        const c = CUSTOM[st.prov];
        kids.push(field('API 类型', sel('s2-dlg-type', c.types, st.apiType, (v) => { st.apiType = v; })));
        kids.push(field('接口地址（Base URL）', input('s2-dlg-base', st.base, c.base, (v) => { st.base = v; })));
      } else {
        const p = curProv();
        if (!st.plan) st.plan = (p.plans[0] || {}).kind || '';
        kids.push(field('类型', sel('s2-dlg-plan', p.plans.map((x) => ({ value: x.kind, label: x.label })), st.plan, (v) => {
          st.plan = v; const pl = curPlan(); st.modelName = pl ? pl.model : ''; paint();
        })));
      }
      kids.push(nameField(), modelField(), keyField());
      kids.push(defaultToggle(), fastToggle());
      body.replaceChildren(...kids);
      validate();
    }

    async function submitNow() {
      if (!validate()) return;
      busy = true; validate();
      const t = target();
      try {
        showResult('pending', `正在校验 ${t.model}（一次真实的最小请求）…`);
        const r = await api('POST', '/api/llm/test', t);
        if (!r.ok) {
          showResult('fail', `${t.model}：${TEST_HINTS[r.kind] || '校验失败'}${r.http_status ? `（HTTP ${r.http_status}）` : ''}`);
          return;
        }
        if (st.editing) {
          await api('PUT', `/api/models/${encodeURIComponent(st.entryId)}`, t);
          dirty = false;
          toast(`已保存 ${t.name}`, 'success', 2200);
        } else {
          await api('POST', '/api/models', t);
          dirty = false;
          toast(`已添加模型 ${t.name}`, 'success', 2200);
        }
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
    if (!st.editing && !isCustom()) { const pl = curPlan(); st.base = pl ? pl.base_url : ''; st.modelName = pl ? pl.model : ''; }
    paint();
    document.body.append(overlay);
    setTimeout(() => $('#s2-dlg-prov') && $('#s2-dlg-prov').focus(), 0);
  }

  S2.register('llm', render);
  S2.openModelDialog = openDialog;
})();
