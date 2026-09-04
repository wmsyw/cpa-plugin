package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestBackendHeadersSetsStableConversationAndSessionID(t *testing.T) {
	req := httptest.NewRequest("POST", "https://example.com/v2/chat/completions", nil)
	convID := "01a06c42375977789222d96e68daa917"
	backendHeaders(req, &storedAuth{}, convID)

	if got := req.Header.Get("X-Conversation-ID"); got != convID {
		t.Fatalf("X-Conversation-ID = %q, want %q", got, convID)
	}
	if got := req.Header.Get("X-Session-ID"); got != convID {
		t.Fatalf("X-Session-ID = %q, want %q", got, convID)
	}
	if got := req.Header.Get("X-IDE-Type"); got != "CLI" {
		t.Fatalf("X-IDE-Type = %q, want CLI", got)
	}
}

func TestResolveConversationID_MultiTurnAffinity(t *testing.T) {
	turn1 := []byte(`{
		"model": "hy4-preview",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Initial conversation prompt."}
		]
	}`)

	turn2 := []byte(`{
		"model": "hy4-preview",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Initial conversation prompt."},
			{"role": "assistant", "content": "Hello, how can I help?"},
			{"role": "user", "content": "Follow-up question 1."}
		]
	}`)

	cid1 := resolveConversationID(nil, turn1)
	cid2 := resolveConversationID(nil, turn2)

	if cid1 != cid2 {
		t.Fatalf("Conversation IDs differed across turns: %q vs %q", cid1, cid2)
	}
	if len(cid1) != 32 {
		t.Fatalf("Conversation ID length = %d, want 32", len(cid1))
	}
}

func TestPrepareUpstreamBodyInjectsPromptCacheKey(t *testing.T) {
	input := []byte(`{
		"model": "hy4-preview",
		"messages": [
			{"role": "system", "content": "base"},
			{"role": "user", "content": "hello"}
		]
	}`)
	convID := "01a06c42375977789222d96e68daa917"
	out := prepareUpstreamBody(input, nil, &storedAuth{}, "hy4-preview", convID)

	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if got := obj["prompt_cache_key"]; got != convID {
		t.Fatalf("prompt_cache_key = %v, want %q", got, convID)
	}
}
