package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveConversationID_ExplicitHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Conversation-ID", "01a06c42-3759-7778-9222-d96e68daa917")
	got := resolveConversationID(headers, nil)
	want := "01a06c42375977789222d96e68daa917"
	if got != want {
		t.Fatalf("resolveConversationID = %q, want %q", got, want)
	}

	headers2 := http.Header{}
	headers2.Set("X-Session-ID", "my-custom-session-12345678901234567890")
	got2 := resolveConversationID(headers2, nil)
	if len(got2) != 32 {
		t.Fatalf("resolveConversationID len = %d, want 32", len(got2))
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

	turn3 := []byte(`{
		"model": "hy4-preview",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Initial conversation prompt."},
			{"role": "assistant", "content": "Hello, how can I help?"},
			{"role": "user", "content": "Follow-up question 1."},
			{"role": "assistant", "content": "Here is the answer."},
			{"role": "user", "content": "Follow-up question 2."}
		]
	}`)

	cid1 := resolveConversationID(nil, turn1)
	cid2 := resolveConversationID(nil, turn2)
	cid3 := resolveConversationID(nil, turn3)

	if cid1 != cid2 || cid2 != cid3 {
		t.Fatalf("Conversation IDs differed across turns: %q vs %q vs %q", cid1, cid2, cid3)
	}
	if len(cid1) != 32 {
		t.Fatalf("Conversation ID length = %d, want 32", len(cid1))
	}

	diffTurn := []byte(`{
		"model": "hy4-preview",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "A completely different user prompt."}
		]
	}`)
	diffCID := resolveConversationID(nil, diffTurn)
	if diffCID == cid1 {
		t.Fatalf("Different conversation produced same ID: %q", diffCID)
	}
}

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
