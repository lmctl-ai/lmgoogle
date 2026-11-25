// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/lmctl-ai/lmgoogle/internal/auth"
	"github.com/lmctl-ai/lmgoogle/internal/safety"
)

const authUsage = `usage: lmgoogle auth <login|setup|status|logout> [options]

Subcommands:
  login [--client-secret PATH] [--token-file PATH] [--readonly] [--manual] [--no-browser]
        [--link] [--link-name NAME] [--link-out PATH] [--keep-link] [--scopes S1,S2]
      Run the native OAuth 2.0 Installed App (Loopback + PKCE) flow using
      client_secret.json and save the refreshable token locally (0600).
      With --link, generates a click-through HTML page of the OAuth steps and uploads
      it via "lmctl upload --link" to produce an anonymous clickable link.
  setup [--link] [--link-name NAME] [--link-out PATH]
      Generate (and optionally upload via "lmctl upload --link") a click-through HTML
      checklist for enabling Google Analytics / YouTube APIs and creating client_secret.json.
  status [--token-file PATH]
      Inspect saved OAuth2 token bundle and ADC availability (secrets masked).
  logout [--token-file PATH]
      Delete the saved local OAuth2 token bundle.
`

func runAuthCommand(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(authUsage)
		return 0
	}
	switch args[0] {
	case "login":
		return runAuthLoginIO(args[1:], os.Stdin, os.Stdout, os.Stderr)
	case "setup":
		return runAuthSetupIO(args[1:], os.Stdout, os.Stderr)
	case "status":
		return runAuthStatus(args[1:])
	case "logout":
		return runAuthLogout(args[1:])
	default:
		fmt.Fprint(os.Stderr, authUsage)
		return 2
	}
}

func emitLinkEvent(w io.Writer, field, url string) {
	b, _ := json.Marshal(map[string]string{field: url})
	fmt.Fprintln(w, string(b))
}

func runAuthSetupIO(args []string, stdout, stderr io.Writer) int {
	useLink := false
	linkName := "lmgoogle-setup.html"
	linkOut := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--link":
			useLink = true
		case "--link-name":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing name after --link-name")
				return 2
			}
			linkName = args[i+1]
			i++
		case "--link-out":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing path after --link-out")
				return 2
			}
			linkOut = args[i+1]
			i++
		case "--help", "-h":
			fmt.Fprint(stdout, authUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown auth setup option: %s\n", args[i])
			return 2
		}
	}

	if !useLink && linkOut == "" {
		useLink = true
	}

	htmlDoc, err := auth.RenderOAuthLinkPage(auth.OAuthLinkPageData{
		Scopes: auth.DefaultScopes,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error rendering setup HTML: %v\n", err)
		return 1
	}

	if linkOut != "" {
		if err := os.WriteFile(linkOut, []byte(htmlDoc), 0o600); err != nil {
			fmt.Fprintf(stderr, "error writing %s: %v\n", linkOut, err)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote Google Cloud OAuth setup HTML to %s\n", linkOut)
	}

	if useLink {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		uploaded, err := auth.UploadHTMLReport(ctx, htmlDoc, linkName)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", safety.RedactSecrets(err.Error()))
			return 1
		}
		emitLinkEvent(stderr, "setup", uploaded.URL)
		fmt.Fprintf(stdout, "Open this anonymous setup checklist link in your browser:\n  %s\n", uploaded.URL)
	}
	return 0
}

func runAuthLoginIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	clientSecretPath := auth.DefaultClientSecretPath()
	tokenPath := auth.DefaultTokenPath()
	scopes := append([]string(nil), auth.DefaultScopes...)
	manual := false
	noBrowser := false
	useLink := false
	linkName := "lmgoogle-oauth.html"
	linkOut := ""
	keepLink := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client-secret":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing path after --client-secret")
				return 2
			}
			clientSecretPath = args[i+1]
			i++
		case "--token-file":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing path after --token-file")
				return 2
			}
			tokenPath = args[i+1]
			i++
		case "--readonly":
			scopes = append([]string(nil), auth.ReadOnlyScopes...)
		case "--scopes":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing comma-separated list after --scopes")
				return 2
			}
			var custom []string
			for _, p := range strings.Split(args[i+1], ",") {
				if t := strings.TrimSpace(p); t != "" {
					custom = append(custom, t)
				}
			}
			if len(custom) == 0 {
				fmt.Fprintln(stderr, "--scopes must not be empty")
				return 2
			}
			scopes = custom
			i++
		case "--manual":
			manual = true
		case "--no-browser":
			noBrowser = true
		case "--link":
			useLink = true
			noBrowser = true
		case "--link-name":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing name after --link-name")
				return 2
			}
			linkName = args[i+1]
			i++
		case "--link-out":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "missing path after --link-out")
				return 2
			}
			linkOut = args[i+1]
			i++
		case "--keep-link":
			keepLink = true
		case "--help", "-h":
			fmt.Fprint(stdout, authUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown auth login option: %s\n", args[i])
			return 2
		}
	}

	cfg, err := auth.LoadClientConfig(clientSecretPath, scopes)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", safety.RedactSecrets(err.Error()))
		fmt.Fprintf(stderr, "hint: download a Desktop OAuth client_secret.json from Google Cloud Console (or run `lmgoogle auth setup --link`) and pass --client-secret PATH\n")
		return 1
	}

	flow, err := auth.StartLoopbackFlow(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer flow.Close()

	var uploadedName string
	if useLink || linkOut != "" {
		htmlDoc, err := auth.RenderOAuthLinkPage(auth.OAuthLinkPageData{
			ClientIDMasked: safety.MaskIdentifier(cfg.ClientID),
			Scopes:         scopes,
			AuthURL:        flow.AuthURL,
			RedirectURI:    flow.Redirect,
			ManualOnly:     manual,
		})
		if err != nil {
			fmt.Fprintf(stderr, "error rendering OAuth link HTML: %v\n", err)
			return 1
		}
		if linkOut != "" {
			if err := os.WriteFile(linkOut, []byte(htmlDoc), 0o600); err != nil {
				fmt.Fprintf(stderr, "error writing %s: %v\n", linkOut, err)
				return 1
			}
		}
		if useLink {
			uploadCtx, uploadCancel := context.WithTimeout(context.Background(), 30*time.Second)
			uploaded, err := auth.UploadHTMLReport(uploadCtx, htmlDoc, linkName)
			uploadCancel()
			if err != nil {
				fmt.Fprintf(stderr, "error: %s\n", safety.RedactSecrets(err.Error()))
				return 1
			}
			uploadedName = uploaded.Name
			emitLinkEvent(stderr, "approve", uploaded.URL)
			fmt.Fprintf(stdout, "Open this anonymous OAuth page link in your browser:\n  %s\n\n", uploaded.URL)
		}
	}

	if uploadedName == "" {
		fmt.Fprintln(stdout, "Open this URL in your browser to authorize Google Analytics, YouTube, and Gmail access:")
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "  "+flow.AuthURL)
		fmt.Fprintln(stdout)
	}

	if !manual && !noBrowser {
		tryOpenBrowser(flow.AuthURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	var code string
	if manual {
		fmt.Fprint(stdout, "Paste the authorization code or full redirect URL: ")
		reader := bufio.NewReader(stdin)
		line, err := reader.ReadString('\n')
		if err != nil && len(strings.TrimSpace(line)) == 0 {
			fmt.Fprintf(stderr, "error reading code: %v\n", err)
			return 1
		}
		code = strings.TrimSpace(line)
	} else {
		fmt.Fprintf(stdout, "Waiting for loopback callback on %s (or paste redirect URL / code if remote)...\n", flow.Redirect)
		type waitResult struct {
			code string
			err  error
		}
		loopCh := make(chan waitResult, 1)
		go func() {
			c, e := flow.Wait(ctx)
			loopCh <- waitResult{code: c, err: e}
		}()
		stdinCh := make(chan string, 1)
		go func() {
			reader := bufio.NewReader(stdin)
			for {
				line, err := reader.ReadString('\n')
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					stdinCh <- trimmed
					return
				}
				if err != nil {
					return
				}
			}
		}()
		select {
		case res := <-loopCh:
			if res.err != nil {
				fmt.Fprintf(stderr, "error waiting for OAuth callback: %v\n", res.err)
				return 1
			}
			code = res.code
		case pasted := <-stdinCh:
			code = pasted
		case <-ctx.Done():
			fmt.Fprintf(stderr, "error waiting for OAuth completion: %v\n", ctx.Err())
			return 1
		}
	}

	tok, err := auth.ExchangeCode(ctx, cfg, flow.Redirect, code, flow.PKCE.Verifier)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", safety.RedactSecrets(err.Error()))
		return 1
	}

	bundle := auth.TokenBundle{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		AuthURI:      cfg.Endpoint.AuthURL,
		TokenURI:     cfg.Endpoint.TokenURL,
		Scopes:       scopes,
		Token:        tok,
	}
	if err := auth.SaveTokenBundle(tokenPath, bundle); err != nil {
		fmt.Fprintf(stderr, "error saving token: %s\n", safety.RedactSecrets(err.Error()))
		return 1
	}

	if uploadedName != "" && !keepLink {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = auth.RemoveUploadedReport(rmCtx, uploadedName)
		rmCancel()
	}

	fmt.Fprintf(stdout, "Saved OAuth2 token bundle to %s (client=%s, scopes=%d, refresh_token=%v)\n",
		tokenPath, safety.MaskIdentifier(cfg.ClientID), len(scopes), tok.RefreshToken != "")
	return 0
}

func runAuthStatus(args []string) int {
	tokenPath := auth.DefaultTokenPath()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--token-file":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "missing path after --token-file")
				return 2
			}
			tokenPath = args[i+1]
			i++
		case "--help", "-h":
			fmt.Print(authUsage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "unknown auth status option: %s\n", args[i])
			return 2
		}
	}

	bundle, err := auth.LoadTokenBundle(tokenPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("oauth2_token: absent (%s)\n", tokenPath)
		} else {
			fmt.Printf("oauth2_token: invalid (%s): %s\n", tokenPath, safety.RedactSecrets(err.Error()))
		}
	} else {
		fmt.Printf("oauth2_token: present (%s)\n", tokenPath)
		fmt.Printf("  client_id: %s\n", safety.MaskIdentifier(bundle.ClientID))
		fmt.Printf("  has_refresh_token: %v\n", bundle.Token.RefreshToken != "")
		if !bundle.Token.Expiry.IsZero() {
			fmt.Printf("  access_expiry: %s\n", bundle.Token.Expiry.UTC().Format(time.RFC3339))
		}
		fmt.Printf("  scopes: %s\n", strings.Join(bundle.Scopes, ", "))
	}

	mode, _, liveErr := auth.LiveClientOptions(context.Background())
	if liveErr != nil {
		fmt.Printf("live_auth_resolution: unavailable (%s)\n", safety.RedactSecrets(liveErr.Error()))
	} else {
		fmt.Printf("live_auth_resolution: %s\n", mode)
	}
	return 0
}

func runAuthLogout(args []string) int {
	tokenPath := auth.DefaultTokenPath()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--token-file":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "missing path after --token-file")
				return 2
			}
			tokenPath = args[i+1]
			i++
		case "--help", "-h":
			fmt.Print(authUsage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "unknown auth logout option: %s\n", args[i])
			return 2
		}
	}
	if err := os.Remove(tokenPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("no OAuth2 token bundle at %s\n", tokenPath)
			return 0
		}
		fmt.Fprintf(os.Stderr, "error removing %s: %v\n", tokenPath, err)
		return 1
	}
	fmt.Printf("removed OAuth2 token bundle %s\n", tokenPath)
	return 0
}

func tryOpenBrowser(rawURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", rawURL)
		}
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}
