'use strict';
/* Pulse dashboard core: safe templating, API client, actions, router, dialogs. */
const P = (window.P = {});

/* ---------- Safe HTML templating: every value is escaped unless wrapped in P.raw() ---------- */
class Raw { constructor(s) { this.s = s; } toString() { return this.s; } }
P.raw = (s) => new Raw(String(s));
P.esc = (v) => String(v == null ? '' : v).replace(/[&<>"'`]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;', '`': '&#96;' }[c]));
function renderVal(v) {
  if (v == null || v === false) return '';
  if (v instanceof Raw) return v.s;
  if (Array.isArray(v)) return v.map(renderVal).join('');
  return P.esc(v);
}
P.html = (strings, ...vals) => {
  let out = '';
  strings.forEach((s, i) => { out += s; if (i < vals.length) out += renderVal(vals[i]); });
  return new Raw(out);
};
P.mount = (el, tpl) => { if (el) el.innerHTML = renderVal(tpl); return el; };
/* For polled content: redraws only when the markup changed, and then gives the focus back
   to the same control (matched by data-action with data-id or data-phone) and reopens
   <details> that were open, so a keyboard user or an expanded section is not lost. */
const lastDrawn = new WeakMap();
P.redraw = (el, tpl) => {
  if (!el) return el;
  const s = renderVal(tpl);
  if (lastDrawn.get(el) === s && el.innerHTML !== '') return el;
  const keyOf = (x) => [x.getAttribute('data-action') || x.id || x.getAttribute('href') || x.tagName, x.getAttribute('data-id') || '', x.getAttribute('data-phone') || ''].join('|');
  const a = document.activeElement;
  const focusKey = a && a !== el && el.contains(a) ? keyOf(a) : null;
  const open = P.$$('details', el).map((d) => d.open);
  el.innerHTML = s;
  lastDrawn.set(el, s);
  P.$$('details', el).forEach((d, i) => { if (open[i]) d.open = true; });
  if (focusKey) { const b = P.$$('button, a, input, select, textarea, summary, [tabindex]', el).find((x) => keyOf(x) === focusKey); if (b) b.focus(); }
  return el;
};
P.icon = (name, cls) => P.raw(`<svg class="icon ${cls || ''}" aria-hidden="true"><use href="#i-${P.esc(name)}"/></svg>`);
P.$ = (sel, root) => (root || document).querySelector(sel);
P.$$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));

/* ---------- Local storage that never throws ---------- */
/* Chat drafts (customer number and unsent text) live in sessionStorage: kept in memory
   while the window is open and never written to disk like localStorage (S33). */
const storeFor = (k) => (k.startsWith('draft.') ? sessionStorage : localStorage);
P.store = {
  get(k, d) { try { const v = storeFor(k).getItem('pulse.' + k); return v == null ? d : JSON.parse(v); } catch (e) { return d; } },
  set(k, v) { try { storeFor(k).setItem('pulse.' + k, JSON.stringify(v)); } catch (e) { /* storage unavailable */ } },
  del(k) { try { storeFor(k).removeItem('pulse.' + k); } catch (e) { /* ignore */ } },
};
/* Chat drafts and remembered numbers hold customer data: drop them when the session ends. */
P.clearLocal = () => { try { localStorage.clear(); sessionStorage.clear(); } catch (e) { /* storage unavailable */ } };

/* ---------- API client ---------- */
P.session = { csrf: '', role: '', product: 'Pulse', version: '' };
P.isAdmin = () => P.session.role === 'admin';

P.api = async function (method, path, body, opts) {
  const o = Object.assign({ quiet: false }, opts || {});
  const init = { method, headers: { 'X-CSRF-Token': P.session.csrf }, credentials: 'same-origin' };
  if (body instanceof FormData) init.body = body;
  else if (body !== undefined && body !== null) { init.headers['Content-Type'] = 'application/json'; init.body = JSON.stringify(body); }
  let res;
  try {
    res = await fetch('/api/v1' + path, init);
  } catch (e) {
    const err = new Error('Pulse is not responding. Check that the app is still running.');
    err.status = 0;
    throw err;
  }
  if (res.status === 401) { P.clearLocal(); window.location.href = '/login'; throw new Error('Signed out'); }
  const ct = res.headers.get('Content-Type') || '';
  const data = ct.includes('application/json') ? await res.json().catch(() => ({})) : await res.text();
  if (!res.ok || (data && data.ok === false && data.error)) {
    const err = new Error((data && data.error) || ('Request failed (' + res.status + ')'));
    err.status = res.status; err.data = data;
    throw err;
  }
  return data;
};
P.get = (p, o) => P.api('GET', p, null, o);
P.post = (p, b, o) => P.api('POST', p, b || {}, o);
P.patch = (p, b, o) => P.api('PATCH', p, b || {}, o);
P.del = (p, o) => P.api('DELETE', p, null, o);

/* Download a file from an authenticated GET endpoint. Fetched first, so an error is shown
   as a message instead of the browser leaving the dashboard for a JSON error page. */
P.download = async (path) => {
  let res;
  try {
    res = await fetch('/api/v1' + path, { credentials: 'same-origin' });
  } catch (e) {
    throw new Error('Pulse is not responding. Check that the app is still running.');
  }
  if (res.status === 401) { P.clearLocal(); window.location.href = '/login'; throw new Error('Signed out'); }
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || 'The download failed (' + res.status + ').');
  }
  const m = /filename="?([^";]+)"?/i.exec(res.headers.get('Content-Disposition') || '');
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = m ? m[1] : path.split('?')[0].split('/').pop();
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60000);
};

/* ---------- Formatting ---------- */
P.fmtNum = (n) => (n == null ? '-' : Number(n).toLocaleString());
P.fmtPct = (r) => (r == null ? 'No data yet' : Math.round(r * 1000) / 10 + '%');
P.fmtTime = (iso) => { if (!iso) return ''; const d = new Date(iso); return isNaN(d) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }); };
/* A bare day (2026-10-24) is that day here: new Date() would read it as UTC midnight,
   which is the evening before in time zones west of UTC. */
const toDate = (iso) => new Date(/^\d{4}-\d{2}-\d{2}$/.test(iso) ? iso + 'T00:00:00' : iso);
P.fmtDate = (iso) => { if (!iso) return ''; const d = toDate(iso); return isNaN(d) || d.getFullYear() < 2000 ? '' : d.toLocaleDateString([], { day: 'numeric', month: 'short', year: 'numeric' }); };
P.fmtDateTime = (iso) => { const d = P.fmtDate(iso); return d ? d + ' ' + P.fmtTime(iso) : ''; };
P.fmtRel = (iso) => {
  if (!iso) return '';
  const d = new Date(iso); if (isNaN(d) || d.getFullYear() < 2000) return '';
  const s = (Date.now() - d.getTime()) / 1000;
  if (s < 0) { const m = Math.round(-s / 60); return m < 60 ? 'in ' + m + ' min' : 'on ' + P.fmtDateTime(iso); }
  if (s < 60) return 'just now';
  if (s < 3600) return Math.floor(s / 60) + ' min ago';
  if (d.toDateString() === new Date().toDateString()) return P.fmtTime(iso);
  if (s < 7 * 86400) return d.toLocaleDateString([], { weekday: 'short' }) + ' ' + P.fmtTime(iso);
  return P.fmtDate(iso);
};
P.fmtPhone = (p) => (p ? '+' + String(p).replace(/^\+/, '') : '');
P.maskPhone = (p) => { const s = P.fmtPhone(p); return s.length > 6 ? s.slice(0, 3) + ' ' + '•'.repeat(Math.max(3, s.length - 7)) + ' ' + s.slice(-4) : s; };
P.who = (name, phone) => (name ? name : P.fmtPhone(phone));
P.plural = (n, one, many) => P.fmtNum(n) + ' ' + (n === 1 ? one : (many || one + 's'));

/* ---------- Toasts ---------- */
P.toast = (msg, opts) => {
  const o = Object.assign({ type: 'info', timeout: 4500 }, opts || {});
  const root = P.$('#toasts');
  const t = document.createElement('div');
  t.className = 'toast' + (o.type === 'error' || o.type === 'warn' ? ' ' + o.type : '');
  t.setAttribute('role', o.type === 'info' ? 'status' : 'alert');
  const span = document.createElement('span');
  span.className = 'grow';
  span.textContent = msg;
  t.appendChild(span);
  let done = false;
  const close = () => { if (!done) { done = true; t.remove(); } };
  if (o.action) {
    const b = document.createElement('button');
    b.type = 'button'; b.className = 'btn small'; b.textContent = o.action.label;
    b.addEventListener('click', () => { o.action.fn(); close(); });
    t.appendChild(b);
  }
  root.appendChild(t);
  setTimeout(() => { if (o.onExpire && !done) o.onExpire(); close(); }, o.timeout);
  return close;
};
P.error = (e) => P.toast(e && e.message ? e.message : String(e), { type: 'error', timeout: 7000 });

/* In-App Audio Synthesizer (Web Audio API)
 * Plays a gentle, pleasant two-tone chime directly inside the browser tab.
 * Does not leak to the Windows desktop and does not require external audio assets. */
/* Browsers keep audio suspended until the user interacts with the page. The context is
   created and resumed on the first click or key press, so a hand-over alert that arrives
   later (from a poll, with no gesture) can still be heard. */
const audioCtx = () => {
  const AudioCtx = window.AudioContext || window.webkitAudioContext;
  if (!AudioCtx) return null;
  try { return P._audioCtx || (P._audioCtx = new AudioCtx()); } catch (e) { return null; }
};
let soundNotice = null;
const unlockAudio = () => {
  if (P.store.get('inAppSound', true) === false) return;
  const ctx = audioCtx();
  if (!ctx) return;
  const done = () => {
    if (ctx.state !== 'running') return;
    document.removeEventListener('pointerdown', unlockAudio, true);
    document.removeEventListener('keydown', unlockAudio, true);
    if (soundNotice) { soundNotice(); soundNotice = null; }
  };
  if (ctx.state === 'running') done();
  else ctx.resume().then(done).catch(() => {});
};
document.addEventListener('pointerdown', unlockAudio, true);
document.addEventListener('keydown', unlockAudio, true);
/* Shown when an alert could not be heard: one click anywhere unlocks the sound. */
const soundBlocked = () => {
  /* The listeners are removed after the first unlock; the browser may suspend the audio
     again later (tab in the background, device change), so "click anywhere" needs them
     back. Adding the same listener twice is harmless. */
  document.addEventListener('pointerdown', unlockAudio, true);
  document.addEventListener('keydown', unlockAudio, true);
  if (soundNotice) return;
  const close = P.toast('Sound is blocked — click anywhere to enable', {
    type: 'warn', timeout: 86400000,
    action: { label: 'Enable sound', fn: () => { soundNotice = null; unlockAudio(); } },
  });
  soundNotice = () => { soundNotice = null; close(); };
};

P.playInAppChime = () => {
  if (P.store.get('inAppSound', true) === false) return;
  const ctx = audioCtx();
  if (!ctx) return;
  if (ctx.state === 'running') { chime(ctx); return; }
  // Without a gesture resume() can stay pending for ever: give it a moment, then say so.
  let settled = false;
  const t = setTimeout(() => { if (!settled) { settled = true; soundBlocked(); } }, 400);
  ctx.resume().then(() => { if (ctx.state === 'running' && !settled) { settled = true; clearTimeout(t); chime(ctx); } }).catch(() => {});
};
function chime(ctx) {
  try {
    const vol = Number(P.store.get('inAppSoundVolume', 0.25));
    const now = ctx.currentTime;

    const osc1 = ctx.createOscillator();
    const gain1 = ctx.createGain();
    osc1.type = 'sine';
    osc1.frequency.setValueAtTime(587.33, now);
    gain1.gain.setValueAtTime(vol, now);
    gain1.gain.exponentialRampToValueAtTime(0.001, now + 0.35);
    osc1.connect(gain1);
    gain1.connect(ctx.destination);
    osc1.start(now);
    osc1.stop(now + 0.35);

    const osc2 = ctx.createOscillator();
    const gain2 = ctx.createGain();
    osc2.type = 'sine';
    osc2.frequency.setValueAtTime(880.00, now + 0.12);
    gain2.gain.setValueAtTime(vol, now + 0.12);
    gain2.gain.exponentialRampToValueAtTime(0.001, now + 0.55);
    osc2.connect(gain2);
    gain2.connect(ctx.destination);
    osc2.start(now + 0.12);
    osc2.stop(now + 0.55);
  } catch (e) {
    /* AudioContext unavailable */
  }
}

/* Delete with an Undo window instead of a blocking dialog. */
P.undoable = (label, commit, undo) => {
  let cancelled = false;
  P.toast(label, {
    timeout: 5000,
    action: { label: 'Undo', fn: () => { cancelled = true; if (undo) undo(); } },
    onExpire: () => { if (!cancelled) commit().catch(P.error); },
  });
};

/* ---------- Busy buttons ---------- */
P.busy = async (btn, fn) => {
  if (!btn) return fn();
  if (btn.classList.contains('busy')) return undefined;
  btn.classList.add('busy'); btn.disabled = true; btn.setAttribute('aria-busy', 'true');
  btn.dataset.busyLock = '1';
  try { return await fn(); } finally {
    btn.classList.remove('busy'); btn.removeAttribute('aria-busy');
    // Only re-enable if nothing inside fn decided the button must stay disabled.
    if (btn.dataset.busyLock === '1') btn.disabled = false;
    delete btn.dataset.busyLock;
  }
};
/* Disable a button even while P.busy is running on it. */
P.setDisabled = (btn, disabled) => { if (!btn) return; btn.disabled = disabled; if (btn.dataset.busyLock) btn.dataset.busyLock = disabled ? '0' : '1'; };

/* ---------- Modal dialogs with focus trap ---------- */
let modalStack = [];
P.modal = (o) => {
  const root = P.$('#modal-root');
  const prevFocus = document.activeElement;
  const wrap = document.createElement('div');
  wrap.className = 'modal-backdrop';
  const id = 'm' + Math.random().toString(36).slice(2, 8);
  wrap.innerHTML = renderVal(P.html`<div class="modal ${o.wide ? 'wide' : ''}" role="dialog" aria-modal="true" aria-labelledby="${id}">
    <div class="modal-head"><h2 id="${id}">${o.title}</h2><button class="btn ghost icon-only" type="button" data-modal-close aria-label="Close">${P.icon('x')}</button></div>
    <div class="modal-body">${o.body || ''}</div>
    ${o.foot ? P.html`<div class="modal-foot">${o.foot}</div>` : ''}
  </div>`);
  root.appendChild(wrap);
  const m = { el: wrap.firstElementChild, closed: false };
  m.close = (result) => {
    if (m.closed) return;
    m.closed = true;
    wrap.remove();
    modalStack = modalStack.filter((x) => x !== m);
    if (prevFocus && prevFocus.focus && document.body.contains(prevFocus)) prevFocus.focus();
    else { const v = P.$('#view'); if (v) v.focus(); }
    if (o.onClose) o.onClose(result);
  };
  // A stray click outside or Escape must not throw away a long text being written.
  const typed = () => P.$$('textarea, input[type="text"], input[type="tel"], input[type="email"], input:not([type])', m.el).map((x) => x.value).join('\u0000');
  let initial = '';
  const softClose = () => {
    if (typed() !== initial && !window.confirm('Close without saving? What you typed will be lost.')) return;
    m.close();
  };
  wrap.addEventListener('mousedown', (e) => { if (e.target === wrap && !o.sticky) softClose(); });
  wrap.addEventListener('click', (e) => { if (e.target.closest('[data-modal-close]')) m.close(); });
  wrap.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !o.sticky) { e.stopPropagation(); softClose(); }
    if (e.key === 'Tab') {
      const f = P.$$('button:not([disabled]), [href], input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])', m.el).filter((x) => x.offsetParent !== null);
      if (!f.length) return;
      const first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
  });
  modalStack.push(m);
  if (o.onOpen) o.onOpen(m.el, m);
  initial = typed();
  const auto = P.$('[autofocus]', m.el) || P.$('input, textarea, select', m.el) || P.$('.modal-foot .btn.primary', m.el) || P.$('[data-modal-close]', m.el);
  if (auto) setTimeout(() => auto.focus(), 0);
  return m;
};

/* Readable name for a reply rule, including the built-in ones. */
P.ruleLabel = (key) => ({
  'system:opt_out': 'Opt-out confirmation', 'system:opt_in': 'Opt-in confirmation', 'system:handover': 'Hand-over to a person',
  'system:fallback': 'Fallback reply', 'system:away': 'Away reply (closed)', 'system:default': 'Fallback reply',
  'system:missing_detail': 'Holding reply (a business detail is empty)',
}[key] || String(key || '').replace(/^system:/, ''));

/* Confirmation dialog. With password:true it asks for the admin password (dangerous actions). */
P.confirm = (o) => new Promise((resolve) => {
  let result = false;
  const m = P.modal({
    title: o.title,
    body: P.html`<form data-form="confirm-dialog" class="stack" novalidate>
      <p>${o.message}</p>
      ${o.extra || ''}
      ${o.password ? P.html`<div class="field"><label for="confirm-pw">Admin password</label><input type="password" id="confirm-pw" name="password" autocomplete="current-password" required autofocus></div>` : ''}
    </form>`,
    foot: P.html`<button class="btn" type="button" data-modal-close>Cancel</button>
      <button class="btn ${o.danger ? 'danger-solid' : 'primary'}" type="button" data-confirm-ok>${o.confirmLabel || 'Confirm'}</button>`,
    onClose: () => resolve(result),
  });
  const ok = () => {
    const form = P.$('form', m.el);
    const data = P.formData(form);
    if (o.password && !data.password) { P.$('#confirm-pw', m.el).setAttribute('aria-invalid', 'true'); P.$('#confirm-pw', m.el).focus(); return; }
    result = data;
    m.close();
  };
  P.$('[data-confirm-ok]', m.el).addEventListener('click', ok);
  P.$('form', m.el).addEventListener('submit', (e) => { e.preventDefault(); ok(); });
});

/* Asks for the owner's own mobile number for a test message: the business number
   cannot message itself. Remembered on this device for next time. */
P.askTestPhone = async () => {
  const saved = P.store.get('testPhone', '');
  const ok = await P.confirm({
    title: 'Send a test to your phone',
    message: 'Enter your own mobile number (not the business number). Send any message from it to your business number first: WhatsApp only allows free text within 24 hours of the customer writing.',
    extra: P.html`<div class="field"><label for="test-phone">Your mobile number</label><input type="tel" id="test-phone" name="phone" value="${saved}" placeholder="+91 98765 43210" autocomplete="tel" required></div>`,
    confirmLabel: 'Send test',
  });
  if (!ok || !String(ok.phone || '').trim()) return '';
  P.store.set('testPhone', ok.phone.trim());
  return ok.phone.trim();
};

/* Fields for an official WhatsApp template (approved by Meta in WhatsApp Manager), used
   for customers who have not written in the last 24 hours. */
P.templateFields = (prefix) => P.html`<details class="full" data-template-fields>
  <summary>Official WhatsApp template (for customers who have not written in 24 hours)</summary>
  <div class="form-grid">
    <div class="field"><label for="${prefix}-tname">Template name</label><input type="text" id="${prefix}-tname" name="template_name" maxlength="512" placeholder="order_update" autocomplete="off"><span class="hint">Exactly as in WhatsApp Manager. The template must be approved by Meta.</span></div>
    <div class="field"><label for="${prefix}-tlang">Language code</label><input type="text" id="${prefix}-tlang" name="template_language" maxlength="8" placeholder="en" autocomplete="off"><span class="hint">For example en, en_US or hi.</span></div>
    <div class="field full"><label for="${prefix}-tparams">Values for {{1}}, {{2}}...</label><textarea id="${prefix}-tparams" name="template_params" rows="2" placeholder="{{first_name}}"></textarea><span class="hint">One per line, in order. You can use variables such as {{first_name}}. Leave empty if the template has no variables.</span></div>
    <div class="field"><label for="${prefix}-theader">Header value (optional)</label><input type="text" id="${prefix}-theader" name="template_header" maxlength="1000" autocomplete="off"><span class="hint">Only if the template's text header has a {{1}}.</span></div>
    <div class="field"><label for="${prefix}-tmedia">Header photo, video or document link (optional)</label><input type="url" id="${prefix}-tmedia" name="template_header_media" maxlength="2000" placeholder="https://example.com/offer.jpg" autocomplete="off"><span class="hint">Only if the template's header is a photo, video or document. A public https link.</span></div>
    <div class="field full"><label for="${prefix}-tbuttons">Link button endings (optional)</label><textarea id="${prefix}-tbuttons" name="template_buttons" rows="2" placeholder="order/{{phone}}"></textarea><span class="hint">One line per button, in the template's button order, for buttons whose link ends in {{1}}. Leave a line empty for a button without a variable.</span></div>
  </div>
</details>`;
/* Button lines keep their position (an empty line is a button without a variable); the
   server drops empty lines at the end. */
P.templateFrom = (d) => (d.template_name && d.template_name.trim()
  ? {
    name: d.template_name.trim(), language: (d.template_language || '').trim(),
    params: (d.template_params || '').split('\n').map((x) => x.trim()).filter(Boolean),
    header_params: (d.template_header || '').trim() ? [d.template_header.trim()] : [],
    header_media: (d.template_header_media || '').trim(),
    button_params: (d.template_buttons || '').split('\n').map((x) => x.trim()),
  }
  : null);

/* ---------- Forms ---------- */
P.formData = (form) => {
  const out = {};
  if (!form) return out;
  for (const el of form.elements) {
    if (!el.name || el.disabled) continue;
    if (el.type === 'checkbox') { if (el.dataset.list !== undefined) { (out[el.name] = out[el.name] || []); if (el.checked) out[el.name].push(el.value); } else out[el.name] = el.checked; }
    else if (el.type === 'radio') { if (el.checked) out[el.name] = el.value; }
    else if (el.type === 'number') out[el.name] = el.value === '' ? null : Number(el.value);
    else if (el.type === 'file') continue;
    else out[el.name] = el.value;
  }
  return out;
};
P.fieldError = (form, name, msg) => {
  const el = form.elements[name];
  if (el && el.setAttribute) { el.setAttribute('aria-invalid', 'true'); el.focus(); }
  P.toast(msg, { type: 'error' });
};

/* ---------- Action registry: every data-action / data-form / data-change must be registered here ---------- */
const ACTIONS = {}, FORMS = {}, CHANGES = {};
P.act = (name, fn) => { ACTIONS[name] = fn; };
P.form = (name, fn) => { FORMS[name] = fn; };
P.change = (name, fn) => { CHANGES[name] = fn; };
/* The confirm dialog handles its own submit; registering keeps the global handler quiet. */
P.form('confirm-dialog', () => {});

document.addEventListener('click', (e) => {
  const el = e.target.closest('[data-action]');
  if (!el || el.disabled || el.getAttribute('aria-disabled') === 'true') return;
  const name = el.getAttribute('data-action');
  const fn = ACTIONS[name];
  if (!fn) { console.error('No handler for action', name); P.toast('This button is not available right now.', { type: 'error' }); return; }
  if (el.tagName === 'A' || el.type === 'submit') e.preventDefault();
  Promise.resolve().then(() => fn(el, e)).catch(P.error);
});
document.addEventListener('submit', (e) => {
  const form = e.target.closest('form[data-form]');
  if (!form) return;
  e.preventDefault();
  const fn = FORMS[form.getAttribute('data-form')];
  if (!fn) { console.error('No handler for form', form.getAttribute('data-form')); return; }
  P.$$('[aria-invalid]', form).forEach((x) => x.removeAttribute('aria-invalid'));
  const btn = e.submitter || P.$('[type="submit"]', form);
  P.busy(btn, () => Promise.resolve(fn(form, P.formData(form), e))).catch(P.error);
});
const onChange = (e) => {
  const el = e.target.closest('[data-change]');
  if (!el) return;
  const fn = CHANGES[el.getAttribute('data-change')];
  if (fn) Promise.resolve().then(() => fn(el, e)).catch(P.error);
};
document.addEventListener('change', onChange);
document.addEventListener('input', (e) => { const el = e.target.closest('[data-change][data-live]'); if (el) onChange(e); });

/* ---------- Router ---------- */
const VIEWS = {};
let current = null;
let pollers = [];
let routeToken = 0;
P.view = (name, def) => { VIEWS[name] = def; };
/* Pollers belong to the page that was current when render started; a page that
   finishes loading after the user moved on registers nothing. */
P.every = (ms, fn) => {
  const tok = P.renderToken;
  if (tok !== routeToken) return 0;
  // A failed poll is retried by the next tick; it must not surface as an unhandled rejection.
  const id = setInterval(() => { if (tok !== routeToken) { clearInterval(id); return; } if (!document.hidden) Promise.resolve().then(fn).catch(() => {}); }, ms);
  pollers.push(id);
  return id;
};
P.closeModals = () => { modalStack.slice().forEach((m) => m.close()); };
P.params = () => {
  const h = window.location.hash.replace(/^#\/?/, '');
  const [name, qs] = h.split('?');
  return { name: name || 'home', q: new URLSearchParams(qs || '') };
};
P.go = (hash) => { if (window.location.hash === hash) P.route(); else window.location.hash = hash; };
P.route = async () => {
  if (P.dirty.active() && !window.confirm('You have unsaved changes. Leave without saving?')) {
    history.replaceState(null, '', P.dirty.hash);
    return;
  }
  P.dirty.clear();
  const { name, q } = P.params();
  let def = VIEWS[name];
  if (!def || (def.admin && !P.isAdmin())) { def = VIEWS.home; }
  pollers.forEach(clearInterval);
  pollers = [];
  const tok = ++routeToken;
  P.closeModals();
  if (current && current.leave) { try { current.leave(); } catch (e) { /* ignore */ } }
  current = def;
  P.$('#page-title').textContent = def.title;
  document.title = def.title + ' - ' + P.session.product;
  P.$$('[data-nav]').forEach((a) => { if (a.getAttribute('data-nav') === name) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current'); });
  P.$('#sidebar').classList.remove('open');
  P.$$('[data-action="toggle-menu"]').forEach((b) => b.setAttribute('aria-expanded', 'false'));
  const view = P.$('#view');
  view.setAttribute('aria-busy', 'true');
  P.store.set('lastView', name);
  // Each page renders into its own container; a slow page that finishes after the user
  // navigated away writes into a detached element instead of over the new page.
  const slot = document.createElement('div');
  P.mount(slot, P.html`<div class="card card-body"><div class="skeleton"></div><div class="skeleton"></div><div class="skeleton"></div></div>`);
  view.replaceChildren(slot);
  P.renderToken = tok;
  try {
    await def.render(slot, q);
  } catch (e) {
    if (tok === routeToken) P.mount(slot, P.html`<div class="notice bad">${P.icon('alert')}<div><p><strong>This page could not be loaded.</strong></p><p>${e.message}</p><button class="btn" type="button" data-action="reload-view">Try again</button></div></div>`);
  }
  if (tok === routeToken) view.removeAttribute('aria-busy');
};
P.act('skip-to-content', () => { const v = P.$('#view'); if (v) v.focus(); });
P.act('reload-view', () => P.route());
P.refresh = () => { if (current && current.refresh) return current.refresh(); return P.route(); };

/* ---------- Unsaved changes bar ---------- */
P.dirty = (() => {
  let state = null;
  const root = () => P.$('#savebar-root');
  return {
    hash: '',
    active: () => !!state,
    clear() { state = null; P.mount(root(), ''); },
    mark(save, discard, label) {
      this.hash = window.location.hash;
      if (state) { state.save = save; state.discard = discard; return; }
      state = { save, discard };
      P.mount(root(), P.html`<div class="savebar" role="region" aria-label="Unsaved changes"><span class="grow">${label || 'You have unsaved changes.'}</span>
        <button class="btn" type="button" data-action="savebar-discard">Discard</button>
        <button class="btn primary" type="button" data-action="savebar-save">Save changes</button></div>`);
    },
    async save(btn) { if (!state) return; const s = state; await P.busy(btn, () => s.save()); },
    discard() { if (!state) return; const d = state.discard; this.clear(); if (d) d(); },
  };
})();
P.act('savebar-save', (el) => P.dirty.save(el));
P.act('savebar-discard', () => P.dirty.discard());
window.addEventListener('beforeunload', (e) => { if (P.dirty.active()) { e.preventDefault(); e.returnValue = ''; } });

/* Track edits on a form and show the save bar. */
P.trackForm = (form, save) => {
  if (!form) return;
  const initial = JSON.stringify(P.formData(form));
  const check = () => {
    if (JSON.stringify(P.formData(form)) !== initial) P.dirty.mark(async () => { await save(form, P.formData(form)); P.dirty.clear(); }, () => P.route());
  };
  form.addEventListener('input', check);
  form.addEventListener('change', check);
};

/* ---------- Shared widgets ---------- */
P.empty = (title, text, action) => P.html`<div class="empty"><h3>${title}</h3><p>${text}</p>${action || ''}</div>`;
P.switchEl = (name, checked, label, hint, attrs) => P.html`<label class="switch"><input type="checkbox" name="${name}" ${checked ? P.raw('checked') : ''} ${attrs || ''}><span class="track" aria-hidden="true"></span><span><span class="label">${label}</span>${hint ? P.html`<br><span class="hint">${hint}</span>` : ''}</span></label>`;
P.statusPill = (cls, text) => P.html`<span class="pill ${cls}"><span class="dot"></span>${text}</span>`;
P.statusText = { pending: ['info', 'Waiting'], sending: ['info', 'Sending'], sent: ['ok', 'Sent'], delivered: ['ok', 'Delivered'], read: ['ok', 'Read'], failed: ['bad', 'Failed'], skipped: ['warn', 'Not sent'], cancelled: ['', 'Cancelled'], dry_run: ['info', 'Test only'], received: ['', 'Received'], running: ['ok', 'Sending'], paused: ['warn', 'Paused'], completed: ['ok', 'Finished'], stopped: ['bad', 'Stopped'] };
P.statusBadge = (s) => { const v = P.statusText[s] || ['', s]; return P.statusPill(v[0], v[1]); };

/* Variable picker shared by the reply, template, campaign and schedule editors. */
P.vars = null;
P.loadVars = async () => { if (!P.vars) P.vars = await P.get('/variables'); return P.vars; };
/* {{message}} (the customer's text) only makes sense in replies to an incoming message. */
P.varPicker = (target, extra, opts) => {
  const withMessage = !!(opts && opts.reply);
  const vars = ((P.vars && P.vars.variables) || []).filter((v) => withMessage || v.key !== 'message');
  const extras = (extra || []).map((k) => ({ key: k, label: k, empty: false }));
  return P.html`<div class="var-picker" role="group" aria-label="Insert a variable">${vars.concat(extras).map((v) => P.html`<button type="button" class="${v.empty ? 'empty' : ''}" data-action="insert-var" data-target="${target}" data-var="${v.key}" title="${v.label}${v.empty ? ' (not filled in yet)' : ''}">{{${v.key}}}</button>`)}</div>`;
};
P.act('insert-var', (el) => {
  const t = document.getElementById(el.dataset.target);
  if (!t) return;
  const token = '{{' + el.dataset.var + '}}';
  const s = t.selectionStart == null ? t.value.length : t.selectionStart;
  const e = t.selectionEnd == null ? t.value.length : t.selectionEnd;
  t.value = t.value.slice(0, s) + token + t.value.slice(e);
  t.focus();
  t.setSelectionRange(s + token.length, s + token.length);
  t.dispatchEvent(new Event('input', { bubbles: true }));
});

/* Live preview of message text with sample customer data. */
P.livePreview = (textarea, box, extra) => {
  let timer = null;
  const run = async () => {
    const text = textarea.value;
    if (!text.trim()) { box.textContent = 'The preview appears here.'; box.classList.add('muted'); return; }
    try {
      const r = await P.post('/templates/preview', { text, extra: extra || {} }, { quiet: true });
      box.classList.remove('muted');
      box.textContent = r.text || '';
      const warn = [];
      if (r.unknown && r.unknown.length) warn.push('Unknown: ' + r.unknown.map((k) => k === 'unclosed {{ }}' ? k : '{{' + k + '}}').join(', '));
      if (r.missing && r.missing.length) warn.push('No value yet for: ' + r.missing.map((k) => '{{' + k + '}}').join(', '));
      const note = box.nextElementSibling;
      if (note && note.classList.contains('preview-note')) {
        note.textContent = warn.join('. ') || r.length + ' characters';
        note.classList.toggle('error-text', warn.length > 0);
        note.classList.toggle('hint', warn.length === 0);
      }
    } catch (e) { /* preview is best-effort */ }
  };
  textarea.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(run, 250); });
  run();
};

/* Keyword chips input used by the reply editor and contact tags. */
P.chips = (wrap) => {
  const input = P.$('input[type="text"]', wrap);
  const hidden = P.$('input[type="hidden"]', wrap);
  const list = () => hidden.value.split(',').map((x) => x.trim()).filter(Boolean);
  const draw = () => {
    P.$$('.tag', wrap).forEach((t) => t.remove());
    list().forEach((k, i) => {
      const t = document.createElement('span');
      t.className = 'tag';
      t.textContent = k;
      const b = document.createElement('button');
      b.type = 'button'; b.setAttribute('aria-label', 'Remove ' + k);
      b.innerHTML = renderVal(P.icon('x', 'icon-sm'));
      b.addEventListener('click', () => { const l = list(); l.splice(i, 1); hidden.value = l.join(', '); draw(); hidden.dispatchEvent(new Event('input', { bubbles: true })); });
      t.appendChild(b);
      wrap.insertBefore(t, input);
    });
  };
  const add = () => {
    const parts = input.value.split(',').map((x) => x.trim()).filter(Boolean);
    if (!parts.length) return;
    const l = list();
    parts.forEach((p) => { if (!l.some((x) => x.toLowerCase() === p.toLowerCase())) l.push(p); });
    hidden.value = l.join(', ');
    input.value = '';
    draw();
    hidden.dispatchEvent(new Event('input', { bubbles: true }));
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add(); }
    else if (e.key === 'Backspace' && !input.value) { const l = list(); l.pop(); hidden.value = l.join(', '); draw(); }
  });
  input.addEventListener('blur', add);
  draw();
  return { add };
};
P.chipsField = (id, name, value, placeholder) => P.html`<div class="chips-input" data-chips><input type="hidden" name="${name}" value="${value || ''}"><input type="text" id="${id}" placeholder="${placeholder}" autocomplete="off"></div>`;
