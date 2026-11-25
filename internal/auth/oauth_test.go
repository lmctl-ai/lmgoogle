// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestLoopbackFlowAndTokenExchange(t *testing.T) {
	var gotVerifier, gotCode string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotCode = r.Form.Get("code")
		gotVerifier = r.Form.Get("code_verifier")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.fake-access","refresh_token":"1//fake-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	cfg := &oauth2.Config{
		ClientID:     "demo-client-id.apps.googleusercontent.com",
		ClientSecret: "GOCSPX-demo-secret",
		Scopes:       DefaultScopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: tokenSrv.URL,
		},
	}

	flow, err := StartLoopbackFlow(cfg)
	if err != nil {
		t.Fatalf("StartLoopbackFlow failed: %v", err)
	}
	defer flow.Close()

	parsed, err := url.Parse(flow.AuthURL)
	if err != nil {
		t.Fatalf("invalid AuthURL: %v", err)
	}
	if parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("expected S256 PKCE challenge")
	}

	// Simulate browser redirect back to loopback callback
	go func() {
		cb := flow.Redirect + "?state=" + url.QueryEscape(flow.PKCE.State) + "&code=auth-code-xyz"
		resp, err := http.Get(cb)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, err := flow.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if code != "auth-code-xyz" {
		t.Fatalf("code = %q, want auth-code-xyz", code)
	}

	tok, err := ExchangeCode(ctx, cfg, flow.Redirect, code, flow.PKCE.Verifier)
	if err != nil {
		t.Fatalf("ExchangeCode failed: %v", err)
	}
	if gotCode != "auth-code-xyz" || gotVerifier != flow.PKCE.Verifier {
		t.Fatalf("unexpected exchange params: code=%q verifier=%q", gotCode, gotVerifier)
	}
	if tok.AccessToken != "ya29.fake-access" || tok.RefreshToken != "1//fake-refresh" {
		t.Fatalf("unexpected token: %+v", tok)
	}

	tokenPath := filepath.Join(t.TempDir(), "lmgoogle", "token.json")
	bundle := TokenBundle{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		AuthURI:      cfg.Endpoint.AuthURL,
		TokenURI:     cfg.Endpoint.TokenURL,
		Scopes:       cfg.Scopes,
		Token:        tok,
	}
	if err := SaveTokenBundle(tokenPath, bundle); err != nil {
		t.Fatalf("SaveTokenBundle failed: %v", err)
	}
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 permissions, got %o", info.Mode().Perm())
	}

	loaded, err := LoadTokenBundle(tokenPath)
	if err != nil {
		t.Fatalf("LoadTokenBundle failed: %v", err)
	}
	if loaded.ClientID != cfg.ClientID || loaded.Token.RefreshToken != "1//fake-refresh" {
		t.Fatalf("loaded mismatch: %+v", loaded)
	}

	// Verify LiveClientOptions picks up the saved token bundle
	t.Setenv(EnvTokenFile, tokenPath)
	t.Setenv(EnvAuthMode, "oauth2")
	mode, opts, err := LiveClientOptions(ctx)
	if err != nil || mode != "oauth2-live" || len(opts) != 1 {
		t.Fatalf("LiveClientOptions got mode=%q opts=%d err=%v", mode, len(opts), err)
	}
}

func TestLoadClientConfigFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client_secret.json")
	payload := map[string]any{
		"installed": map[string]any{
			"client_id":     "12345-abc.apps.googleusercontent.com",
			"client_secret": "GOCSPX-testsecret",
			"auth_uri":      "https://accounts.google.com/o/oauth2/auth",
			"token_uri":     "https://oauth2.googleapis.com/token",
			"redirect_uris": []string{"http://localhost"},
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadClientConfig(path, nil)
	if err != nil {
		t.Fatalf("LoadClientConfig failed: %v", err)
	}
	if cfg.ClientID != "12345-abc.apps.googleusercontent.com" || len(cfg.Scopes) != len(DefaultScopes) {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !strings.Contains(strings.Join(cfg.Scopes, " "), "youtube.upload") {
		t.Fatalf("expected youtube.upload scope in defaults")
	}
}
