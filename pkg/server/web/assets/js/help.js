'use strict';
(function () {
  const { html, icon } = P;

  P.view('help', {
    title: 'Help and safety',
    async render(el) {
      await P.loadVars().catch(() => null);
      const vars = (P.vars && P.vars.variables) || [];
      P.mount(el, html`<div class="stack">
        <section class="card"><div class="card-head"><h2>Using the official WhatsApp API</h2></div><div class="card-body stack-sm">
          <p>Pulse connects through Meta's official WhatsApp Business Platform (Cloud API). This is the way WhatsApp's terms allow businesses to automate messages. You use your own Meta account and number; you need to set it up once:</p>
          <div class="notice warn"><p><strong>Phone Number Requirement:</strong> A number connected to Meta's Cloud API cannot be used in the WhatsApp or WhatsApp Business mobile app at the same time. Use a dedicated SIM, virtual number or landline, or manage all chats here in the Pulse inbox.</p></div>
          <ol class="steps">
            <li>At <strong>developers.facebook.com</strong>, create an app of type <strong>Business</strong> and add the <strong>WhatsApp</strong> product.</li>
            <li>In WhatsApp &gt; API Setup, add your business phone number and copy the <strong>Phone number ID</strong> and the <strong>WhatsApp Business account ID</strong>.</li>
            <li>In Meta Business settings &gt; System users, create a system user, give it your app and WhatsApp account, and generate a <strong>permanent token</strong> with the permissions <code>whatsapp_business_messaging</code> and <code>whatsapp_business_management</code>.</li>
            <li>In the app's App settings &gt; Basic, copy the <strong>App secret</strong>.</li>
            <li>Make Pulse reachable over https, for example with Cloudflare Tunnel: <code>cloudflared tunnel --url http://127.0.0.1:8085</code>. Paste the https address it shows as <strong>Public address</strong>.</li>
            <li>Paste everything in <strong>Settings &gt; WhatsApp connection</strong>, press Test connection, then Save.</li>
            <li>In Meta's WhatsApp &gt; Configuration, set the <strong>Callback URL</strong> to the webhook address and the <strong>Verify token</strong> shown in Pulse, then subscribe to the <strong>messages</strong> field.</li>
            <li>To message customers who have not written in the last 24 hours, create <strong>message templates</strong> in WhatsApp Manager and wait for Meta to approve them. Use them in Campaigns and Scheduled messages.</li>
          </ol>
          <p class="hint">Meta bills some messages (mostly templates) to your Meta account. Check current prices and your messaging limit in WhatsApp Manager.</p>
        </div></section>

        <section class="card"><div class="card-head"><h2>WhatsApp's messaging rules</h2></div><div class="card-body stack-sm">
          <ul>
            <li><strong>Only message people who agreed.</strong> Record how each person opted in (Contacts &gt; Edit). Campaigns skip everyone else.</li>
            <li><strong>24-hour window.</strong> After a customer writes, you can reply freely for 24 hours. After that, only approved templates go out; Pulse refuses other messages before sending.</li>
            <li><strong>Quality rating.</strong> Meta rates your number by how customers react. Home shows it; when it drops, Pulse pauses campaigns.</li>
            <li><strong>Go slowly.</strong> New numbers have a daily limit set by Meta. Keep the Careful pace while it grows.</li>
            <li><strong>Always offer a person.</strong> Customers can type AGENT at any time; you get an alert and the bot stops in that chat.</li>
          </ul>
        </div></section>

        <section class="card"><div class="card-head"><h2>Try your replies safely</h2></div><div class="card-body stack-sm">
          <p>Open Auto-replies &gt; Test a message and type <strong>hi</strong>, a menu number, <strong>STOP</strong> or anything a customer might write. Pulse shows the reply a customer would get, without sending anything.</p>
          <p class="hint">Practice mode (Settings &gt; Safety) lets Pulse run as normal while sending nothing, so you can watch what it would do.</p>
        </div></section>

        <section class="card"><div class="card-head"><h2>Keywords your customers can use</h2></div><div class="card-body">
          <table class="table"><thead><tr><th>Customer types</th><th>What happens</th></tr></thead><tbody>
            <tr><td>STOP, UNSUBSCRIBE, rok do, band karo, mat bhejo</td><td>Marked do-not-message. No more automatic replies, campaigns or scheduled messages.</td></tr>
            <tr><td>START, SUBSCRIBE, shuru karo</td><td>Opt-in recorded and messages allowed again.</td></tr>
            <tr><td>AGENT, HUMAN, PERSON, "talk to someone", "baat karni hai"</td><td>Chat handed to staff, bot paused, alert in the dashboard (Needs attention).</td></tr>
            <tr><td>MENU, HI, HELLO</td><td>Greeting and numbered menu (starter rules).</td></tr>
            <tr><td>HELP</td><td>Lists these keywords (starter rules).</td></tr>
          </tbody></table>
        </div></section>

        <section class="card"><div class="card-head"><h2>Variables you can use in messages</h2></div><div class="card-body">
          <table class="table"><thead><tr><th>Variable</th><th>Meaning</th><th>Example</th></tr></thead><tbody>
            ${vars.map((v) => html`<tr><td><code>{{${v.key}}}</code></td><td>${v.label}</td><td>${v.example}</td></tr>`)}
            <tr><td><code>{{email}}</code>, <code>{{notes}}</code></td><td>From the contact (templates, campaigns, scheduled)</td><td>asha@example.com</td></tr>
          </tbody></table>
          <p class="hint">If a variable has no value, Pulse does not send the message and tells you which detail is missing, so customers never get sentences with gaps.</p>
        </div></section>

        <section class="card"><div class="card-head"><h2>Connecting other systems (for developers)</h2></div><div class="card-body stack-sm">
          <p class="hint">You can skip this section unless a developer is connecting Pulse to your shop or other software.</p>
          <p>Your shop or CRM can send a WhatsApp message by calling <code>POST /api/v1/webhook/send</code> on this computer with the header <code>X-Webhook-Secret</code> (set in Settings &gt; Integrations), or a signature as described there:</p>
          <pre>{"to": "+919876543210", "message": "Hello {{first_name}}, your order {{order_id}} has shipped.", "name": "Asha", "variables": {"order_id": "A-1001"}}</pre>
          <p class="hint">The same rules apply as for staff: opted-out customers are refused, and the customer must have written in the last 24 hours (WhatsApp's rule for free text). For other customers use a scheduled message or campaign with an official template.</p>
        </div></section>

        <section class="card"><div class="card-head"><h2>About</h2></div><div class="card-body">
          <p>${P.session.product} version ${P.session.version}.</p>
          <p class="hint">${P.session.product} is independent software. It is not affiliated with, authorised, maintained, sponsored or endorsed by WhatsApp LLC or Meta Platforms, Inc. WhatsApp is a trademark of WhatsApp LLC.</p>
        </div></section>
      </div>`);
    },
  });
})();
