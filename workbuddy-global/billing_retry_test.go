package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestBillingCall_RetriesOn5xx verifies that a transient upstream 500 is
// retried and ultimately succeeds when the next attempt returns 200.
func TestBillingCall_RetriesOn5xx(t *testing.T) {
	orig := billingRetryDelays
	billingRetryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { billingRetryDelays = orig }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 { // first two attempts → 500
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"k":"v"}}`))
	}))
	defer srv.Close()

	// Temporarily override billingBase so the test server is used.
	restore := setBillingBaseGlobal(srv.URL)
	defer restore()

	sa := &storedAuth{}
	data, err := billingCall(sa, "/test", nil)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if string(data) != `{"k":"v"}` {
		t.Fatalf("unexpected data: %s", string(data))
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (2 retry), got %d", calls)
	}
}

// TestBillingCall_NoRetryOn4xx verifies that business-level errors (4xx,
// non-zero code) are not retried.
func TestBillingCall_NoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"msg":"bad request"}`))
	}))
	defer srv.Close()

	restore := setBillingBaseGlobal(srv.URL)
	defer restore()

	sa := &storedAuth{}
	_, err := billingCall(sa, "/test", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should not retry on 4xx — exactly 1 call.
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry on 4xx), got %d", calls)
	}
}

// TestIsTransientBillingErr covers classification boundaries.
func TestIsTransientBillingErr(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("http 500 from /v2/billing: internal"), true},
		{errors.New("http 503 from /v2/billing: unavailable"), true},
		{errors.New("code=10000 msg=API request failed"), false}, // business code, not transient
		{errors.New("parse failed: unexpected EOF"), false},
	}
	for _, tt := range tests {
		if got := isTransientBillingErr(tt.err); got != tt.want {
			t.Errorf("isTransientBillingErr(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestFetchUserResourceRetainsPackageMetadata(t *testing.T) {
	const (
		deductionStart = int64(1764547200123)
		deductionEnd   = int64(1767225599876)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/billing/meter/get-user-resource" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"Response":{"Data":{"TotalCount":1,"TotalDosage":120,"Accounts":[{"PackageName":"daily grant","PackageCode":"TCACA_code_007_nzdH5h4Nl0","ResourceId":"resource-42","Status":3,"CapacityRemain":75,"CapacitySize":120,"CycleCapacityRemain":75,"CycleCapacitySize":120,"CycleStartTime":"2025-12-01 00:00:00","CycleEndTime":"2025-12-31 23:59:59","DeductionStartTime":1764547200123,"DeductionEndTime":1767225599876}]}}}}`))
	}))
	defer srv.Close()

	restore := setBillingBaseGlobal(srv.URL)
	defer restore()

	summary, err := fetchUserResource(&storedAuth{})
	if err != nil {
		t.Fatalf("fetch user resource: %v", err)
	}
	if len(summary.Packages) != 1 {
		t.Fatalf("package count = %d, want 1", len(summary.Packages))
	}
	pkg := summary.Packages[0]
	if pkg.PackageCode != "TCACA_code_007_nzdH5h4Nl0" || pkg.ResourceID != "resource-42" || pkg.Status != 3 {
		t.Fatalf("package identity not retained: %+v", pkg)
	}
	if pkg.DeductionStartTime != deductionStart || pkg.DeductionEndTime != deductionEnd {
		t.Fatalf("deduction window = (%d, %d), want (%d, %d)", pkg.DeductionStartTime, pkg.DeductionEndTime, deductionStart, deductionEnd)
	}
	if pkg.Remain != 75 || pkg.Used != 45 || pkg.Size != 120 || summary.TotalRemain != 75 || summary.TotalUsed != 45 || summary.TotalSize != 120 {
		t.Fatalf("aggregation changed: package=%+v summary=%+v", pkg, summary)
	}
	if summary.creditsFetched.IsZero() {
		t.Fatal("successful fetch did not record authoritative credits freshness")
	}

	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	var output struct {
		Packages []map[string]any `json:"packages"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("decode summary output: %v", err)
	}
	for _, key := range []string{"name", "remain", "used", "size", "cycle_start", "cycle_end", "package_code", "resource_id", "status", "deduction_start_time", "deduction_end_time"} {
		if _, ok := output.Packages[0][key]; !ok {
			t.Errorf("JSON package output missing %q: %s", key, raw)
		}
	}
}
