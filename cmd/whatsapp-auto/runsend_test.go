package main

import (
	"path/filepath"
	"testing"
	"time"

	"whatsapp-automation/pkg/contacts"
)

// N4: the send command matches blocked numbers like the server (contacts.MatchesTyped):
// "6591234567" typed without a + blocks the Singapore number 6591234567 as well as
// India's 916591234567.
func TestRunSendBlockedListMatchesLikeServer(t *testing.T) {
	list := []string{"6591234567"}
	if !containsPhone(list, "6591234567") || !containsPhone(list, "916591234567") {
		t.Fatal("a typed blocked number must match the stored number with and without the Indian default")
	}
	if containsPhone(list, "14155550100") {
		t.Fatal("another number must not match")
	}
}

// F3 in the send command: a Singapore number that opted out and was deleted must not be
// matched to an Indian contact with the same 10 digits and messaged with its consent.
func TestSendCommandChecksTheExactNumber(t *testing.T) {
	cs, err := contacts.NewStore(filepath.Join(t.TempDir(), "contacts.json"))
	if err != nil {
		t.Fatal(err)
	}
	cs.Upsert("6591234567", "Wei", "whatsapp")
	cs.SetDND("6591234567", true, "Replied STOP", time.Now())
	cs.Delete("6591234567")
	_ = cs.SaveExact(contacts.Contact{Phone: "916591234567", Name: "Ravi", Consent: true, ConsentSource: "form"})

	ct := sendContact(cs, "6591234567")
	if !ct.IsDND || ct.Consent || ct.Name == "Ravi" {
		t.Fatalf("the send must use the Singapore number's own (opted-out) record, got %+v", ct)
	}
	if ct := sendContact(cs, "916591234567"); ct.Name != "Ravi" || ct.IsDND {
		t.Fatalf("the Indian contact is found by its own number: %+v", ct)
	}
}
