package main

import (
	"net/http"
	"testing"
)

func TestBackendHeadersDoesNotSetConversationID(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	backendHeaders(req, &storedAuth{})
	if got := req.Header.Get("X-Conversation-ID"); got != "" {
		t.Fatalf("X-Conversation-ID = %q, want absent", got)
	}
}
