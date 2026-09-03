package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestPublicModelsHideUpstreamIDs(t *testing.T) {
	t.Parallel()

	models := publicModels(wbModels())
	want := map[string]struct{}{
		"glm-5.3": {}, "glm-5.3-flash": {}, "glm-5.2": {}, "glm-5.1": {}, "glm-5v-turbo": {}, "kimi-k3": {},
		"kimi-k2.7": {}, "minimax-m3": {}, "hy3": {}, "hy3-x": {}, "hy3-preview": {}, "hy4-preview": {}, "hy4-preview-x": {},
		"hy3-preview-agent": {}, "deepseek-v4-pro": {}, "deepseek-v4-flash": {},
	}
	if len(models) != len(want) {
		t.Fatalf("public model count = %d, want %d", len(models), len(want))
	}
	for _, model := range models {
		if _, ok := want[model.ID]; !ok {
			t.Errorf("unexpected public model ID %q", model.ID)
		}
	}
	if got := resolveUpstreamModel("kimi-k3", nil); got != "kimi-k3-1" {
		t.Errorf("resolveUpstreamModel() = %q, want kimi-k3-1", got)
	}
	if got := resolveUpstreamModel("glm-5.3-flash", nil); got != "glm-5.3-flash" {
		t.Errorf("resolveUpstreamModel(glm-5.3-flash) = %q, want glm-5.3-flash", got)
	}
}

func TestEnsureVerifiedChatFallbacksAddsEachModelOnce(t *testing.T) {
	t.Parallel()

	models := ensureVerifiedChatFallbacks([]pluginapi.ModelInfo{{ID: "glm-5.3", Name: "GLM-5.3"}})
	if len(models) != 4 {
		t.Fatalf("model count = %d, want 4", len(models))
	}

	want := map[string]struct {
		display string
		context int64
		output  int64
	}{
		"glm-5.3-flash": {display: "GLM-5.3-Flash", context: 1000000, output: 131072},
		"hy4-preview":   {display: "Hy4 Preview", context: 1000000, output: 64000},
		"hy4-preview-x": {display: "Hy4 Preview X", context: 1000000, output: 64000},
	}
	for id, expected := range want {
		var found *pluginapi.ModelInfo
		for i := range models {
			if models[i].ID == id {
				found = &models[i]
				break
			}
		}
		if found == nil {
			t.Errorf("%s fallback missing", id)
			continue
		}
		if found.DisplayName != expected.display {
			t.Errorf("%s display name = %q, want %q", id, found.DisplayName, expected.display)
		}
		if found.ContextLength != expected.context {
			t.Errorf("%s context length = %d, want %d", id, found.ContextLength, expected.context)
		}
		if found.MaxCompletionTokens != expected.output {
			t.Errorf("%s max output = %d, want %d", id, found.MaxCompletionTokens, expected.output)
		}
	}

	models = ensureVerifiedChatFallbacks(models)
	counts := make(map[string]int)
	for _, model := range models {
		counts[model.ID]++
	}
	for id := range want {
		if counts[id] != 1 {
			t.Errorf("%s fallback count = %d, want 1", id, counts[id])
		}
	}
}
