// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderOAuthLinkPageAndSecretGuards(t *testing.T) {
	setupHTML, err := RenderOAuthLinkPage(OAuthLinkPageData{
		GeneratedAt: "2026-11-21T16:00:00Z",
	})
	if err != nil {
		t.Fatalf("setup page error: %v", err)
	}
	for _, want := range []string{
		"analyticsadmin.googleapis.com",
		"analyticsdata.googleapis.com",
		"youtube.googleapis.com",
		"youtubeanalytics.googleapis.com",
		"gmail.googleapis.com",
		"console.cloud.google.com/apis/credentials",
	} {
		if !strings.Contains(setupHTML, want) {
			t.Fatalf("setup HTML missing %q", want)
		}
	}

	authHTML, err := RenderOAuthLinkPage(OAuthLinkPageData{
		ClientIDMasked: "123456******7890",
		Scopes:         []string{"https://www.googleapis.com/auth/analytics.readonly"},
		AuthURL:        "https://accounts.google.com/o/oauth2/v2/auth?client_id=abc&code_challenge=xyz&state=s1",
		RedirectURI:    "http://127.0.0.1:41234/callback",
		GeneratedAt:    "2026-11-21T16:00:00Z",
	})
	if err != nil {
		t.Fatalf("auth page error: %v", err)
	}
	if !strings.Contains(authHTML, `id="authorize-link"`) || !strings.Contains(authHTML, "http://127.0.0.1:41234/callback") {
		t.Fatalf("auth HTML missing authorize button or callback URI: %s", authHTML)
	}

	if _, err := RenderOAuthLinkPage(OAuthLinkPageData{
		AuthURL: "https://accounts.google.com/o/oauth2/v2/auth?code_verifier=leak",
	}); err == nil {
		t.Fatal("expected refusal when code_verifier appears in AuthURL")
	}
}

func TestUploadAndRemoveHTMLReportWithLmctlStub(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	uploadedCopy := filepath.Join(dir, "uploaded.html")
	stub := filepath.Join(dir, "lmctl")
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "$*" >> "` + logFile + `"
if [ "${1:-}" = "upload" ]; then
  cp "$2" "` + uploadedCopy + `"
  echo '{"name":"custom-oauth.html","url":"https://lmctl.ai/r/anon123/custom-oauth.html","size":512}'
  exit 0
fi
if [ "${1:-}" = "uploads" ] && [ "${2:-}" = "rm" ]; then
  echo '{"name":"custom-oauth.html","removed":true}'
  exit 0
fi
exit 2
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv(EnvLmctlBin, stub)

	res, err := UploadHTMLReport(context.Background(), "<html><body>hello</body></html>", "custom-oauth")
	if err != nil {
		t.Fatalf("UploadHTMLReport: %v", err)
	}
	if res.URL != "https://lmctl.ai/r/anon123/custom-oauth.html" || res.Name != "custom-oauth.html" {
		t.Fatalf("unexpected upload result: %+v", res)
	}
	gotHTML, err := os.ReadFile(uploadedCopy)
	if err != nil || string(gotHTML) != "<html><body>hello</body></html>" {
		t.Fatalf("uploaded HTML mismatch: %v %q", err, string(gotHTML))
	}

	if err := RemoveUploadedReport(context.Background(), res.Name); err != nil {
		t.Fatalf("RemoveUploadedReport: %v", err)
	}
	logData, _ := os.ReadFile(logFile)
	logs := string(logData)
	if !strings.Contains(logs, "--link --name custom-oauth.html --json") || !strings.Contains(logs, "uploads rm custom-oauth.html --json") {
		t.Fatalf("unexpected lmctl stub calls: %s", logs)
	}
}
