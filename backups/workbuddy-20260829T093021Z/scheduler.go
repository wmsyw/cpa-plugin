// scheduler.go implements WorkBuddy's CPA scheduler.pick capability.
//
// Credits mode follows the panel-selected account. Expiry mode instead uses
// fresh cached package metadata to spend credits with the earliest deductible
// deadline first. Neither scheduler path performs network I/O.
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
)

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
//
// scheduler_mode:
//   - "off"     → defer everything to the built-in scheduler.
//   - "credits" → use the panel-selected account, with its existing sticky
//     fallback behavior.
//   - "expiry"  → use fresh cached package deadlines without changing the
//     panel selection.
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
}

// pickExpiryAuth ranks only cached data. Fresh, known package deadlines sort
// before unknown data, then by earliest deadline and finally by auth ID. It
// deliberately does not call setActiveAuthID: expiry routing is independent
// from the account selected in the panel.
func pickExpiryAuth(candidates []pluginapi.SchedulerAuthCandidate, now time.Time) string {
	ranked := make([]expiryCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, cachedExpiryCandidate(candidate.ID, now))
	}

	available := ranked[:0]
	for _, candidate := range ranked {
		if !candidate.exhausted {
			available = append(available, candidate)
		}
	}
	if len(available) == 0 {
		// Preserve credits mode's conservative all-exhausted fallback: keep a
		// live panel selection, otherwise use the host's first candidate.
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

	sort.Slice(available, func(i, j int) bool {
		left, right := available[i], available[j]
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
// remain is -1 when unknown; exhausted uses isCreditsExhausted.
// Key is auth.ID (same as SchedulerAuthCandidate.ID and activeAuthID).
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
