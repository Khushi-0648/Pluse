package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/securestore"
)

// N3: a message typed in the inbox or a test message whose outcome is unknown is
// recorded before it goes, settled as "may have been sent", counted towards the daily
// limit, and the person is told to check the chat.
func TestDirectSendWithUnknownOutcomeIsRecorded(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cs.Save(contacts.Contact{Phone: "14155550811"})
	h.cs.RecordInbound("14155550811", time.Now())
	h.s.Messenger = &uncertainWA{h.wa}
	w, out := h.api("POST", "/messages/send", map[string]any{"to": "+14155550811", "message": "Hi"})
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "may have been sent") || !strings.Contains(w.Body.String(), "check the chat") {
		t.Fatalf("the person must be told it may have been sent: %d %v", w.Code, out)
	}
	recs := h.hs.Query(history.Query{Phone: "14155550811"})
	if len(recs) != 1 || recs[0].Status != history.StatusFailed || !recs[0].Uncertain || !strings.HasPrefix(recs[0].Label, DirectLabel) {
		t.Fatalf("the inbox send must be recorded as possibly sent, with a label: %+v", recs)
	}
	if w, _ := h.api("POST", "/setup/test-message", map[string]any{"phone": "+14155550812"}); w.Code != http.StatusGatewayTimeout {
		t.Fatalf("a test message with an unknown outcome: %d %s", w.Code, w.Body.String())
	}
	if h.hs.SentSince(time.Now().Add(-time.Hour)) != 2 {
		t.Fatal("sends that may have gone out count towards the daily limit")
	}
}

// N2: erasing a Singapore customer (6591234567) removes their scheduled messages and
// backup copies, which CleanPhone would have looked for as India's 916591234567.
func TestForgetForeignNumberEverywhere(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.cs.Upsert("6591234567", "Wei", "whatsapp")
	_ = h.cs.Save(contacts.Contact{Phone: "14155550902", Name: "Keep Me"})
	if _, _, err := h.s.Scheduler.Add("6591234567", "Hello", time.Now().Add(time.Hour), "", "admin", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.s.AutoBackup(); err != nil {
		t.Fatal(err)
	}
	if w, out := h.api("POST", "/privacy/forget-contact", map[string]any{"phone": "6591234567", "password": adminPass}); w.Code != 200 {
		t.Fatalf("forget: %d %v", w.Code, out)
	}
	for _, j := range h.s.Scheduler.List() {
		if j.Recipient == "6591234567" {
			t.Fatalf("the scheduled message must be deleted: %+v", j)
		}
	}
	files, _ := filepath.Glob(filepath.Join(h.s.backupDir(), "pulse-backup-*.json"))
	if len(files) != 1 {
		t.Fatalf("backups: %v", files)
	}
	data, _ := securestore.Default().ReadFile(files[0])
	if bytes.Contains(data, []byte("6591234567")) || !bytes.Contains(data, []byte("14155550902")) {
		t.Fatalf("the backup still holds the erased customer or lost another: %s", data)
	}
}

// slowWA holds every send until released, like a slow answer from Meta.
type slowWA struct {
	*fakeWA
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *slowWA) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return s.fakeWA.SendTextMessage(ctx, to, text)
}

// N5: a repeat of a request still in progress gets "in progress, retry later"; once
// done the key counts as received, also after a restart (the history keeps it).
func TestIdempotencyKeyInProgressAndAfterRestart(t *testing.T) {
	h := newHarness(t)
	post := func(body string) *httptest.ResponseRecorder {
		return h.raw("POST", "/api/v1/webhook/send", strings.NewReader(body), map[string]string{"Content-Type": "application/json", "X-Webhook-Secret": "inbound-secret-123456"})
	}
	_ = h.cs.Save(contacts.Contact{Phone: "14155550821"})
	h.cs.RecordInbound("14155550821", time.Now())
	slow := &slowWA{fakeWA: h.wa, started: make(chan struct{}), release: make(chan struct{})}
	h.s.Messenger = slow
	key := "inflight-" + time.Now().Format("150405.000000")
	body := `{"to":"14155550821","message":"Ready","idempotency_key":"` + key + `"}`
	first := make(chan int, 1)
	go func() { first <- post(body).Code }()
	<-slow.started
	if w := post(body); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "in progress") {
		t.Fatalf("a repeat while the first is in flight must be told to retry later: %d %s", w.Code, w.Body.String())
	}
	close(slow.release)
	if code := <-first; code != 200 {
		t.Fatalf("the first request is sent: %d", code)
	}
	if w := post(body); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already received") {
		t.Fatalf("after it finished the key counts as received: %d %s", w.Code, w.Body.String())
	}
	integrationKeys.release(key) // as after a restart: only the history remembers it
	if w := post(body); w.Code != http.StatusConflict || len(h.wa.sent) != 1 {
		t.Fatalf("the history must keep the key across a restart: %d %s", w.Code, w.Body.String())
	}
	if recs := h.hs.Query(history.Query{Phone: "14155550821"}); len(recs) != 1 || strings.Contains(recs[0].Label, key) {
		t.Fatalf("the label must hold a hash of the key, not the key: %+v", recs)
	}
}

// storage-N3: the CSV's ?days= range starts at local midnight like the Reports screen.
func TestAnalyticsCSVRangeMatchesReports(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	now := time.Now()
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	h.hs.Record(history.MessageRecord{ID: "in.yday", Direction: history.DirectionIncoming, Phone: "14155550831", Text: "yesterday", Timestamp: midnight.Add(-time.Minute)})
	h.hs.Record(history.MessageRecord{ID: "in.today", Direction: history.DirectionIncoming, Phone: "14155550831", Text: "today", Timestamp: midnight.Add(time.Minute)})
	w, _ := h.api("GET", "/analytics/export.csv?days=1", nil)
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][8] != "today" {
		t.Fatalf("?days=1 is today only, like Reports: %v", rows)
	}
	if !reportSince(7, now).Equal(midnight.AddDate(0, 0, -6)) {
		t.Fatal("7 days start at midnight six days ago")
	}
}

// N10 and the allow-list: inbox text uses the business time zone, and a test number
// that is exactly a saved contact's number does not allow India's 91 form of it.
func TestInboxTimeZoneAndExactAllowList(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	// UTC+14 and UTC-12 are always on different dates, so one of them differs from the
	// computer's date: the test then fails if the computer's clock is used.
	zone := "Pacific/Kiritimati"
	if l, err := time.LoadLocation(zone); err != nil || time.Now().In(l).Format("2006-01-02") == time.Now().Format("2006-01-02") {
		zone = "Etc/GMT+12"
	}
	_ = h.cfg.Update(func(c *config.Config) error {
		c.BusinessHours.Timezone = zone
		c.Automation.AllowListOnly, c.Automation.AllowList = true, []string{"6591234567"}
		return nil
	})
	h.cs.Upsert("6591234567", "Wei", "whatsapp")
	h.cs.RecordInbound("6591234567", time.Now())
	h.cs.Upsert("916591234567", "Ravi", "whatsapp")
	h.cs.RecordInbound("916591234567", time.Now())
	if w, _ := h.api("POST", "/messages/send", map[string]any{"to": "916591234567", "message": "Hi"}); w.Code != http.StatusConflict {
		t.Fatalf("India's 916591234567 is not the Singapore test number: %d", w.Code)
	}
	w, out := h.api("POST", "/messages/send", map[string]any{"to": "6591234567", "message": "Today is {{date}}"})
	if w.Code != 200 {
		t.Fatalf("send: %d %v", w.Code, out)
	}
	loc, _ := time.LoadLocation(zone)
	want := time.Now().In(loc).Format("2 Jan 2006")
	if text, _ := out["text"].(string); !strings.Contains(text, want) {
		t.Fatalf("{{date}} must follow the business time zone (%s): %q", want, text)
	}
}
