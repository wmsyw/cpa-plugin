// models.go implements the ModelProvider capability: static and per-auth
// model lists, dynamic model discovery via the upstream models API, alias
// reverse resolution (client-facing alias → upstream model id), and the
// host-config oauth-excluded-models filter.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var publicModelIDs = map[string]string{
	"kimi-k3-1": "kimi-k3",
}

func publicModelID(upstreamID string) string {
	if id, ok := publicModelIDs[upstreamID]; ok {
		return id
	}
	return upstreamID
}

func publicModels(models []pluginapi.ModelInfo) []pluginapi.ModelInfo {
	out := make([]pluginapi.ModelInfo, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model.ID = publicModelID(model.ID)
		key := strings.ToLower(model.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	return out
}

func wbModels() []pluginapi.ModelInfo {
	mk := func(id, name string, ctxLen, maxTok int64) pluginapi.ModelInfo {
		return pluginapi.ModelInfo{
			ID:                         id,
			Name:                       name,
			DisplayName:                name,
			InputTokenLimit:            ctxLen,
			OutputTokenLimit:           maxTok,
			ContextLength:              ctxLen,
			MaxCompletionTokens:        maxTok,
			OwnedBy:                    providerName,
			SupportedGenerationMethods: []string{"chat"},
		}
	}
	return []pluginapi.ModelInfo{
		mk("glm-5.3", "GLM-5.3", 1000000, 131072),
		mk("glm-5.3-flash", "GLM-5.3-Flash", 1000000, 131072),
		mk("glm-5.2", "GLM-5.2", 1000000, 131072),
		mk("glm-5.1", "GLM-5.1", 200000, 131072),
		mk("glm-5v-turbo", "GLM-5V Turbo", 200000, 131072),
		mk("kimi-k3-1", "Kimi-K3", 1000000, 131072),
		mk("kimi-k2.7", "Kimi K2.7", 256000, 131072),
		mk("minimax-m3", "MiniMax M3", 512000, 131072),
		mk("hy3", "Hy3", 192000, 64000),
		mk("hy3-x", "Hy3-X", 192000, 64000),
		mk("hy3-preview", "Hy3 Preview", 192000, 64000),
		mk("hy3-preview-agent", "Hy3 Preview Agent", 192000, 64000),
		mk("hy4-preview", "Hy4 Preview", 1000000, 64000),
		mk("hy4-preview-x", "Hy4 Preview X", 1000000, 64000),
		mk("deepseek-v4-pro", "DeepSeek V4 Pro", 1000000, 393216),
		mk("deepseek-v4-flash", "DeepSeek V4 Flash", 1000000, 393216),
	}
}

// verifiedChatFallbackIDs covers models confirmed on the chat route before
// WorkBuddy publishes them in the per-account CLI catalog. Keep this narrow:
// the dynamic catalog remains authoritative for every other model.
var verifiedChatFallbackIDs = map[string]struct{}{
	"glm-5.3-flash": {},
	"hy4-preview":   {},
	"hy4-preview-x": {},
}

func ensureVerifiedChatFallbacks(models []pluginapi.ModelInfo) []pluginapi.ModelInfo {
	seen := make(map[string]struct{}, len(models)+len(verifiedChatFallbackIDs))
	out := make([]pluginapi.ModelInfo, 0, len(models)+len(verifiedChatFallbackIDs))
	for _, model := range models {
		key := strings.ToLower(model.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	for _, fallback := range wbModels() {
		if _, verified := verifiedChatFallbackIDs[fallback.ID]; !verified {
			continue
		}
		key := strings.ToLower(fallback.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, fallback)
	}
	return out
}

func officialMaxCompletionTokens(modelID string, fallback int64) int64 {
	switch modelID {
	case "glm-5.3", "glm-5.3-flash", "glm-5.2", "glm-5.1", "glm-5v-turbo", "kimi-k3-1", "kimi-k2.7", "kimi-k2.6", "minimax-m3":
		return 131072
	case "deepseek-v4-pro", "deepseek-v4-flash":
		return 393216
	}
	return fallback
}

// dynamicContextLength prefers the upstream catalog's current input limit.
// Newer CodeBuddy catalogs expose maxInputTokens/maxAllowedSize; retain the
// legacy contextWindow decoder so older server versions remain compatible.
func dynamicContextLength(maxInputTokens, maxAllowedSize int64, legacy json.RawMessage) int64 {
	if maxInputTokens > 0 {
		return maxInputTokens
	}
	if maxAllowedSize > 0 {
		return maxAllowedSize
	}
	var value float64
	if json.Unmarshal(legacy, &value) == nil && value > 0 {
		return int64(value)
	}
	return 0
}

// dynamicMaxCompletionTokens prefers maxOutputTokens from the current catalog,
// then falls back to the legacy maxTokens field used by earlier API versions.
func dynamicMaxCompletionTokens(maxOutputTokens int64, legacy json.RawMessage) int64 {
	if maxOutputTokens > 0 {
		return maxOutputTokens
	}
	var value float64
	if json.Unmarshal(legacy, &value) == nil && value > 0 {
		return int64(value)
	}
	return 0
}

// officialModelName pins distinct display names where upstream reuses one
// name across different model IDs (hy3-x shipped upstream with name "Hy3",
// identical to hy3, which makes client model lists ambiguous).
func officialModelName(modelID, fallback string) string {
	if name, ok := officialModelNames[modelID]; ok {
		return name
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return modelID
}

var officialModelNames = map[string]string{
	"hy3-x": "Hy3-X",
}

func cachedDynamicModels() ([]pluginapi.ModelInfo, bool) {
	dynamicModelsCache.RLock()
	defer dynamicModelsCache.RUnlock()
	if len(dynamicModelsCache.models) > 0 && time.Since(dynamicModelsCache.fetched) < dynamicModelsCacheTTL {
		return dynamicModelsCache.models, true
	}
	return nil, false
}

func storeDynamicModels(models []pluginapi.ModelInfo) {
	dynamicModelsCache.Lock()
	dynamicModelsCache.models = models
	dynamicModelsCache.fetched = time.Now()
	dynamicModelsCache.Unlock()
}

func fetchDynamicModelsFromStorage(storageJSON []byte) []pluginapi.ModelInfo {
	if models, ok := cachedDynamicModels(); ok {
		return models
	}
	accessToken := ""
	if len(storageJSON) > 0 {
		if tok, ok := extractAccessToken(storageJSON); ok {
			accessToken = tok
		}
	}
	if accessToken == "" {
		return wbModels()
	}
	if dyn, err := callModelsAPI(accessToken); err == nil && len(dyn) > 0 {
		storeDynamicModels(dyn)
		return dyn
	}
	return wbModels()
}

// fetchDynamicModels calls the WorkBuddy API to get the latest model list.
// Falls back to the hardcoded list on any error.
// extractAccessToken handles both flat (CPA UI) and nested (plugin OAuth) auth file shapes.
func extractAccessToken(raw []byte) (string, bool) {
	// flat shape from CPA-Manager-Plus UI
	var flat struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && strings.TrimSpace(flat.AccessToken) != "" {
		return flat.AccessToken, true
	}
	// nested shape from plugin OAuth
	var nested storedAuth
	if err := json.Unmarshal(raw, &nested); err == nil && strings.TrimSpace(nested.Auth.AccessToken) != "" {
		return nested.Auth.AccessToken, true
	}
	return "", false
}

// realmFromToken decodes the JWT iss claim to determine the account realm.
// Global tokens have iss=...workbuddy.ai...; CN tokens have iss=...codebuddy.cn...
// Returns true if the token is Global.
func isGlobalToken(accessToken string) bool {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return false
	}
	payload := parts[1]
	// base64url padding
	if pad := len(payload) % 4; pad != 0 {
		payload += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	var claims struct {
		ISS string `json:"iss"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return false
	}
	return strings.Contains(strings.ToLower(claims.ISS), "workbuddy.ai")
}

// callModelsAPI GETs /console/enterprises/personal/models from the upstream.
// Uses the shared client (connection pooling) with a per-request 15s budget;
// the shared client's own 120s timeout stays as the outer bound.
func callModelsAPI(accessToken string) ([]pluginapi.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Model discovery is per-realm: Global tokens must query workbuddy.ai,
	// not copilot.tencent.com (which 500s for Global tokens). Decode JWT iss.
	isGlobal := isGlobalToken(accessToken)
	modelsURL := endpointModels
	origin := originReferer
	if isGlobal {
		modelsURL = upstreamBaseGlobal + "/console/enterprises/personal/models"
		origin = originRefererGlobal
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", clientUA)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	body := resp.Body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models API status %d", resp.StatusCode)
	}
	var apiResp struct {
		Code int `json:"code"`
		Data struct {
			Models []struct {
				ID                 string          `json:"id"`
				Name               string          `json:"name"`
				Description        string          `json:"description"`
				Credits            string          `json:"credits"`
				Configurable       bool            `json:"configurable"`
				Configured         bool            `json:"configured"`
				IsDefault          bool            `json:"isDefault"`
				SupportsImages     bool            `json:"supportsImages"`
				SupportsReasoning  bool            `json:"supportsReasoning"`
				OnlyReasoning      bool            `json:"onlyReasoning"`
				Reasoning          json.RawMessage `json:"reasoning"`
				DisabledMultimodal bool            `json:"disabledMultimodal"`
				Disabled           bool            `json:"disabled"`
				DisabledReason     string          `json:"disabledReason"`
				ContextWindow      json.RawMessage `json:"contextWindow"`
				MaxTokens          json.RawMessage `json:"maxTokens"`
				MaxAllowedSize     int64           `json:"maxAllowedSize"`
				MaxInputTokens     int64           `json:"maxInputTokens"`
				MaxOutputTokens    int64           `json:"maxOutputTokens"`
			} `json:"models"`
			Agents []struct {
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if apiResp.Code != 0 {
		return nil, fmt.Errorf("models API code %d", apiResp.Code)
	}
	var cliModelIDs []string
	for _, a := range apiResp.Data.Agents {
		if a.Name == "cli" {
			cliModelIDs = a.Models
			break
		}
	}
	if len(cliModelIDs) == 0 {
		return nil, fmt.Errorf("no cli agent models found")
	}
	dynMap := make(map[string]struct {
		ID                 string          `json:"id"`
		Name               string          `json:"name"`
		Description        string          `json:"description"`
		Credits            string          `json:"credits"`
		Configurable       bool            `json:"configurable"`
		Configured         bool            `json:"configured"`
		IsDefault          bool            `json:"isDefault"`
		SupportsImages     bool            `json:"supportsImages"`
		SupportsReasoning  bool            `json:"supportsReasoning"`
		OnlyReasoning      bool            `json:"onlyReasoning"`
		Reasoning          json.RawMessage `json:"reasoning"`
		DisabledMultimodal bool            `json:"disabledMultimodal"`
		Disabled           bool            `json:"disabled"`
		DisabledReason     string          `json:"disabledReason"`
		ContextWindow      json.RawMessage `json:"contextWindow"`
		MaxTokens          json.RawMessage `json:"maxTokens"`
		MaxAllowedSize     int64           `json:"maxAllowedSize"`
		MaxInputTokens     int64           `json:"maxInputTokens"`
		MaxOutputTokens    int64           `json:"maxOutputTokens"`
	}, len(apiResp.Data.Models))
	for _, m := range apiResp.Data.Models {
		dynMap[m.ID] = m
	}
	var out []pluginapi.ModelInfo
	for _, id := range cliModelIDs {
		m, ok := dynMap[id]
		if !ok {
			continue
		}
		if m.Disabled {
			continue
		}
		ctxLen := dynamicContextLength(m.MaxInputTokens, m.MaxAllowedSize, m.ContextWindow)
		maxTok := dynamicMaxCompletionTokens(m.MaxOutputTokens, m.MaxTokens)
		out = append(out, pluginapi.ModelInfo{
			ID:                         m.ID,
			Name:                       officialModelName(m.ID, m.Name),
			DisplayName:                officialModelName(m.ID, m.Name),
			InputTokenLimit:            ctxLen,
			OutputTokenLimit:           maxTok,
			ContextLength:              ctxLen,
			MaxCompletionTokens:        officialMaxCompletionTokens(m.ID, maxTok),
			OwnedBy:                    providerName,
			SupportedGenerationMethods: []string{"chat"},
		})
	}
	return out, nil
}

func cacheModelAliases(host pluginapi.HostConfigSummary) {
	entries := host.OAuthModelAlias[providerName]
	if len(entries) == 0 {
		// Host may key the channel case-insensitively; fall back to a scan.
		for channel, list := range host.OAuthModelAlias {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				entries = list
				break
			}
		}
	}
	byAlias := make(map[string]string, len(entries))
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		alias := strings.TrimSpace(e.Alias)
		if name == "" || alias == "" || strings.EqualFold(name, alias) {
			continue
		}
		byAlias[strings.ToLower(alias)] = name
	}
	modelAliasCache.Lock()
	modelAliasCache.byAlias = byAlias
	modelAliasCache.Unlock()
}

// resolveUpstreamModel maps an aliased requested model back to the real
// upstream model ID. Returns the input unchanged when nothing matches.
func resolveUpstreamModel(model string, attributes map[string]string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		return model
	}
	key := strings.ToLower(m)
	if name, ok := parseModelAliasAttribute(attributes)[key]; ok {
		return name
	}
	modelAliasCache.RLock()
	name, ok := modelAliasCache.byAlias[key]
	modelAliasCache.RUnlock()
	if ok {
		return name
	}
	for upstreamID, publicID := range publicModelIDs {
		if strings.EqualFold(m, publicID) {
			return upstreamID
		}
	}
	return m
}

// parseModelAliasAttribute decodes a per-auth alias override from auth
// attributes. Accepts JSON ([{"name":...,"alias":...}] or {alias:name}) or
// comma-separated "alias=name" pairs.
func parseModelAliasAttribute(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	raw := ""
	for _, k := range []string{"model_alias", "model-alias", "oauth-model-alias"} {
		if v := strings.TrimSpace(attributes[k]); v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	add := func(name, alias string) {
		name, alias = strings.TrimSpace(name), strings.TrimSpace(alias)
		if name != "" && alias != "" && !strings.EqualFold(name, alias) {
			out[strings.ToLower(alias)] = name
		}
	}
	if strings.HasPrefix(raw, "[") {
		var list []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		}
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, e := range list {
				add(e.Name, e.Alias)
			}
			return out
		}
	}
	if strings.HasPrefix(raw, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(raw), &m) == nil {
			for alias, name := range m {
				add(name, alias)
			}
			return out
		}
	}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			add(kv[1], kv[0])
		}
	}
	return out
}

// filterExcludedModels removes models listed in oauth-excluded-models for
// the workbuddy provider. The host passes this config via HostConfigSummary.
func filterExcludedModels(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo {
	if len(host.ExcludedModels) == 0 {
		return models
	}
	// Try exact provider match, then case-insensitive scan.
	excluded := host.ExcludedModels[providerName]
	if len(excluded) == 0 {
		for channel, list := range host.ExcludedModels {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				excluded = list
				break
			}
		}
	}
	if len(excluded) == 0 {
		return models
	}
	excludeSet := make(map[string]struct{}, len(excluded))
	for _, m := range excluded {
		excludeSet[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}
	// Use a fresh slice — models[:0] would alias the input's backing array,
	// which may be the dynamicModelsCache's own slice. Mutating it in place
	// would corrupt the cache for subsequent callers (P0 bug: after one
	// filterExcludedModels call, cache returns the filtered list as the
	// "full" list on the next fetch).
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		if _, skip := excludeSet[strings.ToLower(m.ID)]; skip {
			continue
		}
		out = append(out, m)
	}
	return out
}

// publishUsage reports one upstream attempt into CPAMP request monitoring.
// requestedModel is client-facing (may be alias); upstreamModel is resolved.

func handleModelStatic(raw []byte) ([]byte, error) {
	var req pluginapi.StaticModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cacheModelAliases(req.Host)
	models := wbModels()
	models = filterExcludedModels(models, req.Host)
	models = publicModels(models)
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}

func handleModelForAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// Always return the plugin's canonical provider key. The host skips any
	// response whose Provider doesn't match the auth's provider, so echoing
	// req.AuthProvider back would silently drop the model list whenever the
	// auth file carries a non-canonical provider string.
	cacheModelAliases(req.Host)
	models := ensureVerifiedChatFallbacks(fetchDynamicModelsFromStorage(req.StorageJSON))
	models = filterExcludedModels(models, req.Host)
	models = publicModels(models)
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}
