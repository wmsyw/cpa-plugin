package main

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestSchedulerPickPenaltyQueueRotation(t *testing.T) {
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
	first := pickExpiryAuth(cands, now)
	if first != "auth-A" {
		t.Fatalf("initial pick = %q, want auth-A", first)
	}

	penalizeAuth("auth-A")
	second := pickExpiryAuth(cands, now)
	if second != "auth-B" {
		t.Fatalf("after A 429 pick = %q, want auth-B", second)
	}

	penalizeAuth("auth-B")
	third := pickExpiryAuth(cands, now)
	if third != "auth-C" {
		t.Fatalf("after B 429 pick = %q, want auth-C", third)
	}

	penalizeAuth("auth-C")
	fourth := pickExpiryAuth(cands, now)
	if fourth != "auth-D" {
		t.Fatalf("after C 429 pick = %q, want auth-D", fourth)
	}

	penalizeAuth("auth-D")
	fifth := pickExpiryAuth(cands, now)
	if fifth != "auth-A" {
		t.Fatalf("after all 429 pick = %q, want auth-A (oldest penalized)", fifth)
	}

	clearAuthPenalty("auth-A")
	sixth := pickExpiryAuth(cands, now)
	if sixth != "auth-A" {
		t.Fatalf("after A succeeds pick = %q, want auth-A", sixth)
	}
}
