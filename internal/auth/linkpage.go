// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lmctl-ai/lmgoogle/internal/safety"
)

const EnvLmctlBin = "LMCTL_BIN"

// OAuthLinkPageData parameterizes the self-contained HTML instruction and
// click-through authorization page uploaded via `lmctl upload --link`.
type OAuthLinkPageData struct {
	ClientIDMasked string
	Scopes         []string
	AuthURL        string
	RedirectURI    string
	GeneratedAt    string
	ManualOnly     bool
}

// LinkUploadResult describes the anonymous share link returned by `lmctl upload --link --json`.
type LinkUploadResult struct {
	URL       string `json:"url"`
	Name      string `json:"name,omitempty"`
	Size      int64  `json:"size,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

const oauthLinkHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>lmgoogle — Google Analytics &amp; YouTube Authorization</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; max-width: 720px; margin: 2.5rem auto; padding: 0 1.25rem; line-height: 1.55; }
  h1 { font-size: 1.45rem; margin-bottom: 0.25rem; }
  .sub { opacity: 0.78; font-size: 0.92rem; margin-top: 0; }
  .card { border: 1px solid rgba(128,128,128,0.3); border-radius: 10px; padding: 1.15rem 1.25rem; margin: 1.1rem 0; }
  .step { font-weight: 600; font-size: 1.05rem; margin: 0 0 0.5rem 0; }
  .btn { display: inline-block; background: #1a73e8; color: #fff !important; text-decoration: none; font-weight: 600; padding: 0.7rem 1.15rem; border-radius: 8px; margin: 0.4rem 0; }
  .btn:hover { background: #1558b0; }
  ul.links { margin: 0.4rem 0 0 1.1rem; padding: 0; }
  ul.links li { margin: 0.35rem 0; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.88em; padding: 0.1rem 0.35rem; border-radius: 4px; background: rgba(128,128,128,0.14); word-break: break-all; }
  .note { font-size: 0.88rem; opacity: 0.85; }
</style>
</head>
<body>
  <h1>lmgoogle OAuth 2.0 Setup &amp; Authorization</h1>
  <p class="sub">Generated {{.GeneratedAt}}{{if .ClientIDMasked}} &middot; Client <code>{{.ClientIDMasked}}</code>{{end}}</p>

{{if .AuthURL}}
  <div class="card">
    <p class="step">Step 1: Authorize Google Analytics &amp; YouTube</p>
    <p>Click below to open Google's consent screen and approve access in one OAuth 2.0 PKCE session. No secret keys or tokens ever pass through this page.</p>
    <p><a class="btn" id="authorize-link" href="{{.AuthURL}}" target="_blank" rel="noopener noreferrer">Authorize with Google</a></p>
    <p class="note">Requested scopes:</p>
    <ul class="links">
      {{range .Scopes}}<li><code>{{.}}</code></li>
      {{end}}
    </ul>
  </div>

  <div class="card">
    <p class="step">Step 2: Complete the Loopback Callback</p>
    {{if .ManualOnly}}
    <p>Manual mode is active. After granting consent, Google redirects your browser to <code>{{.RedirectURI}}?code=...</code> (the page may show connection refused). Copy the full URL from your browser address bar and paste it into the waiting <code>lmgoogle</code> terminal prompt.</p>
    {{else}}
    <p>After clicking <strong>Allow</strong> in Google, your browser redirects to <code>{{.RedirectURI}}</code>:</p>
    <ul class="links">
      <li><strong>Local / SSH-forwarded browser:</strong> <code>lmgoogle</code> captures the callback automatically and saves your <code>0600</code> token bundle.</li>
      <li><strong>Remote browser (no localhost tunnel):</strong> if your browser shows "can't reach site" at <code>127.0.0.1</code>, copy the full <code>http://127.0.0.1:...</code> URL from your browser address bar and paste it into the waiting <code>lmgoogle</code> terminal prompt.</li>
    </ul>
    {{end}}
  </div>
{{else}}
  <div class="card">
    <p class="step">Step 1: Enable Required Google APIs</p>
    <p>Select your Google Cloud project and click each API below to enable it:</p>
    <ul class="links">
      <li><a href="https://console.cloud.google.com/apis/library/analyticsadmin.googleapis.com" target="_blank" rel="noopener noreferrer">Enable Google Analytics Admin API</a></li>
      <li><a href="https://console.cloud.google.com/apis/library/analyticsdata.googleapis.com" target="_blank" rel="noopener noreferrer">Enable Google Analytics Data API</a></li>
      <li><a href="https://console.cloud.google.com/apis/library/youtube.googleapis.com" target="_blank" rel="noopener noreferrer">Enable YouTube Data API v3</a></li>
      <li><a href="https://console.cloud.google.com/apis/library/youtubeanalytics.googleapis.com" target="_blank" rel="noopener noreferrer">Enable YouTube Analytics API</a></li>
      <li><a href="https://console.cloud.google.com/apis/library/gmail.googleapis.com" target="_blank" rel="noopener noreferrer">Enable Gmail API</a></li>
    </ul>
  </div>

  <div class="card">
    <p class="step">Step 2: Create a Desktop OAuth Client ID</p>
    <p>Create an OAuth 2.0 Client ID of application type <strong>Desktop app</strong> (and under OAuth consent screen &rarr; Audience, add your Google account under <strong>Test users</strong> while the app is in Testing status, especially for restricted Gmail scopes):</p>
    <p><a class="btn" href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noopener noreferrer">Open Google Cloud Credentials</a></p>
    <p class="note">Download the JSON file to <code>~/.config/lmgoogle/client_secret.json</code>, then run:</p>
    <p><code>lmgoogle auth login --link</code></p>
  </div>
{{end}}

  <div class="card">
    <p class="step">Manage &amp; Revoke Access Anytime</p>
    <p class="note">Review or revoke third-party Google app access at <a href="https://myaccount.google.com/permissions" target="_blank" rel="noopener noreferrer">Google Account Permissions</a>.</p>
  </div>
</body>
</html>
`

var parsedOAuthLinkTmpl = template.Must(template.New("lmgoogle_oauth_link").Parse(oauthLinkHTMLTemplate))

// RenderOAuthLinkPage renders a standalone HTML page with clickable OAuth links.
// It fails closed if secret material (client_secret or code_verifier) appears in AuthURL.
func RenderOAuthLinkPage(data OAuthLinkPageData) (string, error) {
	lowerURL := strings.ToLower(data.AuthURL)
	if strings.Contains(lowerURL, "client_secret=") || strings.Contains(lowerURL, "code_verifier=") {
		return "", errors.New("refusing to render HTML page containing OAuth secret or PKCE verifier")
	}
	if data.GeneratedAt == "" {
		data.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	var buf bytes.Buffer
	if err := parsedOAuthLinkTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render OAuth link HTML: %w", err)
	}
	return buf.String(), nil
}

func resolveLmctlBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(EnvLmctlBin)); custom != "" {
		return custom, nil
	}
	bin, err := exec.LookPath("lmctl")
	if err != nil {
		return "", fmt.Errorf("lmctl CLI not found on PATH (install @lmctl-ai/lmctl or set %s): %w", EnvLmctlBin, err)
	}
	return bin, nil
}

func normalizeHTMLReportName(name string) string {
	n := strings.TrimSpace(filepath.Base(name))
	if n == "" || n == "." || n == "/" {
		n = "lmgoogle-oauth.html"
	}
	lower := strings.ToLower(n)
	if !strings.HasSuffix(lower, ".html") && !strings.HasSuffix(lower, ".htm") {
		n += ".html"
	}
	return n
}

// UploadHTMLReport writes htmlContent to a temporary 0600 file and uploads it via
// `lmctl upload <file.html> --link --name <name> --json` to generate an anonymous share URL.
func UploadHTMLReport(ctx context.Context, htmlContent, reportName string) (*LinkUploadResult, error) {
	bin, err := resolveLmctlBinary()
	if err != nil {
		return nil, err
	}
	name := normalizeHTMLReportName(reportName)
	dir, err := os.MkdirTemp("", "lmgoogle-link-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir for HTML link: %w", err)
	}
	defer os.RemoveAll(dir)

	filePath := filepath.Join(dir, name)
	if err := os.WriteFile(filePath, []byte(htmlContent), 0o600); err != nil {
		return nil, fmt.Errorf("write temporary HTML page: %w", err)
	}

	cmd := exec.CommandContext(ctx, bin, "upload", filePath, "--link", "--name", name, "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("lmctl upload --link failed: %v (%s)", err, safety.RedactSecrets(msg))
	}

	out := strings.TrimSpace(stdout.String())
	var res LinkUploadResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		// Fallback if lmctl emitted a bare URL despite --json.
		if strings.HasPrefix(out, "http://") || strings.HasPrefix(out, "https://") {
			res.URL = out
			res.Name = name
		} else {
			return nil, fmt.Errorf("decode lmctl upload response: %w", err)
		}
	}
	if strings.TrimSpace(res.URL) == "" {
		return nil, errors.New("lmctl upload --link returned empty URL")
	}
	if res.Name == "" {
		res.Name = name
	}
	return &res, nil
}

// RemoveUploadedReport removes a previously uploaded temporary report via `lmctl uploads rm`.
func RemoveUploadedReport(ctx context.Context, reportName string) error {
	bin, err := resolveLmctlBinary()
	if err != nil {
		return err
	}
	name := normalizeHTMLReportName(reportName)
	cmd := exec.CommandContext(ctx, bin, "uploads", "rm", name, "--json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("lmctl uploads rm %q: %v (%s)", name, err, safety.RedactSecrets(strings.TrimSpace(stderr.String())))
	}
	return nil
}
