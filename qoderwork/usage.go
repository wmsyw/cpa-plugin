// usage.go implements the UsagePlugin capability: the host calls handleUsage
// after every request with a canonical record, and we forward to CPAMP. The
// legacy publishUsage path is kept for hosts without UsagePlugin wiring.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type usageAccountIdentity struct {
	account  string
	label    string
	fileName string
}

var usageAccountIdentities sync.Map

// rememberUsageAccount snapshots display identity while the selected auth is available.
// publishUsage only receives the auth ID, so it resolves the immutable display fields here.
func rememberUsageAccount(sa *storedAuth) {
	if sa == nil {
		return
	}
	uid := strings.TrimSpace(sa.Account.UID)
	if uid == "" {
		return
	}
	account := strings.TrimSpace(sa.Account.Nickname)
	if account == "" {
		account = uid
	}
	usageAccountIdentities.Store(uid, usageAccountIdentity{
		account:  account,
		label:    labelForAuth(sa),
		fileName: providerName + "-" + sanitizeUIDForFileName(uid) + ".json",
	})
}

func usageAccountSnapshot(authID string) usageAccountIdentity {
	value, ok := usageAccountIdentities.Load(strings.TrimSpace(authID))
	if !ok {
		return usageAccountIdentity{}
	}
	identity, _ := value.(usageAccountIdentity)
	return identity
}

// handleUsage is the UsagePlugin entry point. The host calls this after every
// request with the canonical usage record (it also records to its own
// DefaultManager, so we don't need to). Our job is just to forward to CPAMP.
//
// v0.7.0 compliance: this replaces the old pattern where each executor path
// called publishUsage directly (which both skipped the host audit and forced
// every call site to remember to publish).
func usageTimingMillis(started, now time.Time, ttft time.Duration) (int64, int64) {
	latencyMs := int64(0)
	if !started.IsZero() {
		latencyMs = now.Sub(started).Milliseconds()
		if latencyMs < 0 {
			latencyMs = 0
		}
	}
	if ttft <= 0 {
		return latencyMs, latencyMs
	}
	return latencyMs, ttft.Milliseconds()
}

func handleUsage(raw []byte) ([]byte, error) {
	var record pluginapi.UsageRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	// Only forward qoderwork's own records; the host will route other plugins'
	// usage to their own UsagePlugin.
	if record.Provider != "" && record.Provider != providerName {
		return okEnvelope(map[string]any{"forwarded": false})
	}
	detail := usage.Detail{
		InputTokens:         record.Detail.InputTokens,
		OutputTokens:        record.Detail.OutputTokens,
		ReasoningTokens:     record.Detail.ReasoningTokens,
		CachedTokens:        record.Detail.CachedTokens,
		CacheReadTokens:     record.Detail.CacheReadTokens,
		CacheCreationTokens: record.Detail.CacheCreationTokens,
		TotalTokens:         record.Detail.TotalTokens,
	}
	started := record.RequestedAt
	if started.IsZero() {
		started = time.Now().Add(-record.Latency)
	}
	// The host may deliver the upstream model key (e.g. "dfmodel") instead of
	// the user-facing alias. Resolve the alias from our cached alias table when
	// the record does not already carry it.
	alias := strings.TrimSpace(record.Alias)
	if alias == "" || alias == record.Model {
		if resolved := aliasForUpstreamModel(strings.TrimSpace(record.Model)); resolved != "" {
			alias = resolved
		}
	}
	forwardUsageToCPAMP(
		alias,
		record.Model,
		record.AuthID,
		started,
		detail,
		record.Failed,
		record.Failure.StatusCode,
		record.Failure.Body,
		record.ReasoningEffort,
		record.TTFT,
	)
	return okEnvelope(map[string]any{"forwarded": true})
}

// aliasForUpstreamModel returns the configured user-facing alias for an
// upstream model key (reverse of modelAliasCache.byAlias). Empty when unknown.
func aliasForUpstreamModel(upstream string) string {
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return ""
	}
	modelAliasCache.RLock()
	defer modelAliasCache.RUnlock()
	for alias, name := range modelAliasCache.byAlias {
		if strings.EqualFold(strings.TrimSpace(name), upstream) {
			return alias
		}
	}
	return ""
}

// publishUsage is kept for backward compatibility with existing call sites
// inside the executor. After v0.7.0 the host calls UsagePlugin.HandleUsage
// itself after every request, so executor-side publish is redundant. We
// forward to CPAMP here too so that hosts WITHOUT UsagePlugin wiring (older
// CPA builds) still emit usage; hosts with the wiring will trigger HandleUsage
// separately, but forwardUsageToCPAMP is idempotent at the CPAMP ingestion
// layer (NDJSON import dedups on timestamp+auth+model+total_tokens).
func publishUsage(requestedModel, upstreamModel, authID string, started time.Time, detail usage.Detail, failed bool, statusCode int, errBody string, reasoning string, ttft time.Duration) {
	model := strings.TrimSpace(upstreamModel)
	if model == "" {
		model = strings.TrimSpace(requestedModel)
	}
	alias := strings.TrimSpace(requestedModel)
	if alias == "" {
		alias = model
	}
	// Fire-and-forget so the executor hot path never blocks on the CPAMP
	// round-trip. handleUsage (the host-driven path) is synchronous because the
	// host already runs it on its own goroutine after the request completes.
	go forwardUsageToCPAMP(alias, model, authID, started, normalizeUsageDetail(detail), failed, statusCode, errBody, reasoning, ttft)
}

// forwardUsageToCPAMP POSTs one NDJSON line to CPAMP usage/import.
// Silent on misconfig / network errors — never blocks chat.
//
// v0.7.0: routed via host.http.do so the call is captured by request-log and
// uses host transport policy (proxy, timeout). Was: raw http.Client.
func forwardUsageToCPAMP(alias, model, authID string, started time.Time, detail usage.Detail, failed bool, statusCode int, errBody string, reasoning string, ttft time.Duration) {
	usageReportMu.RLock()
	url := strings.TrimSpace(usageReportURL)
	key := strings.TrimSpace(usageReportKey)
	usageReportMu.RUnlock()
	if url == "" || key == "" {
		return
	}
	ts := started
	if ts.IsZero() {
		ts = time.Now()
	}
	latencyMs, ttftMs := usageTimingMillis(started, time.Now(), ttft)
	total := detail.TotalTokens
	if total == 0 {
		total = detail.InputTokens + detail.OutputTokens + detail.ReasoningTokens
	}
	failBody := ""
	failCode := 200
	if failed {
		failCode = statusCode
		if failCode <= 0 {
			failCode = 502
		}
		failBody = truncate(redactSecrets(errBody), 512)
	}
	identity := usageAccountSnapshot(authID)
	payload := map[string]any{
		"timestamp":              ts.UTC().Format(time.RFC3339Nano),
		"latency_ms":             latencyMs,
		"ttft_ms":                ttftMs,
		"reasoning_effort":       strings.TrimSpace(reasoning),
		"source":                 "qoderwork",
		"auth_index":             strings.TrimSpace(authID),
		"account_snapshot":       identity.account,
		"auth_label_snapshot":    identity.label,
		"auth_file_snapshot":     identity.fileName,
		"auth_provider_snapshot": providerName,
		"auth_snapshot_at_ms":    ts.UnixMilli(),
		"provider":               providerName,
		"model":                  model,
		"alias":                  alias,
		"endpoint":               "POST /v1/chat/completions",
		"auth_type":              "oauth",
		"executor_type":          "qoderwork",
		"generate":               true,
		"failed":                 failed,
		"tokens":                 usageTokensPayload(detail, total),
		"fail": map[string]any{
			"status_code": failCode,
			"body":        failBody,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	body = append(body, '\n')
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := hostHTTPDo(req)
	if err != nil {
		return
	}
	_ = resp.Body
}

func usageTokensPayload(detail usage.Detail, total int64) map[string]any {
	return map[string]any{
		"cache_input_mode":      "included_in_input",
		"input_tokens":          detail.InputTokens,
		"output_tokens":         detail.OutputTokens,
		"reasoning_tokens":      detail.ReasoningTokens,
		"cached_tokens":         detail.CachedTokens,
		"cache_read_tokens":     detail.CacheReadTokens,
		"cache_creation_tokens": detail.CacheCreationTokens,
		"total_tokens":          total,
	}
}

func normalizeUsageDetail(d usage.Detail) usage.Detail {
	if d.TotalTokens == 0 {
		if total := d.InputTokens + d.OutputTokens + d.ReasoningTokens; total > 0 {
			d.TotalTokens = total
		}
	}
	return d
}

// usageDetailFromMap converts an OpenAI-style "usage" JSON object into a
// usage.Detail, tolerating both snake_case naming and numeric jitter.
func usageDetailFromMap(m map[string]any) usage.Detail {
	if len(m) == 0 {
		return usage.Detail{}
	}
	num := func(keys ...string) int64 {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				switch n := v.(type) {
				case float64:
					return int64(n)
				case int64:
					return n
				case json.Number:
					i, _ := n.Int64()
					return i
				}
			}
		}
		return 0
	}
	d := usage.Detail{
		InputTokens:     num("prompt_tokens", "input_tokens"),
		OutputTokens:    num("completion_tokens", "output_tokens"),
		TotalTokens:     num("total_tokens"),
		CachedTokens:    num("cached_tokens"),
		CacheReadTokens: num("cache_read_input_tokens"),
	}
	// QoderWork / CodeBuddy expose cache accounting through provider-specific
	// fields that the generic keys above do not cover. Map them so request
	// monitoring shows cache hits instead of reporting zero cached tokens.
	if d.CacheReadTokens == 0 {
		d.CacheReadTokens = num("prompt_cache_hit_tokens", "cache_hit_tokens")
	}
	if d.CachedTokens == 0 {
		d.CachedTokens = d.CacheReadTokens
	}
	if d.CacheCreationTokens == 0 {
		d.CacheCreationTokens = num("prompt_cache_write_tokens", "cache_write_tokens")
	}
	if d.CacheReadTokens == 0 {
		if ptd, ok := m["prompt_tokens_details"].(map[string]any); ok {
			if v, ok2 := ptd["cached_tokens"]; ok2 {
				switch n := v.(type) {
				case float64:
					d.CacheReadTokens = int64(n)
				case int64:
					d.CacheReadTokens = n
				case json.Number:
					i, _ := n.Int64()
					d.CacheReadTokens = i
				}
				d.CachedTokens = d.CacheReadTokens
			}
		}
	}
	if d.ReasoningTokens == 0 {
		d.ReasoningTokens = num("completion_thinking_tokens", "thinking_tokens")
	}
	if ct, ok := m["completion_tokens_details"].(map[string]any); ok {
		if v, ok2 := ct["reasoning_tokens"].(float64); ok2 {
			d.ReasoningTokens = int64(v)
		}
	}
	return d
}

// usageDetailFromCompletion extracts the usage block from an aggregated
// non-streaming chat.completion payload.
func usageDetailFromCompletion(payload []byte) usage.Detail {
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return usage.Detail{}
	}
	m, _ := obj["usage"].(map[string]any)
	return usageDetailFromMap(m)
}

// sseUsageCollector scans upstream SSE chunks and keeps the last "usage"
// object seen (QoderWork emits it on the terminal chunk).
type sseUsageCollector struct {
	last    map[string]any
	firstAt time.Time
}

func (c *sseUsageCollector) feed(rawJSON string) {
	if c.firstAt.IsZero() {
		c.firstAt = time.Now()
	}
	var chunk map[string]any
	if json.Unmarshal([]byte(rawJSON), &chunk) != nil {
		return
	}
	if u, ok := chunk["usage"].(map[string]any); ok && len(u) > 0 {
		c.last = u
	}
}

// ttftSince returns the time from request start to the first streamed chunk.
func (c *sseUsageCollector) ttftSince(started time.Time) time.Duration {
	if c == nil || c.firstAt.IsZero() || started.IsZero() {
		return 0
	}
	d := c.firstAt.Sub(started)
	if d < 0 {
		return 0
	}
	return d
}

func (c *sseUsageCollector) detail() usage.Detail {
	return usageDetailFromMap(c.last)
}
