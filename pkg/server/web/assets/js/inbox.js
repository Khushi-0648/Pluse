'use strict';
(function () {
  const { html, icon } = P;
  const st = { filter: 'all', search: '', list: [], phone: '', messages: [], sig: '', file: null, templates: [], older: false, contact: null };

  const statusLabel = { sent: 'Sent', delivered: 'Delivered', read: 'Read', failed: 'Failed', dry_run: 'Test only (not sent)' };

  function chatItem(c) {
    const unread = c.unread > 0;
    return html`<li><button type="button" class="chat-item ${unread ? 'unread' : ''}" data-action="open-chat" data-phone="${c.phone}" aria-current="${c.phone === st.phone ? 'true' : 'false'}">
      <span class="name">${P.who(c.name, c.phone)}</span>
      <span class="time">${P.fmtRel(c.last_at)}</span>
      <span class="preview">${c.last_direction === 'outgoing' ? 'You: ' : ''}${c.last_text || (c.needs_attention ? c.attention_reason : '')}</span>
      <span class="meta">
        ${c.needs_attention ? html`<span class="pill warn" title="${c.attention_reason || ''}">Needs a person</span>` : ''}
        ${c.assigned_to ? html`<span class="pill" title="Assigned to ${c.assigned_to}">${c.assigned_to}</span>` : ''}
        ${unread ? html`<span class="badge" aria-label="${c.unread} unread">${c.unread}</span>` : ''}
      </span>
    </button></li>`;
  }

  function drawList() {
    const ul = P.$('#chat-list');
    if (!ul) return;
    const items = st.list;
    if (!items.length) {
      const msg = st.search ? 'No chats match your search.' : st.filter === 'unread' ? 'You have read everything.' : st.filter === 'attention' ? 'Nobody is waiting for a person.' : st.filter === 'unassigned' ? 'Every chat has someone handling it.' : 'New WhatsApp messages will appear here.';
      P.mount(ul, html`<li>${P.empty('No conversations', msg)}</li>`);
      return;
    }
    P.mount(ul, items.map(chatItem));
  }

  function conv() { return st.list.find((c) => c.phone === st.phone) || st.contact || { phone: st.phone }; }

  async function loadContactInfo(phone) {
    st.contact = null;
    try {
      const r = await P.get('/contacts/' + encodeURIComponent(phone));
      const c = r.contact;
      const paused = c.bot_paused_until && new Date(c.bot_paused_until) > new Date();
      st.contact = { phone: c.phone, name: c.name, consent: c.consent, opted_out: c.is_dnd, in_window: r.in_window, bot_paused: !!paused, needs_attention: c.needs_attention, attention_reason: c.attention_reason, assigned_to: c.assigned_to || '', notes: c.notes || '' };
    } catch (e) { /* not a saved contact yet */ }
  }

  function threadHead(c) {
    const pills = [];
    if (c.opted_out) pills.push(P.statusPill('bad', 'Opted out'));
    else if (c.consent) pills.push(P.statusPill('ok', 'Opted in'));
    if (c.needs_attention) pills.push(P.statusPill('warn', 'Needs a person'));
    else if (c.bot_paused) pills.push(P.statusPill('info', 'You are handling this chat'));
    if (c.in_window) pills.push(P.statusPill('', 'Wrote in last 24 h'));
    return html`<div class="thread-head">
      <button class="btn ghost icon-only" type="button" data-action="close-chat" aria-label="Back to chats">${icon('back')}</button>
      <div class="grow"><div><strong>${c.name || P.fmtPhone(c.phone)}</strong></div>${c.name ? html`<div class="hint">${P.fmtPhone(c.phone)}</div>` : ''}</div>
      <div class="row">${pills}</div>
      <div class="row">
        ${c.bot_paused || c.needs_attention
          ? html`<button class="btn small" type="button" data-action="chat-resume" data-phone="${c.phone}">Resume automatic replies</button>`
          : html`<button class="btn small" type="button" data-action="chat-takeover" data-phone="${c.phone}" title="Stop automatic replies in this chat for 24 hours">Take over</button>`}
        <a class="btn small" href="#/contacts?phone=${c.phone}">Contact</a>
        ${P.isAdmin() ? html`<button class="btn small ghost" type="button" data-action="chat-clear" data-phone="${c.phone}" aria-label="Delete this chat history">${icon('trash', 'icon-sm')}</button>` : ''}
      </div>
    </div>`;
  }

  // Who handles the chat and a private note for the team (customers never see it).
  function threadMeta() {
    const m = st.contact || { assigned_to: '', notes: '' };
    const names = Array.from(new Set(st.list.map((c) => c.assigned_to).concat(P.store.get('myName', '')).filter(Boolean))).sort();
    const summary = (m.assigned_to ? 'Assigned to ' + m.assigned_to : 'Not assigned') + (m.notes ? ' - has a note' : '');
    return html`<details class="thread-meta" ${m.notes ? P.raw('open') : ''}><summary>${summary}</summary>
      <form data-form="chat-meta" class="stack-sm" autocomplete="off">
        <div class="row">
          <label class="sr-only" for="cm-who">Assigned to</label>
          <input type="text" id="cm-who" name="assigned_to" list="cm-names" value="${m.assigned_to}" maxlength="60" placeholder="Who is handling this chat?" class="grow">
          <datalist id="cm-names">${names.map((n) => html`<option value="${n}">`)}</datalist>
          <button class="btn small" type="button" data-action="chat-assign-me">Assign to me</button>
        </div>
        <label class="sr-only" for="cm-note">Internal note</label>
        <textarea id="cm-note" name="notes" rows="2" maxlength="2000" placeholder="Internal note for your team. The customer never sees this.">${m.notes}</textarea>
        <div class="row"><button class="btn small primary" type="submit">Save</button></div>
      </form>
    </details>`;
  }

  function bubbles(msgs) {
    const out = [];
    let lastDay = '';
    if (st.older) out.push(html`<button class="btn small" type="button" data-action="chat-older">Load older messages</button>`);
    msgs.forEach((m) => {
      const day = P.fmtDate(m.timestamp);
      if (day !== lastDay) { out.push(html`<div class="day-sep">${day}</div>`); lastDay = day; }
      const outgoing = m.direction === 'outgoing';
      const by = outgoing ? (m.type === 'auto_reply' ? 'Automatic reply' : m.type === 'broadcast' ? 'Campaign' : m.type === 'scheduled' ? 'Scheduled' : m.type === 'webhook' ? 'Integration' : m.sent_by === 'phone' ? 'From the phone' : 'You') : '';
      out.push(html`<div class="bubble ${outgoing ? 'out' : ''} ${m.status === 'failed' ? 'failed' : ''}">
        ${m.media ? html`<span class="attach">${icon('clip', 'icon-sm')} ${m.media}</span>` : ''}${m.text}
        <span class="meta">${by ? by + ' - ' : ''}${P.fmtTime(m.timestamp)}${outgoing && statusLabel[m.status] ? ' - ' + statusLabel[m.status] : ''}${m.imported ? ' - imported' : ''}${m.error ? ' - ' + m.error : ''}</span>
      </div>`);
    });
    return out;
  }

  function composerNotices(c) {
    const warn = !c.opted_out && !c.in_window && !c.consent;
    return html`${c.opted_out ? html`<div class="notice bad">${icon('alert')}<p>This customer opted out. They need to reply START before you can message them.</p></div>` : ''}
      ${warn ? html`<div class="notice warn">${icon('info')}<p>This customer has not written in the last 24 hours and has no opt-in recorded. Only message them if they expect it.</p></div>` : ''}`;
  }

  function composer(c) {
    return html`<form class="composer" data-form="chat-send" autocomplete="off">
      <div id="composer-notices">${composerNotices(c)}</div>
      <div id="attach-preview"></div>
      <label class="sr-only" for="chat-text">Message</label>
      <textarea id="chat-text" name="message" rows="2" placeholder="Type a message. Enter sends, Shift+Enter adds a new line." ${c.opted_out ? P.raw('disabled') : ''}>${P.store.get('draft.' + c.phone, '')}</textarea>
      <div class="row">
        <input type="file" id="chat-file" accept="image/png,image/jpeg,image/webp,application/pdf" data-change="chat-file" hidden>
        <button class="btn small" type="button" data-action="chat-attach">${icon('clip', 'icon-sm')}Attach</button>
        <label class="sr-only" for="chat-template">Insert a template</label>
        <select id="chat-template" class="grow" data-change="chat-template"><option value="">Insert a template...</option>${st.templates.map((t) => html`<option value="${t.id}">${t.name}</option>`)}</select>
        <button class="btn primary" type="submit" ${c.opted_out ? P.raw('disabled') : ''}>${icon('send', 'icon-sm')}Send</button>
      </div>
    </form>`;
  }

  function drawThread(force) {
    const box = P.$('#thread');
    if (!box) return;
    P.$('.inbox').classList.toggle('has-thread', !!st.phone);
    if (!st.phone) {
      P.mount(box, html`<div class="thread-empty"><div>${icon('inbox')}<p>Choose a conversation to read and reply.</p></div></div>`);
      return;
    }
    const c = conv();
    const msgs = st.messages.slice().reverse();
    const stateSig = JSON.stringify([c.name, c.bot_paused, c.needs_attention, c.opted_out, c.consent, c.in_window]);
    const msgSig = msgs.length + '|' + msgs.map((m) => m.id + m.status).join(',').slice(-600);
    const built = box.dataset.phone === st.phone && P.$('#thread-body', box);
    if (force || !built) {
      // Full build only when a chat is opened; later updates keep the composer untouched.
      box.dataset.phone = st.phone;
      st.sig = { state: stateSig, msgs: msgSig };
      P.mount(box, html`<div id="thread-head">${threadHead(c)}</div><div id="thread-meta">${threadMeta()}</div><div class="thread-body" id="thread-body" tabindex="0" aria-label="Messages">${msgs.length ? bubbles(msgs) : html`<div class="thread-empty">No messages yet.</div>`}</div>${composer(c)}`);
      const nb = P.$('#thread-body');
      nb.scrollTop = nb.scrollHeight;
      const ta = P.$('#chat-text');
      ta.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); ta.form.requestSubmit(); } });
      ta.addEventListener('input', () => P.store.set('draft.' + st.phone, ta.value));
      if (window.matchMedia('(min-width: 861px)').matches) ta.focus();
      drawAttachment();
      return;
    }
    if (!st.sig || typeof st.sig !== 'object') st.sig = { state: '', msgs: '' };
    if (st.sig.state !== stateSig) {
      st.sig.state = stateSig;
      P.mount(P.$('#thread-head'), threadHead(c));
      P.mount(P.$('#composer-notices'), composerNotices(c));
      const ta = P.$('#chat-text');
      const sendBtn = P.$('.composer button[type="submit"]');
      if (ta) ta.disabled = !!c.opted_out;
      if (sendBtn) sendBtn.disabled = !!c.opted_out;
    }
    if (st.sig.msgs !== msgSig) {
      st.sig.msgs = msgSig;
      const body = P.$('#thread-body');
      const fromBottom = body.scrollHeight - body.scrollTop - body.clientHeight;
      P.mount(body, msgs.length ? bubbles(msgs) : html`<div class="thread-empty">No messages yet.</div>`);
      body.scrollTop = fromBottom < 60 ? body.scrollHeight : body.scrollHeight - body.clientHeight - fromBottom;
    }
  }

  let previewUrl = null;
  function clearPreviewUrl() {
    if (previewUrl) {
      URL.revokeObjectURL(previewUrl);
      previewUrl = null;
    }
  }

  function drawAttachment() {
    const box = P.$('#attach-preview');
    if (!box) return;
    clearPreviewUrl();
    if (!st.file) { P.mount(box, ''); return; }
    const isImg = st.file.type.startsWith('image/');
    P.mount(box, html`<div class="attachment">${isImg ? html`<img id="attach-img" alt="">` : icon('file')}<span class="grow">${st.file.name} (${Math.round(st.file.size / 1024)} KB)</span><button class="btn small ghost" type="button" data-action="chat-unattach" aria-label="Remove attachment">${icon('x', 'icon-sm')}</button></div>`);
    if (isImg) {
      previewUrl = URL.createObjectURL(st.file);
      P.$('#attach-img').src = previewUrl;
    }
  }

  async function loadList() {
    const q = new URLSearchParams({ filter: st.filter, search: st.search });
    st.list = await P.get('/conversations?' + q.toString());
    drawList();
  }

  async function loadThread(force) {
    if (!st.phone) { drawThread(force); return; }
    const phone = st.phone;
    const latest = await P.get('/messages?limit=100&phone=' + encodeURIComponent(phone));
    if (phone !== st.phone) return; // the user opened another chat meanwhile
    if (force) {
      st.messages = latest;
      st.older = latest.length === 100;
    } else {
      // Keep older pages the user already loaded below the refreshed newest page.
      const cutoff = latest.length ? new Date(latest[latest.length - 1].timestamp) : null;
      const ids = new Set(latest.map((m) => m.id));
      const tail = cutoff ? st.messages.filter((m) => !ids.has(m.id) && new Date(m.timestamp) < cutoff) : [];
      const newestIn = (list) => { const m = list.find((x) => x.direction === 'incoming'); return m ? m.id : ''; };
      const arrived = newestIn(latest) !== newestIn(st.messages);
      st.messages = latest.concat(tail);
      // A message that arrives while the chat is open on screen counts as read.
      if (arrived && !document.hidden) P.post('/conversations/read', { phone }).then(loadList).catch(() => {});
    }
    drawThread(force);
  }

  async function openChat(phone) {
    st.phone = phone;
    st.file = null;
    st.sig = '';
    await loadContactInfo(phone);
    history.replaceState(null, '', '#/inbox?phone=' + encodeURIComponent(phone) + (st.filter !== 'all' ? '&filter=' + st.filter : ''));
    P.$$('.chat-item').forEach((b) => b.setAttribute('aria-current', b.dataset.phone === phone ? 'true' : 'false'));
    await loadThread(true);
    P.post('/conversations/read', { phone }).then(loadList).catch(() => {});
  }

  async function send() {
    const ta = P.$('#chat-text');
    const text = ta.value.trim();
    if (!text && !st.file) { ta.focus(); return; }
    let body;
    if (st.file) {
      body = new FormData();
      body.append('to', st.phone);
      body.append('message', text);
      body.append('file', st.file);
    } else body = { to: st.phone, message: text };
    const r = await P.post('/messages/send', body);
    if (r && r.dry_run) P.toast(r.message);
    ta.value = '';
    P.store.del('draft.' + st.phone);
    st.file = null;
    drawAttachment();
    await loadThread(true);
    loadList();
  }

  P.form('chat-send', () => send());
  P.form('chat-meta', async (form, d) => {
    const r = await P.post('/conversations/meta', { phone: st.phone, assigned_to: d.assigned_to, notes: d.notes });
    if (st.contact) { st.contact.assigned_to = r.assigned_to; st.contact.notes = r.notes; }
    P.toast('Saved.');
    P.mount(P.$('#thread-meta'), threadMeta());
    loadList();
  });
  P.act('chat-assign-me', async () => {
    let me = P.store.get('myName', '');
    if (!me) {
      const ok = await P.confirm({ title: 'Your name', message: 'Pulse remembers it on this computer for "Assign to me".', extra: html`<div class="field"><label for="my-name" class="sr-only">Your name</label><input type="text" id="my-name" name="name" maxlength="60" required autofocus></div>`, confirmLabel: 'Save' });
      if (!ok || !ok.name || !ok.name.trim()) return;
      me = ok.name.trim();
      P.store.set('myName', me);
    }
    const input = P.$('#cm-who');
    if (input) { input.value = me; input.form.requestSubmit(); }
  });
  P.act('open-chat', (el) => openChat(el.dataset.phone));
  P.act('close-chat', () => { st.phone = ''; history.replaceState(null, '', '#/inbox'); drawThread(true); });
  P.act('chat-attach', () => P.$('#chat-file').click());
  P.act('chat-unattach', () => { st.file = null; drawAttachment(); });
  P.change('chat-file', async (el) => {
    const f = el.files && el.files[0];
    el.value = '';
    if (!f) return;
    if (f.size > 16 * 1024 * 1024) { P.toast('The file is larger than 16 MB.', { type: 'error' }); return; }
    const fd = new FormData();
    fd.append('file', f);
    try { await P.post('/media/validate', fd); } catch (e) { P.error(e); return; }
    st.file = f;
    drawAttachment();
  });
  P.change('chat-template', (el) => {
    const t = st.templates.find((x) => x.id === el.value);
    el.value = '';
    if (!t) return;
    const ta = P.$('#chat-text');
    ta.value = ta.value ? ta.value + '\n' + t.content : t.content;
    ta.focus();
    P.store.set('draft.' + st.phone, ta.value);
  });
  P.act('chat-older', async (btn) => {
    const oldest = st.messages[st.messages.length - 1];
    if (!oldest) return;
    const more = await P.busy(btn, () => P.get('/messages?limit=100&phone=' + encodeURIComponent(st.phone) + '&before=' + encodeURIComponent(oldest.timestamp)));
    st.messages = st.messages.concat(more);
    st.older = more.length === 100;
    st.sig = '';
    drawThread(false);
  });
  P.act('chat-resume', async (btn) => { await P.busy(btn, () => P.post('/contacts/resume-bot', { phone: btn.dataset.phone })); P.toast('Automatic replies resumed.'); await Promise.all([loadList(), loadContactInfo(st.phone)]); drawThread(true); });
  P.act('chat-takeover', async (btn) => { await P.busy(btn, () => P.post('/contacts/pause-bot', { phone: btn.dataset.phone, hours: 24 })); P.toast('Automatic replies are paused in this chat for 24 hours.'); await Promise.all([loadList(), loadContactInfo(st.phone)]); drawThread(true); });
  P.act('chat-clear', async (btn) => {
    const ok = await P.confirm({ title: 'Delete this chat history?', message: 'Messages stored in Pulse for this customer will be deleted. Their phone keeps its copy.', confirmLabel: 'Delete', danger: true });
    if (!ok) return;
    await P.del('/messages?phone=' + encodeURIComponent(btn.dataset.phone));
    P.toast('Chat history deleted.');
    st.messages = [];
    await loadList();
    drawThread(true);
  });
  P.act('inbox-filter', (el) => {
    st.filter = el.dataset.filter;
    P.$$('[data-action="inbox-filter"]').forEach((b) => b.setAttribute('aria-pressed', String(b === el)));
    loadList();
  });
  let searchTimer = null;
  P.change('inbox-search', (el) => { clearTimeout(searchTimer); searchTimer = setTimeout(() => { st.search = el.value.trim(); loadList(); }, 250); });

  P.act('new-message', async () => {
    let contacts = [];
    try { contacts = (await P.get('/contacts?limit=500')).contacts; } catch (e) { /* optional */ }
    P.modal({
      title: 'New message',
      body: html`<form data-form="new-message" id="new-msg-form" class="stack">
        <div class="field"><label for="nm-to">To</label><input type="tel" id="nm-to" name="to" list="nm-contacts" placeholder="+91 98765 43210" required autofocus>
        <datalist id="nm-contacts">${contacts.map((c) => html`<option value="+${c.phone}">${c.name || ''}</option>`)}</datalist></div>
        <div class="field"><label for="nm-text">Message</label><textarea id="nm-text" name="message" required></textarea></div>
      </form>`,
      foot: html`<button class="btn" type="button" data-modal-close>Cancel</button><button class="btn primary" type="submit" form="new-msg-form">Open chat</button>`,
    });
  });
  P.form('new-message', async (form, data) => {
    const phone = data.to.replace(/[^0-9]/g, '');
    if (phone.length < 7) { P.fieldError(form, 'to', 'Enter a phone number with country code.'); return; }
    P.store.set('draft.' + phone, data.message);
    P.$('[data-modal-close]', form.closest('.modal')).click();
    await openChat(phone);
  });

  P.view('inbox', {
    title: 'Inbox',
    async render(el, q) {
      st.filter = q.get('filter') || 'all';
      st.search = '';
      st.phone = q.get('phone') ? q.get('phone').replace(/[^0-9]/g, '') : '';
      st.messages = []; st.sig = ''; st.file = null;
      P.mount(el, html`<div class="card inbox">
        <div class="inbox-list">
          <div class="filters">
            <div class="row-between"><div class="segmented" role="group" aria-label="Filter chats">
              <button type="button" data-action="inbox-filter" data-filter="all" aria-pressed="${String(st.filter === 'all')}">All</button>
              <button type="button" data-action="inbox-filter" data-filter="unread" aria-pressed="${String(st.filter === 'unread')}">Unread</button>
              <button type="button" data-action="inbox-filter" data-filter="attention" aria-pressed="${String(st.filter === 'attention')}">Needs a person</button>
              <button type="button" data-action="inbox-filter" data-filter="unassigned" aria-pressed="${String(st.filter === 'unassigned')}">Unassigned</button>
            </div><button class="btn small primary" type="button" data-action="new-message">${icon('plus', 'icon-sm')}New</button></div>
            <label class="sr-only" for="inbox-search">Search chats</label>
            <input type="search" id="inbox-search" placeholder="Search name, number or text" data-change="inbox-search" data-live>
          </div>
          <ul class="chat-list" id="chat-list" aria-label="Conversations"></ul>
        </div>
        <section class="thread" id="thread" aria-label="Conversation"></section>
      </div>`);
      try { st.templates = await P.get('/templates'); } catch (e) { st.templates = []; }
      await loadList();
      if (st.phone) await openChat(st.phone); else drawThread(true);
      P.every(4000, () => loadList().catch(() => {}));
      P.every(3000, () => { if (st.phone) loadThread(false).catch(() => {}); });
    },
  });
})();
