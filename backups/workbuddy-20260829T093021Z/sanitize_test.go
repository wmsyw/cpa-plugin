package main

import "testing"

func TestSanitizeBlockedTemplates_ClaudeCode(t *testing.T) {
	in := "You are Claude Code, Anthropic's official CLI for Claude."
	out := sanitizeBlockedTemplates(in)
	if out == in {
		t.Fatal("should replace blocked template")
	}
	want := "You are Claude Code, Anthropic's official CLI tool for Claude."
	if out != want {
		t.Fatalf("got %q want %q", out, want)
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

func TestMapOfficialReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct{ model, input, want string }{
		{model: "kimi-k3-1", input: "low", want: "low"},
		{model: "kimi-k3-1", input: "high", want: "high"},
		{model: "kimi-k3-1", input: "max", want: "xhigh"},
		{model: "deepseek-v4-pro", input: "max", want: "xhigh"},
		{model: "deepseek-v4-flash", input: "max", want: "xhigh"},
		{model: "glm-5.3", input: "low", want: "low"},
		{model: "glm-5.3", input: "high", want: "high"},
		{model: "glm-5.3", input: "max", want: "xhigh"},
		{model: "glm-5.3-flash", input: "low", want: "low"},
		{model: "glm-5.3-flash", input: "high", want: "high"},
		{model: "glm-5.3-flash", input: "xhigh", want: "xhigh"},
		{model: "glm-5.3-flash", input: "max", want: "xhigh"},
		{model: "hy3", input: "max", want: "high"},
		{model: "hy4-preview", input: "none", want: "none"},
		{model: "hy4-preview", input: "low", want: "none"},
		{model: "hy4-preview", input: "medium", want: "high"},
		{model: "hy4-preview", input: "high", want: "high"},
		{model: "hy4-preview", input: "xhigh", want: "high"},
		{model: "hy4-preview", input: "max", want: "high"},
		{model: "hy4-preview-x", input: "none", want: "none"},
		{model: "hy4-preview-x", input: "low", want: "none"},
		{model: "hy4-preview-x", input: "medium", want: "high"},
		{model: "hy4-preview-x", input: "high", want: "high"},
		{model: "hy4-preview-x", input: "xhigh", want: "high"},
		{model: "hy4-preview-x", input: "max", want: "high"},
		{model: "hy3-x", input: "low", want: "low"},
		{model: "hy3-x", input: "high", want: "high"},
		{model: "hy3-x", input: "max", want: "high"},
		{model: "glm-5.1", input: "high", want: "high"},
	}
	for _, tt := range tests {
		obj := map[string]any{"reasoning_effort": tt.input}
		mapReasoningEffortInPlace(obj, tt.model)
		if got := obj["reasoning_effort"]; got != tt.want {
			t.Errorf("model %s effort %s mapped to %v, want %s", tt.model, tt.input, got, tt.want)
		}
	}
}
func TestWBModelsAdvertiseOfficialOutputLimits(t *testing.T) {
	t.Parallel()

	want := map[string]int64{
		"glm-5.3": 131072, "glm-5.3-flash": 131072, "glm-5.2": 131072, "glm-5.1": 131072, "glm-5v-turbo": 131072,
		"kimi-k3-1": 131072, "kimi-k2.7": 131072, "minimax-m3": 131072,
		"hy3": 64000, "hy3-x": 64000, "hy3-preview": 64000, "hy3-preview-agent": 64000, "hy4-preview": 64000, "hy4-preview-x": 64000,
		"deepseek-v4-pro": 393216, "deepseek-v4-flash": 393216,
	}
	for _, model := range wbModels() {
		if model.MaxCompletionTokens != want[model.ID] {
			t.Errorf("model %s max output = %d, want %d", model.ID, model.MaxCompletionTokens, want[model.ID])
		}
	}
}

func TestOfficialModelName(t *testing.T) {
	t.Parallel()

	// Upstream ships hy3-x with name "Hy3" (identical to hy3); the plugin
	// must pin a distinct display name so model lists stay unambiguous.
	if got := officialModelName("hy3-x", "Hy3"); got != "Hy3-X" {
		t.Errorf("officialModelName(hy3-x) = %q, want Hy3-X", got)
	}
	if got := officialModelName("hy3", "Hy3"); got != "Hy3" {
		t.Errorf("officialModelName(hy3) = %q, want Hy3", got)
	}
	if got := officialModelName("glm-5.3", "GLM-5.3"); got != "GLM-5.3" {
		t.Errorf("officialModelName(glm-5.3) = %q, want GLM-5.3", got)
	}
	if got := officialModelName("new-model", ""); got != "new-model" {
		t.Errorf("officialModelName(new-model, empty) = %q, want new-model", got)
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
