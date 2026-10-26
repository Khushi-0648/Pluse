package cloudapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/securestore"
)

type graph struct {
	mu       sync.Mutex
	requests []map[string]any
	paths    []string
	auth     []string
	handle   func(w http.ResponseWriter, r *http.Request, body []byte) bool
}

func newGraph(t *testing.T) (*graph, *Client, *config.Config) {
	t.Helper()
	g := &graph{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.paths = append(g.paths, r.Method+" "+r.URL.Path)
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		g.requests = append(g.requests, m)
		h := g.handle
		g.mu.Unlock()
		if h != nil && h(w, r, body) {
			return
		}
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"display_phone_number":"+91 98000 00001","verified_name":"Asha Stores","quality_rating":"GREEN","id":"111"}`))
		case strings.HasSuffix(r.URL.Path, "/media"):
			_, _ = w.Write([]byte(`{"id":"media-1"}`))
		case strings.HasSuffix(r.URL.Path, "/subscribed_apps"), m["status"] != nil:
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.OUT1"}]}`))
		}
	}))
	t.Cleanup(ts.Close)
	cfg := config.DefaultConfig()
	tok, err := securestore.SealString("TOKEN-123")
	if err != nil {
		t.Fatal(err)
	}
	sec, _ := securestore.SealString("app-secret")
	cfg.CloudAPI.PhoneNumberID, cfg.CloudAPI.AccessToken, cfg.CloudAPI.AppSecret = "111", tok, sec
	c := New(cfg)
	c.BaseURL = ts.URL
	c.retryWait = []time.Duration{time.Millisecond, time.Millisecond}
	return g, c, cfg
}

func TestCheckSubscribesAppToAccountOnce(t *testing.T) {
	g, c, cfg := newGraph(t)
	ctx := context.Background()
	if st, _ := c.Check(ctx); !st.Connected || !strings.Contains(st.Warning, "WhatsApp Business account ID") {
		t.Fatalf("without the account ID the owner must be told why messages may not arrive: %+v", st)
	}
	cfg.CloudAPI.BusinessAccountID = "222"
	for i := 0; i < 2; i++ {
		if st, _ := c.Check(ctx); !st.Connected || st.Warning != "" {
			t.Fatalf("check %d: %+v", i, st)
		}
	}
	n := 0
	for _, p := range g.paths {
		if p == "POST /v23.0/222/subscribed_apps" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the app must be subscribed to the account exactly once, got %d in %v", n, g.paths)
	}
	g.mu.Lock()
	g.handle = func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		if strings.HasSuffix(r.URL.Path, "/subscribed_apps") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":200,"message":"Permissions error"}}`))
			return true
		}
		return false
	}
	g.mu.Unlock()
	c.Forget()
	if st, _ := c.Check(ctx); !st.Connected || !strings.Contains(st.Warning, "may not arrive") {
		t.Fatalf("a failed subscription must warn without blocking sends: %+v", st)
	}
}

func TestLargePhotoGoesAsDocument(t *testing.T) {
	g, c, _ := newGraph(t)
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxPhotoBytes)...)
	if _, err := c.SendMediaMessage(context.Background(), "919800000002", big, "shop.png", "image/png", "New stock"); err != nil {
		t.Fatal(err)
	}
	if m := g.last(); m["type"] != "document" {
		t.Fatalf("a photo over 5 MB must go as a document: %v", m)
	}
}

func (g *graph) last() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests[len(g.requests)-1]
}

func TestSendTextReplyTemplateAndRead(t *testing.T) {
	g, c, _ := newGraph(t)
	ctx := context.Background()
	id, err := c.SendTextMessage(ctx, "+91 98000-00002", "Hello")
	if err != nil || id != "wamid.OUT1" {
		t.Fatalf("send = %q, %v", id, err)
	}
	m := g.last()
	if m["to"] != "919800000002" || m["type"] != "text" || m["messaging_product"] != "whatsapp" || m["recipient_type"] != "individual" {
		t.Fatalf("bad text payload: %v", m)
	}
	if text := m["text"].(map[string]any); text["body"] != "Hello" || text["preview_url"] != false {
		t.Fatalf("bad text body: %v", text)
	}
	if g.paths[0] != "POST /v23.0/111/messages" || g.auth[0] != "Bearer TOKEN-123" {
		t.Fatalf("path %q auth %q", g.paths[0], g.auth[0])
	}
	if _, err := c.SendReply(ctx, "919800000002", "Yes", "wamid.IN1"); err != nil {
		t.Fatal(err)
	}
	if ctxm := g.last()["context"].(map[string]any); ctxm["message_id"] != "wamid.IN1" {
		t.Fatalf("reply context missing: %v", g.last())
	}
	if _, err := c.SendTemplate(ctx, "919800000002", "order_ready", "en", TemplateParts{Body: []string{"Asha", "42"}}); err != nil {
		t.Fatal(err)
	}
	tpl := g.last()["template"].(map[string]any)
	b, _ := json.Marshal(tpl)
	if !strings.Contains(string(b), `"name":"order_ready"`) || !strings.Contains(string(b), `"code":"en"`) || !strings.Contains(string(b), `{"text":"Asha","type":"text"},{"text":"42","type":"text"}`) {
		t.Fatalf("bad template payload: %s", b)
	}
	if err := c.MarkAsRead(ctx, "wamid.IN1"); err != nil {
		t.Fatal(err)
	}
	if m := g.last(); m["status"] != "read" || m["message_id"] != "wamid.IN1" {
		t.Fatalf("bad read payload: %v", m)
	}
	// Typing needs a known incoming message; without one it does nothing.
	n := len(g.requests)
	if err := c.ShowTyping(ctx, "919800000009"); err != nil || len(g.requests) != n {
		t.Fatal("typing without a known message must be a no-op")
	}
	if _, err := c.SendTextMessage(ctx, "12", "x"); err == nil {
		t.Fatal("a too-short number must be refused before calling Meta")
	}
}

func TestMediaIsUploadedThenSent(t *testing.T) {
	g, c, _ := newGraph(t)
	var uploaded string
	g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		if strings.HasSuffix(r.URL.Path, "/media") {
			r.Body = io.NopCloser(bytes.NewReader(body))
			if err := r.ParseMultipartForm(1 << 20); err == nil {
				f, h, _ := r.FormFile("file")
				data, _ := io.ReadAll(f)
				uploaded = r.FormValue("messaging_product") + "|" + r.FormValue("type") + "|" + h.Filename + "|" + string(data)
			}
		}
		return false
	}
	if _, err := c.SendMediaMessage(context.Background(), "919800000002", []byte("%PDF-1.4 x"), "price list.pdf", "application/pdf", "Prices"); err != nil {
		t.Fatal(err)
	}
	if uploaded != "whatsapp|application/pdf|price list.pdf|%PDF-1.4 x" {
		t.Fatalf("upload = %q", uploaded)
	}
	m := g.last()
	doc := m["document"].(map[string]any)
	if m["type"] != "document" || doc["id"] != "media-1" || doc["caption"] != "Prices" || doc["filename"] != "price list.pdf" {
		t.Fatalf("bad document payload: %v", m)
	}
	if _, err := c.SendMediaMessage(context.Background(), "919800000002", []byte{0xff, 0xd8, 0xff}, "a.jpg", "image/jpeg", ""); err != nil {
		t.Fatal(err)
	}
	if m := g.last(); m["type"] != "image" {
		t.Fatalf("a JPEG must go as an image: %v", m)
	}
}

func TestErrorsArePlainAndOnlyRefusalsAreRetried(t *testing.T) {
	g, c, _ := newGraph(t)
	var calls atomic.Int32
	g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":130429,"message":"Rate limit hit"}}`))
			return true
		}
		return false
	}
	if id, err := c.SendTextMessage(context.Background(), "919800000002", "hi"); err != nil || id != "wamid.OUT1" || calls.Load() != 3 {
		t.Fatalf("429 must be retried: id=%q err=%v calls=%d", id, err, calls.Load())
	}

	for code, want := range map[int]string{131047: "24 hours", 131026: "could not be delivered", 132001: "template", 100: "did not accept a value", 190: "access token has expired"} {
		code := code
		g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": "technical text"}})
			return true
		}
		_, err := c.SendTextMessage(context.Background(), "919800000002", "hi")
		var ae *APIError
		if !errors.As(err, &ae) || ae.Code != code || !strings.Contains(err.Error(), want) {
			t.Errorf("code %d: got %v, want text with %q", code, err, want)
		}
	}
	if st := c.GetStatus(); st.Connected || !strings.Contains(st.Problem, "access token") {
		t.Fatalf("an expired token must show as a problem: %+v", st)
	}

	// A request that may have reached Meta is never repeated.
	calls.Store(0)
	g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		calls.Add(1)
		time.Sleep(300 * time.Millisecond)
		return false
	}
	c.http.Timeout = 50 * time.Millisecond
	if _, err := c.SendTextMessage(context.Background(), "919800000002", "hi"); err == nil || IsNotConnected(err) {
		t.Fatalf("a timeout must fail without being treated as not sent: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("a timed-out send was repeated %d times", calls.Load())
	}
}

func TestNotConfiguredAndUnreachable(t *testing.T) {
	cfg := config.DefaultConfig()
	c := New(cfg)
	if _, err := c.SendTextMessage(context.Background(), "919800000002", "hi"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
	if c.GetStatus().Configured || c.Connected() {
		t.Fatal("not configured")
	}
	_, c2, _ := newGraph(t)
	c2.BaseURL = "http://127.0.0.1:1" // nothing listens: the request never left
	if _, err := c2.SendTextMessage(context.Background(), "919800000002", "hi"); !IsNotConnected(err) {
		t.Fatalf("an unreachable Meta must mean not connected, got %v", err)
	}
}

func TestCheckRecordsTheNumber(t *testing.T) {
	g, c, _ := newGraph(t)
	st, err := c.Check(context.Background())
	if err != nil || !st.Connected || !st.Configured || st.Phone != "919800000001" || st.VerifiedName != "Asha Stores" || st.QualityRating != "GREEN" || st.LastCheck == nil {
		t.Fatalf("status %+v, %v", st, err)
	}
	if g.paths[0] != "GET /v23.0/111" {
		t.Fatalf("path %q", g.paths[0])
	}
	if _, err := c.Probe(context.Background(), "v22.0", "222", "OTHER"); err != nil || g.paths[1] != "GET /v22.0/222" || g.auth[1] != "Bearer OTHER" {
		t.Fatalf("probe %v %v", g.paths, err)
	}
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

const sampleWebhook = `{"object":"whatsapp_business_account","entry":[{"id":"WABA","changes":[{"field":"messages","value":{
 "messaging_product":"whatsapp","metadata":{"display_phone_number":"919800000001","phone_number_id":"111"},
 "contacts":[{"profile":{"name":"Ravi"},"wa_id":"919800000002"}],
 "messages":[
  {"from":"919800000002","id":"wamid.A","timestamp":"1700000000","type":"text","text":{"body":" hi "}},
  {"from":"919800000002","id":"wamid.B","timestamp":"1700000001","type":"interactive","interactive":{"type":"button_reply","button_reply":{"id":"1","title":"Prices"}}},
  {"from":"919800000002","id":"wamid.C","timestamp":"1700000002","type":"image","image":{"caption":"this one"},"context":{"id":"wamid.OUT1"}},
  {"from":"919800000002","id":"wamid.D","timestamp":"1700000003","type":"location","location":{"latitude":1}},
  {"from":"919800000002","id":"wamid.E","timestamp":"1700000004","type":"reaction","reaction":{"emoji":"x"}}],
 "statuses":[{"id":"wamid.OUT1","status":"delivered","recipient_id":"919800000002"},
  {"id":"wamid.OUT2","status":"failed","recipient_id":"919800000003","errors":[{"code":131047,"title":"Re-engagement message"}]}]}},
 {"field":"messages","value":{"metadata":{"phone_number_id":"999"},"messages":[{"from":"1","id":"wamid.OTHER","type":"text","text":{"body":"not ours"}}]}}]}]}`

func TestWebhookParsingDedupeAndSignature(t *testing.T) {
	_, c, _ := newGraph(t)
	var msgs []Message
	var recs []Receipt
	c.OnMessage(func(m Message) { msgs = append(msgs, m) })
	c.OnReceipt(func(r Receipt) { recs = append(recs, r) })
	body := []byte(sampleWebhook)
	if !VerifySignature("app-secret", body, sign("app-secret", body)) {
		t.Fatal("a correct signature was refused")
	}
	for _, bad := range []string{"", "sha256=00", sign("other", body), strings.TrimPrefix(sign("app-secret", body), "sha256=")} {
		if VerifySignature("app-secret", body, bad) {
			t.Fatalf("signature %q was accepted", bad)
		}
	}
	if VerifySignature("", body, sign("", body)) {
		t.Fatal("without an app secret nothing may be accepted")
	}
	n, err := c.HandleWebhook(body)
	if err != nil || n != 6 {
		t.Fatalf("handled %d, %v", n, err)
	}
	if len(msgs) != 4 || msgs[0].Text != "hi" || msgs[0].Name != "Ravi" || msgs[0].From != "919800000002" || msgs[0].Timestamp.Unix() != 1700000000 {
		t.Fatalf("messages: %+v", msgs)
	}
	if msgs[1].Text != "Prices" || msgs[2].Media != "Photo" || msgs[2].Text != "this one" || msgs[2].ContextID != "wamid.OUT1" || msgs[3].Media != "Location" {
		t.Fatalf("messages: %+v", msgs)
	}
	if len(recs) != 2 || recs[0].Status != "delivered" || recs[1].Status != "failed" || !strings.Contains(recs[1].Error, "24 hours") {
		t.Fatalf("receipts: %+v", recs)
	}
	// Meta repeats deliveries: nothing is handled twice.
	if n, _ := c.HandleWebhook(body); n != 0 || len(msgs) != 4 {
		t.Fatalf("repeat delivery handled %d", n)
	}
	// The newest incoming message is used for the typing indicator.
	g2, c2, _ := newGraph(t)
	_, _ = c2.HandleWebhook(body)
	if err := c2.ShowTyping(context.Background(), "+919800000002"); err != nil {
		t.Fatal(err)
	}
	if m := g2.last(); m["message_id"] != "wamid.D" || m["typing_indicator"] == nil {
		t.Fatalf("typing payload: %v", m)
	}
	if _, err := c.HandleWebhook([]byte("not json")); err == nil {
		t.Fatal("bad JSON must be reported")
	}
}

// A labelled send carries biz_opaque_callback_data, and Meta's status update for it
// brings the label back on the receipt; an unlabelled send carries none.
func TestCallbackDataLabelRoundTrip(t *testing.T) {
	g, c, _ := newGraph(t)
	ctx := WithCallbackData(context.Background(), "pulse:s:job42")
	if _, err := c.SendTextMessage(ctx, "919800000002", "Hello"); err != nil {
		t.Fatal(err)
	}
	if m := g.last(); m["biz_opaque_callback_data"] != "pulse:s:job42" {
		t.Fatalf("label missing from the send: %v", m)
	}
	if _, err := c.SendTextMessage(context.Background(), "919800000002", "Hi"); err != nil {
		t.Fatal(err)
	}
	if _, has := g.last()["biz_opaque_callback_data"]; has {
		t.Fatal("an unlabelled send must not carry a label")
	}
	var recs []Receipt
	c.OnReceipt(func(r Receipt) { recs = append(recs, r) })
	body := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"222","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"111"},"statuses":[{"id":"wamid.L1","status":"delivered","recipient_id":"919800000002","timestamp":"1700000000","biz_opaque_callback_data":"pulse:s:job42"}]}}]}]}`)
	if _, err := c.HandleWebhook(body); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].CallbackData != "pulse:s:job42" || recs[0].MessageID != "wamid.L1" {
		t.Fatalf("label not carried back: %+v", recs)
	}
}

func TestSeenMemoryIsBounded(t *testing.T) {
	_, c, _ := newGraph(t)
	for i := 0; i < seenMax+50; i++ {
		c.firstTime("k" + string(rune(i)))
	}
	if len(c.seen) > seenMax {
		t.Fatalf("seen grew to %d", len(c.seen))
	}
}

func TestProfileNameIsCapped(t *testing.T) {
	if got := profileName("  Asha  "); got != "Asha" {
		t.Fatalf("names are trimmed, got %q", got)
	}
	long := strings.Repeat("é", 5000)
	if got := []rune(profileName(long)); len(got) != maxProfileName {
		t.Fatalf("a long name must be cut to %d characters, got %d", maxProfileName, len(got))
	}
}

// Meta may have queued a message before answering 5xx: a send is not repeated, a read is.
func TestServerErrorRetriesOnlyRepeatableRequests(t *testing.T) {
	g, c, _ := newGraph(t)
	var calls atomic.Int32
	g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":131000,"message":"Something went wrong"}}`))
			return true
		}
		return false
	}
	_, err := c.SendTextMessage(context.Background(), "919800000002", "hi")
	if err == nil || calls.Load() != 1 || !strings.Contains(err.Error(), "may or may not have sent") {
		t.Fatalf("a send answered with 5xx must not be repeated: calls=%d err=%v", calls.Load(), err)
	}
	calls.Store(0)
	if _, err := c.Check(context.Background()); err != nil || calls.Load() != 2 {
		t.Fatalf("a read answered with 5xx is retried: calls=%d err=%v", calls.Load(), err)
	}
}

// Meta's wa_id is the full international number: a Singapore sender is not turned into
// an Indian number, replies go back to it, and a 131050 failure carries its code.
func TestSingaporeNumbersAreNotTakenForIndian(t *testing.T) {
	g, c, _ := newGraph(t)
	var msgs []Message
	var recs []Receipt
	c.OnMessage(func(m Message) { msgs = append(msgs, m) })
	c.OnReceipt(func(r Receipt) { recs = append(recs, r) })
	body := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"222","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"111"},
	 "contacts":[{"profile":{"name":"Wei"},"wa_id":"6591234567"}],
	 "messages":[{"from":"6591234567","id":"wamid.SG1","timestamp":"1700000000","type":"text","text":{"body":"hello"}}],
	 "statuses":[{"id":"wamid.SG2","status":"failed","recipient_id":"6591234567","errors":[{"code":131050,"title":"User stopped marketing messages"}]}]}}]}]}`)
	if _, err := c.HandleWebhook(body); err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].From != "6591234567" || msgs[0].Name != "Wei" {
		t.Fatalf("the Singapore sender must keep its number: %+v", msgs)
	}
	if len(recs) != 1 || recs[0].Phone != "6591234567" || recs[0].Code != CodeMarketingOptOut {
		t.Fatalf("receipt: %+v", recs)
	}
	if _, err := c.SendReply(context.Background(), msgs[0].From, "hi", msgs[0].ID); err != nil {
		t.Fatal(err)
	}
	if m := g.last(); m["to"] != "6591234567" {
		t.Fatalf("the reply must go to the Singapore number, got %v", m["to"])
	}
	if _, err := c.SendTextMessage(context.Background(), "+91 98000 00002", "hi"); err != nil || g.last()["to"] != "919800000002" {
		t.Fatalf("a typed number still works: %v %v", g.last()["to"], err)
	}
}

func TestMaybeSentAndErrorCode(t *testing.T) {
	g, c, _ := newGraph(t)
	g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":131050,"message":"stopped marketing"}}`))
		return true
	}
	_, err := c.SendTextMessage(context.Background(), "919800000002", "hi")
	if ErrorCode(err) != CodeMarketingOptOut || MaybeSent(err) {
		t.Fatalf("a refusal is a definite failure with its code: %v", err)
	}
	g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	if _, err := c.SendTextMessage(context.Background(), "919800000002", "hi"); !MaybeSent(err) {
		t.Fatalf("a 5xx answer to a send may have been sent: %v", err)
	}
	g.handle = func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		_, _ = w.Write([]byte(`{"messages":[]}`))
		return true
	}
	if _, err := c.SendTextMessage(context.Background(), "919800000002", "hi"); !MaybeSent(err) {
		t.Fatalf("an answer without an ID may have been sent: %v", err)
	}
	if MaybeSent(ErrNotConnected) || MaybeSent(nil) {
		t.Fatal("nothing was sent when not connected")
	}
}

// Templates with a media or text header and link buttons need those values too.
func TestTemplateHeaderAndButtons(t *testing.T) {
	g, c, _ := newGraph(t)
	ctx := context.Background()
	parts := TemplateParts{HeaderMedia: "https://example.com/offer.jpg?v=2", Body: []string{"Asha"}, URLButtons: []string{"", "order/42"}}
	if _, err := c.SendTemplate(ctx, "919800000002", "offer", "en", parts); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(g.last()["template"])
	for _, want := range []string{
		`{"parameters":[{"image":{"link":"https://example.com/offer.jpg?v=2"},"type":"image"}],"type":"header"}`,
		`{"parameters":[{"text":"Asha","type":"text"}],"type":"body"}`,
		`{"index":"1","parameters":[{"text":"order/42","type":"text"}],"sub_type":"url","type":"button"}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("template payload misses %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), `"index":"0"`) {
		t.Fatalf("a button without a variable must not be sent: %s", b)
	}
	if _, err := c.SendTemplate(ctx, "919800000002", "hello", "en", TemplateParts{Header: []string{"Diwali"}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(g.last()["template"]); !strings.Contains(string(b), `{"parameters":[{"text":"Diwali","type":"text"}],"type":"header"}`) {
		t.Fatalf("text header missing: %s", b)
	}
	for link, kind := range map[string]string{"https://x.com/a.PNG": "image", "https://x.com/v.mp4": "video", "https://x.com/menu.pdf": "document"} {
		if got := HeaderMediaKind(link); got != kind {
			t.Errorf("%s: got %s, want %s", link, got, kind)
		}
	}
}
