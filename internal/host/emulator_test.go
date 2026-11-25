// SPDX-License-Identifier: MIT

package host

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmulatorEndpointAndAuthRetention(t *testing.T) {
	token := "dummy-private-oauth-token"
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":401,"status":"UNAUTHENTICATED","message":"invalid token ` + token + `"}}`))
	}))
	defer srv.Close()

	t.Setenv("LMGOOGLE_ENDPOINT", srv.URL)
	t.Setenv("LMGOOGLE_ACCESS_TOKEN", token)
	t.Setenv("LMGOOGLE_LIVE", "")
	h, err := NewHost(false)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.fakeSrv != nil || h.AuthMode() != "external-emulator" {
		t.Fatalf("unexpected emulator host state: fake=%v mode=%s", h.fakeSrv != nil, h.AuthMode())
	}
	h.GaListAccounts()
	if requests != 1 || !strings.Contains(h.AuthError(), "401") || strings.Contains(h.AuthError(), token) {
		t.Fatalf("requests=%d authError=%q", requests, h.AuthError())
	}
	h.clearErr()
	if h.AuthError() == "" {
		t.Fatal("auth failure was not retained across clearErr")
	}
}

func TestRejectUnsafeEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"https://googleapis.com",
		"http://user:secret@localhost",
		"http://localhost/path",
		"http://localhost?token=secret",
	} {
		t.Setenv("LMGOOGLE_ENDPOINT", endpoint)
		if _, err := NewHost(false); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatalf("leaked secret from endpoint %q: %v", endpoint, err)
		}
	}
}
