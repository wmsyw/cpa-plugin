package main

import (
	"encoding/json"
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

func TestWBModelsAdvertiseMaximumSupportedContext(t *testing.T) {
	t.Parallel()

	want := map[string]struct {
		context int64
		output  int64
	}{
		"glm-5.3":           {context: 1000000, output: 131072},
		"glm-5.3-flash":     {context: 1000000, output: 131072},
		"glm-5.2":           {context: 1000000, output: 131072},
		"glm-5.1":           {context: 200000, output: 131072},
		"glm-5v-turbo":      {context: 200000, output: 131072},
		"kimi-k3-1":         {context: 1000000, output: 131072},
		"kimi-k2.7":         {context: 256000, output: 131072},
		"minimax-m3":        {context: 512000, output: 131072},
		"hy3":               {context: 192000, output: 64000},
		"hy3-x":             {context: 192000, output: 64000},
		"hy3-preview":       {context: 192000, output: 64000},
		"hy3-preview-agent": {context: 192000, output: 64000},
		"hy4-preview":       {context: 1000000, output: 64000},
		"hy4-preview-x":     {context: 1000000, output: 64000},
		"deepseek-v4-pro":   {context: 1000000, output: 393216},
		"deepseek-v4-flash": {context: 1000000, output: 393216},
	}
	for _, model := range wbModels() {
		expected, ok := want[model.ID]
		if !ok {
			t.Errorf("unexpected fallback model %q", model.ID)
			continue
		}
		if model.ContextLength != expected.context || model.InputTokenLimit != expected.context {
			t.Errorf("%s context = (%d, %d), want %d", model.ID, model.ContextLength, model.InputTokenLimit, expected.context)
		}
		if model.MaxCompletionTokens != expected.output || model.OutputTokenLimit != expected.output {
			t.Errorf("%s output = (%d, %d), want %d", model.ID, model.MaxCompletionTokens, model.OutputTokenLimit, expected.output)
		}
	}
}

func TestDynamicModelLimitsPreferCurrentCatalog(t *testing.T) {
	t.Parallel()

	if got := dynamicContextLength(1000000, 200000, json.RawMessage(`200000`)); got != 1000000 {
		t.Errorf("dynamicContextLength() = %d, want maxInputTokens", got)
	}
	if got := dynamicContextLength(0, 512000, json.RawMessage(`200000`)); got != 512000 {
		t.Errorf("dynamicContextLength() = %d, want maxAllowedSize", got)
	}
	if got := dynamicContextLength(0, 0, json.RawMessage(`262144`)); got != 262144 {
		t.Errorf("dynamicContextLength() = %d, want legacy contextWindow", got)
	}
	if got := dynamicMaxCompletionTokens(32000, json.RawMessage(`131072`)); got != 32000 {
		t.Errorf("dynamicMaxCompletionTokens() = %d, want maxOutputTokens", got)
	}
	if got := dynamicMaxCompletionTokens(0, json.RawMessage(`131072`)); got != 131072 {
		t.Errorf("dynamicMaxCompletionTokens() = %d, want legacy maxTokens", got)
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
