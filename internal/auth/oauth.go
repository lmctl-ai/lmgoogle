// SPDX-License-Identifier: MIT

// Package auth implements native open-source OAuth 2.0 Installed-App (Loopback + PKCE)
// authentication for Google Analytics and YouTube, plus optional ADC fallback.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/auth/credentials"
	"github.com/lmctl-ai/lmgoogle/internal/safety"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

const (
	EnvTokenFile    = "LMGOOGLE_TOKEN_FILE"
	EnvClientSecret = "LMGOOGLE_CLIENT_SECRET"
	EnvAuthMode     = "LMGOOGLE_AUTH_MODE"
)

// DefaultScopes covers Google Analytics (Admin + Data), YouTube (Data + Upload + Analytics), and Gmail.
var DefaultScopes = []string{
	"https://www.googleapis.com/auth/analytics.edit",
	"https://www.googleapis.com/auth/analytics.readonly",
	"https://www.googleapis.com/auth/youtube",
	"https://www.googleapis.com/auth/youtube.upload",
	"https://www.googleapis.com/auth/yt-analytics.readonly",
	"https://www.googleapis.com/auth/gmail.modify",
	"https://www.googleapis.com/auth/gmail.readonly",
}

// ReadOnlyScopes covers read-only Analytics, YouTube, and Gmail access.
var ReadOnlyScopes = []string{
	"https://www.googleapis.com/auth/analytics.readonly",
	"https://www.googleapis.com/auth/youtube.readonly",
	"https://www.googleapis.com/auth/yt-analytics.readonly",
	"https://www.googleapis.com/auth/gmail.readonly",
}

// TokenBundle stores the OAuth2 client configuration (without requiring gcloud)
// alongside the persisted oauth2.Token so refresh works seamlessly across runs.
type TokenBundle struct {
	ClientID     string        `json:"client_id"`
	ClientSecret string        `json:"client_secret,omitempty"`
	AuthURI      string        `json:"auth_uri,omitempty"`
	TokenURI     string        `json:"token_uri,omitempty"`
	Scopes       []string      `json:"scopes,omitempty"`
	Token        *oauth2.Token `json:"token"`
	SavedAt      string        `json:"saved_at,omitempty"`
}

// DefaultConfigDir returns the user config directory for lmgoogle (~/.config/lmgoogle).
func DefaultConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "lmgoogle")
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		return filepath.Join(home, ".config", "lmgoogle")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("lmgoogle-%d", os.Getuid()))
}

// DefaultTokenPath returns LMGOOGLE_TOKEN_FILE or ~/.config/lmgoogle/token.json.
func DefaultTokenPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvTokenFile)); p != "" {
		return p
	}
	return filepath.Join(DefaultConfigDir(), "token.json")
}

// DefaultClientSecretPath returns LMGOOGLE_CLIENT_SECRET or ~/.config/lmgoogle/client_secret.json.
func DefaultClientSecretPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvClientSecret)); p != "" {
		return p
	}
	return filepath.Join(DefaultConfigDir(), "client_secret.json")
}

// LoadClientConfig parses a Google Cloud Console OAuth2 Desktop client_secret.json.
func LoadClientConfig(path string, scopes []string) (*oauth2.Config, error) {
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read client secret %q: %w", path, err)
	}
	cfg, err := google.ConfigFromJSON(raw, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parse OAuth client_secret.json: %s", safety.RedactSecrets(err.Error()))
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, errors.New("client_secret.json missing client_id")
	}
	return cfg, nil
}

// SaveTokenBundle writes the OAuth2 bundle with strict 0600 permissions.
func SaveTokenBundle(path string, bundle TokenBundle) error {
	if bundle.Token == nil {
		return errors.New("cannot save nil OAuth2 token")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if bundle.SavedAt == "" {
		bundle.SavedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit token file: %w", err)
	}
	return nil
}

// LoadTokenBundle reads a saved TokenBundle from path.
func LoadTokenBundle(path string) (*TokenBundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bundle TokenBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, fmt.Errorf("decode token bundle: %w", err)
	}
	if bundle.Token == nil || strings.TrimSpace(bundle.ClientID) == "" {
		return nil, errors.New("token bundle is missing client_id or token")
	}
	return &bundle, nil
}

func (b *TokenBundle) oauth2Config() *oauth2.Config {
	authURL := b.AuthURI
	if authURL == "" {
		authURL = google.Endpoint.AuthURL
	}
	tokenURL := b.TokenURI
	if tokenURL == "" {
		tokenURL = google.Endpoint.TokenURL
	}
	scopes := b.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	return &oauth2.Config{
		ClientID:     b.ClientID,
		ClientSecret: b.ClientSecret,
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}
}

// persistingTokenSource refreshes tokens automatically and updates the stored bundle on change.
type persistingTokenSource struct {
	base       oauth2.TokenSource
	path       string
	bundle     TokenBundle
	lastAccess string
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := p.base.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh OAuth2 token: %s", safety.RedactSecrets(err.Error()))
	}
	if tok.AccessToken != "" && tok.AccessToken != p.lastAccess {
		p.lastAccess = tok.AccessToken
		p.bundle.Token = tok
		p.bundle.SavedAt = time.Now().UTC().Format(time.RFC3339)
		_ = SaveTokenBundle(p.path, p.bundle)
	}
	return tok, nil
}

// LiveClientOptions resolves live authentication options for all Google SDK clients:
//  1. If LMGOOGLE_AUTH_MODE=adc, uses Application Default Credentials (note: YouTube APIs require OAuth).
//  2. Otherwise, if a saved OAuth2 token bundle exists at DefaultTokenPath(), builds a unified
//     auto-refreshing oauth2 HTTP client used across Analytics AND YouTube (per v1.txt).
//  3. Otherwise (when mode is auto/unset), falls back to ADC if available, or returns a clear
//     error explaining how to run `lmgoogle auth login --client-secret client_secret.json`.
func LiveClientOptions(ctx context.Context) (mode string, opts []option.ClientOption, err error) {
	pref := strings.ToLower(strings.TrimSpace(os.Getenv(EnvAuthMode)))
	tokenPath := DefaultTokenPath()

	if pref != "adc" {
		if bundle, loadErr := LoadTokenBundle(tokenPath); loadErr == nil {
			cfg := bundle.oauth2Config()
			ts := &persistingTokenSource{
				base:       cfg.TokenSource(ctx, bundle.Token),
				path:       tokenPath,
				bundle:     *bundle,
				lastAccess: bundle.Token.AccessToken,
			}
			httpClient := oauth2.NewClient(ctx, ts)
			return "oauth2-live", []option.ClientOption{option.WithHTTPClient(httpClient)}, nil
		} else if pref == "oauth" || pref == "oauth2" {
			return "", nil, fmt.Errorf("OAuth2 token not found at %q (%v); run `lmgoogle auth login --client-secret <path>`", tokenPath, loadErr)
		}
	}

	creds, adcErr := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: ReadOnlyScopes,
	})
	if adcErr != nil {
		return "", nil, fmt.Errorf("no OAuth2 token at %q and ADC detection failed (%s); run `lmgoogle auth login --client-secret client_secret.json`", tokenPath, safety.RedactSecrets(adcErr.Error()))
	}
	return "adc-live", []option.ClientOption{option.WithAuthCredentials(creds)}, nil
}

// PKCEPair holds a PKCE verifier and S256 challenge.
type PKCEPair struct {
	Verifier  string
	Challenge string
	State     string
}

// NewPKCE generates a cryptographic PKCE verifier/challenge and CSRF state token.
func NewPKCE() (PKCEPair, error) {
	var vBytes [32]byte
	if _, err := rand.Read(vBytes[:]); err != nil {
		return PKCEPair{}, err
	}
	var sBytes [16]byte
	if _, err := rand.Read(sBytes[:]); err != nil {
		return PKCEPair{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(vBytes[:])
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := hex.EncodeToString(sBytes[:])
	return PKCEPair{
		Verifier:  verifier,
		Challenge: challenge,
		State:     state,
	}, nil
}

// LoopbackResult contains the started loopback server's auth URL and code channel.
type LoopbackFlow struct {
	AuthURL  string
	Redirect string
	PKCE     PKCEPair
	Wait     func(ctx context.Context) (string, error)
	Close    func()
}

// StartLoopbackFlow binds an ephemeral 127.0.0.1 HTTP server for the OAuth 2.0 Installed App flow.
func StartLoopbackFlow(cfg *oauth2.Config) (*LoopbackFlow, error) {
	pkce, err := NewPKCE()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen loopback callback: %w", err)
	}
	redirect := fmt.Sprintf("http://%s/callback", ln.Addr().String())
	cloned := *cfg
	cloned.RedirectURL = redirect

	authURL := cloned.AuthCodeURL(
		pkce.State,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.SetAuthURLParam("code_challenge", pkce.Challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	type cbResult struct {
		code string
		err  error
	}
	ch := make(chan cbResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<html><body><h2>Authorization failed</h2><p>%s</p></body></html>", html.EscapeString(errParam))
			select {
			case ch <- cbResult{err: fmt.Errorf("oauth error: %s %s", errParam, desc)}:
			default:
			}
			return
		}
		if q.Get("state") != pkce.State {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "<html><body><h2>Invalid OAuth state</h2></body></html>")
			select {
			case ch <- cbResult{err: errors.New("oauth callback state mismatch")}:
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "<html><body><h2>Missing authorization code</h2></body></html>")
			select {
			case ch <- cbResult{err: errors.New("oauth callback missing code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, "<html><body><h2>lmgoogle authorization complete</h2><p>You may close this tab and return to the terminal.</p></body></html>")
		select {
		case ch <- cbResult{code: code}:
		default:
		}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	return &LoopbackFlow{
		AuthURL:  authURL,
		Redirect: redirect,
		PKCE:     pkce,
		Wait: func(ctx context.Context) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case res := <-ch:
				return res.code, res.err
			}
		},
		Close: func() {
			shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutCtx)
		},
	}, nil
}

// ExchangeCode exchanges an authorization code (or full redirect URL) with PKCE verifier.
func ExchangeCode(ctx context.Context, cfg *oauth2.Config, redirectURL, codeOrURL, verifier string) (*oauth2.Token, error) {
	code := strings.TrimSpace(codeOrURL)
	if strings.HasPrefix(code, "http://") || strings.HasPrefix(code, "https://") {
		u, err := url.Parse(code)
		if err != nil {
			return nil, fmt.Errorf("parse redirect URL: %w", err)
		}
		code = u.Query().Get("code")
	}
	if code == "" {
		return nil, errors.New("empty authorization code")
	}
	cloned := *cfg
	if redirectURL != "" {
		cloned.RedirectURL = redirectURL
	}
	tok, err := cloned.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return nil, fmt.Errorf("oauth token exchange failed: %s", safety.RedactSecrets(err.Error()))
	}
	return tok, nil
}
