package main

import "testing"

func TestSanitizeBlockedTemplates_ClaudeCode(t *testing.T) {
	in := "You are Claude Code, Anthropic's official CLI for Claude.\nUse the repository."
	got := sanitizeBlockedTemplates(in)
	want := "You are an expert software engineering agent.\nUse the repository."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSanitizeBlockedTemplates_MainBranch(t *testing.T) {
	in := "Main branch (you will usually use this for PRs)"
	out := sanitizeBlockedTemplates(in)
	if out == in {
		t.Fatal("should replace Main branch")
	}
}

func TestSanitizeBlockedTemplates_NoMatch(t *testing.T) {
	in := "Hello world"
	out := sanitizeBlockedTemplates(in)
	if out != in {
		t.Fatal("should pass through unchanged")
	}
}

func TestRewriteSystemInPlaceNeutralizesOnlyIdentityContext(t *testing.T) {
	const identity = "You are Claude Code, Anthropic's official CLI for Claude."
	obj := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": identity + "\nFollow repository instructions."},
			map[string]any{"role": "assistant", "content": "Acknowledged. " + identity},
			map[string]any{"role": "user", "content": identity},
			map[string]any{"role": "tool", "content": identity},
		},
	}

	if !rewriteSystemInPlace(obj) {
		t.Fatal("expected identity context rewrite")
	}
	messages := obj["messages"].([]any)
	if got := messages[0].(map[string]any)["content"]; got != "You are an expert software engineering agent.\nFollow repository instructions." {
		t.Fatalf("system content = %q", got)
	}
	if got := messages[1].(map[string]any)["content"]; got != "Acknowledged. You are an expert software engineering agent." {
		t.Fatalf("assistant content = %q", got)
	}
	for _, index := range []int{2, 3} {
		if got := messages[index].(map[string]any)["content"]; got != identity {
			t.Fatalf("role %s content changed: %q", messages[index].(map[string]any)["role"], got)
		}
	}
}

func TestRewriteSystemInPlaceNeutralizesMultimodalAssistantText(t *testing.T) {
	obj := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.invalid/image"}},
				},
			},
		},
	}

	if !rewriteSystemInPlace(obj) {
		t.Fatal("expected multimodal assistant identity rewrite")
	}
	part := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if got := part["text"]; got != "You are an expert software engineering agent." {
		t.Fatalf("text part = %q", got)
	}
}

func TestMapOfficialReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct{ model, input, want string }{
		{model: "hy4-preview", input: "none", want: "none"},
		{model: "hy4-preview", input: "low", want: "none"},
		{model: "hy4-preview", input: "medium", want: "high"},
		{model: "hy4-preview", input: "high", want: "high"},
		{model: "hy4-preview", input: "xhigh", want: "high"},
		{model: "hy4-preview", input: "max", want: "high"},
		{model: "hy3", input: "max", want: "high"},
		{model: "gpt-5.6-sol", input: "max", want: "max"},
		{model: "gpt-5.6-terra", input: "xhigh", want: "xhigh"},
		{model: "gpt-5.6-luna", input: "low", want: "low"},
		{model: "gpt-5.5", input: "max", want: "xhigh"},
		{model: "gpt-5.4", input: "max", want: "xhigh"},
		{model: "gpt-5.3-codex", input: "max", want: "xhigh"},
		{model: "gemini-3.5-flash", input: "none", want: "minimal"},
		{model: "gemini-3.5-flash", input: "low", want: "low"},
		{model: "gemini-3.5-flash", input: "xhigh", want: "high"},
		{model: "gemini-3.5-flash", input: "max", want: "max"},
		{model: "glm-5.3", input: "max", want: "xhigh"},
		{model: "glm-5.2", input: "max", want: "xhigh"},
		{model: "kimi-k3", input: "max", want: "max"},
		{model: "kimi-k2.6", input: "max", want: "max"},
		{model: "minimax-m3", input: "max", want: "max"},
	}
	for _, tt := range tests {
		obj := map[string]any{"reasoning_effort": tt.input}
		mapReasoningEffortInPlace(obj, tt.model)
		if got := obj["reasoning_effort"]; got != tt.want {
			t.Errorf("model %s effort %s mapped to %v, want %s", tt.model, tt.input, got, tt.want)
		}
	}
}

func TestMapOfficialReasoningEffortLeavesUnknownModelUnchanged(t *testing.T) {
	t.Parallel()

	obj := map[string]any{"reasoning_effort": "max"}
	if changed := mapReasoningEffortInPlace(obj, "unknown"); changed {
		t.Fatal("unknown model should not be rewritten")
	}
	if got := obj["reasoning_effort"]; got != "max" {
		t.Fatalf("unknown model effort = %v, want max", got)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Fatal("short string should be unchanged")
	}
	if truncate("hello world", 5) != "hello" {
		t.Fatal("should truncate to 5 chars")
	}
	if truncate("", 5) != "" {
		t.Fatal("empty string")
	}
}
