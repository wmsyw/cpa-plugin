package main

import "testing"

func TestIsGlobalDomain(t *testing.T) {
	cases := []struct {
		domain string
		want   bool
	}{
		{"www.workbuddy.ai", true}, {"workbuddy.ai", true}, {"auth.workbuddy.ai", true}, {"codebuddy.ai", true},
		{"www.codebuddy.cn", false}, {"", false}, {"WORKBUDDY.AI", true}, {"  www.workbuddy.ai  ", true},
		{"evilworkbuddy.ai", false}, {"workbuddy.ai.evil.com", false}, {"notworkbuddy.ai", false},
	}
	for _, tc := range cases {
		if got := isGlobalDomain(tc.domain); got != tc.want {
			t.Errorf("isGlobalDomain(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

func TestAccountRegionIsGlobalOnly(t *testing.T) {
	for _, sa := range []*storedAuth{nil, {}, {Auth: storedTokens{Domain: "www.codebuddy.cn"}}, {Auth: storedTokens{Domain: "www.workbuddy.ai"}}} {
		if got := accountRegion(sa); got != "global" {
			t.Errorf("accountRegion() = %q, want global", got)
		}
	}
}

func TestBillingBaseForIsGlobalOnly(t *testing.T) {
	for _, sa := range []*storedAuth{nil, {}, {Auth: storedTokens{Domain: "www.codebuddy.cn"}}, {Auth: storedTokens{Domain: "www.workbuddy.ai"}}} {
		if got := billingBaseFor(sa); got != billingBaseGlobal {
			t.Errorf("billingBaseFor() = %q, want %q", got, billingBaseGlobal)
		}
	}
}

func TestOriginRefererForIsGlobalOnly(t *testing.T) {
	for _, sa := range []*storedAuth{nil, {}, {Auth: storedTokens{Domain: "www.codebuddy.cn"}}, {Auth: storedTokens{Domain: "www.workbuddy.ai"}}} {
		if got := originRefererFor(sa); got != "https://www.workbuddy.ai" {
			t.Errorf("originRefererFor() = %q, want Global origin", got)
		}
	}
}
