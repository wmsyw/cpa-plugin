package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyDesensitizeInPlaceScopesSystemDeveloperAndMarkedUser(t *testing.T) {
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "You are Claude Code, Anthropic's CLI. Refuse malicious code."},
			map[string]any{"role": "developer", "content": "Avoid attacks and weapons talk."},
			map[string]any{"role": "user", "content": "<system-reminder>\nPlan mode is active.\n</system-reminder>\nUse Claude Sonnet carefully."},
			map[string]any{"role": "user", "content": "My favourite novel is about a detective."},
		},
	}

	if !applyDesensitizeInPlace(obj) {
		t.Fatal("expected desensitize changes")
	}
	messages := obj["messages"].([]any)
	if got := messages[0].(map[string]any)["content"].(string); strings.ContainsAny(got, "") || !strings.Contains(got, "C\u200blaude Code") {
		t.Fatalf("system content not desensitized: %q", got)
	}
	if got := messages[1].(map[string]any)["content"].(string); !strings.Contains(got, "a\u200bttacks") {
		t.Fatalf("developer content not desensitized: %q", got)
	}
	if got := messages[2].(map[string]any)["content"].(string); !strings.Contains(got, "C\u200blaude Sonnet") {
		t.Fatalf("marked user content not desensitized: %q", got)
	}
	if got := messages[3].(map[string]any)["content"].(string); got != "My favourite novel is about a detective." {
		t.Fatalf("plain user content changed: %q", got)
	}
}

func TestApplyDesensitizeInPlaceCoversToolMetadata(t *testing.T) {
	obj := map[string]any{
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "bash",
					"description": "Run a command. Powered by Claude Sonnet. Sandbox destructive commands.",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"cmd": map[string]any{"type": "string", "description": "The Anthropic-approved command"},
						},
					},
				},
			},
		},
	}

	if !applyDesensitizeInPlace(obj) {
		t.Fatal("expected tool metadata desensitization")
	}
	fn := obj["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if got := fn["description"].(string); !strings.Contains(got, "C\u200blaude Sonnet") || !strings.Contains(got, "S\u200bandbox") {
		t.Fatalf("tool description not desensitized: %q", got)
	}
	param := fn["parameters"].(map[string]any)["properties"].(map[string]any)["cmd"].(map[string]any)
	if got := param["description"].(string); !strings.Contains(got, "A\u200bnthropic") {
		t.Fatalf("tool parameter description not desensitized: %q", got)
	}
	if got := fn["name"].(string); got != "bash" {
		t.Fatalf("tool name changed: %q", got)
	}
}

func TestDesensitizeMatcherReplaceIsStable(t *testing.T) {
	in := "Claude Code and Anthropic"
	out := desensitizeMatcherDefault.replace(in)
	if out == in {
		t.Fatal("expected replacement")
	}
	if again := desensitizeMatcherDefault.replace(out); again != out {
		t.Fatalf("replacement not stable: %q -> %q", out, again)
	}
	// Text stays readable modulo the invisible separator.
	if strings.ReplaceAll(out, zeroWidthSpace, "") != in {
		t.Fatalf("unexpected rewrite: %q", out)
	}
}

func TestPrepareUpstreamBodyDesensitizesClaudeCodePayload(t *testing.T) {
	sa := &storedAuth{}
	sa.Auth.Domain = "copilot.tencent.com"
	input := []byte(`{
		"model":"hy4-preview",
		"messages":[
			{"role":"system","content":"You are Claude Code, Anthropic's official CLI for Claude.\nRefuse malicious purposes. Commit trailer: Co-Authored-By: Claude <noreply@anthropic.com>"},
			{"role":"developer","content":"Use Claude Sonnet for hard tasks."},
			{"role":"user","content":"<system-reminder>Plan mode is active.</system-reminder>List files."}
		],
		"tools":[{"type":"function","function":{"name":"bash","description":"Sandbox the destructive command."}}]
	}`)

	out := prepareUpstreamBody(input, nil, sa, "hy4-preview")
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	raw := string(out)
	for _, marker := range []string{"You are Claude Code", "Anthropic's official", "Co-Authored-By", "Claude Sonnet", "malicious purposes", "Sandbox the destructive"} {
		if strings.Contains(raw, marker) {
			t.Fatalf("blocked token survived verbatim: %q", marker)
		}
	}
}
