'use strict';
(function () {
  const { html } = P;
  let lastAttention = null;
  let lastHandoverAlertId = null;

  P.applyOverview = (o) => {
    const wa = o.whatsapp || {};
    const pill = P.$('#conn-pill');
    let cls = 'bad', text = 'Not connected';
    if (wa.configured && wa.problem) { cls = 'bad'; text = 'Needs attention'; }
    else if (wa.connected) { cls = 'ok'; text = 'Connected' + (wa.phone ? ' as ' + P.fmtPhone(wa.phone) : ''); }
    else if (wa.configured) { cls = 'warn'; text = 'Checking'; }
    pill.className = 'pill ' + cls;
    P.mount(pill, html`<span class="dot"></span><span>${text}</span>`);

    const mode = P.$('#mode-pill');
    const a = o.automation || {};
    let m = null;
    if (a.dry_run) m = ['info', 'Practice mode'];
    else if (a.allow_list_only) m = ['warn', 'Test mode'];
    else if (!a.enabled) m = ['', 'Automatic replies off'];
    mode.hidden = !m;
    if (m) { mode.className = 'pill hide-sm ' + m[0]; mode.textContent = m[1]; }

    const n = (o.counts.needs_attention || 0) + (o.counts.unread_chats || 0);
    ['#badge-inbox', '#badge-inbox-m'].forEach((id) => {
      const b = P.$(id);
      if (!b) return;
      b.hidden = n === 0;
      b.textContent = n > 99 ? '99+' : String(n);
      b.setAttribute('aria-label', n + ' chats need you');
    });

    if (!P.handoverChannel && 'BroadcastChannel' in window) {
      P.handoverChannel = new BroadcastChannel('pulse_handover_sync');
      P.handoverChannel.onmessage = (e) => {
        if (e.data && e.data.type === 'ALERT_SHOWN') {
          lastHandoverAlertId = e.data.id;
        }
      };
    }

    const att = o.counts.needs_attention || 0;
    if (lastAttention !== null && att > lastAttention) {
      const who = (o.attention && o.attention[o.attention.length - 1]) || {};
      const alertId = (who.phone || 'customer') + ':' + att;
      if (lastHandoverAlertId !== alertId) {
        lastHandoverAlertId = alertId;
        if (P.handoverChannel) P.handoverChannel.postMessage({ type: 'ALERT_SHOWN', id: alertId });

        P.playInAppChime();

        const label = who.name ? who.name + ' asked for a person' : 'A customer asked for a person';
        P.toast(label, {
          type: 'warn',
          timeout: 8000,
          action: {
            label: 'Open Chat',
            fn: () => P.go('#/inbox?phone=' + encodeURIComponent(who.phone || ''))
          }
        });
      }
    }
    lastAttention = att;
  };

  async function pollOverview() {
    try { P.applyOverview(await P.get('/overview')); } catch (e) { /* shown by the page itself */ }
  }

  P.act('toggle-menu', () => {
    const sb = P.$('#sidebar');
    const open = !sb.classList.contains('open');
    sb.classList.toggle('open', open);
    P.$$('[data-action="toggle-menu"]').forEach((b) => b.setAttribute('aria-expanded', String(open)));
    if (open) { const first = P.$('.nav-link', sb); if (first) first.focus(); }
  });
  // Tapping the darkened page next to the open mobile menu closes it.
  document.addEventListener('click', (e) => {
    const sb = P.$('#sidebar');
    if (!sb.classList.contains('open') || sb.contains(e.target) || e.target.closest('[data-action="toggle-menu"]')) return;
    sb.classList.remove('open');
    P.$$('[data-action="toggle-menu"]').forEach((b) => b.setAttribute('aria-expanded', 'false'));
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !P.$('#sidebar').classList.contains('open')) return;
    P.$('#sidebar').classList.remove('open');
    P.$$('[data-action="toggle-menu"]').forEach((b) => b.setAttribute('aria-expanded', 'false'));
    const btn = P.$('[data-action="toggle-menu"]');
    if (btn) btn.focus();
  });

  P.act('logout', async () => {
    const ok = await P.confirm({
      title: 'Sign out?',
      message: 'You will be signed out on this device. WhatsApp, contacts and chats are not affected.',
      confirmLabel: 'Sign out',
    });
    if (!ok) return;
    try { await P.post('/logout'); } catch (e) { /* already signed out */ }
    try {
      localStorage.clear();
      sessionStorage.clear();
    } catch (e) { /* storage unavailable */ }
    P.mount(P.$('#view'), '');
    window.location.replace('/login');
  });

  window.addEventListener('pageshow', (e) => {
    if (e.persisted) {
      window.location.reload();
    }
  });

  document.addEventListener('DOMContentLoaded', async () => {
    try {
      const me = await P.get('/me');
      P.session = { csrf: me.csrf, role: me.role, product: me.product, version: me.version };
    } catch (e) {
      P.mount(P.$('#view'), html`<div class="notice bad"><p>${e.message}</p></div>`);
      return;
    }
    if (!P.isAdmin()) P.$$('[data-admin]').forEach((x) => { x.hidden = true; });
    P.$('#whoami').textContent = P.isAdmin() ? 'Signed in as admin' : 'Signed in as staff';
    window.addEventListener('hashchange', P.route);
    if (!window.location.hash) history.replaceState(null, '', '#/' + P.store.get('lastView', 'home'));
    await P.route();
    pollOverview();
    setInterval(() => { pollOverview(); }, 10000);
  });
})();
