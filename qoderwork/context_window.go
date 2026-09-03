package main

import "sync"

const oneMillionContextWindow int64 = 1_000_000

type contextWindowOption struct {
	TokenCount int64 `json:"token_count"`
	IsDefault  bool  `json:"is_default"`
}

var fallbackOneMillionContextModels = map[string]struct{}{
	"qmodel_38max":  {},
	"qmodel_latest": {},
	"qmodel":        {},
	"q37fmodel":     {},
	"dmodel":        {},
	"dfmodel":       {},
	"gmodel":        {},
	"gm51model":     {},
}

var oneMillionContextState = struct {
	sync.RWMutex
	models map[string]struct{}
}{models: cloneModelSet(fallbackOneMillionContextModels)}

func cloneModelSet(source map[string]struct{}) map[string]struct{} {
	cloned := make(map[string]struct{}, len(source))
	for model := range source {
		cloned[model] = struct{}{}
	}
	return cloned
}

func fallbackSupportsOneMillionContext(modelKey string) bool {
	_, ok := fallbackOneMillionContextModels[modelKey]
	return ok
}

// supportsOneMillionContext uses the latest successful model-catalog response.
// The verified static set is only the initial/API-failure fallback.
func supportsOneMillionContext(modelKey string) bool {
	oneMillionContextState.RLock()
	_, ok := oneMillionContextState.models[modelKey]
	oneMillionContextState.RUnlock()
	return ok
}

func storeOneMillionContextModels(models map[string]struct{}) {
	merged := cloneModelSet(fallbackOneMillionContextModels)
	for model := range models {
		merged[model] = struct{}{}
	}
	oneMillionContextState.Lock()
	oneMillionContextState.models = merged
	oneMillionContextState.Unlock()
}

func contextConfigHasWindow(config map[string]contextWindowOption, window int64) bool {
	for _, option := range config {
		if option.TokenCount == window {
			return true
		}
	}
	return false
}

func preferredContextLength(modelKey string, fallback int64) int64 {
	if supportsOneMillionContext(modelKey) {
		return oneMillionContextWindow
	}
	return fallback
}

func fallbackContextLength(modelKey string, fallback int64) int64 {
	if fallbackSupportsOneMillionContext(modelKey) {
		return oneMillionContextWindow
	}
	return fallback
}

func applyPreferredContextWindow(payload map[string]any, modelKey string) {
	if !supportsOneMillionContext(modelKey) {
		return
	}
	parameters, ok := payload["parameters"].(map[string]any)
	if !ok {
		parameters = make(map[string]any, 1)
		payload["parameters"] = parameters
	}
	parameters["context_length"] = oneMillionContextWindow
}
