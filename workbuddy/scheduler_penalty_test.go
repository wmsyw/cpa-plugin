package main

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestSchedulerPickPenaltyQueueRotation(t *testing.T) {
	// Reset penalties
	authPenaltyMu.Lock()
	authPenalties = make(map[string]int64)
	authPenaltySeq = 0
	authPenaltyMu.Unlock()

	cands := []pluginapi.SchedulerAuthCandidate{
		{ID: "auth-A", Provider: providerName},
		{ID: "auth-B", Provider: providerName},
		{ID: "auth-C", Provider: providerName},
		{ID: "auth-D", Provider: providerName},
	}

	now := time.Now()
	// Initially, all have same deadline/state, picks auth-A (alphabetical fallback)
	first := pickExpiryAuth(cands, now)
	if first != "auth-A" {
		t.Fatalf("initial pick = %q, want auth-A", first)
	}

	// auth-A hits 429 -> penalize auth-A
	penalizeAuth("auth-A")

	// Next pick should rotate to auth-B!
	second := pickExpiryAuth(cands, now)
	if second != "auth-B" {
		t.Fatalf("after A 429 pick = %q, want auth-B", second)
	}

	// auth-B hits 429 -> penalize auth-B
	penalizeAuth("auth-B")

	// Next pick should rotate to auth-C!
	third := pickExpiryAuth(cands, now)
	if third != "auth-C" {
		t.Fatalf("after B 429 pick = %q, want auth-C", third)
	}

	// auth-C hits 429 -> penalize auth-C
	penalizeAuth("auth-C")

	// Next pick should rotate to auth-D!
	fourth := pickExpiryAuth(cands, now)
	if fourth != "auth-D" {
		t.Fatalf("after C 429 pick = %q, want auth-D", fourth)
	}

	// auth-D hits 429 -> all 4 are now penalized!
	penalizeAuth("auth-D")

	// Next pick should loop back to auth-A (penalized longest ago)!
	fifth := pickExpiryAuth(cands, now)
	if fifth != "auth-A" {
		t.Fatalf("after all 429 pick = %q, want auth-A (oldest penalized)", fifth)
	}

	// auth-A succeeds! clear penalty on auth-A
	clearAuthPenalty("auth-A")

	// auth-A should stay preferred since B, C, D are still penalized
	sixth := pickExpiryAuth(cands, now)
	if sixth != "auth-A" {
		t.Fatalf("after A succeeds pick = %q, want auth-A", sixth)
	}
}
