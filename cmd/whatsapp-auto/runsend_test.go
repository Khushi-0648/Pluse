package main

import "testing"

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
