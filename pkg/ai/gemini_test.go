package ai

import (
	"context"
	"strings"
	"testing"
)

func TestAIHeuristicFallback(t *testing.T) {
	engine := NewEngine(Config{Enabled: true})

	// Question heuristic
	reply, err := engine.GenerateReply(context.Background(), "Bob", "How much does it cost?")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(reply, "pricing") && !strings.Contains(reply, "plans") {
		t.Fatalf("Expected pricing response, got: %s", reply)
	}

	// General greeting fallback
	reply, err = engine.GenerateReply(context.Background(), "Alice", "Need assistance")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(reply, "menu") {
		t.Fatalf("Expected menu mention in fallback, got: %s", reply)
	}
}
