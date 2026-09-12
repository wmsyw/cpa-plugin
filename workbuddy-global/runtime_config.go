// runtime_config.go carries the plugin feature state that drives the model
// runtime: the optional config_yaml `models` static override list. (The CN
// plugin folds this into its desensitize feature runtime; this plugin keeps
// desensitize always-on and only tracks configured models.)
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// guardExecutorReadiness blocks executor traffic until the auth's model
// catalog bootstrap reaches an executable state (ready or stale).
func guardExecutorReadiness(authID string) []byte {
	if currentModelRuntime().snapshotForAuthID(authID).State.executable() {
		return nil
	}
	return errorEnvelopeWithStatus(
		"not_ready",
		"WorkBuddy model catalog is not ready",
		http.StatusServiceUnavailable,
	)
}

func errorEnvelopeWithStatus(code, message string, status int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: status}})
	return raw
}

type featureRuntimeConfig struct {
	configuredModels []string
}

var featureRuntime atomic.Pointer[featureRuntimeConfig]

func init() {
	cfg, err := parseFeatureRuntime(nil)
	if err != nil {
		panic(err)
	}
	featureRuntime.Store(cfg)
}

func currentFeatureRuntime() *featureRuntimeConfig {
	cfg := featureRuntime.Load()
	if cfg == nil {
		return nil
	}
	snapshot := *cfg
	snapshot.configuredModels = append([]string(nil), cfg.configuredModels...)
	return &snapshot
}

type featureConfigYAML struct {
	Models yaml.Node `yaml:"models"`
}

// parseFeatureRuntime parses the config_yaml `models` static override. The
// list is validated with the same rules as the CN plugin: plain string
// sequence, single-line, non-empty, unique, within the ID length cap.
func parseFeatureRuntime(raw []byte) (*featureRuntimeConfig, error) {
	var doc featureConfigYAML
	if strings.TrimSpace(string(raw)) != "" {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, errors.New("invalid config_yaml")
		}
	}
	models, err := normalizedConfiguredModels(doc.Models)
	if err != nil {
		return nil, err
	}
	return &featureRuntimeConfig{configuredModels: models}, nil
}

func normalizedConfiguredModels(node yaml.Node) ([]string, error) {
	if node.Kind == 0 {
		return nil, nil
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" && node.Style&yaml.TaggedStyle == 0 {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" || node.Style&yaml.TaggedStyle != 0 {
		return nil, errors.New("models must be an array of strings")
	}
	models := make([]string, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for i, entry := range node.Content {
		if entry.Kind != yaml.ScalarNode || entry.Tag != "!!str" || entry.Style&yaml.TaggedStyle != 0 {
			return nil, errors.New("models entries must be strings")
		}
		if strings.IndexFunc(entry.Value, func(r rune) bool {
			return r == '\r' || r == '\n' || r == 0x85 || r == 0x2028 || r == 0x2029
		}) >= 0 {
			return nil, errors.New("models entries must be single-line strings")
		}
		id := strings.TrimSpace(entry.Value)
		if id == "" {
			return nil, errors.New("models entries must not be empty")
		}
		if len(id) > maxDiscoveredModelIDBytes {
			return nil, errors.New("models entry exceeds maximum ID length")
		}
		if _, exists := seen[id]; exists {
			return nil, errors.New("models entries must not be duplicated")
		}
		seen[id] = struct{}{}
		models[i] = id
	}
	return models, nil
}
