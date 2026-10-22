'use strict';
(function () {
  const { html, icon } = P;
  const TABS = [['whatsapp', 'WhatsApp connection'], ['business', 'Business details'], ['hours', 'Opening hours'], ['safety', 'Safety and pace'], ['integrations', 'Integrations'], ['privacy', 'Privacy and data'], ['account', 'Account']];
  const DAYS = { mon: 'Monday', tue: 'Tuesday', wed: 'Wednesday', thu: 'Thursday', fri: 'Friday', sat: 'Saturday', sun: 'Sunday' };
  const ZONES = ['Local', 'Asia/Kolkata', 'Asia/Dubai', 'Asia/Singapore', 'Asia/Jakarta', 'Africa/Lagos', 'Africa/Nairobi', 'Europe/London', 'Europe/Berlin', 'America/New_York', 'America/Chicago', 'America/Los_Angeles', 'America/Sao_Paulo', 'Australia/Sydney', 'UTC'];
  let S = null;
  let C = null; // the WhatsApp connection (secrets are never sent, only whether they are saved)
  let tab = 'whatsapp';

  const field = (id, label, input, hint) => html`<div class="field"><label for="${id}">${label}</label>${input}${hint ? html`<span class="hint">${hint}</span>` : ''}</div>`;
  const num = (name, value, min, max, step) => html`<input type="number" id="s-${name}" name="${name}" value="${value}" min="${min}" max="${max}" step="${step || 1}" required>`;
  const lines = (list) => (list || []).join('\n');
  const toList = (s) => (s || '').split(/[\n,]+/).map((x) => x.trim()).filter(Boolean);

  async function save(section, body, msg) {
    await P.patch('/settings', { [section]: body });
    P.vars = null; // business details feed the variable picker
    S = await P.get('/settings');
    P.dirty.clear();
    P.toast(msg || 'Saved.');
  }

  /* ---------- WhatsApp connection ---------- */
  const QUALITY = { GREEN: ['ok', 'High'], YELLOW: ['warn', 'Medium'], RED: ['bad', 'Low'] };
  function connectionStatus() {
    const st = C.status || {};
    if (!st.configured) return html`<p class="hint">Not connected yet. Enter your details below and press Save.</p>`;
    const q = QUALITY[st.quality_rating];
    return html`<div class="stack-sm">
      <div class="row-between"><span>Status</span>${P.statusPill(st.connected ? 'ok' : 'bad', st.connected ? 'Connected' : 'Not working')}</div>
      ${st.display_phone ? html`<div class="row-between"><span>Business number</span><strong>${st.display_phone}</strong></div>` : ''}
      ${st.verified_name ? html`<div class="row-between"><span>Name shown to customers</span><strong>${st.verified_name}</strong></div>` : ''}
      ${q ? html`<div class="row-between"><span>Quality rating from Meta</span>${P.statusPill(q[0], q[1])}</div>` : ''}
      ${st.last_check ? html`<div class="row-between"><span>Last checked</span><span>${P.fmtDateTime(st.last_check)}</span></div>` : ''}
      ${st.last_webhook ? html`<div class="row-between"><span>Last message from Meta</span><span>${P.fmtDateTime(st.last_webhook)}</span></div>` : ''}
      ${st.problem ? html`<div class="notice bad">${icon('alert')}<p>${st.problem}</p></div>` : ''}
    </div>`;
  }
  function whatsappTab() {
    const saved = (has) => (has ? 'Saved - leave empty to keep it' : '');
    return html`<div class="stack">
      <form class="card" data-form="set-connection" id="f-connection" novalidate>
        <div class="card-head"><div><h2>Your WhatsApp Business API</h2><p>Pulse uses Meta's official WhatsApp Business Platform with your own Meta app, number and access token. Nothing is shared with anyone else. Help &gt; Using the official WhatsApp API explains where to find each value.</p></div></div>
        <div class="card-body form-grid">
          ${field('c-pnid', 'Phone number ID', html`<input type="text" id="c-pnid" name="phone_number_id" value="${C.phone_number_id}" inputmode="numeric" autocomplete="off" required>`, 'In Meta: WhatsApp > API Setup. A long number; it is not your phone number.')}
          ${field('c-waba', 'WhatsApp Business account ID', html`<input type="text" id="c-waba" name="business_account_id" value="${C.business_account_id}" inputmode="numeric" autocomplete="off">`, 'Shown on the same page.')}
          <div class="field"><label for="c-token">Access token</label>
            <div class="input-group"><input type="password" id="c-token" name="access_token" autocomplete="off" placeholder="${saved(C.has_access_token)}">
            <button class="btn" type="button" data-action="toggle-secret" data-target="c-token" aria-pressed="false">Show</button></div>
            <span class="hint">A permanent token of a system user (Business settings > System users). Stored encrypted on this computer and never shown again.</span></div>
          <div class="field"><label for="c-secret">App secret</label>
            <div class="input-group"><input type="password" id="c-secret" name="app_secret" autocomplete="off" placeholder="${saved(C.has_app_secret)}">
            <button class="btn" type="button" data-action="toggle-secret" data-target="c-secret" aria-pressed="false">Show</button></div>
            <span class="hint">In Meta: App settings > Basic. Pulse uses it to check that incoming messages really come from Meta.</span></div>
          <div class="full">${field('c-public', 'Public address of this computer', html`<input type="url" id="c-public" name="public_url" value="${C.public_url}" placeholder="https://your-name.trycloudflare.com" autocomplete="off">`, 'Meta must reach Pulse over the internet to deliver incoming messages. Start a secure tunnel (for example Cloudflare Tunnel) to this computer and paste its https address here. Only the webhook address below works through it; the dashboard stays private.')}</div>
          <details class="full"><summary>Advanced</summary>
            ${field('c-ver', 'Graph API version', html`<input type="text" id="c-ver" name="api_version" value="${C.api_version}" placeholder="v23.0" autocomplete="off">`, 'Leave as it is unless Meta asks you to change it.')}
          </details>
        </div>
        <div class="card-foot"><button class="btn" type="button" data-action="conn-test">Test connection</button><button class="btn primary" type="submit">Save</button></div>
      </form>
      <section class="card"><div class="card-head"><div><h2>Connection</h2></div></div><div class="card-body" id="conn-status">${connectionStatus()}</div></section>
      <section class="card"><div class="card-head"><div><h2>Receive messages</h2><p>In Meta, open WhatsApp &gt; Configuration, press Edit next to Webhook, paste these two values, press Verify and save, then subscribe to the <strong>messages</strong> field.</p></div></div>
        <div class="card-body stack">
          <div class="field"><label for="c-hook">Callback URL (webhook address)</label>
            <div class="input-group"><input type="text" id="c-hook" value="${C.webhook_url || ''}" placeholder="Save the public address first" readonly>
            <button class="btn" type="button" data-action="copy-field" data-target="c-hook">Copy</button></div></div>
          <div class="field"><label for="c-verify">Verify token</label>
            <div class="input-group"><input type="text" id="c-verify" value="${C.verify_token || ''}" placeholder="Created when you save" readonly>
            <button class="btn" type="button" data-action="copy-field" data-target="c-verify">Copy</button>
            <button class="btn" type="button" data-action="conn-regen" ${C.verify_token ? '' : P.raw('disabled')}>New token</button></div>
            <span class="hint">A new token stops Meta's current webhook setup until you paste the new one there too.</span></div>
        </div></section>
      <section class="card"><div class="card-head"><div><h2>Disconnect</h2><p>Removes the access token, app secret and number from this computer. Pulse stops sending and receiving until you enter them again.</p></div></div>
        <div class="card-body row-between"><span class="hint">Messages and contacts are kept unless you choose otherwise.</span><button class="btn danger" type="button" data-action="conn-disconnect" ${C.status && C.status.configured ? '' : P.raw('disabled')}>Disconnect</button></div></section>
    </div>`;
  }
  const connBody = (d) => ({ phone_number_id: (d.phone_number_id || '').trim(), business_account_id: (d.business_account_id || '').trim(), access_token: (d.access_token || '').trim(), app_secret: (d.app_secret || '').trim(), public_url: (d.public_url || '').trim(), api_version: (d.api_version || '').trim() });
  P.form('set-connection', async (form, d) => {
    if (!/^\d{5,30}$/.test((d.phone_number_id || '').trim())) { P.fieldError(form, 'phone_number_id', 'Enter the phone number ID (digits only).'); return; }
    if (!C.has_access_token && !(d.access_token || '').trim()) { P.fieldError(form, 'access_token', 'Paste the access token.'); return; }
    const ok = await P.confirm({ title: 'Save the WhatsApp connection?', message: 'Enter your admin password to change how Pulse connects to WhatsApp.', confirmLabel: 'Save', password: true });
    if (!ok) return;
    const r = await P.post('/connection', Object.assign(connBody(d), { password: ok.password }));
    C = r.connection;
    draw();
    P.toast(C.status && C.status.connected ? 'Saved. Connected to ' + C.status.display_phone + '.' : 'Saved. ' + ((C.status && C.status.problem) || 'Pulse is checking the connection.'), { timeout: 7000 });
  });
  P.act('conn-test', async (btn) => {
    const d = P.formData(P.$('#f-connection'));
    const b = connBody(d);
    const r = await P.busy(btn, () => P.post('/connection/test', { phone_number_id: b.phone_number_id, access_token: b.access_token, api_version: b.api_version }).catch((e) => ({ ok: false, error: e.message })));
    if (r.ok) P.toast(r.message, { timeout: 7000 });
    else P.toast('The test did not work: ' + r.error, { type: 'error', timeout: 9000 });
  });
  P.act('copy-field', async (btn) => {
    const i = document.getElementById(btn.dataset.target);
    if (!i || !i.value) return;
    try { await navigator.clipboard.writeText(i.value); } catch (e) { i.select(); document.execCommand('copy'); }
    P.toast('Copied.');
  });
  P.act('conn-regen', async () => {
    const ok = await P.confirm({ title: 'Create a new verify token?', message: "Meta's webhook setup needs the new token too. Paste it in WhatsApp > Configuration and verify again.", confirmLabel: 'Create token', password: true });
    if (!ok) return;
    const r = await P.post('/connection', { password: ok.password, phone_number_id: C.phone_number_id, business_account_id: C.business_account_id, public_url: C.public_url, api_version: C.api_version, regenerate_verify_token: true });
    C = r.connection;
    draw();
    P.toast('New verify token created. Paste it in Meta.');
  });
  P.act('conn-disconnect', async () => {
    const ok = await P.confirm({
      title: 'Disconnect WhatsApp?', danger: true, password: true, confirmLabel: 'Disconnect',
      message: 'Pulse stops sending and receiving until you enter the Business API details again. Running campaigns are paused and automatic replies are switched off.',
      extra: html`<label class="check"><input type="checkbox" name="delete_data"><span>Also delete stored messages, contacts and scheduled messages</span></label>`,
    });
    if (!ok) return;
    const r = await P.post('/connection/disconnect', { password: ok.password, delete_data: !!ok.delete_data });
    P.toast(r.message);
    C = await P.get('/connection');
    draw();
  });

  /* ---------- Business ---------- */
  function businessTab() {
    const b = S.business;
    return html`<form class="card" data-form="set-business" id="f-business" novalidate>
      <div class="card-head"><div><h2>Business details</h2><p>Automatic replies use these details. If a detail is left empty, a reply that needs it is never sent with a gap: the customer gets a short holding reply and the chat appears under Needs attention.</p></div></div>
      <div class="card-body form-grid">
        ${field('b-name', 'Business name (required)', html`<input type="text" id="b-name" name="name" value="${b.name}" maxlength="80" required>`)}
        ${field('b-hours', 'Opening hours, as customers should read them (required)', html`<input type="text" id="b-hours" name="hours" value="${b.hours}" maxlength="200" placeholder="Mon-Sat 9:00 to 18:00, closed Sunday" required>`)}
        ${field('b-address', 'Address', html`<input type="text" id="b-address" name="address" value="${b.address}" maxlength="300">`)}
        ${field('b-website', 'Website', html`<input type="url" id="b-website" name="website" value="${b.website}" maxlength="200" placeholder="https://">`)}
        ${field('b-email', 'Email', html`<input type="email" id="b-email" name="email" value="${b.email}" maxlength="120">`)}
        <div class="full">${field('b-services', 'Services (sent for menu option 1)', html`<textarea id="b-services" name="services" maxlength="1500" placeholder="List what you offer, one per line.">${b.services}</textarea>`)}</div>
        <div class="full">${field('b-prices', 'Prices (sent for menu option 2)', html`<textarea id="b-prices" name="prices" maxlength="1500" placeholder="Your real prices, or how customers can get a quote.">${b.prices}</textarea>`)}</div>
      </div>
      <div class="card-foot"><button class="btn primary" type="submit">Save business details</button></div>
    </form>`;
  }
  P.form('set-business', async (form, d) => {
    if (!d.name.trim()) { P.fieldError(form, 'name', 'Enter the business name.'); return; }
    if (!d.hours.trim()) { P.fieldError(form, 'hours', 'Enter your opening hours.'); return; }
    await save('business', d, 'Business details saved.');
  });

  /* ---------- Opening hours ---------- */
  function hoursTab() {
    const h = S.business_hours;
    return html`<form class="card" data-form="set-hours" id="f-hours" novalidate>
      <div class="card-head"><div><h2>Opening hours</h2><p>Outside these hours the first message of a visit gets the away reply. Menu replies still work.</p></div></div>
      <div class="card-body stack">
        ${P.switchEl('enabled', h.enabled, 'Use opening hours', 'Off means Pulse treats you as always open.')}
        <div class="form-grid">
          ${field('h-tz', 'Time zone', html`<input type="text" id="h-tz" name="timezone" value="${h.timezone}" list="h-zones" maxlength="60"><datalist id="h-zones">${ZONES.map((z) => html`<option value="${z}">`)}</datalist>`, 'Local uses this computer\'s time zone.')}
        </div>
        <div class="table-wrap"><table class="table"><thead><tr><th>Day</th><th>Open</th><th>From</th><th>To</th></tr></thead><tbody>
          ${h.days.map((d) => html`<tr><td>${DAYS[d.day]}</td>
            <td><input type="checkbox" name="open_${d.day}" aria-label="Open on ${DAYS[d.day]}" ${d.open ? P.raw('checked') : ''}></td>
            <td><input type="time" name="start_${d.day}" value="${d.start}" aria-label="${DAYS[d.day]} opening time"></td>
            <td><input type="time" name="end_${d.day}" value="${d.end}" aria-label="${DAYS[d.day]} closing time"></td></tr>`)}
        </tbody></table></div>
        <p class="hint">A closing time earlier than the opening time means you are open past midnight.</p>
        ${field('h-holidays', 'Closed on these dates', html`<textarea id="h-holidays" name="holidays" rows="3" placeholder="2026-11-01&#10;2026-12-25">${lines(h.holidays)}</textarea>`, 'One date per line: year-month-day, for example 2026-12-25.')}
        <div class="field"><label for="h-away">Away reply</label><textarea id="h-away" name="away_reply" maxlength="1000">${h.away_reply}</textarea>${P.varPicker('h-away', [], { reply: true })}</div>
      </div>
      <div class="card-foot"><button class="btn primary" type="submit">Save opening hours</button></div>
    </form>`;
  }
  function hoursBody(d) {
    return {
      enabled: !!d.enabled, timezone: d.timezone.trim() || 'Local', away_reply: d.away_reply,
      holidays: toList(d.holidays),
      days: Object.keys(DAYS).map((k) => ({ day: k, open: !!d['open_' + k], start: d['start_' + k] || '09:00', end: d['end_' + k] || '18:00' })),
    };
  }
  P.form('set-hours', (form, d) => save('business_hours', hoursBody(d), 'Opening hours saved.'));

  /* ---------- Safety and pace ---------- */
  function presetCard(p, current) {
    return html`<button type="button" class="preset" data-action="set-pace" data-pace="${p.Name}" aria-pressed="${String(current === p.Name)}">
      <h4>${p.Label}</h4><span class="hint">${p.Explain}</span>
      <dl><dt>Messages per day</dt><dd>${p.DailyLimit}</dd><dt>Gap between campaign messages</dt><dd>${p.MinDelay}-${p.MaxDelay} s</dd>
      <dt>Replies per customer per hour</dt><dd>${p.RepliesPerContactHour}</dd><dt>Warm-up limit</dt><dd>${p.Warmup ? 'On (50/day)' : 'Off'}</dd></dl>
    </button>`;
  }
  function safetyTab() {
    const a = S.automation;
    return html`<div class="stack">
      <section class="card"><div class="card-head"><div><h2>Automatic replies</h2><p>The master switch for the bot.</p></div></div>
        <div class="card-body stack-sm">
          <label class="switch"><input type="checkbox" data-change="automation-master" ${a.enabled ? P.raw('checked') : ''}><span class="track" aria-hidden="true"></span><span class="label">Automatic replies are ${a.enabled ? 'on' : 'off'}</span></label>
          ${S.setup_missing.length && !a.enabled ? html`<div class="notice warn">${icon('info')}<div><p>Before switching on:</p><ul>${S.setup_missing.map((m) => html`<li>${m}</li>`)}</ul></div></div>` : ''}
        </div></section>
      <section class="card"><div class="card-head"><div><h2>Sending pace</h2><p>How fast Pulse sends. Slower is safer, especially for new numbers.${a.pace === 'custom' ? ' You are using custom values.' : ''}</p></div></div>
        <div class="card-body grid-3">${S.pace_presets.map((p) => presetCard(p, a.pace))}</div></section>
      <form class="card" data-form="set-safety" id="f-safety" novalidate>
        <div class="card-head"><div><h2>Safety rules</h2></div></div>
        <div class="card-body stack">
          ${P.switchEl('require_opt_in', a.require_opt_in, 'Campaigns only go to contacts with a recorded opt-in', 'Strongly recommended. WhatsApp requires opt-in before you message people.')}
          ${P.switchEl('enforce_24h_window', a.enforce_24h_window, 'Campaigns and scheduled messages only go to people who opted in or wrote in the last 24 hours', 'WhatsApp itself only allows free text within 24 hours of the customer writing; after that Pulse sends the official template you chose, or nothing.')}
          ${P.switchEl('dry_run', a.dry_run, 'Practice mode: send nothing', 'Pulse shows what it would have sent, without sending anything. Useful for trying out your rules.')}
          ${P.switchEl('allow_list_only', a.allow_list_only, 'Test mode: only reply to and send to the numbers below')}
          ${field('s-allow', 'Numbers allowed in test mode', html`<textarea id="s-allow" name="allow_list" rows="2" placeholder="+919876543210">${lines(a.allow_list)}</textarea>`, 'One number per line, with country code.')}
          ${field('s-block', 'Blocked numbers: never message these', html`<textarea id="s-block" name="blacklist_phones" rows="2">${lines(a.blacklist_phones)}</textarea>`, 'One number per line. No replies, campaigns or scheduled messages go to them.')}
          ${P.switchEl('auto_read', a.auto_read, 'Mark messages as read when the bot replies')}
          ${P.switchEl('typing_simulation', a.typing_simulation, 'Show "typing..." before automatic replies', 'This also marks the message as read.')}
          <details><summary>Advanced pace settings</summary>
            <div class="form-grid">
              ${field('s-daily_limit', 'Messages per day', num('daily_limit', a.daily_limit, 1, 5000), 'Automatic replies, campaigns, scheduled and integration messages together.')}
              ${field('s-cooldown_seconds', 'Minimum seconds between replies to one customer', num('cooldown_seconds', a.cooldown_seconds, 0, 3600))}
              ${field('s-max_replies_per_contact_hour', 'Automatic replies per customer per hour', num('max_replies_per_contact_hour', a.max_replies_per_contact_hour, 1, 100), 'When reached, the chat is paused for an hour and flagged for staff.')}
              ${field('s-typing_duration_ms', 'How long "typing..." shows (milliseconds)', num('typing_duration_ms', a.typing_duration_ms, 0, 8000, 100), '1000 milliseconds = 1 second.')}
              ${field('s-broadcast_min_delay', 'Shortest gap between campaign messages (seconds)', num('broadcast_min_delay', a.broadcast_min_delay, 2, 600))}
              ${field('s-broadcast_max_delay', 'Longest gap between campaign messages (seconds)', num('broadcast_max_delay', a.broadcast_max_delay, 2, 900))}
              ${field('s-batch_size', 'Pause after this many campaign messages', num('batch_size', a.batch_size, 0, 500))}
              ${field('s-batch_pause_seconds', 'Length of that pause (seconds)', num('batch_pause_seconds', a.batch_pause_seconds, 0, 3600))}
              ${field('s-handover_pause_hours', 'Hours the bot stays quiet after a hand-over', num('handover_pause_hours', a.handover_pause_hours, 1, 720))}
              <div class="field">${P.switchEl('warmup_mode', a.warmup_mode, 'Warm-up limit (50 automated messages per day)')}</div>
            </div>
          </details>
        </div>
        <div class="card-foot"><button class="btn primary" type="submit">Save safety settings</button></div>
      </form>
      <div class="card" id="f-notify">
        <div class="card-head"><div><h2>Alerts</h2><p>A sound and a message in the dashboard when a customer asks for a person.</p></div></div>
        <div class="card-body stack-sm">
          <label class="check">
            <input type="checkbox" id="in-app-sound" ${P.store.get('inAppSound', true) ? 'checked' : ''} data-action="toggle-sound">
            <span>Play a sound when a customer asks for a person</span>
          </label>
          <div class="row"><button class="btn" type="button" data-action="test-alert">Play a test alert</button></div>
          <p class="hint">Alerts appear only inside the Pulse window, so customer messages never pop up on the Windows desktop. Keep the Pulse window open (it can be minimised) to hear them.</p>
        </div>
      </div>
    </div>`;
  }
  function safetyBody(d) {
    const pace = ['daily_limit', 'cooldown_seconds', 'max_replies_per_contact_hour', 'typing_duration_ms', 'broadcast_min_delay', 'broadcast_max_delay', 'batch_size', 'batch_pause_seconds', 'warmup_mode'];
    const a = S.automation;
    const body = {
      require_opt_in: !!d.require_opt_in, enforce_24h_window: !!d.enforce_24h_window, dry_run: !!d.dry_run, allow_list_only: !!d.allow_list_only,
      allow_list: toList(d.allow_list), blacklist_phones: toList(d.blacklist_phones), auto_read: !!d.auto_read,
      typing_simulation: !!d.typing_simulation, handover_pause_hours: d.handover_pause_hours,
    };
    // Only send pace numbers that changed, so presets stay selected otherwise.
    pace.forEach((k) => { const v = k === 'warmup_mode' ? !!d[k] : d[k]; if (v !== a[k]) body[k] = v; });
    return body;
  }
  P.form('set-safety', async (form, d) => {
    if (d.broadcast_max_delay < d.broadcast_min_delay) { P.fieldError(form, 'broadcast_max_delay', 'The longest gap must be at least the shortest gap.'); return; }
    if (d.allow_list_only && !toList(d.allow_list).length) { P.fieldError(form, 'allow_list', 'Add at least one number that test mode may message.'); return; }
    await save('automation', safetyBody(d), 'Safety settings saved.');
    draw();
  });
  P.form('set-notify', (form, d) => save('notifications', { desktop: !!d.desktop }, 'Alert settings saved.'));
  const keepEdits = () => !P.dirty.active() || window.confirm('You have unsaved changes on this page. Discard them?');
  P.act('set-pace', async (btn) => {
    if (!keepEdits()) return;
    await P.busy(btn, () => save('automation', { pace: btn.dataset.pace }, btn.querySelector('h4').textContent + ' pace selected.'));
    draw();
  });
  P.change('automation-master', async (el) => {
    if (!keepEdits()) { el.checked = !el.checked; return; }
    try {
      await save('automation', { enabled: el.checked }, el.checked ? 'Automatic replies are on.' : 'Automatic replies are off.');
    } catch (e) { el.checked = !el.checked; throw e; }
    draw();
  });
  P.act('toggle-sound', (el) => {
    P.store.set('inAppSound', el.checked);
    P.toast(el.checked ? 'In-app audio alerts enabled.' : 'In-app audio alerts muted.');
    if (el.checked) P.playInAppChime();
  });
  P.act('test-alert', async (btn) => {
    P.playInAppChime();
    P.toast('Sample In-App Alert: Customer requested an agent', {
      type: 'warn',
      timeout: 8000,
      action: { label: 'Open Chat', fn: () => P.go('#/inbox') }
    });
  });

  /* ---------- Integrations ---------- */
  function integrationsTab() {
    const w = S.webhook;
    const origin = window.location.origin;
    return html`<div class="stack">
      <form class="card" data-form="set-webhook" id="f-webhook" novalidate>
        <div class="card-head"><div><h2>Connect your shop, website or CRM</h2><p>Optional. Pulse can tell another system when a message arrives, when a customer asks for a person, and when someone opts in or out. You only need this if a developer is connecting Pulse to your other software.</p></div></div>
        <div class="card-body stack">
          ${P.switchEl('enabled', w.enabled, 'Tell my other system about new events')}
          <div class="form-grid">
            <div class="full">${field('w-url', 'Address of the other system', html`<input type="url" id="w-url" name="url" value="${w.url}" placeholder="https://hooks.example.com/pulse">`, 'Your developer gives you this. It must be an internet address starting with https:// or http://.')}</div>
            <div class="full field"><label for="w-secret">Security key</label>
              <div class="input-group"><input type="password" id="w-secret" name="secret" value="${w.secret}" autocomplete="off" placeholder="${S.has_webhook_secret ? 'Saved - type to replace' : 'At least 16 characters'}">
              <button class="btn" type="button" data-action="toggle-secret" data-target="w-secret" aria-pressed="false">Show</button>
              <button class="btn" type="button" data-action="gen-secret">Generate</button></div>
              <span class="hint">Press Generate and share the key with your developer. It proves messages really come from Pulse, and lets their system send WhatsApp messages through Pulse.</span></div>
            ${field('w-timeout', 'Wait for an answer (seconds)', html`<input type="number" id="w-timeout" name="timeout_seconds" value="${w.timeout_seconds}" min="1" max="30">`)}
            ${field('w-retries', 'Tries before giving up', html`<input type="number" id="w-retries" name="max_retries" value="${w.max_retries}" min="1" max="10">`)}
          </div>
        </div>
        <div class="card-foot"><button class="btn" type="button" data-action="webhook-test">Send a test</button><button class="btn primary" type="submit">Save</button></div>
      </form>
      <section class="card"><div class="card-head"><div><h2>Not delivered</h2><p>Updates the other system did not accept, even after several tries.</p></div><div class="row"><button class="btn small" type="button" data-action="webhook-replay-all">Try all again</button><button class="btn small" type="button" data-action="webhook-clear">Clear list</button></div></div>
        <div id="wh-failed"><div class="card-body"><div class="skeleton"></div></div></div></section>
      <section class="card"><div class="card-head"><div><h2>Let other systems send WhatsApp messages</h2><p>For example, an order confirmation from your online shop.</p></div></div>
        <div class="card-body stack-sm">
          <details><summary>Instructions for your developer</summary>
            <p>POST JSON to <code>${origin}/api/v1/webhook/send</code> with header <code>X-Webhook-Secret</code> set to the security key above, or sign it: send <code>X-Webhook-Timestamp</code> (Unix seconds) and <code>X-Signature-SHA256: sha256=HMAC(key, timestamp + "." + body)</code>. Signed requests older than 5 minutes are refused. Outgoing events carry <code>X-Pulse-Signature: t=timestamp,v1=HMAC(key, timestamp + "." + body)</code>; check it and refuse timestamps older than 5 minutes. The older <code>X-Signature-SHA256</code> (HMAC of the body only) is still sent. See Help for an example.</p>
          </details>
          <p class="hint">This only works on this computer unless your developer makes Pulse reachable from the internet securely.</p>
        </div></section>
      <section class="card"><div class="card-head"><div><h2>Developer access key</h2><p>Only needed if a developer writes programs that control Pulse. Anyone with this key has full admin access, so keep it private.</p></div></div>
        <div class="card-body row-between"><span>${S.has_api_key ? P.statusPill('ok', 'A key is set') : P.statusPill('', 'No key')}</span>
          <div class="row"><button class="btn" type="button" data-action="api-key" data-op="generate">${S.has_api_key ? 'Replace key' : 'Create key'}</button>${S.has_api_key ? html`<button class="btn danger" type="button" data-action="api-key" data-op="revoke">Revoke key</button>` : ''}</div></div></section>
    </div>`;
  }
  async function loadFailed() {
    const box = P.$('#wh-failed');
    if (!box) return;
    const r = await P.get('/webhook/failed');
    P.mount(box, r.failed.length ? html`<div class="table-wrap"><table class="table cards"><thead><tr><th>Event</th><th>Created</th><th>Attempts</th><th>Last error</th><th class="right"></th></tr></thead><tbody>
      ${r.failed.map((f) => html`<tr><td data-label="Event">${EVENT_NAMES[f.event] || f.event}</td><td data-label="Created">${P.fmtDateTime(f.created_at)}</td><td data-label="Attempts">${f.attempts}</td><td data-label="Error">${f.last_error}</td><td class="actions"><button class="btn small" type="button" data-action="webhook-replay" data-id="${f.id}">Retry</button></td></tr>`)}
      </tbody></table></div>` : html`<div class="card-body hint">Everything was delivered.${r.pending ? ' ' + r.pending + ' updates are waiting to be sent.' : ''}</div>`);
  }
  const EVENT_NAMES = { 'message.received': 'New message', 'handover.requested': 'Customer asked for a person', 'contact.opted_out': 'Customer opted out', 'contact.opted_in': 'Customer opted in', ping: 'Test' };
  P.form('set-webhook', async (form, d) => {
    if (d.enabled && !d.url.trim()) { P.fieldError(form, 'url', 'Enter the URL.'); return; }
    await save('webhook', { enabled: !!d.enabled, url: d.url.trim(), secret: d.secret, timeout_seconds: d.timeout_seconds, max_retries: d.max_retries }, 'Integration settings saved.');
  });
  P.act('toggle-secret', (btn) => {
    const i = document.getElementById(btn.dataset.target);
    const show = i.type === 'password';
    i.type = show ? 'text' : 'password';
    btn.textContent = show ? 'Hide' : 'Show';
    btn.setAttribute('aria-pressed', String(show));
  });
  P.act('gen-secret', () => {
    const a = new Uint8Array(24);
    crypto.getRandomValues(a);
    const i = P.$('#w-secret');
    i.value = Array.from(a, (b) => b.toString(16).padStart(2, '0')).join('');
    i.type = 'text';
    i.dispatchEvent(new Event('input', { bubbles: true }));
  });
  P.act('webhook-test', async (btn) => {
    const d = P.formData(P.$('#f-webhook'));
    const r = await P.busy(btn, () => P.post('/webhook/test', { url: d.url, secret: d.secret }).catch((e) => ({ ok: false, error: e.message })));
    if (r.ok) P.toast('The test arrived. The connection works.');
    else P.toast('The test did not arrive: ' + r.error, { type: 'error', timeout: 8000 });
  });
  P.act('webhook-replay', async (btn) => { await P.busy(btn, () => P.post('/webhook/replay', { id: btn.dataset.id })); P.toast('Pulse will try again.'); loadFailed(); });
  P.act('webhook-replay-all', async (btn) => { const r = await P.busy(btn, () => P.post('/webhook/replay', { id: '' })); P.toast('Trying ' + r.queued + ' updates again.'); loadFailed(); });
  P.act('webhook-clear', async () => { await P.del('/webhook/failed'); loadFailed(); });
  P.act('api-key', async (btn) => {
    const gen = btn.dataset.op === 'generate';
    const ok = await P.confirm({ title: gen ? 'Create a new developer key?' : 'Remove the developer key?', message: gen ? 'Any previous key stops working. The new key is shown only once.' : 'Programs using the key will stop working.', confirmLabel: gen ? 'Create key' : 'Remove', danger: !gen, password: true });
    if (!ok) return;
    const r = await P.post('/settings/api-key', { password: ok.password, action: btn.dataset.op });
    S = await P.get('/settings');
    draw();
    if (r.api_key) P.modal({ title: 'Your developer key', body: html`<p>Copy it now. It will not be shown again.</p><pre>${r.api_key}</pre>`, foot: html`<button class="btn primary" type="button" data-modal-close>Done</button>` });
    else P.toast('Developer key removed.');
  });

  /* ---------- Privacy ---------- */
  function privacyTab() {
    return html`<div class="stack">
      <form class="card" data-form="set-storage" id="f-storage" novalidate>
        <div class="card-head"><div><h2>How long to keep messages</h2></div></div>
        <div class="card-body form-grid">
          ${field('p-ret', 'Delete messages older than (days)', html`<input type="number" id="p-ret" name="retention_days" value="${S.storage.retention_days}" min="0" max="3650">`, '0 keeps them until you delete them. Checked every day.')}
          ${field('p-max', 'Keep at most this many messages', html`<input type="number" id="p-max" name="max_messages" value="${S.storage.max_messages}" min="1000" max="1000000" step="1000">`, 'The oldest are removed first.')}
        </div>
        <div class="card-foot"><button class="btn primary" type="submit">Save</button></div>
      </form>
      <section class="card"><div class="card-head"><div><h2>Customer requests</h2><p>When a customer asks for a copy of their data, or asks you to delete it. Privacy laws such as India's DPDP Act give them this right.</p></div></div>
        <div class="card-body stack">
          <form class="input-group" data-form="export-one"><label class="sr-only" for="p-one">Phone number</label><input type="tel" id="p-one" name="phone" placeholder="Customer phone, for example +919876543210"><button class="btn" type="submit">Download their data</button></form>
          <form class="input-group" data-form="forget-one"><label class="sr-only" for="p-forget">Phone number</label><input type="tel" id="p-forget" name="phone" placeholder="Customer phone"><button class="btn danger" type="submit">Delete all their data</button></form>
        </div></section>
      <section class="card"><div class="card-head"><div><h2>Imported past chats</h2><p>Messages copied from the phone by Pulse versions before 2.0 (if you allowed it).</p></div></div>
        <div class="card-body row-between"><span class="hint">Contacts that only came from the import are removed too.</span><button class="btn danger" type="button" data-action="delete-imported">Delete imported chats</button></div></section>
      <form class="card" data-form="set-autobackup" id="f-autobackup" novalidate>
        <div class="card-head"><div><h2>Automatic backups</h2><p>Every day Pulse saves contacts, rules, templates, business details and opening hours, encrypted, and keeps the last 7 days.</p></div></div>
        <div class="card-body stack">
          ${P.switchEl('auto_backup', S.storage.auto_backup, 'Back up automatically every day')}
          ${field('p-copydir', 'Also save a copy in this folder (optional)', html`<input type="text" id="p-copydir" name="backup_copy_dir" value="${S.storage.backup_copy_dir || ''}" placeholder="E:\\Pulse backups">`, 'For example a USB drive or a OneDrive folder, so a broken computer does not lose everything. These copies only open on this Windows account; use Download backup below to move to a new computer.')}
          <div id="auto-backups"><div class="skeleton"></div></div>
        </div>
        <div class="card-foot"><button class="btn" type="button" data-action="backup-now">Back up now</button><button class="btn primary" type="submit">Save</button></div>
      </form>
      <section class="card"><div class="card-head"><div><h2>Backup</h2><p>Contacts, rules, templates, business details and opening hours. Messages are not included.</p></div></div>
        <div class="card-body row"><button class="btn" type="button" data-action="backup">${icon('download', 'icon-sm')}Download backup</button>
          <label class="btn" for="restore-file">${icon('upload', 'icon-sm')}Restore from backup</label><input type="file" id="restore-file" accept=".json,application/json" data-change="restore" hidden></div></section>
      <section class="card"><div class="card-head"><div><h2>Delete data</h2></div></div>
        <div class="card-body stack">
          <form class="input-group" data-form="purge-days"><label class="sr-only" for="p-days">Days</label><input type="number" id="p-days" name="days" min="1" max="3650" placeholder="Days, for example 90"><button class="btn danger" type="submit">Delete older messages</button></form>
          <div class="row-between"><span>Delete every message, contact and scheduled message.</span><button class="btn danger-solid" type="button" data-action="delete-everything">Delete everything</button></div>
          <div class="row"><button class="btn" type="button" data-action="export-all">${icon('download', 'icon-sm')}Download all data</button></div>
        </div></section>
      <section class="card"><div class="card-body hint">Data folder: <code>${S.data_dir || 'next to the program'}</code>. ${S.encrypted ? 'Stored encrypted with a key protected by your Windows account.' : 'Not encrypted on this system; protect the folder.'}</div></section>
    </div>`;
  }
  async function loadAutoBackups() {
    const box = P.$('#auto-backups');
    if (!box) return;
    const r = await P.get('/backups');
    P.mount(box, html`${r.backups.length ? html`<ul class="list">${r.backups.map((b) => html`<li><span class="grow">${P.fmtDateTime(b.created_at)}</span><button class="btn small" type="button" data-action="backup-restore" data-name="${b.name}">Restore</button></li>`)}</ul>`
      : html`<p class="hint">No automatic backup yet. The first one is made a few minutes after Pulse starts, or press Back up now.</p>`}
      ${r.old_session ? html`<div class="notice warn">${icon('alert')}<div><p>${r.old_session} files of the old linked-device connection (Pulse before 2.0) are still on this computer. Pulse no longer uses them. Delete them so no old WhatsApp session stays behind.</p><button class="btn small danger" type="button" data-action="delete-old-session">Delete old connection files</button></div></div>` : ''}
      ${r.old_copies ? html`<div class="notice warn">${icon('alert')}<div><p>${r.old_copies} unencrypted files from the old version (before 1.4) are still kept in the data folder, in case the upgrade needed to be undone. Delete them once Pulse works as expected.</p><button class="btn small danger" type="button" data-action="delete-old-copies">Delete unencrypted old copies</button></div></div>` : ''}`);
  }
  P.act('delete-old-session', async () => {
    const ok = await P.confirm({ title: 'Delete the old connection files?', message: 'These hold the WhatsApp Web session of Pulse before 2.0. Messages and contacts are not affected. This cannot be undone.', confirmLabel: 'Delete', danger: true, password: true });
    if (!ok) return;
    const r = await P.post('/privacy/delete-old-session', { password: ok.password });
    P.toast(r.message);
    loadAutoBackups();
  });
  P.act('delete-old-copies', async () => {
    const ok = await P.confirm({ title: 'Delete the unencrypted old copies?', message: 'These are the contact and message files from the version before 1.4. Their content is already in Pulse, encrypted. This cannot be undone.', confirmLabel: 'Delete', danger: true, password: true });
    if (!ok) return;
    const r = await P.post('/privacy/delete-old-copies', { password: ok.password });
    P.toast(r.message);
    loadAutoBackups();
  });
  P.form('set-autobackup', async (form, d) => {
    await save('storage', { auto_backup: !!d.auto_backup, backup_copy_dir: d.backup_copy_dir.trim() }, 'Backup settings saved.');
    draw();
  });
  P.act('backup-now', async (btn) => { const r = await P.busy(btn, () => P.post('/backups/now')); P.toast(r.message); loadAutoBackups(); });
  P.act('backup-restore', async (btn) => {
    const ok = await P.confirm({ title: 'Restore this backup?', message: 'Contacts, rules and templates from this backup are added or updated. Nothing is deleted, and opt-outs are kept.', confirmLabel: 'Restore', password: true });
    if (!ok) return;
    const r = await P.post('/backups/restore', { name: btn.dataset.name, password: ok.password });
    P.toast(r.message);
  });
  P.form('set-storage', (form, d) => save('storage', { retention_days: d.retention_days, max_messages: d.max_messages }, 'Storage settings saved.'));
  P.form('export-one', (form, d) => { const p = d.phone.replace(/[^0-9]/g, ''); if (p.length < 7) { P.fieldError(form, 'phone', 'Enter the phone number.'); return; } P.download('/privacy/export?phone=' + p); });
  P.form('forget-one', async (form, d) => {
    const p = d.phone.replace(/[^0-9]/g, '');
    if (p.length < 7) { P.fieldError(form, 'phone', 'Enter the phone number.'); return; }
    const ok = await P.confirm({ title: 'Delete all data for +' + p + '?', message: 'Their contact record, messages and scheduled messages will be deleted.', confirmLabel: 'Delete', danger: true, password: true });
    if (!ok) return;
    const r = await P.post('/privacy/forget-contact', { phone: p, password: ok.password });
    form.reset();
    P.toast(r.message);
  });
  P.act('delete-imported', async () => {
    const ok = await P.confirm({ title: 'Delete imported chats?', message: 'Past messages copied from the phone, and contacts that only came from that import, will be deleted from Pulse. Your phone keeps everything.', confirmLabel: 'Delete', danger: true });
    if (!ok) return;
    const r = await P.post('/privacy/delete-imported');
    P.toast(r.message);
  });
  P.act('backup', () => P.download('/backup'));
  P.act('export-all', () => P.download('/privacy/export'));
  P.change('restore', async (el) => {
    const f = el.files && el.files[0];
    el.value = '';
    if (!f) return;
    let bundle;
    try { bundle = JSON.parse(await f.text()); } catch (e) { P.toast('This is not a valid backup file.', { type: 'error' }); return; }
    const ok = await P.confirm({ title: 'Restore this backup?', message: 'Contacts, rules and templates from the file are added or updated. Nothing is deleted.', confirmLabel: 'Restore', password: true });
    if (!ok) return;
    const r = await P.post('/restore', { password: ok.password, bundle });
    P.toast(r.message);
    S = await P.get('/settings');
  });
  P.form('purge-days', async (form, d) => {
    if (!d.days || d.days < 1) { P.fieldError(form, 'days', 'Enter a number of days.'); return; }
    const ok = await P.confirm({ title: 'Delete messages older than ' + d.days + ' days?', message: 'This cannot be undone.', confirmLabel: 'Delete', danger: true, password: true });
    if (!ok) return;
    const r = await P.post('/privacy/purge', { password: ok.password, days: d.days });
    form.reset();
    P.toast(r.message);
  });
  P.act('delete-everything', async () => {
    const ok = await P.confirm({ title: 'Delete everything?', message: 'Every stored message, contact and scheduled message will be deleted. Settings, rules and templates stay. This cannot be undone.', confirmLabel: 'Delete everything', danger: true, password: true });
    if (!ok) return;
    const r = await P.post('/privacy/purge', { password: ok.password, all: true });
    P.toast(r.message);
  });

  /* ---------- Account ---------- */
  function accountTab() {
    return html`<div class="stack">
      <div class="grid-2">
        <form class="card" data-form="admin-password" novalidate>
          <div class="card-head"><div><h2>Admin password</h2><p>Changing it signs out every other device.</p></div></div>
          <div class="card-body stack">
            ${field('a-cur', 'Current password', html`<input type="password" id="a-cur" name="current_password" autocomplete="current-password" required>`)}
            ${field('a-new', 'New password', html`<input type="password" id="a-new" name="new_password" autocomplete="new-password" minlength="8" required>`, 'At least 8 characters with letters and numbers; no easy words.')}
            ${field('a-new2', 'New password again', html`<input type="password" id="a-new2" name="new_password2" autocomplete="new-password" required>`)}
          </div>
          <div class="card-foot"><button class="btn primary" type="submit">Change password</button></div>
        </form>
        <form class="card" data-form="staff-password" novalidate>
          <div class="card-head"><div><h2>Staff sign-in</h2><p>Staff can use the inbox, contacts, templates and scheduled messages, but not settings, rules or campaigns.</p></div>${S.has_operator_password ? P.statusPill('ok', 'On') : P.statusPill('', 'Off')}</div>
          <div class="card-body stack">
            ${field('o-cur', 'Your admin password', html`<input type="password" id="o-cur" name="current_password" autocomplete="current-password" required>`)}
            ${field('o-new', 'Staff password', html`<input type="password" id="o-new" name="new_password" autocomplete="new-password">`, 'Leave empty and save to switch staff sign-in off.')}
          </div>
          <div class="card-foot"><button class="btn primary" type="submit">Save staff password</button></div>
        </form>
      </div>
      <section class="card"><div class="card-head"><div><h2>This computer</h2></div></div>
        <div class="card-body stack-sm">
          ${S.autostart_supported ? html`<label class="switch"><input type="checkbox" data-change="autostart" ${S.autostart ? P.raw('checked') : ''}><span class="track" aria-hidden="true"></span><span><span class="label">Start Pulse when Windows starts</span><br><span class="hint">Runs in the background so replies keep working after a restart. Also set Windows to never sleep while plugged in.</span></span></label>` : html`<p class="hint">Starting automatically is available on Windows. On other systems use a service manager.</p>`}
          <div class="row"><button class="btn" type="button" data-action="desktop-shortcut">Create a desktop shortcut</button></div>
        </div></section>
      <section class="card"><div class="card-head"><div><h2>Activity log</h2><p>Sign-ins, setting changes, hand-overs, opt-outs and deletions.</p></div><button class="btn small" type="button" data-action="audit-csv">${icon('download', 'icon-sm')}Download for Excel</button></div>
        <div id="audit-box"><div class="card-body"><div class="skeleton"></div></div></div></section>
    </div>`;
  }
  const ACTIVITY = {
    LOGIN: 'Signed in', LOGIN_FAILED: 'Wrong password entered', LOGIN_LOCKED: 'Sign-in blocked after wrong passwords', LOGOUT: 'Signed out',
    REAUTH_FAILED: 'Wrong password for a protected action', PASSWORD_SET: 'Admin password chosen', PASSWORD_CHANGED: 'Admin password changed', PASSWORD_RESET: 'Admin password reset',
    OPERATOR_PASSWORD: 'Staff password changed', API_KEY_GENERATE: 'Developer key created', API_KEY_REVOKE: 'Developer key removed', AUTOSTART: 'Start with Windows changed',
    SETTINGS_SAVED: 'Settings changed', OPT_OUT: 'Customer opted out', OPT_IN: 'Customer opted in', AGENT_HANDOVER: 'Customer asked for a person',
    LOOP_DETECTED: 'Possible reply loop stopped', DAILY_LIMIT_REACHED: 'Daily sending limit reached', REPLY_CAP: 'Reply limit reached for a customer', RULE_SKIPPED: 'Reply not sent (missing detail)',
    BROADCAST_START: 'Campaign started', BROADCAST_PAUSE: 'Campaign paused', BROADCAST_PAUSED: 'Campaign paused', BROADCAST_RESUME: 'Campaign resumed', BROADCAST_CANCEL: 'Campaign cancelled',
    BROADCAST_COMPLETED: 'Campaign finished', BROADCAST_AUTO_PAUSE: 'Campaign paused automatically', BROADCAST_ERROR: 'Campaign stopped by an error',
    SCHEDULED: 'Message scheduled', SEND_OUTSIDE_WINDOW: 'Message sent to someone who had not written recently', CHAT_CLEARED: 'Chat deleted',
    CONTACT_SAVED: 'Contact saved', CONTACT_DELETED: 'Contact deleted', CONTACTS_BULK: 'Contacts changed together', CONSENT_CHANGED: 'Opt-in changed', DND_CHANGED: 'Do-not-message changed',
    BOT_PAUSED: 'Automatic replies paused for a customer', BOT_RESUMED: 'Automatic replies resumed for a customer', CONTACTS_IMPORTED: 'Contacts imported', CONTACTS_EXPORTED: 'Contacts downloaded',
    CONTACTS_PURGED: 'Contacts deleted', CONTACT_FORGOTTEN: "Customer's data deleted on request", DATA_EXPORTED: 'Data downloaded', DATA_DELETED_ALL: 'All data deleted', DATA_PURGED: 'Old messages deleted',
    RETENTION: 'Old messages deleted automatically', IMPORTED_DELETED: 'Imported chats deleted', BACKUP_DOWNLOADED: 'Backup downloaded', BACKUP_RESTORED: 'Backup restored', REPORT_EXPORTED: 'Report downloaded',
    RULE_SAVED: 'Reply rule saved', RULE_TOGGLED: 'Reply rule switched on/off', RULE_DELETED: 'Reply rule deleted', RULES_RESET: 'Starter rules restored', TEMPLATE_SAVED: 'Template saved', TEMPLATE_DELETED: 'Template deleted',
    WEBHOOK_FAILED: 'Update to other system not delivered', WEBHOOK_TEST: 'Integration tested', WEBHOOK_REJECTED: 'Blocked a message request with a wrong key',
    OLD_COPIES_DELETED: 'Unencrypted old copies deleted',
    APP_STARTED: 'Pulse started', APP_STOPPED: 'Pulse stopped', DASHBOARD_RESTARTED: 'Dashboard restarted after a problem', AUTO_BACKUP: 'Automatic backup saved', AUTO_BACKUP_FAILED: 'Automatic backup failed',
    CHAT_ASSIGNED: 'Chat assigned', CHAT_NOTE: 'Chat note saved',
    PAIR_CODE: 'Linking code requested', UNLINK: 'WhatsApp number unlinked', WHATSAPP_LOGGED_OUT: 'Phone removed this computer', RISK_ACKNOWLEDGED: 'Messaging rules accepted', HISTORY_IMPORT_CHOICE: 'Past chats choice made',
    CONNECTION_SAVED: 'WhatsApp connection changed', DISCONNECT: 'WhatsApp disconnected', CLOUD_WEBHOOK_VERIFIED: 'Meta confirmed the webhook', CLOUD_WEBHOOK_REJECTED: 'Blocked a webhook call that did not come from Meta', OLD_SESSION_DELETED: 'Old connection files deleted',
  };
  const WHO = { admin: 'Admin', operator: 'Staff', system: 'Pulse', 'api-key': 'Developer key', unknown: 'Unknown', webhook: 'Other system' };
  const activityName = (a) => ACTIVITY[a] || (a.charAt(0) + a.slice(1).replace(/_/g, ' ').toLowerCase());
  async function loadAudit() {
    const box = P.$('#audit-box');
    if (!box) return;
    const ev = await P.get('/audit?limit=100');
    P.mount(box, ev.length ? html`<div class="table-wrap"><table class="table cards"><thead><tr><th>When</th><th>What</th><th>By</th><th>Details</th></tr></thead><tbody>
      ${ev.map((e) => html`<tr><td data-label="When" class="nowrap">${P.fmtDateTime(e.timestamp)}</td><td data-label="What">${activityName(e.action)}</td><td data-label="By">${WHO[e.user] || e.user}</td><td data-label="Details">${e.details}</td></tr>`)}
      </tbody></table></div>` : html`<div class="card-body hint">No activity yet.</div>`);
  }
  P.form('admin-password', async (form, d) => {
    if (d.new_password !== d.new_password2) { P.fieldError(form, 'new_password2', 'The new passwords do not match.'); return; }
    const r = await P.post('/settings/password', { current_password: d.current_password, new_password: d.new_password });
    form.reset();
    P.toast(r.message);
  });
  P.form('staff-password', async (form, d) => {
    const r = await P.post('/settings/operator-password', { current_password: d.current_password, new_password: d.new_password });
    S = await P.get('/settings');
    P.toast(r.message);
    draw();
  });
  P.change('autostart', async (el) => {
    try { await P.post('/settings/autostart', { enabled: el.checked }); P.toast(el.checked ? 'Pulse will start with Windows.' : 'Pulse will not start with Windows.'); }
    catch (e) { el.checked = !el.checked; throw e; }
  });
  P.act('desktop-shortcut', async (btn) => { const r = await P.busy(btn, () => P.post('/app/create-shortcut')); P.toast(r.message); });
  P.act('audit-csv', () => P.download('/audit/export.csv'));
  /* ---------- Page ---------- */
  function draw() {
    const el = P.$('#settings-body');
    if (!el) return;
    P.$$('[data-action="settings-tab"]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.tab === tab)));
    const body = { whatsapp: whatsappTab, business: businessTab, hours: hoursTab, safety: safetyTab, integrations: integrationsTab, privacy: privacyTab, account: accountTab }[tab]();
    P.mount(el, body);
    P.dirty.clear();
    const forms = { business: ['#f-business', (f, d) => save('business', d, 'Business details saved.')], hours: ['#f-hours', (f, d) => save('business_hours', hoursBody(d), 'Opening hours saved.')], safety: ['#f-safety', (f, d) => save('automation', safetyBody(d), 'Safety settings saved.')], integrations: ['#f-webhook', (f, d) => save('webhook', { enabled: !!d.enabled, url: d.url.trim(), secret: d.secret, timeout_seconds: d.timeout_seconds, max_retries: d.max_retries }, 'Integration settings saved.')], privacy: ['#f-storage', (f, d) => save('storage', { retention_days: d.retention_days, max_messages: d.max_messages }, 'Storage settings saved.')] };
    if (forms[tab]) P.trackForm(P.$(forms[tab][0]), forms[tab][1]);
    if (tab === 'integrations') loadFailed().catch(P.error);
    if (tab === 'account') loadAudit().catch(P.error);
    if (tab === 'privacy') loadAutoBackups().catch(P.error);
    if (tab === 'safety') { const s = P.$('#browser-alert-state'); if (s && P.store.get('browserAlerts', false)) s.textContent = 'Browser alerts are on while this page is open.'; }
  }
  P.act('settings-tab', (btn) => {
    if (P.dirty.active() && !window.confirm('You have unsaved changes. Leave this tab without saving?')) return;
    tab = btn.dataset.tab;
    history.replaceState(null, '', '#/settings?tab=' + tab);
    draw();
  });

  P.view('settings', {
    title: 'Settings',
    admin: true,
    async render(el, q) {
      tab = TABS.some((t) => t[0] === q.get('tab')) ? q.get('tab') : 'whatsapp';
      await P.loadVars();
      [S, C] = await Promise.all([P.get('/settings'), P.get('/connection')]);
      P.mount(el, html`<div class="tabs" role="tablist" aria-label="Settings sections">${TABS.map(([k, l]) => html`<button type="button" role="tab" data-action="settings-tab" data-tab="${k}" aria-selected="${String(k === tab)}">${l}</button>`)}</div><div id="settings-body"></div>`);
      draw();
    },
  });
})();
