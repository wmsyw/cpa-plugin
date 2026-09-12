// scheduler.go implements WorkBuddy's CPA scheduler.pick capability.
//
// Credits mode follows the panel-selected account. Expiry mode instead uses
// fresh cached package metadata to spend credits with the earliest deductible
// deadline first. Neither scheduler path performs network I/O.
// When an account encounters 429 / rate limit, penalizeAuth moves it to the
// back of the candidate line so subsequent requests automatically rotate to
// the next healthy credential without requiring manual intervention or timers.
package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	schedulerModeOff     = "off"
	schedulerModeCredits = "credits"
	schedulerModeExpiry  = "expiry"
)

var (
	schedulerMode   = schedulerModeOff
	schedulerModeMu sync.RWMutex

	authPenaltyMu  sync.RWMutex
	authPenaltySeq int64
	authPenalties  = make(map[string]int64)
)

func normalizeAuthPenaltyKey(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimSuffix(id, ".json")
	id = strings.TrimPrefix(id, "wbglobal-")
	id = strings.TrimPrefix(id, "workbuddy-global-")
	id = strings.TrimPrefix(id, "workbuddy-")
	return id
}

// penalizeAuth records that this auth ID encountered 429 / rate limit / quota failure.
// Each penalized auth is stamped with an increasing sequence number so it sorts to
// the back of the candidate queue, yielding its turn to healthy accounts.
func penalizeAuth(identifiers ...string) {
	authPenaltyMu.Lock()
	defer authPenaltyMu.Unlock()
	authPenaltySeq++
	for _, raw := range identifiers {
		if k := normalizeAuthPenaltyKey(raw); k != "" {
			authPenalties[k] = authPenaltySeq
		}
	}
}

// clearAuthPenalty clears the penalty after an account successfully completes a request.
func clearAuthPenalty(identifiers ...string) {
	authPenaltyMu.Lock()
	defer authPenaltyMu.Unlock()
	for _, raw := range identifiers {
		if k := normalizeAuthPenaltyKey(raw); k != "" {
			delete(authPenalties, k)
		}
	}
}

// getAuthPenalty returns the penalty sequence for an auth ID (0 = unpenalized).
func getAuthPenalty(id string) int64 {
	authPenaltyMu.RLock()
	defer authPenaltyMu.RUnlock()
	k := normalizeAuthPenaltyKey(id)
	if k == "" {
		return 0
	}
	return authPenalties[k]
}

// setSchedulerMode is a test helper that returns a restore func.
func setSchedulerMode(mode string) func() {
	schedulerModeMu.Lock()
	old := schedulerMode
	schedulerMode = mode
	schedulerModeMu.Unlock()
	return func() {
		schedulerModeMu.Lock()
		schedulerMode = old
		schedulerModeMu.Unlock()
	}
}

func loadedSchedulerMode() string {
	schedulerModeMu.RLock()
	defer schedulerModeMu.RUnlock()
	return schedulerMode
}

// handleSchedulerPick selects an enabled WorkBuddy auth candidate. Other
// providers are always deferred to the built-in scheduler.
func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}

	mode := loadedSchedulerMode()
	if mode != schedulerModeCredits && mode != schedulerModeExpiry {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}

	// Collect enabled WorkBuddy candidates only.
	var wbCandidates []pluginapi.SchedulerAuthCandidate
	for _, c := range req.Candidates {
		if c.Provider != providerName || candidateDisabled(c) {
			continue
		}
		// Gate on model readiness: auths whose catalog bootstrap failed are
		// excluded so requests never route to an account without a model list.
		if !currentModelRuntime().snapshotForAuthID(c.ID).State.executable() {
			continue
		}
		wbCandidates = append(wbCandidates, c)
	}
	if len(wbCandidates) == 0 {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}

	var picked string
	if mode == schedulerModeExpiry {
		picked = pickExpiryAuth(wbCandidates, time.Now())
	} else {
		// Preserve credits mode's panel-sticky behavior exactly.
		cands := make([]activeAuthCandidate, 0, len(wbCandidates))
		for _, c := range wbCandidates {
			_, exhausted := cachedCreditsScore(c.ID)
			cands = append(cands, activeAuthCandidate{
				ID:        c.ID,
				Disabled:  false, // already filtered
				Exhausted: exhausted,
			})
		}
		picked = pickActiveAuth(cands)
	}

	if picked == "" {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}
	return okEnvelope(pluginapi.SchedulerPickResponse{
		AuthID:  picked,
		Handled: true,
	})
}

type expiryCandidate struct {
	id        string
	deadline  int64
	known     bool
	exhausted bool
	penalty   int64
}

// pickExpiryAuth ranks candidates by penalty first (429 moved to end of queue),
// then by package expiration deadlines.
func pickExpiryAuth(candidates []pluginapi.SchedulerAuthCandidate, now time.Time) string {
	ranked := make([]expiryCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		c := cachedExpiryCandidate(candidate.ID, now)
		c.penalty = getAuthPenalty(candidate.ID)
		ranked = append(ranked, c)
	}

	available := make([]expiryCandidate, 0, len(ranked))
	for _, candidate := range ranked {
		if !candidate.exhausted {
			available = append(available, candidate)
		}
	}
	if len(available) == 0 {
		current := getActiveAuthID()
		for _, candidate := range ranked {
			if candidate.id == current {
				return current
			}
		}
		if len(ranked) > 0 {
			return ranked[0].id
		}
		return ""
	}

	sort.SliceStable(available, func(i, j int) bool {
		left, right := available[i], available[j]
		// 1. Unpenalized candidates always come before penalized candidates
		if (left.penalty == 0) != (right.penalty == 0) {
			return left.penalty == 0
		}
		// 2. Among penalized candidates, the one penalized EARLIER comes first
		if left.penalty != right.penalty {
			return left.penalty < right.penalty
		}
		// 3. Normal expiry ranking
		if left.known != right.known {
			return left.known
		}
		if left.known && left.deadline != right.deadline {
			return left.deadline < right.deadline
		}
		return left.id < right.id
	})
	return available[0].id
}

func cachedExpiryCandidate(authID string, now time.Time) expiryCandidate {
	candidate := expiryCandidate{id: authID}
	value, ok := accountCache.Load(authID)
	if !ok {
		return candidate
	}
	entry, ok := value.(*accountCacheEntry)
	if !ok || entry == nil || entry.credits == nil {
		return candidate
	}
	credits := entry.credits
	age := now.Sub(credits.creditsFetched)
	if credits.creditsFetched.IsZero() || age < 0 || age > accountCacheTTL {
		return candidate
	}
	candidate.exhausted = isCreditsExhausted(credits)
	if candidate.exhausted {
		return candidate
	}
	nowMillis := now.UnixMilli()
	for _, pkg := range credits.Packages {
		if pkg.Status != 0 || pkg.Remain <= 0 {
			continue
		}
		if pkg.DeductionStartTime > nowMillis {
			continue
		}
		if pkg.DeductionEndTime <= nowMillis {
			continue
		}
		if !candidate.known || pkg.DeductionEndTime < candidate.deadline {
			candidate.known = true
			candidate.deadline = pkg.DeductionEndTime
		}
	}
	return candidate
}

// candidateDisabled reports host-disabled auth from Status/metadata.
func candidateDisabled(c pluginapi.SchedulerAuthCandidate) bool {
	st := strings.ToLower(strings.TrimSpace(c.Status))
	if st == "disabled" {
		return true
	}
	if c.Metadata != nil {
		if v, ok := c.Metadata["disabled"]; ok {
			switch t := v.(type) {
			case bool:
				return t
			case string:
				return strings.EqualFold(strings.TrimSpace(t), "true")
			}
		}
	}
	return false
}

// cachedCreditsScore returns (remain, exhausted) from accountCache.
func cachedCreditsScore(authID string) (int64, bool) {
	v, ok := accountCache.Load(authID)
	if !ok {
		return -1, false
	}
	entry, ok := v.(*accountCacheEntry)
	if !ok || entry.credits == nil {
		return -1, false
	}
	return entry.credits.TotalRemain, isCreditsExhausted(entry.credits)
}
