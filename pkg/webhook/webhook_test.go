package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/security"
)

func TestDeliverySignedRetriedAndReplayed(t *testing.T) {
	var calls, fail atomic.Int32
	fail.Store(1)
	var lastSig, lastBody atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		lastBody.Store(b)
		lastSig.Store(r.Header.Get("X-Signature-SHA256"))
		if fail.Load() == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	oldV, oldB := validateURL, backoff
	validateURL = func(string) error { return nil }
	backoff = func(int) time.Duration { return 10 * time.Millisecond }
	defer func() { validateURL, backoff = oldV, oldB }()

	cfg := config.DefaultConfig()
	_ = cfg.Update(func(c *config.Config) error {
		c.Webhook = config.WebhookConfig{Enabled: true, URL: ts.URL, Secret: "0123456789abcdef", TimeoutSeconds: 2, MaxRetries: 3}
		return nil
	})
	d := New(cfg, filepath.Join(t.TempDir(), "failed.json"))
	d.client = func(time.Duration) *http.Client { return ts.Client() }
	d.Start()
	defer d.Stop()

	d.Send("handover.requested", map[string]string{"phone": "+1"})
	deadline := time.Now().Add(5 * time.Second)
	for len(d.Failed()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() != 3 || len(d.Failed()) != 1 {
		t.Fatalf("expected 3 attempts then a failed entry, got %d attempts, %d failed", calls.Load(), len(d.Failed()))
	}
	body := lastBody.Load().([]byte)
	if !security.VerifyWebhookHMAC("0123456789abcdef", body, lastSig.Load().(string)) {
		t.Fatal("payload signature does not verify")
	}
	var env Envelope
	if json.Unmarshal(body, &env) != nil || env.Event != "handover.requested" {
		t.Fatalf("envelope wrong: %s", body)
	}

	fail.Store(0)
	if d.Replay("") != 1 {
		t.Fatal("replay should queue the failed event")
	}
	deadline = time.Now().Add(5 * time.Second)
	for d.PendingCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() != 4 || len(d.Failed()) != 0 {
		t.Fatalf("replay not delivered: %d calls, %d failed", calls.Load(), len(d.Failed()))
	}
}

func TestDisabledWebhookSendsNothing(t *testing.T) {
	d := New(config.DefaultConfig(), "")
	d.Send("message.received", nil)
	if d.PendingCount() != 0 {
		t.Fatal("nothing should be queued while the webhook is off")
	}
	var nilD *Dispatcher
	nilD.Send("x", nil) // must not panic
	if _, _, err := d.Test("http://127.0.0.1:1/", "", time.Second); err == nil {
		t.Fatal("test pings to local addresses must be refused")
	}
}

func enabledConfig(url string) *config.Config {
	cfg := config.DefaultConfig()
	_ = cfg.Update(func(c *config.Config) error {
		c.Webhook = config.WebhookConfig{Enabled: true, URL: url, TimeoutSeconds: 2, MaxRetries: 5}
		return nil
	})
	return cfg
}

// A failing event blocks the ones after it, so a receiver never gets "opted in" before
// the earlier "opted out" (F22).
func TestEventsDeliveredInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env Envelope
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &env)
		if calls.Add(1) <= 2 { // the first event fails twice
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		mu.Lock()
		got = append(got, env.Event)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	oldV, oldB := validateURL, backoff
	validateURL = func(string) error { return nil }
	backoff = func(int) time.Duration { return 30 * time.Millisecond }
	defer func() { validateURL, backoff = oldV, oldB }()

	d := New(enabledConfig(ts.URL), "")
	d.client = func(time.Duration) *http.Client { return ts.Client() }
	d.Send("contact.opted_out", nil)
	d.Send("contact.opted_in", nil)
	d.Send("message.received", nil)
	d.Start()
	defer d.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for d.PendingCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, ",") != "contact.opted_out,contact.opted_in,message.received" {
		t.Fatalf("events delivered out of order: %v", got)
	}
}

// Events not yet delivered at close are saved and sent after the next start.
func TestPendingEventsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := enabledConfig("https://example.invalid/hook")
	d := New(cfg, filepath.Join(dir, "webhook-failed.json"))
	d.Send("contact.opted_out", map[string]string{"phone": "+14155550100"})
	d.Send("contact.opted_in", map[string]string{"phone": "+14155550100"})
	d.Stop() // never started: both are still waiting
	d2 := New(cfg, filepath.Join(dir, "webhook-failed.json"))
	if d2.PendingCount() != 2 {
		t.Fatalf("pending events must be kept across a restart, got %d", d2.PendingCount())
	}
	if !strings.Contains(string(d2.pending[0].Payload), "opted_out") {
		t.Fatal("saved events must keep their order")
	}
	if _, err := os.Stat(filepath.Join(dir, "webhook-pending.json")); err != nil {
		t.Fatal("the saved queue must stay on disk until the events are delivered (a crash must not lose them)")
	}
	d2.Forget("14155550100")
	d2.Stop()
	if New(cfg, filepath.Join(dir, "webhook-failed.json")).PendingCount() != 0 {
		t.Fatal("erased events must not come back after a restart")
	}
}

// B9: Forget matches the phone field exactly, never a longer number or a message text.
func TestForgetMatchesExactPhoneOnly(t *testing.T) {
	d := New(enabledConfig("https://example.invalid/hook"), "")
	d.Send("message.received", map[string]string{"phone": "+14155550100", "text": "hi"})
	d.Send("message.received", map[string]string{"phone": "+141555501001", "text": "call +14155550100"})
	d.Send("message.received", map[string]string{"phone": "+15550000000"})
	d.mu.Lock()
	d.failed = append(d.failed, Delivery{ID: "f1", Payload: d.pending[0].Payload}, Delivery{ID: "f2", Payload: d.pending[1].Payload})
	d.mu.Unlock()
	d.Forget("+14155550100")
	if d.PendingCount() != 2 {
		t.Fatalf("only the exact number's event may go, %d left", d.PendingCount())
	}
	for _, p := range d.pending {
		if eventForPhone(p.Payload, "14155550100") {
			t.Fatal("the customer's event is still queued")
		}
	}
	if f := d.Failed(); len(f) != 1 || f[0].ID != "f2" {
		t.Fatalf("failed list = %+v", f)
	}
	d.Forget("")
	if d.PendingCount() != 0 || len(d.Failed()) != 0 {
		t.Fatal("an empty Forget drops everything")
	}
}

// B10: events lost to a full queue are logged, at most once a minute with a count.
func TestDroppedEventsAreLoggedThrottled(t *testing.T) {
	var logged []int
	old := logDropped
	logDropped = func(n int) { logged = append(logged, n) }
	defer func() { logDropped = old }()
	d := New(enabledConfig("https://example.invalid/hook"), "")
	for i := 0; i < maxPending+5; i++ {
		d.Send("message.received", nil)
	}
	if d.PendingCount() != maxPending {
		t.Fatalf("queue = %d", d.PendingCount())
	}
	if len(logged) != 1 || logged[0] != 1 {
		t.Fatalf("first drop logged at once, the rest held back: %v", logged)
	}
	d.mu.Lock()
	d.dropLoggedAt = time.Now().Add(-2 * time.Minute)
	d.mu.Unlock()
	d.Send("message.received", nil)
	if len(logged) != 2 || logged[1] != 5 {
		t.Fatalf("after a minute the count is logged: %v", logged)
	}
	d.Send("message.received", nil)
	d.Stop()
	if len(logged) != 3 || logged[2] != 1 {
		t.Fatalf("Stop logs what is left: %v", logged)
	}
}

// A crash (no Stop) must not lose queued events: the queue is written soon after each change.
func TestPendingEventsSurviveCrash(t *testing.T) {
	dir := t.TempDir()
	cfg := enabledConfig("https://example.invalid/hook")
	d := New(cfg, filepath.Join(dir, "webhook-failed.json"))
	d.Send("contact.opted_out", map[string]string{"phone": "+14155550101"})
	if err := d.pendSaver.Flush(); err != nil { // what the 1-second timer does
		t.Fatal(err)
	}
	// No Stop: Pulse was killed.
	if n := New(cfg, filepath.Join(dir, "webhook-failed.json")).PendingCount(); n != 1 {
		t.Fatalf("a queued event must survive a crash, got %d", n)
	}
}
