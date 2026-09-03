package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func testGlobalJWT(t *testing.T, issuer string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"iss": issuer})
	if err != nil {
		t.Fatal(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestValidateGlobalAuthRequiresGlobalRealm(t *testing.T) {
	globalJWT := testGlobalJWT(t, "https://auth.workbuddy.ai/realms/workbuddy")
	cnJWT := testGlobalJWT(t, "https://auth.codebuddy.cn/realms/codebuddy")
	cases := []struct {
		name   string
		sa     *storedAuth
		wantOK bool
	}{
		{"global domain opaque token", &storedAuth{Auth: storedTokens{AccessToken: "opaque", Domain: "www.workbuddy.ai"}}, true},
		{"global domain global JWT", &storedAuth{Auth: storedTokens{AccessToken: globalJWT, Domain: "www.workbuddy.ai"}}, true},
		{"empty domain global JWT", &storedAuth{Auth: storedTokens{AccessToken: globalJWT}}, true},
		{"empty domain opaque token", &storedAuth{Auth: storedTokens{AccessToken: "opaque"}}, false},
		{"CN domain", &storedAuth{Auth: storedTokens{AccessToken: "opaque", Domain: "www.codebuddy.cn"}}, false},
		{"global domain CN JWT", &storedAuth{Auth: storedTokens{AccessToken: cnJWT, Domain: "www.workbuddy.ai"}}, false},
	}
	for _, tc := range cases {
		err := validateGlobalAuth(tc.sa)
		if (err == nil) != tc.wantOK {
			t.Errorf("%s: err=%v, wantOK=%v", tc.name, err, tc.wantOK)
		}
	}
	if got := cases[2].sa.Auth.Domain; got != "www.workbuddy.ai" {
		t.Fatalf("empty-domain Global JWT normalized to %q", got)
	}
}
