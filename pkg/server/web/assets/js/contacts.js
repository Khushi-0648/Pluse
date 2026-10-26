'use strict';
(function () {
  const { html, icon } = P;
  const st = { search: '', filter: '', tag: '', page: 1, data: null, selected: new Set(), reveal: P.store.get('revealPhones', false), tags: [], csv: null };

  function phoneCell(p) { return st.reveal ? P.fmtPhone(p) : P.maskPhone(p); }

  function row(c) {
    const checked = st.selected.has(c.phone);
    return html`<tr>
      <td><input type="checkbox" data-change="contact-select" data-phone="${c.phone}" aria-label="Select ${P.who(c.name, c.phone)}" ${checked ? P.raw('checked') : ''}></td>
      <td data-label="Name"><button class="link-btn" type="button" data-action="contact-edit" data-phone="${c.phone}">${c.name || 'No name'}</button></td>
      <td data-label="Phone" class="nowrap mono">${phoneCell(c.phone)}</td>
      <td data-label="Tags">${(c.tags || []).map((t) => html`<span class="tag">${t}</span> `)}</td>
      <td data-label="Consent">${c.is_dnd ? P.statusPill('bad', 'Opted out') : c.consent ? html`<span title="${c.consent_source || ''}">${P.statusPill('ok', 'Opted in')}</span>` : P.statusPill('', 'Not recorded')}</td>
      <td data-label="Last activity" class="nowrap">${P.fmtRel(c.last_inbound_at || c.last_contacted_at || c.created_at)}</td>
      <td class="actions">
        <a class="btn small" href="#/inbox?phone=${c.phone}">Chat</a>
        <button class="btn small" type="button" data-action="contact-edit" data-phone="${c.phone}">Edit</button>
        ${P.isAdmin() ? html`<button class="btn small ghost icon-only" type="button" data-action="contact-delete" data-phone="${c.phone}" data-name="${P.who(c.name, c.phone)}" aria-label="Delete ${P.who(c.name, c.phone)}">${icon('trash', 'icon-sm')}</button>` : ''}
      </td>
    </tr>`;
  }

  function bulkBar() {
    const n = st.selected.size;
    if (!n) return '';
    return html`<div class="notice info row-between" role="region" aria-label="Bulk actions"><span><strong>${P.plural(n, 'contact')}</strong> selected</span>
      <div class="row">
        <button class="btn small" type="button" data-action="bulk-tag">Add tag</button>
        <button class="btn small" type="button" data-action="bulk-untag">Remove tag</button>
        <button class="btn small" type="button" data-action="bulk-optout">Mark do-not-message</button>
        ${P.isAdmin() ? html`<button class="btn small danger" type="button" data-action="bulk-delete">Delete</button>` : ''}
        <button class="btn small ghost" type="button" data-action="bulk-clear">Clear selection</button>
      </div></div>`;
  }

  function drawTable() {
    const box = P.$('#contacts-table');
    if (!box || !st.data) return;
    const d = st.data;
    const allOnPage = d.contacts.length > 0 && d.contacts.every((c) => st.selected.has(c.phone));
    P.mount(P.$('#bulk-bar'), bulkBar());
    if (!d.contacts.length) {
      P.mount(box, P.empty(st.search || st.filter || st.tag ? 'No contacts match' : 'No contacts yet',
        st.search || st.filter || st.tag ? 'Try another search or filter.' : 'Contacts are added automatically when customers message you. You can also add them yourself or import them from a spreadsheet.',
        html`<button class="btn primary" type="button" data-action="contact-add">Add contact</button>`));
      return;
    }
    P.mount(box, html`<div class="table-wrap ${st.reveal ? 'reveal-on' : ''}"><table class="table cards">
      <thead><tr><th><input type="checkbox" data-change="contact-select-all" aria-label="Select all on this page" ${allOnPage ? P.raw('checked') : ''}></th><th>Name</th><th>Phone</th><th>Tags</th><th>Consent</th><th>Last activity</th><th class="right">Actions</th></tr></thead>
      <tbody>${d.contacts.map(row)}</tbody></table></div>
      <div class="card-foot"><span class="hint grow">${P.plural(d.total, 'contact')}</span>
        <button class="btn small" type="button" data-action="contacts-page" data-page="${d.page - 1}" ${d.page <= 1 ? P.raw('disabled') : ''}>Previous</button>
        <span class="hint">Page ${d.page} of ${Math.max(1, d.pages)}</span>
        <button class="btn small" type="button" data-action="contacts-page" data-page="${d.page + 1}" ${d.page >= d.pages ? P.raw('disabled') : ''}>Next</button>
      </div>`);
  }

  // Deletes still inside their Undo window: hidden from every reload until they commit.
  const pendingDelete = new Set();
  let loadSeq = 0;
  async function load() {
    const seq = ++loadSeq;
    const q = new URLSearchParams({ search: st.search, filter: st.filter, tag: st.tag, page: String(st.page), limit: '100' });
    const data = await P.get('/contacts?' + q.toString());
    if (seq !== loadSeq) return; // a newer search or page was asked for meanwhile
    const shown = data.contacts.filter((c) => !pendingDelete.has(c.phone));
    data.total -= data.contacts.length - shown.length;
    data.contacts = shown;
    st.data = data;
    drawTable();
  }

  async function loadTags() {
    st.tags = await P.get('/contacts/tags');
    const sel = P.$('#contact-tag');
    if (sel) P.mount(sel, html`<option value="">All tags</option>${st.tags.map((t) => html`<option value="${t}" ${t === st.tag ? P.raw('selected') : ''}>${t}</option>`)}`);
  }

  async function editContact(phone) {
    let c = { phone: '', name: '', email: '', tags: [], notes: '', consent: false, consent_source: '', is_dnd: false };
    let recent = [];
    let inWindow = false;
    if (phone) {
      const page = P.renderToken;
      const r = await P.get('/contacts/' + encodeURIComponent(phone));
      if (page !== P.renderToken) return; // the user moved to another page meanwhile
      c = r.contact; recent = r.recent_messages || []; inWindow = r.in_window;
    }
    const isNew = !phone;
    P.modal({
      title: isNew ? 'Add contact' : P.who(c.name, c.phone),
      wide: !isNew,
      body: html`<form data-form="contact-save" id="contact-form" class="stack" novalidate>
        <input type="hidden" name="original_phone" value="${c.phone}">
        <input type="hidden" name="seen_is_dnd" value="${c.is_dnd ? 'true' : 'false'}">
        <input type="hidden" name="seen_consent" value="${c.consent ? 'true' : 'false'}">
        <div class="form-grid">
          <div class="field"><label for="cf-name">Name</label><input type="text" id="cf-name" name="name" value="${c.name}" maxlength="120" autofocus></div>
          <div class="field"><label for="cf-phone">Phone with country code</label><input type="tel" id="cf-phone" name="phone" value="${c.phone ? '+' + c.phone : ''}" required placeholder="+91 98765 43210"></div>
          <div class="field"><label for="cf-email">Email</label><input type="email" id="cf-email" name="email" value="${c.email || ''}" maxlength="200"></div>
          <div class="field"><label for="cf-tags">Tags</label>${P.chipsField('cf-tags', 'tags', (c.tags || []).join(', '), 'Type a tag and press Enter')}</div>
          <div class="field full"><label for="cf-notes">Notes</label><textarea id="cf-notes" name="notes" maxlength="2000">${c.notes || ''}</textarea></div>
        </div>
        <fieldset><legend>Permission to message</legend>
          <div class="stack-sm">
            <label class="check"><input type="checkbox" name="consent" ${c.consent ? P.raw('checked') : ''} ${c.is_dnd ? P.raw('disabled') : ''}><span><span class="label">This person agreed to receive messages from us</span><br><span class="hint">Needed for campaigns. WhatsApp's rules require opt-in before you message someone.</span></span></label>
            <div class="field"><label for="cf-source">How did they agree?</label><input type="text" id="cf-source" name="consent_source" value="${c.consent_source || ''}" maxlength="200" placeholder="For example: signed the form in the shop on 12 Oct"></div>
            <label class="check"><input type="checkbox" name="is_dnd" ${c.is_dnd ? P.raw('checked') : ''}><span><span class="label">Do not message</span><br><span class="hint">Set automatically when the customer replies STOP.</span></span></label>
            ${!isNew ? html`<p class="hint">${inWindow ? 'They wrote to you in the last 24 hours.' : 'They have not written in the last 24 hours.'}${c.consent_at ? ' Consent recorded ' + P.fmtDate(c.consent_at) + '.' : ''}</p>` : ''}
          </div>
        </fieldset>
        ${recent.length ? html`<div><h3>Recent messages</h3><ul class="list">${recent.slice(0, 8).map((m) => html`<li><span class="hint nowrap">${P.fmtDateTime(m.timestamp)}</span><span class="grow">${m.direction === 'outgoing' ? 'You: ' : ''}${m.text || m.media}</span></li>`)}</ul></div>` : ''}
      </form>`,
      foot: html`${!isNew && P.isAdmin() ? html`<button class="btn danger" type="button" data-action="contact-forget" data-phone="${c.phone}">Delete all data</button><span class="grow"></span>` : ''}
        <button class="btn" type="button" data-modal-close>Cancel</button>
        <button class="btn primary" type="submit" form="contact-form">Save</button>`,
      onOpen: (el) => P.$$('[data-chips]', el).forEach(P.chips),
    });
  }

  P.form('contact-save', async (form, data) => {
    if (!data.phone || data.phone.replace(/[^0-9]/g, '').length < 7) { P.fieldError(form, 'phone', 'Enter the phone number with country code.'); return; }
    // A contact that already had consent when the form opened needs no new source; the form
    // carries that state, so it also works for a contact opened from the inbox.
    if (data.consent && !data.consent_source.trim() && data.seen_consent !== 'true') {
      P.fieldError(form, 'consent_source', 'Say how this person agreed to receive messages.'); return;
    }
    const body = {
      original_phone: data.original_phone, phone: data.phone, name: data.name, email: data.email, notes: data.notes,
      tags: data.tags.split(',').map((t) => t.trim()).filter(Boolean), consent: !!data.consent && !data.is_dnd, consent_source: data.consent_source, is_dnd: !!data.is_dnd,
      seen_is_dnd: data.seen_is_dnd === 'true', seen_consent: data.seen_consent === 'true',
    };
    await P.post('/contacts', body);
    P.$('[data-modal-close]', form.closest('.modal')).click();
    P.toast('Contact saved.');
    await Promise.all([load(), loadTags()]);
  });

  P.act('contact-add', () => editContact(''));
  P.act('contact-edit', (el) => editContact(el.dataset.phone));
  P.act('contact-delete', (el) => {
    const phone = el.dataset.phone;
    const idx = st.data.contacts.findIndex((c) => c.phone === phone);
    if (idx < 0) return;
    const [item] = st.data.contacts.splice(idx, 1);
    st.data.total -= 1;
    st.selected.delete(phone);
    pendingDelete.add(phone);
    drawTable();
    // Undo puts back only this contact, so other deletes in their Undo window stay hidden.
    P.undoable('Deleted ' + el.dataset.name + '.',
      () => P.del('/contacts/' + encodeURIComponent(phone)).finally(() => pendingDelete.delete(phone)).then(load),
      () => {
        pendingDelete.delete(phone);
        if (!st.data.contacts.some((c) => c.phone === phone)) { st.data.contacts.splice(Math.min(idx, st.data.contacts.length), 0, item); st.data.total += 1; }
        drawTable();
      });
  });
  P.act('contact-forget', async (el) => {
    const ok = await P.confirm({ title: 'Delete all data for this person?', message: 'The contact, every stored message and any scheduled messages to them will be deleted. Use this when a customer asks you to erase their data.', confirmLabel: 'Delete everything', danger: true, password: true });
    if (!ok) return;
    const r = await P.post('/privacy/forget-contact', { phone: el.dataset.phone, password: ok.password });
    P.$$('.modal [data-modal-close]').forEach((b) => b.click());
    P.toast(r.message);
    load().catch(P.error);
  });

  P.change('contact-select', (el) => { if (el.checked) st.selected.add(el.dataset.phone); else st.selected.delete(el.dataset.phone); P.mount(P.$('#bulk-bar'), bulkBar()); });
  P.change('contact-select-all', (el) => { st.data.contacts.forEach((c) => (el.checked ? st.selected.add(c.phone) : st.selected.delete(c.phone))); drawTable(); });
  P.act('bulk-clear', () => { st.selected.clear(); drawTable(); });

  async function askTag(title) {
    const res = await P.confirm({ title, message: 'Tag name', extra: html`<div class="field"><label for="bulk-tag-name" class="sr-only">Tag</label><input type="text" id="bulk-tag-name" name="tag" list="bulk-tags" maxlength="40" required autofocus><datalist id="bulk-tags">${st.tags.map((t) => html`<option value="${t}">`)}</datalist></div>`, confirmLabel: 'Apply' });
    return res && res.tag && res.tag.trim();
  }
  async function bulk(action, tag, password) {
    const r = await P.post('/contacts/bulk', { action, phones: Array.from(st.selected), tag, password });
    P.toast('Updated ' + P.plural(r.updated, 'contact') + '.');
    st.selected.clear();
    await Promise.all([load(), loadTags()]);
  }
  P.act('bulk-tag', async () => { const t = await askTag('Add a tag to the selected contacts'); if (t) await bulk('tag', t); });
  P.act('bulk-untag', async () => { const t = await askTag('Remove a tag from the selected contacts'); if (t) await bulk('untag', t); });
  P.act('bulk-optout', async () => {
    const ok = await P.confirm({ title: 'Mark as do-not-message?', message: 'Pulse will never send automatic replies, campaigns or scheduled messages to these ' + P.plural(st.selected.size, 'contact') + '.', confirmLabel: 'Mark do-not-message' });
    if (ok) await bulk('opt_out');
  });
  P.act('bulk-delete', async () => {
    const ok = await P.confirm({ title: 'Delete ' + P.plural(st.selected.size, 'contact') + '?', message: 'Their messages stay in the inbox until you delete them too.', confirmLabel: 'Delete', danger: true, password: true });
    if (ok) await bulk('delete', '', ok.password);
  });

  P.act('contacts-page', (el) => { st.page = Number(el.dataset.page) || 1; load().catch(P.error); });
  let timer = null;
  P.change('contact-search', (el) => { clearTimeout(timer); timer = setTimeout(() => { st.search = el.value.trim(); st.page = 1; st.selected.clear(); load().catch(P.error); }, 250); });
  P.change('contact-filter', (el) => { st.filter = el.value; st.page = 1; st.selected.clear(); load().catch(P.error); });
  P.change('contact-tag', (el) => { st.tag = el.value; st.page = 1; st.selected.clear(); load().catch(P.error); });
  P.act('contact-reveal', (el) => { st.reveal = !st.reveal; P.store.set('revealPhones', st.reveal); el.setAttribute('aria-pressed', String(st.reveal)); el.textContent = st.reveal ? 'Hide numbers' : 'Show numbers'; drawTable(); });
  P.act('contacts-export', () => P.download('/contacts/export.csv'));

  /* ---------- CSV import wizard ---------- */
  const FIELDS = [['phone', 'Phone number (required)'], ['name', 'Name'], ['email', 'Email'], ['tags', 'Tags'], ['notes', 'Notes'], ['consent', 'Opt-in (yes / no)']];
  P.act('contacts-import', () => {
    st.csv = null;
    P.modal({
      title: 'Import contacts from a spreadsheet',
      wide: true,
      body: html`<div class="stack" id="import-body">
        <p>Put one contact per row. Only a phone number column is required; name, email, tags, notes and an opt-in column are optional. Contacts you already have are updated, and nothing already saved is erased.</p>
        <p class="hint">In Excel or Google Sheets, save or download the sheet as "CSV" first, then choose that file here.</p>
        <div class="field"><label for="csv-file">Spreadsheet file (.csv)</label><input type="file" id="csv-file" accept=".csv,text/csv" data-change="csv-chosen"></div>
        <div id="import-preview"></div>
      </div>`,
      foot: html`<button class="btn" type="button" data-modal-close>Close</button><button class="btn primary" type="button" data-action="csv-import" id="csv-go" disabled>Import</button>`,
    });
  });
  P.change('csv-chosen', async (el) => {
    const f = el.files && el.files[0];
    if (!f) return;
    if (f.size > 20 * 1024 * 1024) { P.toast('The file is larger than 20 MB.', { type: 'error' }); return; }
    const fd = new FormData();
    fd.append('file', f);
    const p = await P.post('/contacts/import/preview', fd);
    st.csv = { file: f, preview: p };
    const opts = (sel) => html`<option value="-1">Not in file</option>${p.columns.map((c, i) => html`<option value="${i}" ${i === sel ? P.raw('selected') : ''}>${c}</option>`)}`;
    P.mount(P.$('#import-preview'), html`<div class="stack">
      <h3>Match the columns</h3>
      <div class="form-grid">${FIELDS.map(([k, label]) => html`<div class="field"><label for="map-${k}">${label}</label><select id="map-${k}" data-map="${k}">${opts(p.mapping[k])}</select></div>`)}</div>
      <label class="check"><input type="checkbox" id="map-header" ${p.mapping.header ? P.raw('checked') : ''}><span class="label">The first row contains column names</span></label>
      <h3>First rows</h3>
      <div class="table-wrap"><table class="table"><thead><tr>${p.columns.map((c) => html`<th>${c}</th>`)}</tr></thead><tbody>${p.rows.map((r) => html`<tr>${r.map((v) => html`<td>${v}</td>`)}</tr>`)}</tbody></table></div>
      <p class="hint">${P.plural(p.total, 'row')} in the file.</p>
    </div>`);
    P.$('#csv-go').disabled = false;
  });
  P.act('csv-import', async (btn) => {
    if (!st.csv) return;
    const mapping = { header: P.$('#map-header').checked };
    P.$$('[data-map]').forEach((s) => { mapping[s.dataset.map] = Number(s.value); });
    if (mapping.phone < 0) { P.toast('Choose which column has the phone number.', { type: 'error' }); return; }
    const fd = new FormData();
    fd.append('file', st.csv.file);
    fd.append('mapping', JSON.stringify(mapping));
    const r = await P.busy(btn, () => P.post('/contacts/import', fd));
    const res = r.result;
    btn.disabled = true;
    P.mount(P.$('#import-body'), html`<div class="stack">
      <div class="notice ok">${icon('check')}<p><strong>${res.added} added, ${res.updated} updated, ${res.skipped} skipped.</strong></p></div>
      ${res.warnings.length ? html`<div><h3>Skipped rows</h3><ul>${res.warnings.slice(0, 100).map((w) => html`<li>${w}</li>`)}</ul></div>` : ''}
      ${res.overwrites.length ? html`<details><summary>Updated contacts (${res.overwrites.length})</summary><ul>${res.overwrites.slice(0, 200).map((w) => html`<li>${w}</li>`)}</ul></details>` : ''}
    </div>`);
    await Promise.all([load(), loadTags()]);
  });

  P.view('contacts', {
    title: 'Contacts',
    async render(el, q) {
      st.search = ''; st.filter = q.get('filter') || ''; st.tag = q.get('tag') || ''; st.page = 1; st.selected.clear();
      P.mount(el, html`<div class="stack">
        <div class="row-between">
          <div class="row grow">
            <label class="sr-only" for="contact-search">Search contacts</label>
            <input type="search" id="contact-search" class="grow" placeholder="Search name, number, email, notes or tag" data-change="contact-search" data-live>
            <label class="sr-only" for="contact-filter">Filter</label>
            <select id="contact-filter" data-change="contact-filter">
              <option value="">All contacts</option>
              <option value="consent" ${st.filter === 'consent' ? P.raw('selected') : ''}>Opted in</option>
              <option value="no_consent" ${st.filter === 'no_consent' ? P.raw('selected') : ''}>No opt-in recorded</option>
              <option value="opted_out" ${st.filter === 'opted_out' ? P.raw('selected') : ''}>Opted out</option>
              <option value="attention" ${st.filter === 'attention' ? P.raw('selected') : ''}>Needs a person</option>
              <option value="recent" ${st.filter === 'recent' ? P.raw('selected') : ''}>Wrote in last 24 hours</option>
            </select>
            <label class="sr-only" for="contact-tag">Tag</label>
            <select id="contact-tag" data-change="contact-tag"><option value="">All tags</option></select>
          </div>
          <div class="row">
            <button class="btn" type="button" data-action="contact-reveal" aria-pressed="${String(st.reveal)}">${st.reveal ? 'Hide numbers' : 'Show numbers'}</button>
            ${P.isAdmin() ? html`<button class="btn" type="button" data-action="contacts-import">${icon('upload', 'icon-sm')}Import</button><button class="btn" type="button" data-action="contacts-export">${icon('download', 'icon-sm')}Export</button>` : ''}
            <button class="btn primary" type="button" data-action="contact-add">${icon('plus', 'icon-sm')}Add contact</button>
          </div>
        </div>
        <div id="bulk-bar"></div>
        <div class="card" id="contacts-table"></div>
      </div>`);
      await Promise.all([load(), loadTags()]);
      if (q.get('phone')) editContact(q.get('phone').replace(/[^0-9]/g, '')).catch(P.error);
    },
  });
})();
