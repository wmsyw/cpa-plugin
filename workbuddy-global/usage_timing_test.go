package main

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestUsageTimingMillis(t *testing.T) {
	t.Parallel()

	started := time.Unix(100, 0)
	now := started.Add(1250 * time.Millisecond)
	latencyMs, ttftMs := usageTimingMillis(started, now, 0)
	if latencyMs != 1250 || ttftMs != latencyMs {
		t.Errorf("nonstream timing = latency %dms, TTFT %dms; want both 1250ms", latencyMs, ttftMs)
	}

	latencyMs, ttftMs = usageTimingMillis(started, now, 320*time.Millisecond)
	if latencyMs != 1250 || ttftMs != 320 {
		t.Errorf("stream timing = latency %dms, TTFT %dms; want 1250ms/320ms", latencyMs, ttftMs)
	}

	latencyMs, ttftMs = usageTimingMillis(time.Time{}, now, 0)
	if latencyMs != 0 || ttftMs != 0 {
		t.Errorf("zero timing = latency %dms, TTFT %dms; want 0ms/0ms", latencyMs, ttftMs)
	}
}

func TestUsageTokensDeclareCacheIncludedInInput(t *testing.T) {
	t.Parallel()

	tokens := usageTokensPayload(usage.Detail{
		InputTokens:     41_083,
		CachedTokens:    41_024,
		CacheReadTokens: 41_024,
	}, 41_189)
	if got := tokens["cache_input_mode"]; got != "included_in_input" {
		t.Fatalf("cache_input_mode = %v, want included_in_input", got)
	}
	if got := tokens["input_tokens"]; got != int64(41_083) {
		t.Fatalf("input_tokens = %v, want 41083", got)
	}
	if got := tokens["cache_read_tokens"]; got != int64(41_024) {
		t.Fatalf("cache_read_tokens = %v, want 41024", got)
	}
}
