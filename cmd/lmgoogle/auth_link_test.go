// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lmctl-ai/lmgoogle/internal/auth"
)

func installMockLmctl(t *testing.T) (stubPath, uploadedHTMLPath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	uploadedHTMLPath = filepath.Join(dir, "uploaded.html")
	logPath = filepath.Join(dir, "lmctl.log")
	stubPath = filepath.Join(dir, "lmctl")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "$*" >> "` + logPath + `"
if [ "${1:-}" = "upload" ]; then
  cp "$2" "` + uploadedHTMLPath + `"
  name="report.html"
  shift 2
  while [ $# -gt 0 ]; do
    if [ "$1" = "--name" ]; then name="$2"; shift 2; else shift; fi
  done
  printf '{"name":"%s","url":"https://lmctl.ai/r/anon-session/%s","size":2048}\n' "$name" "$name"
  exit 0
fi
if [ "${1:-}" = "uploads" ] && [ "${2:-}" = "rm" ]; then
  printf '{"name":"%s","removed":true}\n' "${3:-}"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(stubPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write lmctl stub: %v", err)
	}
	t.Setenv(auth.EnvLmctlBin, stubPath)
	return stubPath, uploadedHTMLPath, logPath
}

func TestAuthSetupLink(t *testing.T) {
	_, uploadedHTML, _ := installMockLmctl(t)
	var out, errBuf bytes.Buffer
	code := runAuthSetupIO([]string{"--link", "--link-name", "ga-yt-setup.html"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("runAuthSetupIO exit=%d stderr=%s", code, errBuf.String())
	}

	var event map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(errBuf.Bytes()), &event); err != nil {
		t.Fatalf("expected JSON line on stderr, got %q: %v", errBuf.String(), err)
	}
	if event["setup"] != "https://lmctl.ai/r/anon-session/ga-yt-setup.html" {
		t.Fatalf("unexpected setup URL event: %+v", event)
	}

	raw, err := os.ReadFile(uploadedHTML)
	if err != nil {
		t.Fatalf("read uploaded HTML: %v", err)
	}
	page := string(raw)
	for _, want := range []string{
		"analyticsadmin.googleapis.com",
		"analyticsdata.googleapis.com",
		"youtube.googleapis.com",
		"youtubeanalytics.googleapis.com",
		"console.cloud.google.com/apis/credentials",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("setup page missing %q", want)
		}
	}
}

func TestAuthLoginLinkLoopbackEndToEnd(t *testing.T) {
	_, uploadedHTML, logPath := installMockLmctl(t)

	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "missing PKCE S256", http.StatusBadRequest)
			return
		}
		redir := q.Get("redirect_uri")
		http.Redirect(w, r, redir+"?code=auth-code-from-click&state="+q.Get("state"), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("code") != "auth-code-from-click" || r.FormValue("code_verifier") == "" {
			http.Error(w, "invalid grant or verifier", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.link-access","refresh_token":"1//link-refresh","token_type":"Bearer","expires_in":3600}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	dir := t.TempDir()
	clientSecret := filepath.Join(dir, "client_secret.json")
	tokenFile := filepath.Join(dir, "token.json")
	const testSecret = "GOCSPX-link-test-secret"
	secretJSON := `{"installed":{"client_id":"link-client-999.apps.googleusercontent.com","client_secret":"` + testSecret + `","auth_uri":"` + srvURL + `/auth","token_uri":"` + srvURL + `/token","redirect_uris":["http://localhost"]}}`
	if err := os.WriteFile(clientSecret, []byte(secretJSON), 0o600); err != nil {
		t.Fatalf("write client_secret: %v", err)
	}

	var out, errBuf bytes.Buffer
	done := make(chan int, 1)
	go func() {
		// Pass empty stdin EOF immediately so we also prove non-interactive stdin does not abort loopback wait.
		done <- runAuthLoginIO([]string{
			"--client-secret", clientSecret,
			"--token-file", tokenFile,
			"--link",
			"--link-name", "click-oauth.html",
		}, strings.NewReader(""), &out, &errBuf)
	}()

	// Wait until uploadedHTML exists, then parse and click the embedded Authorize link.
	deadline := time.Now().Add(5 * time.Second)
	var pageBytes []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(uploadedHTML); err == nil && len(b) > 0 {
			pageBytes = b
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if len(pageBytes) == 0 {
		t.Fatalf("uploaded HTML was never created; stderr=%s", errBuf.String())
	}

	page := string(pageBytes)
	if strings.Contains(page, testSecret) {
		t.Fatalf("client_secret leaked into uploaded HTML")
	}
	re := regexp.MustCompile(`id="authorize-link"[^>]*href="([^"]+)"`)
	m := re.FindStringSubmatch(page)
	if len(m) != 2 {
		t.Fatalf("could not find #authorize-link href in uploaded HTML:\n%s", page)
	}
	authorizeURL := html.UnescapeString(m[1])

	resp, err := http.Get(authorizeURL)
	if err != nil {
		t.Fatalf("clicking authorize URL failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 callback confirmation page, got %d", resp.StatusCode)
	}

	exitCode := <-done
	if exitCode != 0 {
		t.Fatalf("runAuthLoginIO exit=%d stderr=%s stdout=%s", exitCode, errBuf.String(), out.String())
	}
	if !strings.Contains(errBuf.String(), `"approve":"https://lmctl.ai/r/anon-session/click-oauth.html"`) {
		t.Fatalf("expected approve JSON event on stderr, got %q", errBuf.String())
	}

	bundle, err := auth.LoadTokenBundle(tokenFile)
	if err != nil {
		t.Fatalf("LoadTokenBundle: %v", err)
	}
	if bundle.Token.AccessToken != "ya29.link-access" || bundle.Token.RefreshToken != "1//link-refresh" {
		t.Fatalf("unexpected token bundle: %+v", bundle.Token)
	}

	logs, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logs), "uploads rm click-oauth.html --json") {
		t.Fatalf("expected lmctl uploads rm cleanup call, got: %s", string(logs))
	}
}
