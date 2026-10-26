package bot

import (
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
)

// The failing messaging tests of the eighth-package audit (24 Oct 2026), replayed.

// P4: opt-outs in the customer's own words and look-alike spellings.
func TestAudit8OptOutPhrases(t *testing.T) {
	for _, s := range []string{"cancel", "quit", "stop sending", "please stop messaging me", "STOP please",
		"dont message me again", "ＳＴＯＰ", "SТОР" /* Cyrillic Т, О, Р */, "STOP\u200b", "Stop."} {
		if !IsOptOut(s) {
			t.Errorf("%q must opt out", s)
		}
	}
	for _, s := range []string{"hi", "what time do you stop working today", "is the bus stop near you"} {
		if IsOptOut(s) {
			t.Errorf("%q is not an opt-out", s)
		}
	}
}

// P5: hand-over requests in longer sentences.
func TestAudit8HandoverPhrases(t *testing.T) {
	for _, s := range []string{"AGENT please", "I want a human being", "agent", "talk to someone", "baat karni hai"} {
		if !IsHandoverRequest(s) {
			t.Errorf("%q must hand over", s)
		}
	}
}

// D37: identical messages from another bot trip loop detection.
func TestAudit8IdenticalMessagesTripLoopDetection(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 8; i++ {
		r.say(asha, "Bot", "Thank you for contacting us")
		r.now = r.now.Add(11 * time.Second) // past the cooldown, as in the audit
	}
	if n := r.m.count(); n > 3 {
		t.Fatalf("8 identical messages got %d replies; loop detection should stop it by the 3rd", n)
	}
	if c, _ := r.cs.Get(asha); !c.BotPaused(r.now) {
		t.Fatal("the chat must be paused as a possible bot loop")
	}
}

// D36: raising the daily limit takes effect at once, without a restart.
func TestAudit8RaisedDailyLimitAppliesAtOnce(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.Automation.DailyLimit = 2
		c.Automation.CooldownSeconds = 0
		return nil
	})
	for i, p := range []string{"919800000101", "919800000102", "919800000103"} {
		r.say(p, "C", "hi")
		_ = i
	}
	if r.m.count() != 2 {
		t.Fatalf("limit 2: got %d replies", r.m.count())
	}
	_ = r.cfg.Update(func(c *config.Config) error { c.Automation.DailyLimit = 1000; return nil })
	r.say("919800000104", "D", "hi")
	if r.m.count() != 3 {
		t.Fatalf("after raising the limit a reply must go out at once: got %d replies", r.m.count())
	}
}

// S43: direction overrides and control characters are removed before storing.
func TestAudit8HiddenCharactersRemoved(t *testing.T) {
	r := newRig(t)
	r.say(asha, "Evil\u202eName\x00", "hello\u202e\x00 there")
	msgs := r.hs.List(5, asha, "")
	if len(msgs) == 0 {
		t.Fatal("message not stored")
	}
	for _, m := range msgs {
		if strings.ContainsAny(m.Text+m.Name, "\u202e\x00") {
			t.Fatalf("hidden characters kept: %q / %q", m.Name, m.Text)
		}
	}
	if c, _ := r.cs.Get(asha); strings.ContainsAny(c.Name, "\u202e\x00") {
		t.Fatalf("hidden characters kept in the contact name: %q", c.Name)
	}
}
