package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestPublicModelsMatchGlobalCatalog(t *testing.T) {
	t.Parallel()

	models := publicModels(wbModels())
	want := map[string]struct{}{
		"hy4-preview": {}, "hy3": {}, "gpt-5.6-sol": {}, "gpt-5.6-terra": {},
		"gpt-5.6-luna": {}, "gpt-5.5": {}, "gpt-5.4": {}, "gpt-5.3-codex": {},
		"gemini-3.5-flash": {}, "glm-5.3": {}, "glm-5.2": {}, "kimi-k3": {},
		"kimi-k2.6": {}, "minimax-m3": {},
	}
	if len(models) != len(want) {
		t.Fatalf("public model count = %d, want %d", len(models), len(want))
	}
	for _, model := range models {
		if _, ok := want[model.ID]; !ok {
			t.Errorf("unexpected public model ID %q", model.ID)
		}
	}
	if got := resolveUpstreamModel("kimi-k3", nil); got != "kimi-k3" {
		t.Errorf("resolveUpstreamModel() = %q, want kimi-k3", got)
	}
	if got := resolveUpstreamModel("glm-5.3", nil); got != "glm-5.3" {
		t.Errorf("resolveUpstreamModel(glm-5.3) = %q, want glm-5.3", got)
	}
}

func TestEnsureVerifiedChatFallbacksAddsGlobalCatalogOnce(t *testing.T) {
	t.Parallel()

	models := ensureVerifiedChatFallbacks(nil)
	if len(models) != len(wbModels()) {
		t.Fatalf("model count = %d, want %d", len(models), len(wbModels()))
	}
	for _, expected := range wbModels() {
		var found *pluginapi.ModelInfo
		for i := range models {
			if models[i].ID == expected.ID {
				found = &models[i]
				break
			}
		}
		if found == nil {
			t.Errorf("%s fallback missing", expected.ID)
			continue
		}
		if found.DisplayName != expected.DisplayName {
			t.Errorf("%s display name = %q, want %q", expected.ID, found.DisplayName, expected.DisplayName)
		}
		if found.ContextLength != expected.ContextLength {
			t.Errorf("%s context length = %d, want %d", expected.ID, found.ContextLength, expected.ContextLength)
		}
		if found.MaxCompletionTokens != expected.MaxCompletionTokens {
			t.Errorf("%s max output = %d, want %d", expected.ID, found.MaxCompletionTokens, expected.MaxCompletionTokens)
		}
	}

	models = ensureVerifiedChatFallbacks(models)
	counts := make(map[string]int)
	for _, model := range models {
		counts[model.ID]++
	}
	for _, expected := range wbModels() {
		if counts[expected.ID] != 1 {
			t.Errorf("%s fallback count = %d, want 1", expected.ID, counts[expected.ID])
		}
	}
}
