package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrepareUpstreamBodyNormalizesDeveloperBeforeIdentityRewrite(t *testing.T) {
	sa := &storedAuth{}
	sa.Auth.Domain = "copilot.tencent.com"
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
	content, _ := developer["content"].(string)
	// The desensitize layer inserts U+200B after the first rune of blocked
	// tokens; compare modulo that invisible separator.
	if normalized := strings.ReplaceAll(content, zeroWidthSpace, ""); normalized != "You are Claude Code, Anthropic's official CLI tool for Claude.\nKeep answers short." {
		t.Fatalf("developer content = %q", content)
	}
	if obj["stream"] != true {
		t.Fatalf("stream = %v, want true", obj["stream"])
	}
}

func TestNormalizeDeveloperRoleInPlacePreservesOrder(t *testing.T) {
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "developer", "content": "first"},
			map[string]any{"role": "user", "content": "second"},
			map[string]any{"role": "DEVELOPER", "content": "third"},
		},
	}

	if !normalizeDeveloperRoleInPlace(obj) {
		t.Fatal("expected developer role normalization")
	}
	messages := obj["messages"].([]any)
	want := []string{"system", "user", "system"}
	for i, role := range want {
		if got := messages[i].(map[string]any)["role"]; got != role {
			t.Fatalf("message %d role = %q, want %q", i, got, role)
		}
	}
}

func TestNormalizeDeveloperRoleInPlaceNoopWithoutDeveloper(t *testing.T) {
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "base"},
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	if normalizeDeveloperRoleInPlace(obj) {
		t.Fatal("unexpected rewrite without developer messages")
	}
}
