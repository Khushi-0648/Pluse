'use strict';
(function () {
  const { html, icon } = P;
  const st = { templates: [], tags: [], list: [], preview: null, previewKey: '', jobs: [] };
  const hiddenIds = new Set(); // deleted items waiting out their Undo window

  function audienceFrom(data) {
    if (data.audience === 'tag') return { tag: data.tag };
    if (data.audience === 'numbers') return { phones: (data.numbers || '').split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean) };
    return { all: true };
  }
  function previewKey(data) { return JSON.stringify([data.message, data.audience, data.tag, data.numbers, P.templateFrom(data)]); }

  function reportCells(r) {
    return html`<td data-label="Sent">${r.sent}</td><td data-label="Delivered">${r.delivered}</td><td data-label="Read">${r.read}</td><td data-label="Replied">${r.replied}</td><td data-label="Failed">${r.failed}</td>`;
  }

  function activeCard(c) {
    if (!c || (c.status !== 'running' && c.status !== 'paused')) return '';
    const r = c.report, done = r.total - r.pending;
    return html`<section class="card" aria-labelledby="active-title">
      <div class="card-head"><div><h2 id="active-title">${c.name}</h2><p>${c.audience} - started ${P.fmtRel(c.started_at)}</p></div>${P.statusBadge(c.status)}</div>
      <div class="card-body stack-sm">
        <progress max="${r.total || 1}" value="${done}" aria-label="Progress"></progress>
        <div class="hint">${done} of ${r.total} processed: ${r.sent} sent, ${r.delivered} delivered, ${r.read} read, ${r.failed} failed, ${r.skipped} not sent.</div>
        ${c.stop_reason ? html`<div class="notice warn">${icon('alert')}<p>${c.stop_reason}</p></div>` : ''}
      </div>
      <div class="card-foot">
        ${c.status === 'running' ? html`<button class="btn" type="button" data-action="cmp-pause" data-id="${c.id}">${icon('pause', 'icon-sm')}Pause</button>` : html`<button class="btn primary" type="button" data-action="cmp-resume" data-id="${c.id}">${icon('play', 'icon-sm')}Resume</button>`}
        <button class="btn danger" type="button" data-action="cmp-cancel" data-id="${c.id}">Cancel campaign</button>
        <a class="btn" href="#/campaigns?id=${c.id}">Report</a>
      </div></section>`;
  }

  function historyTable() {
    const done = st.list.filter((c) => c.status !== 'running' && c.status !== 'paused');
    if (!done.length) return P.empty('No campaigns yet', 'Finished campaigns and their results appear here.');
    return html`<div class="table-wrap"><table class="table cards"><thead><tr><th>Campaign</th><th>Date</th><th>Status</th><th>Sent</th><th>Delivered</th><th>Read</th><th>Replied</th><th>Failed</th><th class="right">Actions</th></tr></thead>
      <tbody>${done.map((c) => html`<tr><td data-label="Campaign"><strong>${c.name}</strong><div class="hint">${c.audience}</div></td><td data-label="Date" class="nowrap">${P.fmtDateTime(c.started_at || c.created_at)}</td><td data-label="Status">${P.statusBadge(c.status)}</td>${reportCells(c.report)}
        <td class="actions"><a class="btn small" href="#/campaigns?id=${c.id}">Report</a><button class="btn small" type="button" data-action="cmp-csv" data-id="${c.id}">Download</button><button class="btn small ghost icon-only" type="button" data-action="cmp-delete" data-id="${c.id}" aria-label="Delete report ${c.name}">${icon('trash', 'icon-sm')}</button></td></tr>`)}</tbody></table></div>`;
  }

  function composeForm(q) {
    const tplId = q.get('template') || '';
    const tpl = st.templates.find((t) => t.id === tplId);
    return html`<form class="card" data-form="cmp-start" id="cmp-form" novalidate>
      <div class="card-head"><div><h2>New campaign</h2><p>Goes only to contacts with a recorded opt-in, at the pace set in Settings. Customers who wrote in the last 24 hours get the message below; everyone else gets your official WhatsApp template.</p></div></div>
      <div class="card-body form-grid">
        <div class="field"><label for="cmp-name">Campaign name</label><input type="text" id="cmp-name" name="name" maxlength="80" placeholder="For example: Diwali opening hours"></div>
        <div class="field"><label for="cmp-tpl">Start from a template</label><select id="cmp-tpl" data-change="cmp-template"><option value="">No template</option>${st.templates.map((t) => html`<option value="${t.id}" ${t.id === tplId ? P.raw('selected') : ''}>${t.name}</option>`)}</select></div>
        <div class="field full"><label for="cmp-msg">Message</label><textarea id="cmp-msg" name="message" rows="5" maxlength="4000" required>${tpl ? tpl.content : ''}</textarea>${P.varPicker('cmp-msg', ['email', 'notes'])}</div>
        <div class="field full"><span class="label">Preview with a sample customer</span><div class="preview-box" id="cmp-preview-text"></div><span class="preview-note hint"></span></div>
        ${P.templateFields('cmp')}
        <fieldset class="full"><legend>Who should receive it?</legend>
          <div class="stack-sm">
            <label class="check"><input type="radio" name="audience" value="all" checked><span>All contacts who opted in</span></label>
            <label class="check"><input type="radio" name="audience" value="tag"><span>Contacts with a tag</span></label>
            <div class="field"><label for="cmp-tag" class="sr-only">Tag</label><select id="cmp-tag" name="tag">${st.tags.length ? st.tags.map((t) => html`<option value="${t}">${t}</option>`) : html`<option value="">No tags yet</option>`}</select></div>
            <label class="check"><input type="radio" name="audience" value="numbers"><span>Specific numbers</span></label>
            <div class="field"><label for="cmp-numbers" class="sr-only">Phone numbers</label><textarea id="cmp-numbers" name="numbers" rows="2" placeholder="Paste numbers separated by commas or new lines. They must already be contacts."></textarea></div>
          </div>
        </fieldset>
        <div class="full notice">${icon('info')}<p>Keep it relevant to people who asked to hear from you. Do not send offers for prohibited goods, misleading claims, or requests for card numbers, passwords or codes. Every message should tell people they can reply STOP.</p></div>
        <div class="full" id="cmp-plan" aria-live="polite"></div>
      </div>
      <div class="card-foot">
        <button class="btn" type="button" data-action="cmp-test">Send a test to myself</button>
        <button class="btn" type="button" data-action="cmp-preview">Check recipients</button>
        <button class="btn primary" type="submit" id="cmp-go" disabled>Start campaign</button>
      </div>
    </form>`;
  }

  function drawPlan(p) {
    const fits = p.fits_today && !p.needs_template;
    P.mount(P.$('#cmp-plan'), html`<div class="stack">
      <div class="notice ${p.recipients && fits ? 'ok' : 'warn'}">${icon(p.recipients && fits ? 'check' : 'alert')}<div>
        <p><strong>${P.plural(p.recipients, 'person', 'people')} will receive this message.</strong> ${p.excluded ? P.plural(p.excluded, 'contact') + ' left out.' : ''}</p>
        <p class="hint">${P.fmtNum(p.daily_left)} messages left in today's limit.${!p.fits_today ? ' Send to fewer people or continue tomorrow.' : ''}</p>
        ${p.outside_window ? html`<p>${P.plural(p.outside_window, 'person has', 'people have')} not written to you in the last 24 hours. ${p.needs_template ? 'WhatsApp only allows an approved template for them: fill in "Official WhatsApp template" above, or choose people who wrote recently.' : 'They get your official WhatsApp template.'}</p>` : ''}
      </div></div>
      ${p.exclusions.length ? html`<div><h3>Left out, and why</h3>${p.exclusions.map((g) => html`<details><summary>${g.count} - ${g.reason}</summary><ul>${g.people.map((x) => html`<li>${P.who(x.name, x.phone)}</li>`)}${g.count > g.people.length ? html`<li class="hint">and ${g.count - g.people.length} more</li>` : ''}</ul></details>`)}</div>` : ''}
      ${p.samples.length ? html`<div><h3>How it will look</h3>${p.samples.map((s) => html`<div class="stack-sm"><span class="hint">To ${s.name}</span><div class="preview-box">${s.text}</div></div>`)}</div>` : ''}
    </div>`);
    P.setDisabled(P.$('#cmp-go'), !(p.recipients > 0 && fits));
  }

  async function runPreview() {
    const form = P.$('#cmp-form');
    const data = P.formData(form);
    if (!data.message.trim()) { P.fieldError(form, 'message', 'Write the message first.'); return null; }
    const p = await P.post('/campaigns/preview', { message: data.message, audience: audienceFrom(data), official_template: P.templateFrom(data) });
    st.preview = p; st.previewKey = previewKey(data);
    drawPlan(p);
    return p;
  }

  P.act('cmp-preview', (btn) => P.busy(btn, runPreview));
  P.act('cmp-test', async () => {
    const data = P.formData(P.$('#cmp-form'));
    if (!data.message.trim()) { P.fieldError(P.$('#cmp-form'), 'message', 'Write the message first.'); return; }
    const phone = await P.askTestPhone();
    if (!phone) return;
    const r = await P.post('/campaigns/test-send', { message: data.message, phone });
    P.toast(r.message);
  });
  P.change('cmp-template', (el) => {
    const t = st.templates.find((x) => x.id === el.value);
    if (!t) return;
    const ta = P.$('#cmp-msg');
    ta.value = t.content;
    ta.dispatchEvent(new Event('input', { bubbles: true }));
    if (!P.$('#cmp-name').value) P.$('#cmp-name').value = t.name;
  });
  P.form('cmp-start', async (form, data) => {
    if (!st.preview || st.previewKey !== previewKey(data)) { await runPreview(); P.toast('Check the recipients, then press Start again.'); return; }
    const ok = await P.confirm({ title: 'Start the campaign?', message: 'The message goes to ' + P.plural(st.preview.recipients, 'person', 'people') + ', one at a time with pauses. You can pause or cancel at any time.', confirmLabel: 'Start campaign' });
    if (!ok) return;
    await P.post('/campaigns', { name: data.name, message: data.message, audience: audienceFrom(data), official_template: P.templateFrom(data) });
    P.toast('Campaign started.');
    st.preview = null;
    P.route();
  });
  P.act('cmp-pause', async (btn) => { await P.busy(btn, () => P.post('/campaigns/' + encodeURIComponent(btn.dataset.id) + '/pause')); P.toast('Campaign paused.'); P.refresh(); });
  P.act('cmp-resume', async (btn) => { await P.busy(btn, () => P.post('/campaigns/' + encodeURIComponent(btn.dataset.id) + '/resume')); P.toast('Campaign resumed.'); P.refresh(); });
  P.act('cmp-cancel', async (btn) => {
    const ok = await P.confirm({ title: 'Cancel this campaign?', message: 'Messages already sent stay sent. Nobody else will receive it, and it cannot be resumed.', confirmLabel: 'Cancel campaign', danger: true });
    if (!ok) return;
    await P.post('/campaigns/' + encodeURIComponent(btn.dataset.id) + '/cancel');
    P.toast('Campaign cancelled.');
    P.refresh();
  });
  P.act('cmp-csv', (btn) => P.download('/campaigns/' + encodeURIComponent(btn.dataset.id) + '/report.csv'));
  P.act('cmp-delete', (btn) => {
    const id = btn.dataset.id;
    hiddenIds.add(id); // keeps the 4-second refresh from showing it again during Undo
    st.list = st.list.filter((c) => c.id !== id);
    P.redraw(P.$('#cmp-history'), historyTable());
    // Undo reloads instead of restoring a snapshot, so other deletes still waiting stay hidden.
    P.undoable('Deleted the campaign report.', () => P.del('/campaigns/' + encodeURIComponent(id)).finally(() => hiddenIds.delete(id)), () => {
      hiddenIds.delete(id);
      P.get('/campaigns').then((l) => { st.list = l.filter((c) => !hiddenIds.has(c.id)); P.redraw(P.$('#cmp-history'), historyTable()); }).catch(P.error);
    });
  });

  let detailSig = '';
  async function renderDetail(el, id) {
    const d = await P.get('/campaigns/' + encodeURIComponent(id));
    const c = d.campaign, r = d.report;
    c.recipients = c.recipients || [];
    d.exclusions = d.exclusions || [];
    // Only redraw when something changed, so open sections and focus are not lost.
    const sig = JSON.stringify([c.status, c.stop_reason, r]);
    if (sig === detailSig && el.firstChild) return c.status;
    detailSig = sig;
    const rate = (a, b) => (b ? Math.round((a * 100) / b) + '%' : '-');
    const tile = (label, v, note) => html`<div class="card stat"><div class="stat-label">${label}</div><div class="stat-value">${P.fmtNum(v)}</div><div class="stat-note">${note}</div></div>`;
    P.mount(el, html`<div class="stack">
      <div class="row-between"><a class="btn" href="#/campaigns">${icon('back', 'icon-sm')}All campaigns</a><div class="row">${P.statusBadge(c.status)}<button class="btn" type="button" data-action="cmp-csv" data-id="${c.id}">${icon('download', 'icon-sm')}Download for Excel</button></div></div>
      ${activeCard(Object.assign({ report: r }, c))}
      <section class="card"><div class="card-head"><div><h2>${c.name}</h2><p>${c.audience} - created ${P.fmtDateTime(c.created_at)} by ${c.created_by || 'admin'}</p></div></div>
        <div class="card-body stack-sm"><div class="preview-box">${c.message}</div>${c.template ? html`<p class="hint">Customers outside the 24-hour window: official template "${c.template.name}" (${c.template.language})${c.template.params && c.template.params.length ? ' with ' + c.template.params.join(', ') : ''}.</p>` : ''}</div></section>
      <div class="grid-4">${tile('Sent', r.sent, 'of ' + r.total)}${tile('Delivered', r.delivered, rate(r.delivered, r.sent) + ' of sent')}${tile('Read', r.read, rate(r.read, r.sent) + ' of sent')}${tile('Replied', r.replied, rate(r.replied, r.sent) + ' of sent, within 3 days')}</div>
      ${d.exclusions.length ? html`<section class="card"><div class="card-head"><h2>Not included (${r.excluded})</h2></div><div class="card-body">${d.exclusions.map((g) => html`<details><summary>${g.count} - ${g.reason}</summary><ul>${g.people.map((x) => html`<li>${P.who(x.name, x.phone)}</li>`)}</ul></details>`)}</div></section>` : ''}
      <section class="card"><div class="card-head"><h2>Recipients</h2></div><div class="table-wrap"><table class="table cards"><thead><tr><th>Customer</th><th>Status</th><th>Sent</th><th>Replied</th><th>Note</th></tr></thead>
        <tbody>${c.recipients.slice(0, 500).map((x) => html`<tr><td data-label="Customer">${P.who(x.name, x.phone)}</td><td data-label="Status">${P.statusBadge(x.status)}</td><td data-label="Sent" class="nowrap">${P.fmtDateTime(x.sent_at)}</td><td data-label="Replied" class="nowrap">${P.fmtDateTime(x.replied_at)}</td><td data-label="Note">${x.error || ''}</td></tr>`)}</tbody></table></div>
        ${c.recipients.length > 500 ? html`<div class="card-foot"><span class="hint">Showing 500 of ${c.recipients.length}. Download the report for the full list.</span></div>` : ''}</section>
    </div>`);
    return c.status;
  }

  P.view('campaigns', {
    title: 'Campaigns',
    admin: true,
    async render(el, q) {
      if (q.get('id')) {
        detailSig = '';
        const status = await renderDetail(el, q.get('id'));
        if (status === 'running' || status === 'paused') {
          const poll = P.every(5000, async () => {
            const s = await renderDetail(el, q.get('id'));
            if (s !== 'running' && s !== 'paused') clearInterval(poll); // finished: nothing changes any more
          });
        }
        return;
      }
      await P.loadVars();
      [st.templates, st.tags, st.list] = await Promise.all([P.get('/templates'), P.get('/contacts/tags'), P.get('/campaigns')]);
      st.preview = null;
      const active = st.list.find((c) => c.status === 'running' || c.status === 'paused');
      P.mount(el, html`<div class="stack">
        <div id="cmp-active">${activeCard(active)}</div>
        ${active ? '' : composeForm(q)}
        <section class="card"><div class="card-head"><div><h2>Past campaigns</h2><p>Delivered and read come from WhatsApp receipts; replies count if the customer wrote within 3 days.</p></div></div><div id="cmp-history">${historyTable()}</div></section>
      </div>`);
      if (!active) P.livePreview(P.$('#cmp-msg'), P.$('#cmp-preview-text'));
      P.every(4000, async () => {
        st.list = (await P.get('/campaigns')).filter((c) => !hiddenIds.has(c.id));
        const a = st.list.find((c) => c.status === 'running' || c.status === 'paused');
        if (!!a !== !!active) { P.route(); return; }
        // Redrawn only when something changed, keeping the focus and open sections.
        P.redraw(P.$('#cmp-active'), activeCard(a));
        P.redraw(P.$('#cmp-history'), historyTable());
      });
    },
  });

  /* ---------------- Scheduled messages ---------------- */
  function jobRow(j) {
    return html`<tr>
      <td data-label="To">${P.who(j.recipient_name, j.recipient)}</td>
      <td data-label="When" class="nowrap">${P.fmtDateTime(j.scheduled_at)}<div class="hint">${j.status === 'pending' ? P.fmtRel(j.scheduled_at) : ''}</div></td>
      <td data-label="Message"><div class="reply-preview">${j.message}</div>${j.template ? html`<div class="hint">Or template "${j.template.name}"</div>` : ''}</td>
      <td data-label="Status">${P.statusBadge(j.status)}${j.error ? html`<div class="hint">${j.error}</div>` : ''}</td>
      <td class="actions">
        ${j.status === 'pending' ? html`<button class="btn small" type="button" data-action="job-cancel" data-id="${j.id}">Cancel</button>` : ''}
        ${j.status === 'failed' || j.status === 'skipped' ? html`<button class="btn small" type="button" data-action="job-retry" data-id="${j.id}">Send now</button>` : ''}
        <button class="btn small ghost icon-only" type="button" data-action="job-delete" data-id="${j.id}" aria-label="Delete">${icon('trash', 'icon-sm')}</button>
      </td></tr>`;
  }
  function drawJobs() {
    const box = P.$('#jobs-box');
    if (!box) return;
    P.redraw(box, st.jobs.length
      ? html`<div class="table-wrap"><table class="table cards"><thead><tr><th>To</th><th>When</th><th>Message</th><th>Status</th><th class="right">Actions</th></tr></thead><tbody>${st.jobs.map(jobRow)}</tbody></table></div>`
      : P.empty('Nothing scheduled', 'Schedule a reminder or follow-up to send later.'));
  }
  async function loadJobs() { st.jobs = (await P.get('/scheduled')).filter((j) => !hiddenIds.has(j.id)); drawJobs(); }

  function localInputValue(d) {
    const pad = (n) => String(n).padStart(2, '0');
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  P.form('job-add', async (form, data) => {
    if (!data.recipient.trim()) { P.fieldError(form, 'recipient', 'Enter the phone number.'); return; }
    if (!data.message.trim()) { P.fieldError(form, 'message', 'Write the message.'); return; }
    if (!data.scheduled_at) { P.fieldError(form, 'scheduled_at', 'Choose a date and time.'); return; }
    const when = new Date(data.scheduled_at).toISOString();
    const tmpl = P.templateFrom(data);
    const v = await P.post('/scheduled', { recipient: data.recipient, message: data.message, scheduled_at: when, official_template: tmpl, validate_only: true });
    if (v.warnings && v.warnings.length) {
      const ok = await P.confirm({ title: 'Schedule anyway?', message: v.warnings.join(' '), confirmLabel: 'Schedule' });
      if (!ok) return;
    }
    await P.post('/scheduled', { recipient: data.recipient, message: data.message, scheduled_at: when, official_template: tmpl });
    P.toast('Message scheduled for ' + P.fmtDateTime(when) + '.');
    form.reset();
    P.$('#job-when').value = localInputValue(new Date(Date.now() + 3600e3));
    P.$('#job-msg').dispatchEvent(new Event('input', { bubbles: true }));
    loadJobs().catch(P.error);
  });
  P.change('job-template', (el) => {
    const t = st.templates.find((x) => x.id === el.value);
    if (!t) return;
    const ta = P.$('#job-msg');
    ta.value = t.content;
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  });
  P.act('job-cancel', async (btn) => { await P.busy(btn, () => P.post('/scheduled/' + encodeURIComponent(btn.dataset.id) + '/cancel')); P.toast('Cancelled.'); loadJobs().catch(P.error); });
  P.act('job-retry', async (btn) => {
    const id = btn.dataset.id;
    const path = '/scheduled/' + encodeURIComponent(id) + '/retry';
    // A message whose last attempt may have reached the customer is only sent again
    // after the person checked the chat and confirmed.
    const ask = () => P.confirm({ title: 'Send it again?', message: 'This message may already have reached the customer: WhatsApp\'s answer was lost, or Pulse stopped while sending it. Check the chat first and send it again only if they did not get it.', confirmLabel: 'Send again', danger: true });
    const job = st.jobs.find((j) => j.id === id);
    let confirm = false;
    if (job && job.maybe_sent) {
      if (!(await ask())) return;
      confirm = true;
    }
    try {
      await P.busy(btn, () => P.post(path, { confirm }));
    } catch (e) {
      if (confirm || !(e.data && e.data.needs_confirm)) throw e;
      if (!(await ask())) return;
      await P.busy(btn, () => P.post(path, { confirm: true }));
    }
    P.toast('It will be sent within a few seconds.');
    loadJobs().catch(P.error);
  });
  P.act('job-delete', (btn) => {
    const id = btn.dataset.id;
    st.jobs = st.jobs.filter((j) => j.id !== id);
    drawJobs();
    hiddenIds.add(id);
    P.undoable('Deleted the scheduled message.', () => P.del('/scheduled/' + encodeURIComponent(id)).finally(() => hiddenIds.delete(id)).then(loadJobs), () => { hiddenIds.delete(id); loadJobs().catch(P.error); });
  });

  P.view('scheduled', {
    title: 'Scheduled messages',
    async render(el, q) {
      await P.loadVars();
      let contacts = [];
      [st.templates, contacts] = await Promise.all([P.get('/templates'), P.get('/contacts?limit=500').then((r) => r.contacts).catch(() => [])]);
      const tpl = st.templates.find((t) => t.id === q.get('template'));
      P.mount(el, html`<div class="stack">
        <form class="card" data-form="job-add" novalidate>
          <div class="card-head"><div><h2>Schedule a message</h2><p>Sent at the chosen time if the customer opted in or wrote to you. If they have not written in the 24 hours before, WhatsApp only allows your official template.</p></div></div>
          <div class="card-body form-grid">
            <div class="field"><label for="job-to">To</label><input type="tel" id="job-to" name="recipient" list="job-contacts" placeholder="+91 98765 43210" required>
              <datalist id="job-contacts">${contacts.map((c) => html`<option value="+${c.phone}">${c.name || ''}</option>`)}</datalist></div>
            <div class="field"><label for="job-when">When</label><input type="datetime-local" id="job-when" name="scheduled_at" required value="${localInputValue(new Date(Date.now() + 3600e3))}"></div>
            <div class="field"><label for="job-tpl">Start from a template</label><select id="job-tpl" data-change="job-template"><option value="">No template</option>${st.templates.map((t) => html`<option value="${t.id}" ${tpl && t.id === tpl.id ? P.raw('selected') : ''}>${t.name}</option>`)}</select></div>
            <div class="field full"><label for="job-msg">Message</label><textarea id="job-msg" name="message" rows="4" maxlength="4000" required>${tpl ? tpl.content : ''}</textarea>${P.varPicker('job-msg', ['email', 'notes'])}</div>
            <div class="field full"><span class="label">Preview</span><div class="preview-box" id="job-preview"></div><span class="preview-note hint"></span></div>
            ${P.templateFields('job')}
          </div>
          <div class="card-foot"><button class="btn primary" type="submit">Schedule</button></div>
        </form>
        <section class="card"><div class="card-head"><h2>Scheduled and sent</h2></div><div id="jobs-box"></div></section>
      </div>`);
      P.livePreview(P.$('#job-msg'), P.$('#job-preview'));
      await loadJobs();
      P.every(10000, () => loadJobs().catch(() => {}));
    },
  });
})();
