package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestBuildQoderBodyContextWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		modelKey string
		want     int64
	}{
		{modelKey: "qmodel_38max", want: oneMillionContextWindow},
		{modelKey: "qmodel_latest", want: oneMillionContextWindow},
		{modelKey: "qmodel", want: oneMillionContextWindow},
		{modelKey: "q37fmodel", want: oneMillionContextWindow},
		{modelKey: "dmodel", want: oneMillionContextWindow},
		{modelKey: "dfmodel", want: oneMillionContextWindow},
		{modelKey: "gmodel", want: oneMillionContextWindow},
		{modelKey: "gm51model", want: oneMillionContextWindow},
		{modelKey: "auto"},
		{modelKey: "kmodel"},
		{modelKey: "mmodel"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.modelKey, func(t *testing.T) {
			t.Parallel()

			raw, err := buildQoderBodyForMode(&openAIRequest{
				Messages: []json.RawMessage{json.RawMessage(`{"role":"user","content":"test"}`)},
			}, tt.modelKey, "personal_standard", promptModeProxy)
			if err != nil {
				t.Fatalf("buildQoderBodyForMode() error = %v", err)
			}

			var payload struct {
				Parameters map[string]json.RawMessage `json:"parameters"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}

			contextRaw, exists := payload.Parameters["context_length"]
			if tt.want == 0 {
				if exists {
					t.Fatalf("context_length unexpectedly set to %s", contextRaw)
				}
				return
			}
			if !exists {
				t.Fatal("context_length missing")
			}
			var got int64
			if err := json.Unmarshal(contextRaw, &got); err != nil {
				t.Fatalf("decode context_length: %v", err)
			}
			if got != tt.want {
				t.Fatalf("context_length = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWBModelsAdvertisePreferredContextWindow(t *testing.T) {
	t.Parallel()

	want := map[string]int64{
		"auto":          180000,
		"qmodel_38max":  oneMillionContextWindow,
		"qmodel_latest": oneMillionContextWindow,
		"qmodel":        oneMillionContextWindow,
		"q37fmodel":     oneMillionContextWindow,
		"dmodel":        oneMillionContextWindow,
		"dfmodel":       oneMillionContextWindow,
		"gmodel":        oneMillionContextWindow,
		"gm51model":     oneMillionContextWindow,
		"kmodel":        180000,
		"mmodel":        180000,
	}
	for _, model := range wbModels() {
		if model.ContextLength != want[model.ID] {
			t.Errorf("model %s context length = %d, want %d", model.ID, model.ContextLength, want[model.ID])
		}
	}
}

func TestContextConfigHasWindow(t *testing.T) {
	t.Parallel()

	config := map[string]contextWindowOption{
		"200K": {TokenCount: 200000, IsDefault: true},
		"1M":   {TokenCount: oneMillionContextWindow},
	}
	if !contextConfigHasWindow(config, oneMillionContextWindow) {
		t.Fatal("1M context option was not detected")
	}
	if contextConfigHasWindow(config, 400000) {
		t.Fatal("missing 400K context option was reported as present")
	}
}
func TestWBModelsAdvertiseOfficialOutputLimits(t *testing.T) {
	t.Parallel()

	want := map[string]int64{
		"auto": 32000, "qmodel_38max": 131072, "qmodel_latest": 131072,
		"qmodel": 131072, "q37fmodel": 131072, "dmodel": 393216,
		"dfmodel": 393216, "gmodel": 131072, "gm51model": 131072,
		"kmodel": 131072, "mmodel": 131072,
	}
	for _, model := range wbModels() {
		if model.MaxCompletionTokens != want[model.ID] {
			t.Errorf("model %s max output = %d, want %d", model.ID, model.MaxCompletionTokens, want[model.ID])
		}
	}
}
func TestPublicModelsHideUpstreamIDs(t *testing.T) {
	t.Parallel()

	models := publicModels(append(wbModels(), pluginapi.ModelInfo{ID: "q36fmodel", Name: "Qwen3.6-Flash"}))
	want := map[string]struct{}{
		"auto": {}, "qwen3.8-max": {}, "qwen3.7-max": {}, "qwen3.7-plus": {},
		"qwen3.7-flash": {}, "deepseek-v4-pro": {}, "deepseek-v4-flash": {},
		"glm-5.3": {}, "glm-5.2": {}, "kimi-k2.7-code": {}, "minimax-m2.7": {},
	}
	if len(models) != len(want) {
		t.Fatalf("public model count = %d, want %d", len(models), len(want))
	}
	for _, model := range models {
		if _, ok := want[model.ID]; !ok {
			t.Errorf("unexpected public model ID %q", model.ID)
		}
	}
	if got := resolveUpstreamModel("qwen3.7-flash", nil); got != "q37fmodel" {
		t.Errorf("resolveUpstreamModel() = %q, want q37fmodel", got)
	}
	if got := resolveUpstreamModel("deepseek-v4-flash", nil); got != "dfmodel" {
		t.Errorf("resolveUpstreamModel() = %q, want dfmodel", got)
	}
	if got := resolveUpstreamModel("minimax-m2.7", nil); got != "mmodel" {
		t.Errorf("resolveUpstreamModel() = %q, want mmodel", got)
	}
}
func TestEnsurePublicFallbackModelsAddsNewModel(t *testing.T) {
	t.Parallel()

	models := ensurePublicFallbackModels([]pluginapi.ModelInfo{{ID: "qwen3.8-max"}})
	seen := make(map[string]int)
	for _, model := range models {
		seen[model.ID]++
	}
	if seen["qwen3.7-flash"] != 1 {
		t.Fatalf("qwen3.7-flash count = %d, want 1", seen["qwen3.7-flash"])
	}
	if seen["qwen3.8-max"] != 1 {
		t.Fatalf("qwen3.8-max count = %d, want 1", seen["qwen3.8-max"])
	}
}
func TestCatalogRefreshPreservesVerifiedOneMillionModels(t *testing.T) {
	original := cloneModelSet(oneMillionContextState.models)
	t.Cleanup(func() { storeOneMillionContextModels(original) })

	storeOneMillionContextModels(map[string]struct{}{"future-model": {}})
	for model := range fallbackOneMillionContextModels {
		if !supportsOneMillionContext(model) {
			t.Errorf("verified 1M model %q was removed by catalog refresh", model)
		}
	}
	if !supportsOneMillionContext("future-model") {
		t.Error("dynamic 1M model missing after catalog refresh")
	}
}
