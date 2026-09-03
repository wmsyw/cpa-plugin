package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func parsePickResponse(t *testing.T, raw []byte) pluginapi.SchedulerPickResponse {
	t.Helper()
	var env struct {
		OK     bool                            `json:"ok"`
		Result pluginapi.SchedulerPickResponse `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatal("envelope not ok")
	}
	return env.Result
}

func resetActiveAuth(t *testing.T) {
	t.Helper()
	setActiveAuthID("")
	// Pre-v0.6.31 tests assume plugin handles routing; default mode is now off.
	// Flip to credits mode for the duration of each pick test so behavior stays
	// identical to before the scheduler_mode fix.
	restoreMode := setSchedulerMode(schedulerModeCredits)
	t.Cleanup(func() {
		setActiveAuthID("")
		restoreMode()
	})
}

func TestSchedulerPick_NonWorkbuddy_Defers(t *testing.T) {
	resetActiveAuth(t)
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: "other",
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "x", Provider: "other"},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if resp.Handled {
		t.Fatal("non-workbuddy candidates should defer")
	}
}

// TestSchedulerPick_OffMode_Defers covers the v0.6.31 fix: scheduler_mode=off
// must make the plugin decline to handle routing, even for workbuddy candidates.
func TestSchedulerPick_OffMode_Defers(t *testing.T) {
	setActiveAuthID("")
	restoreMode := setSchedulerMode(schedulerModeOff)
	t.Cleanup(func() {
		setActiveAuthID("")
		restoreMode()
	})
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-only", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if resp.Handled {
		t.Fatal("scheduler_mode=off should defer to built-in scheduler")
	}
}

func TestSchedulerPick_SingleCandidate_PicksIt(t *testing.T) {
	resetActiveAuth(t)
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-only", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "wb-only" {
		t.Fatalf("want wb-only handled, got %+v", resp)
	}
	if getActiveAuthID() != "wb-only" {
		t.Fatalf("active auth should stick to wb-only, got %q", getActiveAuthID())
	}
}

func TestSchedulerPick_PrefersPanelSelection(t *testing.T) {
	resetActiveAuth(t)
	accountCache.Store("wb-a", &accountCacheEntry{credits: &creditsSummary{TotalRemain: 10, TotalSize: 10}})
	accountCache.Store("wb-b", &accountCacheEntry{credits: &creditsSummary{TotalRemain: 500, TotalSize: 500}})
	defer func() {
		accountCache.Delete("wb-a")
		accountCache.Delete("wb-b")
	}()
	setActiveAuthID("wb-a")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-a", Provider: providerName},
			{ID: "wb-b", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "wb-a" {
		t.Fatalf("want panel selection wb-a, got %+v", resp)
	}
}

func TestSchedulerPick_StaysOnExhaustedSelection(t *testing.T) {
	resetActiveAuth(t)
	// When selected is exhausted AND a non-exhausted candidate exists,
	// it should switch to the non-exhausted one and update activeAuthID.
	accountCache.Store("wb-exhausted", &accountCacheEntry{
		credits: &creditsSummary{TotalRemain: 0, TotalUsed: 500, TotalSize: 500},
	})
	accountCache.Store("wb-ok", &accountCacheEntry{
		credits: &creditsSummary{TotalRemain: 300, TotalUsed: 0, TotalSize: 300},
	})
	defer func() {
		accountCache.Delete("wb-exhausted")
		accountCache.Delete("wb-ok")
	}()
	setActiveAuthID("wb-exhausted")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-exhausted", Provider: providerName},
			{ID: "wb-ok", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "wb-ok" {
		t.Fatalf("want switch to wb-ok, got %+v", resp)
	}
	if getActiveAuthID() != "wb-ok" {
		t.Fatalf("active should update to wb-ok, got %q", getActiveAuthID())
	}
}

func TestSchedulerPick_AllExhausted_KeepsCurrent(t *testing.T) {
	resetActiveAuth(t)
	// When ALL candidates are exhausted, keep current selection rather than
	// flip-flopping between exhausted accounts.
	accountCache.Store("wb-a", &accountCacheEntry{
		credits: &creditsSummary{TotalRemain: 0, TotalUsed: 100, TotalSize: 100},
	})
	accountCache.Store("wb-b", &accountCacheEntry{
		credits: &creditsSummary{TotalRemain: 0, TotalUsed: 200, TotalSize: 200},
	})
	defer func() {
		accountCache.Delete("wb-a")
		accountCache.Delete("wb-b")
	}()
	setActiveAuthID("wb-a")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-a", Provider: providerName},
			{ID: "wb-b", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "wb-a" {
		t.Fatalf("want stay on wb-a (all exhausted), got %+v", resp)
	}
}

func TestSchedulerPick_SwitchesOnlyWhenSelectionGone(t *testing.T) {
	resetActiveAuth(t)
	accountCache.Store("wb-ok", &accountCacheEntry{
		credits: &creditsSummary{TotalRemain: 300, TotalUsed: 0, TotalSize: 300},
	})
	defer accountCache.Delete("wb-ok")
	// Selected auth is NOT in candidates (host disabled it) → should switch.
	setActiveAuthID("wb-gone")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-ok", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "wb-ok" {
		t.Fatalf("want switch to wb-ok, got %+v", resp)
	}
	if getActiveAuthID() != "wb-ok" {
		t.Fatalf("active should update to wb-ok, got %q", getActiveAuthID())
	}
}

func TestSchedulerPick_SkipsDisabledCandidates(t *testing.T) {
	resetActiveAuth(t)
	accountCache.Store("wb-live", &accountCacheEntry{
		credits: &creditsSummary{TotalRemain: 50, TotalSize: 50},
	})
	defer accountCache.Delete("wb-live")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-off", Provider: providerName, Status: "disabled"},
			{ID: "wb-live", Provider: providerName, Status: "active"},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "wb-live" {
		t.Fatalf("want wb-live, got %+v", resp)
	}
	// All disabled → defer
	raw2, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "wb-off", Provider: providerName, Status: "disabled", Metadata: map[string]any{"disabled": true}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	resp2 := parsePickResponse(t, raw2)
	if resp2.Handled {
		t.Fatalf("all disabled should defer, got %+v", resp2)
	}
}

func TestCandidateDisabled(t *testing.T) {
	if !candidateDisabled(pluginapi.SchedulerAuthCandidate{Status: "disabled"}) {
		t.Fatal("status disabled")
	}
	if !candidateDisabled(pluginapi.SchedulerAuthCandidate{Metadata: map[string]any{"disabled": true}}) {
		t.Fatal("meta disabled")
	}
	if candidateDisabled(pluginapi.SchedulerAuthCandidate{Status: "active"}) {
		t.Fatal("active should not be disabled")
	}
}

func TestEnsureDefaultActiveAuth(t *testing.T) {
	resetActiveAuth(t)
	id := ensureDefaultActiveAuth([]wbAccount{
		{AuthIndex: "a1", AuthID: "a1", Disabled: true},
		{AuthIndex: "a2", AuthID: "a2", Exhausted: false},
		{AuthIndex: "a3", AuthID: "a3"},
	})
	if id != "a2" {
		t.Fatalf("want first ready a2, got %q", id)
	}
	if getActiveAuthID() != "a2" {
		t.Fatalf("stuck active %q", getActiveAuthID())
	}
	// Already set + still live + not exhausted → keep
	id2 := ensureDefaultActiveAuth([]wbAccount{
		{AuthIndex: "a2", AuthID: "a2"},
		{AuthIndex: "a3", AuthID: "a3"},
	})
	if id2 != "a2" {
		t.Fatalf("should keep a2, got %q", id2)
	}
}

func TestEnsureDefaultActiveAuth_SwitchesWhenExhausted(t *testing.T) {
	resetActiveAuth(t)
	// Selected a1 is exhausted → should switch to first non-exhausted.
	setActiveAuthID("a1")
	id := ensureDefaultActiveAuth([]wbAccount{
		{AuthIndex: "a1", AuthID: "a1", Exhausted: true},
		{AuthIndex: "a2", AuthID: "a2", Exhausted: false},
		{AuthIndex: "a3", AuthID: "a3", Exhausted: false},
	})
	if id != "a2" {
		t.Fatalf("want switch to a2, got %q", id)
	}
	if getActiveAuthID() != "a2" {
		t.Fatalf("active should be a2, got %q", getActiveAuthID())
	}
}

func TestEnsureDefaultActiveAuth_AllExhausted_KeepsCurrent(t *testing.T) {
	resetActiveAuth(t)
	setActiveAuthID("a1")
	id := ensureDefaultActiveAuth([]wbAccount{
		{AuthIndex: "a1", AuthID: "a1", Exhausted: true},
		{AuthIndex: "a2", AuthID: "a2", Exhausted: true},
	})
	if id != "a1" {
		t.Fatalf("all exhausted should keep a1, got %q", id)
	}
}

func expiryPick(t *testing.T, active string, entries map[string]*creditsSummary, order ...string) pluginapi.SchedulerPickResponse {
	t.Helper()
	setActiveAuthID(active)
	restoreMode := setSchedulerMode(schedulerModeExpiry)
	t.Cleanup(func() {
		setActiveAuthID("")
		restoreMode()
	})
	for id, credits := range entries {
		accountCache.Store(id, &accountCacheEntry{credits: credits, fetched: time.Now()})
		id := id
		t.Cleanup(func() { accountCache.Delete(id) })
	}
	candidates := make([]pluginapi.SchedulerAuthCandidate, 0, len(order))
	for _, id := range order {
		candidates = append(candidates, pluginapi.SchedulerAuthCandidate{ID: id, Provider: providerName})
	}
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName, Candidates: candidates,
	}))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	return parsePickResponse(t, raw)
}

func TestSchedulerPick_ExpiryUsesMinimumPackageDeadline(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-time.Second)
	entries := map[string]*creditsSummary{
		"wb-later": {TotalRemain: 20, creditsFetched: fresh, Packages: []packageSummary{
			{Remain: 10, Status: 0, DeductionEndTime: now.Add(4 * time.Hour).UnixMilli()},
			{Remain: 10, Status: 0, DeductionEndTime: now.Add(2 * time.Hour).UnixMilli()},
		}},
		"wb-sooner": {TotalRemain: 10, creditsFetched: fresh, Packages: []packageSummary{
			{Remain: 10, Status: 0, DeductionEndTime: now.Add(3 * time.Hour).UnixMilli()},
		}},
	}
	resp := expiryPick(t, "panel", entries, "wb-later", "wb-sooner")
	if !resp.Handled || resp.AuthID != "wb-later" {
		t.Fatalf("want account with minimum package deadline, got %+v", resp)
	}
	if getActiveAuthID() != "panel" {
		t.Fatalf("expiry mode changed panel selection to %q", getActiveAuthID())
	}
}

func TestSchedulerPick_ExpiryRejectsInvalidPackageWindows(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-time.Second)
	invalid := &creditsSummary{TotalRemain: 40, creditsFetched: fresh, Packages: []packageSummary{
		{Remain: 10, Status: 0, DeductionEndTime: now.Add(-time.Minute).UnixMilli()},
		{Remain: 10, Status: 0, DeductionStartTime: now.Add(time.Hour).UnixMilli(), DeductionEndTime: now.Add(2 * time.Hour).UnixMilli()},
		{Remain: 10, Status: 0, DeductionEndTime: 0},
		{Remain: 10, Status: 3, DeductionEndTime: now.Add(time.Minute).UnixMilli()},
	}}
	valid := &creditsSummary{TotalRemain: 10, creditsFetched: fresh, Packages: []packageSummary{
		{Remain: 10, Status: 0, DeductionStartTime: 0, DeductionEndTime: now.Add(3 * time.Hour).UnixMilli()},
	}}
	resp := expiryPick(t, "", map[string]*creditsSummary{"wb-invalid": invalid, "wb-valid": valid}, "wb-invalid", "wb-valid")
	if resp.AuthID != "wb-valid" {
		t.Fatalf("want valid absent-start package, got %+v", resp)
	}
}

func TestSchedulerPick_ExpiryFreshKnownBeforeStaleUnknown(t *testing.T) {
	now := time.Now()
	known := &creditsSummary{TotalRemain: 1, creditsFetched: now, Packages: []packageSummary{
		{Remain: 1, Status: 0, DeductionEndTime: now.Add(time.Hour).UnixMilli()},
	}}
	stale := &creditsSummary{TotalRemain: 1, creditsFetched: now.Add(-accountCacheTTL - time.Second), Packages: []packageSummary{
		{Remain: 1, Status: 0, DeductionEndTime: now.Add(time.Minute).UnixMilli()},
	}}
	unknown := &creditsSummary{TotalRemain: 1}
	resp := expiryPick(t, "", map[string]*creditsSummary{"z-known": known, "a-stale": stale, "b-unknown": unknown}, "a-stale", "b-unknown", "z-known")
	if resp.AuthID != "z-known" {
		t.Fatalf("fresh known deadline must rank first, got %+v", resp)
	}

	resp = expiryPick(t, "", map[string]*creditsSummary{"z-stale": stale, "a-unknown": unknown}, "z-stale", "a-unknown")
	if resp.AuthID != "a-unknown" {
		t.Fatalf("unknown/stale tie must use auth ID, got %+v", resp)
	}
}

func TestSchedulerPick_ExpirySkipsExhaustedAndKeepsAllExhaustedPanel(t *testing.T) {
	now := time.Now()
	exhausted := &creditsSummary{TotalRemain: 0, TotalSize: 10, creditsFetched: now}
	available := &creditsSummary{TotalRemain: 1, creditsFetched: now, Packages: []packageSummary{
		{Remain: 1, Status: 0, DeductionEndTime: now.Add(time.Hour).UnixMilli()},
	}}
	resp := expiryPick(t, "wb-exhausted", map[string]*creditsSummary{"wb-exhausted": exhausted, "wb-ok": available}, "wb-exhausted", "wb-ok")
	if resp.AuthID != "wb-ok" || getActiveAuthID() != "wb-exhausted" {
		t.Fatalf("want available without panel mutation, got %+v panel=%q", resp, getActiveAuthID())
	}

	resp = expiryPick(t, "wb-b", map[string]*creditsSummary{"wb-a": exhausted, "wb-b": exhausted}, "wb-a", "wb-b")
	if resp.AuthID != "wb-b" || getActiveAuthID() != "wb-b" {
		t.Fatalf("all exhausted should conservatively keep panel account, got %+v panel=%q", resp, getActiveAuthID())
	}
}

func TestSchedulerPick_ExpiryDeadlineTieUsesAuthID(t *testing.T) {
	now := time.Now()
	deadline := now.Add(time.Hour).UnixMilli()
	makeCredits := func() *creditsSummary {
		return &creditsSummary{TotalRemain: 1, creditsFetched: now, Packages: []packageSummary{{Remain: 1, Status: 0, DeductionEndTime: deadline}}}
	}
	resp := expiryPick(t, "panel", map[string]*creditsSummary{"wb-z": makeCredits(), "wb-a": makeCredits()}, "wb-z", "wb-a")
	if resp.AuthID != "wb-a" || getActiveAuthID() != "panel" {
		t.Fatalf("deadline tie should pick lexical auth ID without panel mutation, got %+v panel=%q", resp, getActiveAuthID())
	}
}
