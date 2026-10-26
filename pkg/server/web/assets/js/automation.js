'use strict';
(function () {
  const { html, icon } = P;
  const st = { rules: [], conflicts: [], settings: null, templates: [] };
  const TYPE = { exact: 'Whole message or first word', contains: 'Anywhere in the message', prefix: 'Message starts with', regex: 'Pattern (advanced)' };

  /* ---------------- Auto-replies ---------------- */
  function ruleRow(r, i) {
    const kws = r.pattern.split(',').map((k) => k.trim()).filter(Boolean);
    return html`<li class="rule">
      <label class="switch"><input type="checkbox" data-change="rule-toggle" data-name="${r.name}" ${r.enabled ? P.raw('checked') : ''} aria-label="${r.enabled ? 'Switch off' : 'Switch on'} ${r.name}"><span class="track" aria-hidden="true"></span></label>
      <div class="grow">
        <div class="row"><strong>${r.name}</strong>${r.action === 'handover' ? P.statusPill('warn', 'Hands chat to a person') : ''}${!r.enabled ? P.statusPill('', 'Off') : ''}</div>
        ${r.description ? html`<div class="hint">${r.description}</div>` : ''}
        <div class="keywords"><span class="hint">${TYPE[r.type] || r.type}:</span>${kws.map((k) => html`<span class="tag">${k}</span>`)}</div>
        <div class="reply-preview">${r.reply}</div>
        ${r.missing && r.missing.length ? html`<div class="error-text">Customers get the holding reply instead until you fill in: ${r.missing.map((m) => '{{' + m + '}}').join(', ')}. <a href="#/settings?tab=business">Business details</a></div>` : ''}
      </div>
      <div class="row rule-actions">
        <button class="btn small ghost icon-only" type="button" data-action="rule-move" data-index="${i}" data-dir="-1" aria-label="Move ${r.name} up" ${i === 0 ? P.raw('disabled') : ''}>${icon('up', 'icon-sm')}</button>
        <button class="btn small ghost icon-only" type="button" data-action="rule-move" data-index="${i}" data-dir="1" aria-label="Move ${r.name} down" ${i === st.rules.length - 1 ? P.raw('disabled') : ''}>${icon('down', 'icon-sm')}</button>
        <button class="btn small" type="button" data-action="rule-edit" data-name="${r.name}">Edit</button>
        <button class="btn small" type="button" data-action="rule-duplicate" data-name="${r.name}">Duplicate</button>
        <button class="btn small ghost icon-only" type="button" data-action="rule-delete" data-name="${r.name}" aria-label="Delete ${r.name}">${icon('trash', 'icon-sm')}</button>
      </div>
    </li>`;
  }

  function drawRules() {
    const box = P.$('#rules-box');
    if (!box) return;
    P.mount(P.$('#rule-conflicts'), st.conflicts.length ? html`<div class="notice warn">${icon('alert')}<div><p><strong>Some keywords are used by more than one rule.</strong> The rule higher in the list wins.</p><ul>${st.conflicts.map((c) => html`<li>${c}</li>`)}</ul></div></div>` : '');
    if (!st.rules.length) {
      P.mount(box, P.empty('No auto-replies', 'Create a rule, or restore the starter rules.', html`<button class="btn primary" type="button" data-action="rule-new">New rule</button>`));
      return;
    }
    P.mount(box, html`<ul class="list">${st.rules.map(ruleRow)}</ul>`);
  }

  // Rules and templates deleted but still inside their Undo window.
  const pendingRules = new Set(), pendingTemplates = new Set();
  async function loadRules() {
    const r = await P.get('/rules');
    st.allRules = r.rules; // full order, including rules waiting to be deleted
    st.rules = r.rules.filter((x) => !pendingRules.has(x.name)); st.conflicts = r.conflicts || [];
    drawRules();
  }

  function systemReplies(a) {
    return html`<form class="card" data-form="system-replies" id="system-replies">
      <div class="card-head"><div><h2>Built-in replies</h2><p>Sent for keywords every customer can use, in any chat.</p></div></div>
      <div class="card-body stack">
        <div class="field">${P.switchEl('enable_default_reply', a.enable_default_reply, 'Reply when nothing matches', 'Sent at most once every 30 minutes per customer.')}</div>
        <div class="field"><label for="sr-default">Fallback reply</label><textarea id="sr-default" name="default_reply" maxlength="1000">${a.default_reply}</textarea>${P.varPicker('sr-default', [], { reply: true })}</div>
        <div class="field"><label for="sr-missing">Reply when an answer needs a detail you have not filled in</label><textarea id="sr-missing" name="missing_detail_reply" maxlength="1000">${a.missing_detail_reply || ''}</textarea>
          <span class="hint">For example, a customer asks for prices but Settings &gt; Business details has no prices yet. The customer gets this reply, and the chat appears under Needs attention so someone can answer.</span></div>
        <div class="field"><label for="sr-handover">Reply when a customer asks for a person</label><textarea id="sr-handover" name="handover_reply" maxlength="1000">${a.handover_reply}</textarea>${P.varPicker('sr-handover', [], { reply: true })}
          <span class="hint">Triggered by AGENT, HUMAN, PERSON, "talk to someone", "baat karni hai" and similar. Automatic replies then pause for ${a.handover_pause_hours} hours and you get an alert.</span></div>
        <div class="field"><label for="sr-optout">Reply to STOP</label><textarea id="sr-optout" name="opt_out_reply" maxlength="1000">${a.opt_out_reply}</textarea>
          <span class="hint">Triggered by STOP, UNSUBSCRIBE, "rok do", "band karo", "मत भेजो" and similar. The customer is marked do-not-message.</span></div>
        <div class="field"><label for="sr-optin">Reply to START</label><textarea id="sr-optin" name="opt_in_reply" maxlength="1000">${a.opt_in_reply}</textarea>
          <span class="hint">Triggered by START, SUBSCRIBE, "shuru karo" and similar. Records the customer's opt-in.</span></div>
      </div>
    </form>`;
  }

  P.change('rule-toggle', async (el) => {
    const enabled = el.checked;
    try {
      await P.post('/rules/toggle', { name: el.dataset.name, enabled });
      P.toast(el.dataset.name + (enabled ? ' is on.' : ' is off.'));
      await loadRules();
    } catch (e) { el.checked = !enabled; throw e; }
  });
  P.act('rule-move', async (el) => {
    const i = Number(el.dataset.index), j = i + Number(el.dataset.dir);
    if (j < 0 || j >= st.rules.length) return;
    // Reorder the full list, so a rule waiting out its Undo window keeps its place.
    const names = (st.allRules || st.rules).map((r) => r.name);
    const a = names.indexOf(st.rules[i].name), b = names.indexOf(st.rules[j].name);
    [names[a], names[b]] = [names[b], names[a]];
    await P.post('/rules/reorder', { names });
    await loadRules();
    const btn = P.$(`[data-action="rule-move"][data-index="${j}"][data-dir="${el.dataset.dir}"]`) || P.$(`[data-action="rule-move"][data-index="${j}"]`);
    if (btn && !btn.disabled) btn.focus();
  });
  P.act('rule-delete', (el) => {
    const name = el.dataset.name;
    pendingRules.add(name);
    st.rules = st.rules.filter((r) => r.name !== name);
    drawRules();
    P.undoable('Deleted rule ' + name + '.', () => P.del('/rules/' + encodeURIComponent(name)).finally(() => pendingRules.delete(name)).then(loadRules), () => { pendingRules.delete(name); loadRules().catch(P.error); });
  });
  P.act('rule-reset', async () => {
    const ok = await P.confirm({ title: 'Restore the starter rules?', message: 'All your auto-reply rules will be replaced by the starter set (greeting menu, services, prices, hours, talk to a person, thanks, goodbye, help).', confirmLabel: 'Restore', danger: true });
    if (!ok) return;
    await P.post('/rules/reset-defaults');
    P.toast('Starter rules restored.');
    loadRules().catch(P.error);
  });
  P.act('rule-new', () => ruleEditor(null));
  P.act('rule-edit', (el) => ruleEditor(st.rules.find((r) => r.name === el.dataset.name)));
  P.act('rule-duplicate', (el) => {
    const r = st.rules.find((x) => x.name === el.dataset.name);
    ruleEditor(Object.assign({}, r, { name: (r.name + ' copy').slice(0, 64), enabled: false }), true);
  });

  async function ruleEditor(r, isCopy) {
    await P.loadVars();
    const isNew = !r || isCopy;
    r = r || { name: '', type: 'exact', pattern: '', reply: '', description: '', enabled: true, action: '' };
    P.modal({
      title: isNew ? 'New auto-reply' : 'Edit ' + r.name,
      wide: true,
      body: html`<form data-form="rule-save" id="rule-form" class="stack" novalidate>
        <input type="hidden" name="original_name" value="${isNew ? '' : r.name}">
        <div class="form-grid">
          <div class="field"><label for="rf-name">Name</label><input type="text" id="rf-name" name="name" value="${r.name}" maxlength="64" required autofocus><span class="hint">Letters, numbers, spaces, dots, dashes.</span></div>
          <div class="field"><label for="rf-type">Match</label><select id="rf-type" name="type">${Object.keys(TYPE).map((t) => html`<option value="${t}" ${t === r.type ? P.raw('selected') : ''}>${TYPE[t]}</option>`)}</select></div>
          <div class="field full"><label for="rf-kw">Keywords</label>${P.chipsField('rf-kw', 'pattern', r.pattern, 'Type a keyword and press Enter')}
            <span class="hint">Numbers such as 1 or 2 only match right after a reply that shows a numbered menu.</span></div>
          <div class="field full"><label for="rf-reply">Reply</label><textarea id="rf-reply" name="reply" maxlength="4000" rows="6" required>${r.reply}</textarea>${P.varPicker('rf-reply', [], { reply: true })}</div>
          <div class="field full"><span class="label">Preview with a sample customer</span><div class="preview-box" id="rf-preview" aria-live="polite"></div><span class="preview-note hint"></span></div>
          <div class="field"><label for="rf-action">After replying</label><select id="rf-action" name="action"><option value="">Keep the bot on</option><option value="handover" ${r.action === 'handover' ? P.raw('selected') : ''}>Hand the chat to a person and alert staff</option></select></div>
          <div class="field"><label for="rf-desc">Description (optional)</label><input type="text" id="rf-desc" name="description" value="${r.description}" maxlength="256"></div>
          <div class="field full">${P.switchEl('enabled', r.enabled, 'Rule is on')}</div>
        </div>
      </form>`,
      foot: html`<button class="btn" type="button" data-modal-close>Cancel</button><button class="btn primary" type="submit" form="rule-form">Save rule</button>`,
      onOpen: (el) => { P.$$('[data-chips]', el).forEach(P.chips); P.livePreview(P.$('#rf-reply', el), P.$('#rf-preview', el)); },
    });
  }

  P.form('rule-save', async (form, data) => {
    if (!data.name.trim()) { P.fieldError(form, 'name', 'Give the rule a name.'); return; }
    if (!data.pattern.trim()) { P.$('#rf-kw').focus(); P.toast('Add at least one keyword.', { type: 'error' }); return; }
    if (!data.reply.trim()) { P.fieldError(form, 'reply', 'Write the reply.'); return; }
    const r = await P.post('/rules', { name: data.name, original_name: data.original_name, type: data.type, pattern: data.pattern, reply: data.reply, description: data.description, enabled: !!data.enabled, action: data.action });
    P.$('[data-modal-close]', form.closest('.modal')).click();
    P.toast('Rule saved.' + (r.conflicts && r.conflicts.length ? ' Some keywords overlap with other rules.' : ''));
    loadRules().catch(P.error);
  });

  P.form('rule-test', async (form, data) => {
    if (!data.message.trim()) { P.fieldError(form, 'message', 'Type a customer message.'); return; }
    const r = await P.post('/rules/test', { message: data.message, menu_active: !!data.menu_active });
    P.mount(P.$('#rule-test-result'), html`<div class="stack-sm">
      <div class="row">${r.rule ? P.statusPill(r.reply ? 'ok' : 'warn', P.ruleLabel(r.rule)) : P.statusPill('', 'No reply')}${r.handover ? P.statusPill('warn', 'Hands chat to a person') : ''}</div>
      ${r.reply ? html`<div class="preview-box">${r.reply}</div>` : ''}${r.skipped ? html`<p class="hint">${r.skipped}</p>` : ''}
    </div>`);
  });

  async function saveSystemReplies(form, data) {
    await P.patch('/settings', { automation: { enable_default_reply: !!data.enable_default_reply, default_reply: data.default_reply, handover_reply: data.handover_reply, opt_out_reply: data.opt_out_reply, opt_in_reply: data.opt_in_reply, missing_detail_reply: data.missing_detail_reply } });
    P.toast('Built-in replies saved.');
  }
  P.form('system-replies', async (form, data) => { await saveSystemReplies(form, data); P.dirty.clear(); });

  P.view('replies', {
    title: 'Auto-replies',
    admin: true,
    async render(el) {
      await P.loadVars();
      st.settings = await P.get('/settings');
      P.mount(el, html`<div class="stack">
        <section class="card"><div class="card-head"><div><h2>Test a message</h2><p>See which reply a customer would get. Nothing is sent.</p></div></div>
          <form class="card-body stack-sm" data-form="rule-test">
            <div class="input-group"><label class="sr-only" for="rt-msg">Customer message</label><input type="text" id="rt-msg" name="message" placeholder="For example: hi, 2, prices, talk to a person, stop" maxlength="500"><button class="btn primary" type="submit">Test</button></div>
            <label class="check"><input type="checkbox" name="menu_active"><span>The customer has just seen the numbered menu</span></label>
            <div id="rule-test-result" aria-live="polite"></div>
          </form></section>
        <div id="rule-conflicts"></div>
        <section class="card"><div class="card-head"><div><h2>Auto-reply rules</h2><p>Checked from top to bottom; the first match replies.</p></div>
          <div class="row"><button class="btn" type="button" data-action="rule-reset">Restore starter rules</button><button class="btn primary" type="button" data-action="rule-new">${icon('plus', 'icon-sm')}New rule</button></div></div>
          <div id="rules-box"></div></section>
        ${systemReplies(st.settings.automation)}
      </div>`);
      P.trackForm(P.$('#system-replies'), saveSystemReplies);
      await loadRules();
    },
  });

  /* ---------------- Templates ---------------- */
  function drawTemplates() {
    const box = P.$('#tpl-box');
    if (!box) return;
    if (!st.templates.length) {
      P.mount(box, P.empty('No templates', 'Templates are saved messages you can reuse in chats, campaigns and scheduled messages.', P.isAdmin() ? html`<button class="btn primary" type="button" data-action="tpl-new">New template</button>` : ''));
      return;
    }
    P.mount(box, html`<div class="grid-3">${st.templates.map((t) => html`<article class="card">
      <div class="card-head"><div><h3>${t.name}</h3><p>${t.category}${t.description ? ' - ' + t.description : ''}</p></div></div>
      <div class="card-body"><div class="reply-preview">${t.content}</div></div>
      <div class="card-foot">
        ${P.isAdmin() ? html`<a class="btn small" href="#/campaigns?template=${t.id}">Use in campaign</a><button class="btn small" type="button" data-action="tpl-edit" data-id="${t.id}">Edit</button><button class="btn small ghost icon-only" type="button" data-action="tpl-delete" data-id="${t.id}" data-name="${t.name}" aria-label="Delete ${t.name}">${icon('trash', 'icon-sm')}</button>` : html`<a class="btn small" href="#/scheduled?template=${t.id}">Schedule</a>`}
      </div></article>`)}</div>`);
  }
  async function loadTemplates() { st.templates = (await P.get('/templates')).filter((t) => !pendingTemplates.has(t.id)); drawTemplates(); }

  async function tplEditor(t) {
    await P.loadVars();
    const isNew = !t;
    t = t || { id: '', name: '', category: '', description: '', content: '' };
    P.modal({
      title: isNew ? 'New template' : 'Edit ' + t.name,
      wide: true,
      body: html`<form data-form="tpl-save" id="tpl-form" class="stack" novalidate>
        <input type="hidden" name="id" value="${t.id}">
        <div class="form-grid">
          <div class="field"><label for="tf-name">Name</label><input type="text" id="tf-name" name="name" value="${t.name}" maxlength="80" required autofocus></div>
          <div class="field"><label for="tf-cat">Category</label><input type="text" id="tf-cat" name="category" value="${t.category}" maxlength="40" placeholder="General"></div>
          <div class="field full"><label for="tf-desc">When to use it (optional)</label><input type="text" id="tf-desc" name="description" value="${t.description || ''}" maxlength="200"></div>
          <div class="field full"><label for="tf-content">Message</label><textarea id="tf-content" name="content" rows="6" maxlength="4000" required>${t.content}</textarea>${P.varPicker('tf-content', ['email', 'notes'])}</div>
          <div class="field full"><span class="label">Preview with a sample customer</span><div class="preview-box" id="tf-preview" aria-live="polite"></div><span class="preview-note hint"></span></div>
        </div>
      </form>`,
      foot: html`<button class="btn" type="button" data-modal-close>Cancel</button><button class="btn primary" type="submit" form="tpl-form">Save template</button>`,
      onOpen: (el) => P.livePreview(P.$('#tf-content', el), P.$('#tf-preview', el)),
    });
  }
  P.act('tpl-new', () => tplEditor(null));
  P.act('tpl-edit', (el) => tplEditor(st.templates.find((t) => t.id === el.dataset.id)));
  P.act('tpl-delete', (el) => {
    const id = el.dataset.id;
    pendingTemplates.add(id);
    st.templates = st.templates.filter((t) => t.id !== id);
    drawTemplates();
    P.undoable('Deleted template ' + el.dataset.name + '.', () => P.del('/templates/' + encodeURIComponent(id)).finally(() => pendingTemplates.delete(id)).then(loadTemplates), () => { pendingTemplates.delete(id); loadTemplates().catch(P.error); });
  });
  P.form('tpl-save', async (form, data) => {
    if (!data.name.trim()) { P.fieldError(form, 'name', 'Give the template a name.'); return; }
    if (!data.content.trim()) { P.fieldError(form, 'content', 'Write the message.'); return; }
    await P.post('/templates', data);
    P.$('[data-modal-close]', form.closest('.modal')).click();
    P.toast('Template saved.');
    loadTemplates().catch(P.error);
  });

  P.view('templates', {
    title: 'Templates',
    async render(el) {
      P.mount(el, html`<div class="stack">
        <div class="row-between"><p class="muted">Reusable messages for chats, campaigns and scheduled messages. Variables such as {{first_name}} are filled in for each customer.</p>
          ${P.isAdmin() ? html`<button class="btn primary" type="button" data-action="tpl-new">${icon('plus', 'icon-sm')}New template</button>` : ''}</div>
        <div id="tpl-box"></div></div>`);
      await loadTemplates();
    },
  });
})();
