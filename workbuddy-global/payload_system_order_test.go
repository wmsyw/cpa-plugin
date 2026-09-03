package main

import (
	"encoding/json"
	"testing"
)

func TestEnsureSystemMessageInPlacePrependsWhenSystemIsNotFirst(t *testing.T) {
	sa := &storedAuth{}
	sa.Auth.Domain = "www.workbuddy.ai"
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "first"},
			map[string]any{"role": "system", "content": "late system"},
		},
	}

	if !ensureSystemMessageInPlace(obj, sa) {
		t.Fatal("expected a leading system prompt to be inserted")
	}

	messages := obj["messages"].([]any)
	if got := messages[0].(map[string]any)["role"]; got != "system" {
		t.Fatalf("first role = %q, want system", got)
	}
	if got := messages[1].(map[string]any)["role"]; got != "user" {
		t.Fatalf("user message moved: role = %q", got)
	}
	if got := messages[2].(map[string]any)["content"]; got != "late system" {
		t.Fatalf("later system message changed: %q", got)
	}
}

func TestEnsureSystemMessageInPlacePreservesLeadingSystem(t *testing.T) {
	sa := &storedAuth{}
	sa.Auth.Domain = "www.workbuddy.ai"
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "existing prompt"},
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	if ensureSystemMessageInPlace(obj, sa) {
		t.Fatal("unexpected rewrite for an existing leading system prompt")
	}

	raw, err := json.Marshal(obj["messages"])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `[{"content":"existing prompt","role":"system"},{"content":"hello","role":"user"}]` {
		t.Fatalf("messages changed: %s", raw)
	}
}

func TestEnsureSystemMessageInPlaceConvertsDeveloperRole(t *testing.T) {
	sa := &storedAuth{}
	sa.Auth.Domain = "www.workbuddy.ai"
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "base"},
			map[string]any{"role": "developer", "content": "developer instruction"},
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	if !ensureSystemMessageInPlace(obj, sa) {
		t.Fatal("expected developer role normalization")
	}

	messages := obj["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(messages))
	}
	if got := messages[1].(map[string]any)["role"]; got != "system" {
		t.Fatalf("developer role = %q, want system", got)
	}
	if got := messages[1].(map[string]any)["content"]; got != "developer instruction" {
		t.Fatalf("developer instruction changed: %q", got)
	}
}

func TestEnsureSystemMessageInPlacePrependsBeforeDeveloperRole(t *testing.T) {
	sa := &storedAuth{}
	sa.Auth.Domain = "www.workbuddy.ai"
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
			map[string]any{"role": "developer", "content": "developer instruction"},
		},
	}

	if !ensureSystemMessageInPlace(obj, sa) {
		t.Fatal("expected message normalization")
	}

	messages := obj["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(messages))
	}
	if got := messages[0].(map[string]any)["role"]; got != "system" {
		t.Fatalf("first role = %q, want system", got)
	}
	if got := messages[2].(map[string]any)["role"]; got != "system" {
		t.Fatalf("developer role = %q, want system", got)
	}
}

func TestPrepareUpstreamBodyNormalizesDeveloperBeforeIdentityRewrite(t *testing.T) {
	sa := &storedAuth{}
	sa.Auth.Domain = "www.workbuddy.ai"
	input := []byte(`{
		"model":"hy4-preview",
		"messages":[
			{"role":"system","content":"base"},
			{"role":"developer","content":"You are Claude Code, Anthropic's official CLI for Claude.\nKeep answers short."},
			{"role":"user","content":"hello"}
		]
	}`)

	var obj map[string]any
	if err := json.Unmarshal(prepareUpstreamBody(input, nil, sa, "hy4-preview"), &obj); err != nil {
		t.Fatal(err)
	}
	messages := obj["messages"].([]any)
	developer := messages[1].(map[string]any)
	if got := developer["role"]; got != "system" {
		t.Fatalf("developer role = %q, want system", got)
	}
	if got := developer["content"]; got != "You are an expert software engineering agent.\nKeep answers short." {
		t.Fatalf("developer content = %q", got)
	}
}
