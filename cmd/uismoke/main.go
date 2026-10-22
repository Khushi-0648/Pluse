//go:build uismoke

// uismoke builds Pulse, starts it with an empty data folder, and drives the real
// dashboard in headless Edge or Chrome: it signs in, opens every page, clicks every
// non-destructive control, submits the main forms and fails on any JavaScript error.
//
//	go run -tags uismoke ./cmd/uismoke
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const port = 18085

var base = fmt.Sprintf("http://127.0.0.1:%d", port)

type cdp struct {
	ws      *websocket.Conn
	next    atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan json.RawMessage
	errs    []string
	netErrs []string
}

func (c *cdp) read(ctx context.Context) {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(data, &msg)
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if ch != nil {
				if len(msg.Error) > 0 {
					ch <- msg.Error
				} else {
					ch <- msg.Result
				}
			}
			continue
		}
		switch msg.Method {
		case "Page.javascriptDialogOpening":
			var p struct {
				Type, Message string
			}
			_ = json.Unmarshal(msg.Params, &p)
			c.addErr("native " + p.Type + " dialog: " + p.Message)
			go func() { _, _ = c.call("Page.handleJavaScriptDialog", map[string]any{"accept": true}) }()
		case "Runtime.exceptionThrown":
			var p struct {
				ExceptionDetails struct {
					Text      string `json:"text"`
					Exception struct {
						Description string `json:"description"`
					} `json:"exception"`
				} `json:"exceptionDetails"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			c.addErr("exception: " + p.ExceptionDetails.Text + " " + p.ExceptionDetails.Exception.Description)
		case "Runtime.consoleAPICalled":
			var p struct {
				Type string `json:"type"`
				Args []struct {
					Value       any    `json:"value"`
					Description string `json:"description"`
				} `json:"args"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Type == "error" {
				var parts []string
				for _, a := range p.Args {
					parts = append(parts, fmt.Sprint(a.Value, a.Description))
				}
				c.addErr("console.error: " + strings.Join(parts, " "))
			}
		case "Log.entryAdded":
			var p struct {
				Entry struct {
					Level, Text, URL string
				} `json:"entry"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Entry.Level == "error" {
				line := p.Entry.Text + " " + p.Entry.URL
				if strings.Contains(strings.ToLower(p.Entry.Text), "content security policy") {
					c.addErr("CSP violation: " + line)
				} else {
					c.mu.Lock()
					c.netErrs = append(c.netErrs, line)
					c.mu.Unlock()
				}
			}
		}
	}
}

func (c *cdp) addErr(s string) {
	c.mu.Lock()
	c.errs = append(c.errs, s)
	c.mu.Unlock()
	fmt.Println("   !! " + s)
}

func (c *cdp) call(method string, params any) (json.RawMessage, error) {
	id := c.next.Add(1)
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%s timed out", method)
	}
}

func (c *cdp) eval(js string) (any, error) {
	r, err := c.call("Runtime.evaluate", map[string]any{"expression": js, "awaitPromise": true, "returnByValue": true})
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(r, &out)
	if out.ExceptionDetails != nil {
		return nil, errors.New(out.ExceptionDetails.Text)
	}
	return out.Result.Value, nil
}

func (c *cdp) must(js string) any {
	v, err := c.eval(js)
	if err != nil {
		fail("script failed: %v\n%s", err, js)
	}
	return v
}

func (c *cdp) navigate(url string) {
	if _, err := c.call("Page.navigate", map[string]any{"url": url}); err != nil {
		fail("navigate: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	c.waitFor(`document.readyState === 'complete'`, 10*time.Second)
}

func (c *cdp) waitFor(cond string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if v, err := c.eval("!!(" + cond + ")"); err == nil && v == true {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fail("timed out waiting for: %s", cond)
}

func shot(c *cdp, path string) {
	r, err := c.call("Page.captureScreenshot", map[string]any{"format": "png"})
	if err != nil {
		fail("screenshot: %v", err)
	}
	var out struct {
		Data []byte `json:"data"`
	}
	_ = json.Unmarshal(r, &out)
	_ = os.WriteFile(path, out.Data, 0644)
}

func fail(f string, a ...any) {
	fmt.Printf("FAIL: "+f+"\n", a...)
	cleanup()
	os.Exit(1)
}

var cleanups []func()

func cleanup() {
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}

func browser() string {
	for _, p := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func main() {
	tmp, err := os.MkdirTemp("", "pulse-uismoke-")
	if err != nil {
		fail("%v", err)
	}
	cleanups = append(cleanups, func() { _ = os.RemoveAll(tmp) })
	exe := filepath.Join(tmp, "pulse-test.exe")
	fmt.Println("== building")
	if out, err := exec.Command("go", "build", "-o", exe, "./cmd/whatsapp-auto").CombinedOutput(); err != nil {
		fail("build: %v\n%s", err, out)
	}

	// A stand-in for Meta's Graph API on this computer: the test never reaches Meta.
	var graphMu sync.Mutex
	var graphCalls []string
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		graphMu.Lock()
		graphCalls = append(graphCalls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(body))
		graphMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"display_phone_number":"+91 98000 00001","verified_name":"Verma Dental","quality_rating":"GREEN"}`))
		case strings.Contains(string(body), `"status"`):
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.SMOKE` + fmt.Sprint(time.Now().UnixNano()) + `"}]}`))
		}
	}))
	cleanups = append(cleanups, graph.Close)

	fmt.Println("== starting the app with an empty data folder")
	logf, _ := os.Create(filepath.Join(tmp, "app.log"))
	app := exec.Command(exe, "serve", "-config", filepath.Join(tmp, "config.yaml"), "-port", fmt.Sprint(port))
	app.Env = append(os.Environ(), "PULSE_DATA_DIR="+filepath.Join(tmp, "data"), "PULSE_GRAPH_BASE_URL="+graph.URL)
	app.Dir = tmp // never run the test copy where real data files live
	app.Stdout, app.Stderr = logf, logf
	if err := app.Start(); err != nil {
		fail("%v", err)
	}
	cleanups = append(cleanups, func() { _ = app.Process.Kill(); _ = app.Wait(); logf.Close() })
	deadline := time.Now().Add(20 * time.Second)
	for {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			fail("app did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}
	noticeData, err := os.ReadFile(filepath.Join(tmp, "data", "FIRST-RUN-PASSWORD.txt"))
	if err != nil {
		fail("first-run password file missing: %v", err)
	}
	m := regexp.MustCompile(`password: (\S+)`).FindSubmatch(noticeData)
	if m == nil {
		fail("cannot read the one-time password")
	}
	oneTime := string(m[1])
	if b, _ := os.ReadFile(filepath.Join(tmp, "app.log")); strings.Contains(string(b), oneTime) {
		fail("the one-time password was written to the log (S2)")
	}

	exeB := browser()
	if exeB == "" {
		fail("Edge or Chrome is required")
	}
	profile := filepath.Join(tmp, "browser")
	br := exec.Command(exeB, "--headless=new", "--remote-debugging-port=19333", "--user-data-dir="+profile, "--no-first-run", "--disable-gpu", "--window-size=1280,900", "about:blank")
	if err := br.Start(); err != nil {
		fail("%v", err)
	}
	cleanups = append(cleanups, func() { _ = br.Process.Kill(); _ = br.Wait(); time.Sleep(500 * time.Millisecond) })

	var wsURL string
	for i := 0; i < 50 && wsURL == ""; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, err := http.Get("http://127.0.0.1:19333/json/list")
		if err != nil {
			continue
		}
		var targets []struct{ Type, WebSocketDebuggerUrl string }
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_ = json.Unmarshal(b, &targets)
		for _, t := range targets {
			if t.Type == "page" {
				wsURL = t.WebSocketDebuggerUrl
				break
			}
		}
	}
	if wsURL == "" {
		fail("browser debugging endpoint not found")
	}
	ctx := context.Background()
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		fail("%v", err)
	}
	ws.SetReadLimit(64 << 20)
	c := &cdp{ws: ws, pending: map[int64]chan json.RawMessage{}}
	go c.read(ctx)
	for _, m := range []string{"Runtime.enable", "Page.enable", "Log.enable"} {
		if _, err := c.call(m, map[string]any{}); err != nil {
			fail("%v", err)
		}
	}

	fmt.Println("== sign in with the one-time password and choose a password")
	c.navigate(base + "/login")
	c.must(fmt.Sprintf(`document.getElementById('password').value = %q; document.querySelector('form').submit(); true`, oneTime))
	c.waitFor(`location.pathname === '/setup-password' && document.readyState === 'complete'`, 10*time.Second)
	c.must(`document.getElementById('new_password').value='admin12345'; document.getElementById('confirm_password').value='admin12345'; document.querySelector('button[type=submit]').click(); true`)
	c.waitFor(`!document.getElementById('client-error').hidden`, 5*time.Second)
	c.must(`document.getElementById('new_password').value='river7Lamp'; document.getElementById('confirm_password').value='river7Lamp'; document.querySelector('form').submit(); true`)
	c.waitFor(`location.pathname === '/' && window.P && P.session && P.session.csrf`, 15*time.Second)
	if _, err := os.Stat(filepath.Join(tmp, "data", "FIRST-RUN-PASSWORD.txt")); err == nil {
		fail("the one-time password file should be deleted after the password is chosen")
	}

	const helpers = `
	window.__sleep = (ms) => new Promise((r) => setTimeout(r, ms));
	window.__closeModals = async () => { for (let i = 0; i < 5; i++) { const x = document.querySelector('.modal [data-modal-close]'); if (!x) break; x.click(); await __sleep(80); } };
	window.__toast = () => [...document.querySelectorAll('.toast')].map((t) => t.textContent.trim()).join(' | ');
	window.__waitToast = async (re) => { for (let i = 0; i < 60; i++) { if (new RegExp(re, 'i').test(__toast())) return __toast(); await __sleep(100); } throw new Error('toast not shown: ' + re + ' / ' + __toast()); };
	window.__wait = async (sel) => { for (let i = 0; i < 100; i++) { if (document.querySelector(sel)) return; await __sleep(50); } throw new Error('never appeared: ' + sel); };
	window.__open = async (hash, sel) => { await __closeModals(); location.hash = hash; await __sleep(300); await __wait(sel); await __sleep(200); };
	window.__set = (sel, v) => { const e = document.querySelector(sel); if (!e) throw new Error('missing ' + sel); e.value = v; e.dispatchEvent(new Event('input', { bubbles: true })); e.dispatchEvent(new Event('change', { bubbles: true })); };
	true`
	c.must(helpers)

	views := []string{"home", "inbox", "contacts", "replies", "templates", "campaigns", "scheduled", "reports", "settings", "help"}
	skip := `new Set(['logout','conn-disconnect','conn-regen','delete-old-session','delete-old-copies','delete-everything','delete-imported','contact-forget','bulk-delete','rule-reset','cmp-cancel','api-key','chat-clear','backup','export-all','contacts-export','report-csv','audit-csv','cmp-csv','contact-delete','rule-delete','tpl-delete','job-delete','cmp-delete','desktop-shortcut','send-test','automation-on','ack-risk','chat-attach','toggle-menu','webhook-replay-all','webhook-clear','set-pace','browser-alerts','gen-secret','insert-var','reload-view','savebar-save','savebar-discard'])`
	clickAll := `(async (extra) => {
		const skip = ` + skip + `;
		(extra || []).forEach((x) => skip.add(x));
		const seen = new Set(); const clicked = [];
		for (let round = 0; round < 60; round++) {
			const b = [...document.querySelectorAll('#view [data-action]')].find((x) => !skip.has(x.dataset.action) && !x.disabled && x.offsetParent !== null && !seen.has(x.dataset.action + '|' + (x.dataset.tab || x.dataset.filter || x.dataset.days || '')));
			if (!b) break;
			seen.add(b.dataset.action + '|' + (b.dataset.tab || b.dataset.filter || b.dataset.days || ''));
			b.click(); clicked.push(b.dataset.action);
			await __sleep(350);
			await __closeModals();
			if (P.dirty.active()) P.dirty.clear();
		}
		return clicked.join(',');
	})`
	for _, v := range views {
		fmt.Printf("== page %s\n", v)
		c.must(fmt.Sprintf(`location.hash = '#/%s'; true`, v))
		c.waitFor(`document.getElementById('view').getAttribute('aria-busy') !== 'true' && !document.querySelector('#view .skeleton')`, 10*time.Second)
		c.must(`__closeModals()`)
		if bad, _ := c.eval(`document.querySelector('#view .notice.bad strong') ? document.querySelector('#view .notice.bad').textContent : ''`); bad != "" && bad != nil && strings.Contains(fmt.Sprint(bad), "could not be loaded") {
			fail("page %s failed to load: %v", v, bad)
		}
		got := c.must(clickAll + `([])`)
		fmt.Printf("   clicked: %v\n", got)
	}

	fmt.Println("== settings tabs")
	for _, tab := range []string{"whatsapp", "business", "hours", "safety", "integrations", "privacy", "account"} {
		c.must(fmt.Sprintf(`location.hash = '#/settings?tab=%s'; true`, tab))
		c.waitFor(`document.querySelector('#settings-body') && document.querySelector('#settings-body').children.length > 0`, 10*time.Second)
		c.must(`__sleep(300)`)
		got := c.must(clickAll + `(['settings-tab'])`)
		fmt.Printf("   %s clicked: %v\n", tab, got)
	}

	fmt.Println("== flows")
	flows := []struct{ name, js string }{
		{"save business details", `(async () => { await __open('#/settings?tab=business', '#f-business');
			__set('#b-name', 'Verma Dental'); __set('#b-hours', 'Mon-Sat 9:00-18:00'); __set('#b-services', 'Cleaning and fillings');
			document.querySelector('#f-business button[type=submit]').click(); return __waitToast('Business details saved'); })()`},
		{"choose balanced pace", `(async () => { await __open('#/settings?tab=safety', '#f-safety');
			document.querySelector('[data-action=set-pace][data-pace=balanced]').click(); await __waitToast('Balanced pace selected');
			const s = await P.get('/settings'); if (s.automation.pace !== 'balanced') throw new Error('pace not saved'); return 'ok'; })()`},
		{"unsaved changes bar", `(async () => { await __open('#/settings?tab=business', '#f-business'); __set('#b-address', '12 MG Road');
			await __sleep(100); if (!document.querySelector('.savebar')) throw new Error('save bar missing'); document.querySelector('[data-action=savebar-save]').click();
			await __waitToast('saved'); const s = await P.get('/settings'); if (s.business.address !== '12 MG Road') throw new Error('not saved'); return 'ok'; })()`},
		{"add contact with consent", `(async () => { await __open('#/contacts', '[data-action=contact-add]'); document.querySelector('[data-action=contact-add]').click(); await __wait('.modal #contact-form');
			__set('#cf-name', 'Asha Verma'); __set('#cf-phone', '+91 98765 43210'); document.querySelector('#contact-form [name=consent]').click(); __set('#cf-source', 'Signed up in the clinic');
			document.querySelector('.modal button[type=submit]').click(); await __waitToast('Contact saved');
			const r = await P.get('/contacts?search=Asha'); if (r.total !== 1 || !r.contacts[0].consent) throw new Error('contact not saved: ' + JSON.stringify(r)); return 'ok'; })()`},
		{"new auto-reply", `(async () => { await __open('#/replies', '#rules-box .rule'); document.querySelector('[data-action=rule-new]').click(); await __wait('.modal #rule-form');
			__set('#rf-name', 'Delivery'); document.querySelector('#rule-form [name=pattern]').value = 'delivery, shipping'; __set('#rf-reply', 'We deliver within 2 days, {{first_name}}.');
			document.querySelector('.modal button[type=submit]').click(); await __waitToast('Rule saved');
			const r = await P.get('/rules'); if (!r.rules.some((x) => x.name === 'Delivery')) throw new Error('rule missing'); return 'ok'; })()`},
		{"switch a rule off", `(async () => { await __open('#/replies', '[data-change=rule-toggle][data-name=Delivery]'); const t = document.querySelector('[data-change=rule-toggle][data-name=Delivery]'); t.click(); await __waitToast('is off');
			const r = await P.get('/rules'); if (r.rules.find((x) => x.name === 'Delivery').enabled) throw new Error('rule still on'); return 'ok'; })()`},
		{"test a message", `(async () => { await __open('#/replies', '#rt-msg'); __set('#rt-msg', 'hi'); document.querySelector('[data-form=rule-test] button[type=submit]').click(); await __sleep(600);
			const t = document.getElementById('rule-test-result').textContent; if (!/greeting_menu/.test(t) || !/Verma Dental/.test(t)) throw new Error('unexpected: ' + t); return 'ok'; })()`},
		{"new template", `(async () => { await __open('#/templates', '[data-action=tpl-new]'); document.querySelector('[data-action=tpl-new]').click(); await __wait('.modal #tpl-form');
			__set('#tf-name', 'Thanks'); __set('#tf-content', 'Thank you {{first_name}} for visiting {{business_name}}.');
			document.querySelector('.modal button[type=submit]').click(); await __waitToast('Template saved'); return 'ok'; })()`},
		{"template with unknown variable is refused", `(async () => { await __open('#/templates', '[data-action=tpl-new]'); document.querySelector('[data-action=tpl-new]').click(); await __wait('.modal #tpl-form');
			__set('#tf-name', 'Bad'); __set('#tf-content', 'Code {{coupon}}'); await __sleep(600);
			if (!/coupon/.test(document.querySelector('.preview-note').textContent)) throw new Error('preview did not warn');
			document.querySelector('.modal button[type=submit]').click(); await __waitToast('Unknown variable'); await __closeModals(); return 'ok'; })()`},
		{"schedule a message with an official template", `(async () => { await __open('#/scheduled', '#job-to'); __set('#job-to', '+919876543210'); __set('#job-msg', 'Reminder for {{first_name}}');
			__set('#job-tname', 'appointment_reminder'); __set('#job-tlang', 'en'); __set('#job-tparams', '{{first_name}}');
			document.querySelector('[data-form=job-add] button[type=submit]').click(); await __waitToast('scheduled for');
			const j = await P.get('/scheduled'); if (j.length !== 1 || !j[0].template || j[0].template.name !== 'appointment_reminder') throw new Error('job missing: ' + JSON.stringify(j)); return 'ok'; })()`},
		{"campaign outside the 24-hour window needs a template", `(async () => { await __open('#/campaigns', '#cmp-msg'); __set('#cmp-msg', 'Hello {{first_name}}, news from {{business_name}}.');
			document.querySelector('[data-action=cmp-preview]').click(); await __sleep(800);
			let t = document.getElementById('cmp-plan').textContent; if (!/1 person will receive/.test(t) || !/not written to you/.test(t)) throw new Error('plan: ' + t);
			if (!document.getElementById('cmp-go').disabled) throw new Error('start must wait for a template');
			__set('#cmp-tname', 'monthly_news'); __set('#cmp-tparams', '{{first_name}}');
			document.querySelector('[data-action=cmp-preview]').click(); await __sleep(800);
			t = document.getElementById('cmp-plan').textContent; if (!/official WhatsApp template/.test(t)) throw new Error('plan: ' + t);
			if (document.getElementById('cmp-go').disabled) throw new Error('start should be enabled with a template'); return 'ok'; })()`},
		{"free text outside the 24-hour window is refused", `(async () => { await __open('#/inbox?phone=919876543210', '#chat-text'); __set('#chat-text', 'hello');
			document.querySelector('[data-form=chat-send] button[type=submit]').click(); await __waitToast('24 hours'); return 'ok'; })()`},
		{"home asks to connect the Business API", `(async () => { await __open('#/home', '#connect-title'); if (!/Connect your WhatsApp Business API/.test(document.getElementById('connect-title').textContent)) throw new Error('no connect card'); return 'ok'; })()`},
		{"save the WhatsApp connection", `(async () => { await __open('#/settings?tab=whatsapp', '#f-connection');
			__set('#c-pnid', '1234567890'); __set('#c-waba', '9876543210'); __set('#c-token', 'EAAG-smoke-token'); __set('#c-secret', 'uismoke-app-secret'); __set('#c-public', 'https://pulse-smoke.trycloudflare.com');
			document.querySelector('#f-connection button[type=submit]').click(); await __wait('.modal #confirm-pw');
			document.querySelector('.modal #confirm-pw').value = 'river7Lamp'; document.querySelector('.modal [data-confirm-ok]').click();
			await __waitToast('Connected to');
			const c = await P.get('/connection'); if (!c.status.connected || c.webhook_url !== 'https://pulse-smoke.trycloudflare.com/cloud/webhook' || !c.verify_token) throw new Error(JSON.stringify(c));
			if (JSON.stringify(c).includes('smoke-token') || JSON.stringify(await P.get('/settings')).includes('smoke-token')) throw new Error('token sent to the browser');
			await __wait('#c-hook'); if (document.getElementById('c-hook').value !== c.webhook_url) throw new Error('webhook address not shown');
			return 'ok'; })()`},
		{"test connection", `(async () => { await __open('#/settings?tab=whatsapp', '#f-connection'); document.querySelector('[data-action=conn-test]').click(); await __waitToast('Connected to'); return 'ok'; })()`},
		{"automation cannot start before setup", `(async () => { try { await P.patch('/settings', { automation: { enabled: true } }); } catch (e) { if (/Finish setup/.test(e.message)) return 'ok'; throw e; } throw new Error('automation switched on too early'); })()`},
		{"reports", `(async () => { await __open('#/reports', '.stat-value'); if (!document.querySelector('.stat-value')) throw new Error('no stats'); return 'ok'; })()`},
	}
	for _, f := range flows {
		c.must(`document.querySelectorAll('.toast').forEach((t) => t.remove()); true`)
		if _, err := c.eval(f.js); err != nil {
			fail("flow %q: %v (toasts: %v)", f.name, err, c.must(`__toast()`))
		}
		fmt.Printf("   ok: %s\n", f.name)
		c.must(`__closeModals()`)
	}

	fmt.Println("== incoming message through the webhook (as Meta sends it through a tunnel)")
	verify := fmt.Sprint(c.must(`P.get('/connection').then((x) => x.verify_token)`))
	hookURL := base + "/cloud/webhook"
	req, _ := http.NewRequest(http.MethodGet, hookURL+"?hub.mode=subscribe&hub.verify_token="+verify+"&hub.challenge=smoke42", nil)
	req.Host = "pulse-smoke.trycloudflare.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail("%v", err)
	}
	if b, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || string(b) != "smoke42" {
		fail("webhook verification: %d %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	inbound := fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"9876543210","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"1234567890"},"contacts":[{"profile":{"name":"Asha Verma"},"wa_id":"919876543210"}],"messages":[{"from":"919876543210","id":"wamid.IN1","timestamp":"%d","type":"text","text":{"body":"hi"}}]}}]}]}`, time.Now().Unix())
	post := func(sig string) int {
		req, _ := http.NewRequest(http.MethodPost, hookURL, strings.NewReader(inbound))
		req.Host = "pulse-smoke.trycloudflare.com"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "157.240.1.1")
		if sig != "" {
			req.Header.Set("X-Hub-Signature-256", sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fail("%v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("sha256=00"); code != http.StatusUnauthorized {
		fail("an unsigned webhook was accepted: %d", code)
	}
	mac := hmac.New(sha256.New, []byte("uismoke-app-secret"))
	mac.Write([]byte(inbound))
	if code := post("sha256=" + hex.EncodeToString(mac.Sum(nil))); code != 200 {
		fail("signed webhook refused: %d", code)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/login", nil)
	req.Host = "pulse-smoke.trycloudflare.com"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusNotFound {
		fail("the dashboard must not be reachable through the tunnel: %v %v", err, resp)
	}
	more := []struct{ name, js string }{
		{"incoming message is in the inbox and can be answered", `(async () => { for (let i = 0; i < 50; i++) { const m = await P.get('/messages?phone=919876543210'); const list = m.messages || m; if (JSON.stringify(list).includes('wamid.IN1')) break; await __sleep(100); if (i === 49) throw new Error('incoming message not stored'); }
			await __open('#/inbox?phone=919876543210', '#chat-text'); __set('#chat-text', 'Thank you for writing');
			document.querySelector('[data-form=chat-send] button[type=submit]').click(); await __sleep(1200);
			const m = await P.get('/messages?phone=919876543210'); if (!JSON.stringify(m).includes('Thank you for writing')) throw new Error('reply not recorded: ' + JSON.stringify(m)); return 'ok'; })()`},
		{"home shows the connected number", `(async () => { await __open('#/home', '.stat-value'); await __sleep(500); if (!/Connected/.test(document.getElementById('conn-pill').textContent)) throw new Error(document.getElementById('conn-pill').textContent); return 'ok'; })()`},
	}
	for _, f := range more {
		c.must(`document.querySelectorAll('.toast').forEach((t) => t.remove()); true`)
		if _, err := c.eval(f.js); err != nil {
			fail("flow %q: %v (toasts: %v)", f.name, err, c.must(`__toast()`))
		}
		fmt.Printf("   ok: %s\n", f.name)
		c.must(`__closeModals()`)
	}
	graphMu.Lock()
	sent := strings.Join(graphCalls, "\n")
	graphMu.Unlock()
	if !strings.Contains(sent, "Bearer EAAG-smoke-token") || !strings.Contains(sent, "Thank you for writing") {
		fail("the reply did not go to the Graph API stand-in:\n%s", sent)
	}
	if b, _ := os.ReadFile(filepath.Join(tmp, "config.yaml")); strings.Contains(string(b), "smoke-token") || strings.Contains(string(b), "uismoke-app-secret") {
		fail("the access token or app secret is readable in config.yaml")
	}

	if dir := os.Getenv("UISMOKE_SHOTS"); dir != "" {
		fmt.Println("== screenshots in " + dir)
		_ = os.MkdirAll(dir, 0755)
		for _, v := range []string{"home", "inbox?phone=919876543210", "contacts", "replies", "campaigns", "settings?tab=safety", "reports"} {
			c.must(fmt.Sprintf(`location.hash = '#/%s'; true`, v))
			c.must(`__sleep(1200)`)
			shot(c, filepath.Join(dir, strings.NewReplacer("?", "-", "=", "-").Replace(v)+".png"))
		}
	}

	fmt.Println("== phone width")
	if _, err := c.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 375, "height": 800, "deviceScaleFactor": 2, "mobile": true}); err != nil {
		fail("%v", err)
	}
	for _, v := range []string{"home", "inbox", "contacts", "settings"} {
		c.must(fmt.Sprintf(`location.hash = '#/%s'; true`, v))
		c.must(`__sleep(900)`)
		if w := c.must(`document.documentElement.scrollWidth`); fmt.Sprint(w) != "375" {
			fail("page %s scrolls sideways at 375px (width %v)", v, w)
		}
		if dir := os.Getenv("UISMOKE_SHOTS"); dir != "" {
			shot(c, filepath.Join(dir, "phone-"+v+".png"))
		}
	}

	if b, _ := os.ReadFile(filepath.Join(tmp, "data", "contacts.json")); strings.Contains(string(b), "Asha") {
		fail("contacts are stored in plaintext (S9)")
	}
	c.mu.Lock()
	errs := append([]string(nil), c.errs...)
	netErrs := append([]string(nil), c.netErrs...)
	c.mu.Unlock()
	fmt.Printf("== %d expected HTTP errors from intentionally refused requests\n", len(netErrs))
	cleanup()
	if len(errs) > 0 {
		fmt.Printf("FAIL: %d JavaScript errors\n", len(errs))
		for _, e := range errs {
			fmt.Println(" - " + e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS: every page loaded, every control responded, all flows worked, no JavaScript errors.")
}
