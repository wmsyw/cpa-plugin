package main

import "testing"

func TestMapOfficialReasoningEffortCN(t *testing.T) {
	t.Parallel()

	tests := []struct{ model, input, want string }{
		{model: "hy4-preview", input: "none", want: "none"},
		{model: "hy4-preview", input: "low", want: "none"},
		{model: "hy4-preview", input: "medium", want: "high"},
		{model: "hy4-preview", input: "high", want: "high"},
		{model: "hy4-preview", input: "xhigh", want: "high"},
		{model: "hy4-preview", input: "max", want: "high"},
		{model: "hy3", input: "max", want: "high"},
		{model: "hy3-x", input: "max", want: "high"},
		{model: "glm-5.3", input: "max", want: "xhigh"},
		{model: "glm-5.3-flash", input: "max", want: "xhigh"},
		{model: "glm-5.2", input: "max", want: "xhigh"},
		{model: "kimi-k3-1", input: "max", want: "xhigh"},
		// Same xhigh-group rule as kimi-k3-1; upstream not yet routable
		// (11102 as of 2026-09-12), mapping pre-provisioned per user call.
		{model: "kimi-k2.8-preview", input: "max", want: "xhigh"},
		{model: "kimi-k2.8-preview", input: "low", want: "low"},
		{model: "deepseek-v4-pro", input: "max", want: "xhigh"},
		// Same family rule as deepseek-v4-pro; xhigh accepted by the CN
		// gateway (live-probed 2026-09-10).
		{model: "deepseek-v4.1-flash", input: "max", want: "xhigh"},
		{model: "deepseek-v4.1-flash", input: "low", want: "low"},
		{model: "kimi-k2.7", input: "max", want: "max"},
		{model: "minimax-m3", input: "max", want: "max"},
		{model: "glm-5.1", input: "max", want: "max"},
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
