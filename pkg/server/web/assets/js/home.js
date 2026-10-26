'use strict';
(function () {
  const { html, icon } = P;
  let state = { overview: null, sections: {} };
  let root = null;

  const healthText = { good: ['ok', 'Healthy'], watch: ['warn', 'Keep an eye on it'], risk: ['bad', 'At risk'] };

  function banners(o) {
    const out = [];
    const wa = o.whatsapp || {};
    if (o.password_reset_at) out.push(html`<div class="notice warn">${icon('shield')}<div><p><strong>The admin password was reset with the reset tool on ${P.fmtDateTime(o.password_reset_at)}.</strong> If that was not you, change the password now (Settings &gt; Account) and check the activity log.</p></div></div>`);
    if (wa.problem) out.push(html`<div class="notice bad">${icon('alert')}<div><p><strong>${wa.problem}</strong></p></div></div>`);
    else if (wa.warning && P.isAdmin()) out.push(html`<div class="notice warn">${icon('alert')}<div><p>${wa.warning} <a href="#/settings?tab=whatsapp">Open WhatsApp connection</a></p></div></div>`);
    if (o.automation.dry_run) out.push(html`<div class="notice info">${icon('info')}<div><p><strong>Practice mode is on.</strong> Automatic replies, campaigns and scheduled messages are shown in the inbox but not actually sent. Turn it off in Settings &gt; Safety.</p></div></div>`);
    if (o.automation.allow_list_only) out.push(html`<div class="notice warn">${icon('shield')}<div><p><strong>Test mode is on.</strong> Pulse only replies to and sends to the test numbers listed in Settings &gt; Safety.</p></div></div>`);
    return out;
  }

  const quality = { GREEN: ['ok', 'High'], YELLOW: ['warn', 'Medium'], RED: ['bad', 'Low'] };

  // Until the owner's own WhatsApp Business API details are entered, Home shows how to
  // connect; afterwards, the state of the connection.
  function connectCard(o) {
    const wa = o.whatsapp || {};
    if (!wa.configured) {
      if (!P.isAdmin()) return html`<div class="notice warn">${icon('phone')}<p>WhatsApp is not connected yet. Ask the admin to connect the WhatsApp Business API.</p></div>`;
      return html`<section class="card" aria-labelledby="connect-title">
        <div class="card-head"><div><h2 id="connect-title">Connect your WhatsApp Business API</h2><p>Pulse sends and receives through Meta's official WhatsApp Business Platform, with your own Meta account and number.</p></div></div>
        <div class="card-body stack">
          <ol class="steps">
            <li>Create a business app at developers.facebook.com and add the WhatsApp product.</li>
            <li>Copy the phone number ID, the WhatsApp Business account ID and a permanent access token.</li>
            <li>Paste them in <strong>Settings &gt; WhatsApp connection</strong>, then follow the steps there to receive messages.</li>
          </ol>
          <div class="row"><a class="btn primary" href="#/settings?tab=whatsapp">Open WhatsApp connection</a><a class="btn" href="#/help">Step-by-step guide</a></div>
        </div>
      </section>`;
    }
    if (wa.connected && !wa.problem) return '';
    const q = quality[wa.quality_rating] || null;
    return html`<section class="card" aria-labelledby="connect-title">
      <div class="card-head"><div><h2 id="connect-title">WhatsApp connection</h2><p>${wa.display_phone || 'Your business number'}${wa.verified_name ? ' - ' + wa.verified_name : ''}</p></div>${P.statusPill(wa.connected ? 'ok' : 'bad', wa.connected ? 'Connected' : 'Not working')}</div>
      <div class="card-body stack-sm">
        ${wa.problem ? html`<div class="notice bad">${icon('alert')}<p>${wa.problem}</p></div>` : html`<p class="hint">Checking the connection with Meta...</p>`}
        ${q ? html`<div class="row-between"><span>Quality rating</span>${P.statusPill(q[0], q[1])}</div>` : ''}
        ${wa.last_check ? html`<div class="row-between"><span>Last checked</span><span>${P.fmtRel(wa.last_check)}</span></div>` : ''}
        ${P.isAdmin() ? html`<div class="row"><a class="btn" href="#/settings?tab=whatsapp">Open WhatsApp connection</a></div>` : ''}
      </div>
    </section>`;
  }

  const stepAction = {
    risk: () => html`<button class="btn small" type="button" data-action="show-risk">Read the rules</button>`,
    link: () => html`<a class="btn small" href="#/settings?tab=whatsapp">Connect</a>`,
    webhook: () => html`<a class="btn small" href="#/settings?tab=whatsapp">Set up</a>`,
    business: () => html`<a class="btn small" href="#/settings?tab=business">Enter details</a>`,
    test: (o) => o.whatsapp.connected ? html`<button class="btn small" type="button" data-action="send-test">Send test</button>` : html`<span class="hint">Connect first</span>`,
    automation: (o) => o.setup.missing.length ? html`<span class="hint">Finish the steps above</span>` : html`<button class="btn small primary" type="button" data-action="automation-on">Switch on</button>`,
  };

  function checklist(o) {
    if (o.setup.done === o.setup.total || !P.isAdmin()) return '';
    return html`<section class="card" aria-labelledby="setup-title">
      <div class="card-head"><div><h2 id="setup-title">Get started</h2><p>${o.setup.done} of ${o.setup.total} done</p></div><progress max="${o.setup.total}" value="${o.setup.done}" aria-label="Setup progress"></progress></div>
      <ul class="list checklist">${o.setup.steps.map((s) => html`<li>
        <span class="check-mark ${s.done ? 'done' : ''}" aria-hidden="true">${s.done ? icon('check') : ''}</span>
        <span class="grow">${s.label}<span class="sr-only">${s.done ? ' (done)' : ' (to do)'}</span></span>
        ${s.done ? '' : (stepAction[s.key] ? stepAction[s.key](o) : '')}
      </li>`)}</ul>
    </section>`;
  }

  function stats(o) {
    const t = o.today;
    const tile = (label, value, note) => html`<div class="card stat"><div class="stat-label">${label}</div><div class="stat-value">${P.fmtNum(value)}</div><div class="stat-note">${note}</div></div>`;
    return html`<div class="grid-4">
      ${tile('Messages received today', t.received, P.plural(t.customers, 'customer') + ' active')}
      ${tile('Messages sent today', t.sent, t.failed ? P.plural(t.failed, 'failed message') : 'None failed')}
      ${tile('Automatic replies today', t.auto_replies, o.automation.enabled ? 'Automation is on' : 'Automation is off')}
      ${tile('Need a person', o.counts.needs_attention, o.counts.unread_chats ? P.plural(o.counts.unread_chats, 'unread chat') : 'No unread chats')}
    </div>`;
  }

  function attention(o) {
    const list = o.attention || [];
    return html`<section class="card" aria-labelledby="att-title">
      <div class="card-head"><div><h2 id="att-title">Needs attention</h2><p>Customers who asked for a person or were paused for safety.</p></div>${list.length ? html`<a class="btn small" href="#/inbox?filter=attention">Open all</a>` : ''}</div>
      ${list.length ? html`<ul class="list">${list.map((a) => html`<li>
        <div class="grow"><div><strong>${a.name}</strong></div><div class="hint">${a.reason || 'Asked for a person'} ${a.since ? '- ' + P.fmtRel(a.since) : ''}</div></div>
        <a class="btn small" href="#/inbox?phone=${a.phone}">Open chat</a>
        <button class="btn small" type="button" data-action="resume-bot" data-phone="${a.phone}">Resume automatic replies</button>
      </li>`)}</ul>` : html`<div class="card-body">${P.empty('Nobody is waiting', 'When a customer asks for a person, they appear here and you get an alert.')}</div>`}
    </section>`;
  }

  function health(o) {
    const h = o.health;
    const [cls, label] = healthText[h.level] || ['', h.level];
    return html`<section class="card" aria-labelledby="health-title">
      <div class="card-head"><div><h2 id="health-title">Account health</h2><p>Last 24 hours</p></div>${P.statusPill(cls, label)}</div>
      <div class="card-body stack-sm">
        <div class="row-between"><span>Sent</span><strong>${P.fmtNum(h.sent)}</strong></div>
        <div class="row-between"><span>Failed</span><strong>${P.fmtNum(h.failed)}</strong></div>
        <div class="row-between"><span>Opted out</span><strong>${P.fmtNum(h.opt_outs)}</strong></div>
        <div class="row-between"><span>Asked for a person</span><strong>${P.fmtNum(h.handovers)}</strong></div>
        <div class="row-between"><span>Left in today's limit</span><strong>${P.fmtNum(h.daily_left)} of ${P.fmtNum(h.daily_limit)}</strong></div>
        ${h.advice.length ? html`<ul class="hint">${h.advice.map((a) => html`<li>${a}</li>`)}</ul>` : ''}
      </div>
    </section>`;
  }

  function campaignCard(o) {
    const c = o.campaign;
    if (!c) return '';
    const r = c.report;
    const done = r.total - r.pending;
    return html`<section class="card" aria-labelledby="camp-title">
      <div class="card-head"><div><h2 id="camp-title">${c.name}</h2><p>${c.audience}</p></div>${P.statusBadge(c.status)}</div>
      <div class="card-body stack-sm">
        <progress max="${r.total || 1}" value="${done}" aria-label="Campaign progress"></progress>
        <div class="hint">${done} of ${r.total} processed - ${r.sent} sent, ${r.failed} failed${c.stop_reason ? ' - ' + c.stop_reason : ''}</div>
      </div>
      ${P.isAdmin() ? html`<div class="card-foot">
        ${c.status === 'running' ? html`<button class="btn" type="button" data-action="campaign-pause" data-id="${c.id}">${icon('pause', 'icon-sm')}Pause</button>` : html`<button class="btn" type="button" data-action="campaign-resume" data-id="${c.id}">${icon('play', 'icon-sm')}Resume</button>`}
        <a class="btn" href="#/campaigns?id=${c.id}">View report</a>
      </div>` : ''}
    </section>`;
  }

  let lastSig = '';
  function draw(el, force) {
    const o = state.overview;
    if (!o || !el) return;
    const active = document.activeElement;
    const sig = JSON.stringify(o);
    if (!force && sig === lastSig) return;
    lastSig = sig;
    const focusKey = active && el.contains(active) && active.dataset && active.dataset.action ? [active.dataset.action, active.dataset.phone || active.dataset.id || ''] : null;
    P.mount(el, html`<div class="stack">
      ${banners(o)}
      ${connectCard(o)}
      ${checklist(o)}
      ${stats(o)}
      <div class="grid-2">${attention(o)}${health(o)}</div>
      ${campaignCard(o)}
    </div>`);
    if (focusKey) {
      const again = P.$$('[data-action]', el).find((b) => b.dataset.action === focusKey[0] && (b.dataset.phone || b.dataset.id || '') === focusKey[1]);
      if (again) again.focus();
    }
  }

  async function load(el) {
    state.overview = await P.get('/overview');
    P.applyOverview(state.overview);
    draw(el);
    maybeShowRisk();
  }

  let riskShown = false;
  function maybeShowRisk() {
    const o = state.overview;
    if (riskShown || !o || o.risk_acknowledged || !P.isAdmin()) return;
    riskShown = true;
    showRisk();
  }

  function showRisk() {
    const m = P.modal({
      title: "Before you start: WhatsApp's messaging rules",
      sticky: true,
      body: html`<div class="stack">
        <p>Pulse uses Meta's official WhatsApp Business Platform (Cloud API), with your own Meta account. Meta's WhatsApp Business policies apply to every message:</p>
        <ul>
          <li>Only message people who agreed to hear from your business, and record that opt-in.</li>
          <li>You can reply freely for 24 hours after a customer writes. After that, only message templates approved by Meta can be sent.</li>
          <li>Honour STOP at once. Pulse does this automatically.</li>
          <li>Meta rates the quality of your number. Many blocks or reports lower it and can reduce how many people you may message per day.</li>
          <li>Meta charges for some messages (mostly templates). Check the current prices in your Meta account.</li>
        </ul>
        <p class="hint">Pulse is independent software and is not affiliated with or endorsed by WhatsApp or Meta.</p>
      </div>`,
      foot: html`<a class="btn" href="#/help">Read the safety guide</a><button class="btn primary" type="button" data-action="ack-risk">I understand</button>`,
    });
    P.riskModal = m;
  }

  P.act('show-risk', showRisk);
  P.act('ack-risk', async (btn) => {
    await P.busy(btn, () => P.post('/setup/acknowledge-risk'));
    if (P.riskModal) P.riskModal.close();
    P.refresh();
  });
  P.act('send-test', async () => {
    const phone = await P.askTestPhone();
    if (!phone) return;
    const r = await P.post('/setup/test-message', { phone });
    P.toast(r.message);
    P.refresh();
  });
  P.act('automation-on', async (btn) => {
    await P.busy(btn, () => P.patch('/settings', { automation: { enabled: true } }));
    P.toast('Automatic replies are on.');
    P.refresh();
  });
  P.act('resume-bot', async (btn) => {
    await P.busy(btn, () => P.post('/contacts/resume-bot', { phone: btn.dataset.phone }));
    P.toast('Automatic replies resumed for this customer.');
    P.refresh();
  });
  P.act('campaign-pause', async (btn) => { await P.busy(btn, () => P.post('/campaigns/' + encodeURIComponent(btn.dataset.id) + '/pause')); P.toast('Campaign paused.'); P.refresh(); });
  P.act('campaign-resume', async (btn) => { await P.busy(btn, () => P.post('/campaigns/' + encodeURIComponent(btn.dataset.id) + '/resume')); P.toast('Campaign resumed.'); P.refresh(); });

  P.view('home', {
    title: 'Home',
    async render(el) {
      root = el;
      lastSig = '';
      await load(el);
      P.every(4000, () => load(el).catch(() => {}));
    },
    refresh() { return load(root); },
  });
})();
