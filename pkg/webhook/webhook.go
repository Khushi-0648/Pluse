// Package webhook delivers signed event notifications to the owner's URL with retries.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/version"
)

// Delivery is one event sent (or being sent) to the webhook URL.
type Delivery struct {
	ID         string          `json:"id"`
	Event      string          `json:"event"`
	CreatedAt  time.Time       `json:"created_at"`
	Attempts   int             `json:"attempts"`
	LastError  string          `json:"last_error,omitempty"`
	StatusCode int             `json:"status_code,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	next       time.Time
}

// Dispatcher queues events and retries failed deliveries with exponential backoff.
type Dispatcher struct {
	cfg      *config.Config
	mu       sync.Mutex
	pending  []*Delivery
	failed   []Delivery
	path     string
	pendPath string // events not yet delivered, kept on disk and sent at the next start
	// pendSaver writes the queue about a second after every change, so a crash, a forced
	// stop or a reboot does not lose events (such as contact.opted_out) still waiting.
	pendSaver *securestore.Saver
	box       *securestore.Box
	wake      chan struct{}
	client    func(time.Duration) *http.Client
	cached    *http.Client
	cachedTO  time.Duration
	stop      chan struct{}
	stopOnce  sync.Once
	// dropped counts events lost to a full queue since dropLoggedAt (not yet logged).
	dropped      int
	dropLoggedAt time.Time
}

const maxFailed = 200

// New creates a dispatcher. failedPath stores deliveries that ran out of retries;
// events still waiting when Pulse closes are kept next to it (encrypted like all data)
// and delivered at the next start.
func New(cfg *config.Config, failedPath string) *Dispatcher {
	d := &Dispatcher{cfg: cfg, path: failedPath, box: securestore.Default(), wake: make(chan struct{}, 1), client: security.SafeHTTPClient, stop: make(chan struct{})}
	if failedPath != "" {
		if data, err := d.box.ReadFile(failedPath); err == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &d.failed)
		}
		d.pendPath = filepath.Join(filepath.Dir(failedPath), "webhook-pending.json")
		if data, err := d.box.ReadFile(d.pendPath); err == nil && len(data) > 0 {
			var saved []*Delivery
			if json.Unmarshal(data, &saved) == nil {
				now := time.Now()
				for _, p := range saved {
					p.next = now
				}
				d.pending = saved
			}
		}
		// The file stays until those events are delivered; every change rewrites it.
		d.pendSaver = securestore.NewSaver(time.Second, d.persistPending)
	}
	return d
}

// Start runs the delivery loop until Stop.
func (d *Dispatcher) Start() { go d.loop() }

// Stop ends the delivery loop and saves events that were not delivered yet.
func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() {
		close(d.stop)
		d.mu.Lock()
		unlogged := d.dropped
		d.dropped = 0
		d.mu.Unlock()
		if unlogged > 0 {
			logDropped(unlogged)
		}
		if d.pendSaver == nil {
			return
		}
		if err := d.pendSaver.Flush(); err != nil {
			fmt.Printf("[Webhook] %d events could not be saved for delivery later: %v\n", d.PendingCount(), err)
		}
	})
}

// pendingChanged schedules saving the queue.
func (d *Dispatcher) pendingChanged() {
	if d.pendSaver != nil {
		d.pendSaver.Trigger()
	}
}

// persistPending writes the queue (or removes the file when it is empty).
func (d *Dispatcher) persistPending() error {
	d.mu.Lock()
	if len(d.pending) == 0 {
		d.removePendingFile()
		d.mu.Unlock()
		return nil
	}
	data, err := json.Marshal(d.pending)
	d.mu.Unlock()
	if err != nil {
		return err
	}
	return d.box.WriteFileNoBackup(d.pendPath, data)
}

func (d *Dispatcher) removePendingFile() {
	if d.pendPath == "" {
		return
	}
	_ = os.Remove(d.pendPath)
	securestore.RemoveBackup(d.pendPath)
}

// Envelope is the JSON body sent for every event.
type Envelope struct {
	ID      string    `json:"id"`
	Event   string    `json:"event"`
	Time    time.Time `json:"time"`
	Source  string    `json:"source"`
	Version string    `json:"version"`
	Data    any       `json:"data"`
}

// Send queues an event when the webhook is enabled. It never blocks on the network.
func (d *Dispatcher) Send(event string, data any) {
	if d == nil {
		return
	}
	wh := d.cfg.Snapshot().Webhook
	if !wh.Enabled || wh.URL == "" {
		return
	}
	env := Envelope{ID: "evt_" + security.RandomToken(8), Event: event, Time: time.Now().UTC(), Source: version.Product, Version: version.Version, Data: data}
	body, err := json.Marshal(env)
	if err != nil {
		return
	}
	d.mu.Lock()
	var report int
	if len(d.pending) >= maxPending {
		// The other system has been unreachable for a long time: the oldest event is lost.
		// Recorded in the activity log, at most once a minute with a count.
		d.pending = d.pending[1:]
		d.dropped++
		if now := time.Now(); now.Sub(d.dropLoggedAt) >= dropLogEvery {
			report, d.dropped, d.dropLoggedAt = d.dropped, 0, now
		}
	}
	d.pending = append(d.pending, &Delivery{ID: env.ID, Event: event, CreatedAt: env.Time, Payload: body, next: time.Now()})
	d.mu.Unlock()
	if report > 0 {
		logDropped(report)
	}
	d.pendingChanged()
	d.poke()
}

const (
	maxPending   = 1000
	dropLogEvery = time.Minute
)

// logDropped records lost events in the activity log (overridable in tests).
var logDropped = func(n int) {
	security.LogAudit("WEBHOOK_EVENT_DROPPED", "local", "system", fmt.Sprintf("%d event(s) for the other system were dropped because %d were already waiting (the other system has not answered for a long time)", n, maxPending), "failed")
}

func (d *Dispatcher) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Overridable in tests, which deliver to a local server.
var (
	validateURL = security.ValidateWebhookURL
	backoff     = defaultBackoff
)

func defaultBackoff(attempt int) time.Duration {
	steps := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	if attempt-1 < len(steps) {
		return steps[attempt-1]
	}
	return time.Hour
}

func (d *Dispatcher) loop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-t.C:
		case <-d.wake:
		}
		d.flushDue()
	}
}

// flushDue delivers events strictly in the order they happened. While the oldest one
// is failing and waiting to be retried, later events wait behind it, so a receiver
// never sees, for example, "opted in" before the earlier "opted out".
func (d *Dispatcher) flushDue() {
	for {
		select {
		case <-d.stop:
			return
		default:
		}
		wh := d.cfg.Snapshot().Webhook
		d.mu.Lock()
		// Switched off (or no address) since these were queued: the owner no longer wants
		// customer messages to leave Pulse this way, so nothing queued is sent any more.
		if (!wh.Enabled || wh.URL == "") && len(d.pending) > 0 {
			n := len(d.pending)
			d.pending = nil
			d.mu.Unlock()
			d.pendingChanged()
			security.LogAudit("WEBHOOK_QUEUE_DROPPED", "local", "system", fmt.Sprintf("Dropped %d queued events because sending events to another system is switched off", n), "success")
			return
		}
		if len(d.pending) == 0 || d.pending[0].next.After(time.Now()) {
			d.mu.Unlock()
			return
		}
		p := d.pending[0]
		d.mu.Unlock()
		code, err := d.deliver(wh, p.Payload)
		d.mu.Lock()
		if !d.isPendingLocked(p) {
			// Removed while it was being sent (erasure request or "delete all data"):
			// it must not come back as a failed delivery.
			d.mu.Unlock()
			continue
		}
		p.Attempts++
		p.StatusCode = code
		if err == nil {
			d.removePendingLocked(p)
		} else {
			p.LastError = err.Error()
			if p.Attempts >= wh.MaxRetries || !wh.Enabled {
				d.removePendingLocked(p)
				d.failed = append(d.failed, *p)
				if len(d.failed) > maxFailed {
					d.failed = d.failed[len(d.failed)-maxFailed:]
				}
				d.saveLocked()
				security.LogAudit("WEBHOOK_FAILED", "local", "system", fmt.Sprintf("Event %s could not be delivered after %d attempts: %s", p.Event, p.Attempts, p.LastError), "failed")
			} else {
				p.next = time.Now().Add(backoff(p.Attempts))
				d.mu.Unlock()
				return // later events wait for this one
			}
		}
		d.mu.Unlock()
	}
}

func (d *Dispatcher) isPendingLocked(p *Delivery) bool {
	for _, q := range d.pending {
		if q == p {
			return true
		}
	}
	return false
}

func (d *Dispatcher) removePendingLocked(p *Delivery) {
	for i, q := range d.pending {
		if q == p {
			d.pending = append(d.pending[:i], d.pending[i+1:]...)
			d.pendingChanged() // only schedules the save, which takes d.mu itself later
			return
		}
	}
}

func (d *Dispatcher) saveLocked() {
	if d.path == "" {
		return
	}
	data, err := json.Marshal(d.failed)
	if err == nil {
		_ = d.box.WriteFile(d.path, data)
	}
}

func (d *Dispatcher) deliver(wh config.WebhookConfig, body []byte) (int, error) {
	if wh.URL == "" {
		return 0, errors.New("no webhook URL configured")
	}
	if err := validateURL(wh.URL); err != nil {
		return 0, err
	}
	to := time.Duration(wh.TimeoutSeconds) * time.Second
	if d.cached == nil || d.cachedTO != to {
		d.cached, d.cachedTO = d.client(to), to
	}
	return Post(d.cached, wh.URL, wh.Secret, body)
}

// Post sends a signed payload and returns the HTTP status.
func Post(c *http.Client, url, secret string, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout+time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.Product+"/"+version.Version)
	ts := fmt.Sprint(time.Now().Unix())
	req.Header.Set("X-Pulse-Timestamp", ts)
	if secret != "" {
		req.Header.Set("X-Signature-SHA256", "sha256="+security.SignWebhookHMAC(secret, body))
		// Also signs the timestamp, so a receiver can refuse old, replayed deliveries.
		// X-Signature-SHA256 stays for systems already connected.
		req.Header.Set("X-Pulse-Signature", "t="+ts+",v1="+security.SignWebhookHMAC(secret, append([]byte(ts+"."), body...)))
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("the server answered HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// Test sends a ping to url and reports the result without queueing.
func (d *Dispatcher) Test(url, secret string, timeout time.Duration) (int, time.Duration, error) {
	if err := validateURL(url); err != nil {
		return 0, 0, err
	}
	body, _ := json.Marshal(Envelope{ID: "evt_test_" + security.RandomToken(4), Event: "ping", Time: time.Now().UTC(), Source: version.Product, Version: version.Version, Data: map[string]string{"message": "Test from " + version.Product}})
	start := time.Now()
	code, err := Post(d.client(timeout), url, secret, body)
	return code, time.Since(start).Round(time.Millisecond), err
}

// Failed returns deliveries that ran out of retries, newest first.
func (d *Dispatcher) Failed() []Delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Delivery, 0, len(d.failed))
	for i := len(d.failed) - 1; i >= 0; i-- {
		out = append(out, d.failed[i])
	}
	return out
}

// PendingCount returns the number of events waiting to be delivered.
func (d *Dispatcher) PendingCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}

// Replay moves a failed delivery back into the queue. An empty id replays all.
func (d *Dispatcher) Replay(id string) int {
	d.mu.Lock()
	n := 0
	kept := d.failed[:0]
	for _, f := range d.failed {
		if id == "" || f.ID == id {
			f.Attempts, f.LastError, f.next = 0, "", time.Now()
			fc := f
			d.pending = append(d.pending, &fc)
			n++
			continue
		}
		kept = append(kept, f)
	}
	d.failed = append([]Delivery(nil), kept...)
	d.saveLocked()
	d.mu.Unlock()
	d.pendingChanged()
	d.poke()
	return n
}

// Forget drops queued and failed events for one customer (phone, with or without the
// leading +), or every event when phone is empty. Only an event whose "phone" field is
// exactly that number matches: +1415555010 must not also erase +14155550100's events,
// nor an event that merely quotes the number in a message text.
func (d *Dispatcher) Forget(phone string) {
	d.mu.Lock()
	keepP := d.pending[:0:0]
	for _, p := range d.pending {
		if phone != "" && !eventForPhone(p.Payload, phone) {
			keepP = append(keepP, p)
		}
	}
	d.pending = keepP
	keepF := d.failed[:0:0]
	for _, f := range d.failed {
		if phone != "" && !eventForPhone(f.Payload, phone) {
			keepF = append(keepF, f)
		}
	}
	d.failed = keepF
	d.saveLocked()
	if d.path != "" {
		securestore.RemoveBackup(d.path)
	}
	d.removePendingFile() // erased at once; the remaining events are written again below
	d.mu.Unlock()
	d.pendingChanged()
}

// eventForPhone reports whether an event envelope's data.phone is phone.
func eventForPhone(payload []byte, phone string) bool {
	var env struct {
		Data struct {
			Phone string `json:"phone"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &env) != nil || env.Data.Phone == "" {
		return false
	}
	return strings.TrimPrefix(env.Data.Phone, "+") == strings.TrimPrefix(phone, "+")
}

// ClearFailed deletes the failed-delivery list.
func (d *Dispatcher) ClearFailed() {
	d.mu.Lock()
	d.failed = nil
	d.saveLocked()
	d.mu.Unlock()
}
