'use strict';
(function () {
  const { html } = P;
  let latestSince = null; // time of the newest hand-over request this tab has seen
  let lastHandoverAlertId = null;

  let updateShown = false;
  P.applyOverview = (o) => {
    // Pulse was updated while this tab stayed open: the old page code may not match the
    // new server, so offer a reload (kept on screen until used).
    if (o.version && P.session.version && o.version !== P.session.version && !updateShown) {
      updateShown = true;
      P.toast('Pulse was updated to version ' + o.version + '. Reload to use the new version.', {
        timeout: 86400000,
        action: { label: 'Reload', fn: () => window.location.reload() },
      });
    }
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

    // Alert on a request newer than any seen before, not on a higher count: the same
    // customer asking again, or one request resolved while another arrives, still alerts.
    const who = o.latest_attention || {};
    const since = who.since ? Date.parse(who.since) || 0 : 0;
    const isNew = latestSince !== null && since > latestSince;
    if (latestSince === null || since > latestSince) latestSince = since;
    if (isNew) {
      const alertId = (who.phone || 'customer') + ':' + who.since;
      if (lastHandoverAlertId !== alertId) {
        lastHandoverAlertId = alertId;
        if (P.handoverChannel) P.handoverChannel.postMessage({ type: 'ALERT_SHOWN', id: alertId });

        P.playInAppChime();

        // Not every hand-over is a request: a voice note Pulse cannot read lands here too.
        const label = (who.name || 'A customer') + ' needs a person' + (who.reason ? ': ' + who.reason : '');
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
  };

  async function pollOverview() {
    // A hidden tab does not poll: background requests would keep a phone session alive
    // past its idle limit. The next visible poll catches up.
    if (document.hidden) return;
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
    P.clearLocal();
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
      P.session = { csrf: me.csrf, role: me.role, product: me.product, version: me.version, local: me.local !== false, remoteAccess: !!me.remote_access };
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
