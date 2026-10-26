'use strict';
(function () {
  const { html, icon } = P;
  let days = P.store.get('reportDays', 30);

  async function draw(el) {
    const d = await P.get('/analytics?days=' + days);
    const s = d.stats;
    const tile = (label, v, note) => html`<div class="card stat"><div class="stat-label">${label}</div><div class="stat-value">${v}</div><div class="stat-note">${note}</div></div>`;
    const max = Math.max(1, ...d.series.map((x) => x.count));
    const range = days ? 'Since ' + P.fmtDate(d.since) : 'All time' + (d.first_message_at ? ' (from ' + P.fmtDate(d.first_message_at) + ')' : '');
    P.mount(el, html`<div class="stack">
      <div class="row-between">
        <div class="segmented" role="group" aria-label="Date range">${[[7, '7 days'], [30, '30 days'], [90, '90 days'], [0, 'All time']].map(([v, l]) => html`<button type="button" data-action="report-range" data-days="${v}" aria-pressed="${String(days === v)}">${l}</button>`)}</div>
        <button class="btn" type="button" data-action="report-csv">${icon('download', 'icon-sm')}${days ? 'Download these messages for Excel' : 'Download all messages for Excel'}</button>
      </div>
      <p class="hint">${range}. Only messages Pulse handled are counted.${d.imported_excluded ? ' ' + P.fmtNum(d.imported_excluded) + ' imported past messages are not included.' : ''}</p>
      <div class="grid-4">
        ${tile('Received', P.fmtNum(s.incoming), P.plural(s.customers, 'customer'))}
        ${tile('Sent', P.fmtNum(s.sent), s.auto_replies + ' automatic, ' + s.broadcasts + ' campaign, ' + s.direct_sends + ' by staff')}
        ${tile('Delivered', P.fmtPct(d.delivery_rate), P.fmtNum(s.delivered) + ' confirmed by WhatsApp')}
        ${tile('Read', P.fmtPct(d.read_rate), P.fmtNum(s.read) + ' read receipts')}
      </div>
      <div class="grid-4">
        ${tile('Failed', P.fmtNum(s.failed), d.failure_rate == null ? 'No sends yet' : P.fmtPct(d.failure_rate) + ' of attempts')}
        ${tile('Opted out', P.fmtNum(d.opt_outs), 'replied STOP or similar')}
        ${tile('Asked for a person', P.fmtNum(d.handovers), 'hand-overs to staff')}
        ${tile('Scheduled sent', P.fmtNum(s.scheduled), s.dry_run ? s.dry_run + ' in practice mode (not sent)' : 'reminders and follow-ups')}
      </div>
      ${d.series.length ? html`<section class="card"><div class="card-head"><h2>Messages per day</h2></div><div class="card-body">
        <div class="bars" role="img" aria-label="Messages per day chart">${d.series.map((x) => html`<span class="bar" data-h="${Math.round((x.count / max) * 100)}" title="${x.key}: ${x.count}"></span>`)}</div>
        <div class="bars-labels"><span>${P.fmtDate(d.series[0].key)}</span><span>${P.fmtDate(d.series[d.series.length - 1].key)}</span></div>
        <table class="sr-only"><caption>Messages per day</caption><tbody>${d.series.map((x) => html`<tr><th>${x.key}</th><td>${x.count}</td></tr>`)}</tbody></table>
      </div></section>` : ''}
      <div class="grid-2">
        <section class="card"><div class="card-head"><h2>Most used auto-replies</h2></div><div class="card-body">
          ${d.top_rules.length ? d.top_rules.map((r) => html`<div class="hbar"><div><div>${P.ruleLabel(r.key)}</div><progress max="${d.top_rules[0].count}" value="${r.count}" aria-label="${P.ruleLabel(r.key)}"></progress></div><strong class="right">${r.count}</strong></div>`) : P.empty('No automatic replies yet', 'They appear here once customers start messaging.')}
        </div></section>
        <section class="card"><div class="card-head"><h2>Recent campaigns</h2><a class="btn small" href="#/campaigns">All campaigns</a></div>
          ${(d.campaigns || []).length ? html`<ul class="list">${d.campaigns.map((c) => html`<li><div class="grow"><a href="#/campaigns?id=${c.id}">${c.name}</a><div class="hint">${P.fmtDate(c.started_at || c.created_at)} - ${c.report.sent} sent, ${c.report.read} read, ${c.report.replied} replied</div></div>${P.statusBadge(c.status)}</li>`)}</ul>` : html`<div class="card-body">${P.empty('No campaigns yet', 'Campaign results appear here.')}</div>`}
        </section>
      </div>
    </div>`);
    P.$$('.bars .bar', el).forEach((b) => b.style.setProperty('--h', Math.max(1, Number(b.dataset.h)) + '%'));
  }

  P.act('report-range', (el) => { days = Number(el.dataset.days); P.store.set('reportDays', days); P.route(); });
  P.act('report-csv', () => P.download('/analytics/export.csv' + (days ? '?days=' + days : '')));

  P.view('reports', { title: 'Reports', admin: true, render: draw });
})();
