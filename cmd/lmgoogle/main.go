// SPDX-License-Identifier: MIT

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/lmctl-ai/lmcode-go"
	"github.com/lmctl-ai/lmgoogle/internal/host"
	"github.com/lmctl-ai/lmgoogle/internal/invoke"
	"github.com/lmctl-ai/lmgoogle/internal/membudget"
	"github.com/lmctl-ai/lmgoogle/internal/safety"
	"github.com/lmctl-ai/lmgoogle/schema"
)

//go:embed VERSION
var rawVersion string

//go:embed THIRD_PARTY_NOTICES
var embeddedNotices string

const usageText = `usage: lmgoogle --version | --help | --notices | --lmcode-schema [--json] | --lmcode-signatures [--without-std] | --lmcode FILE [--lmcode FILE ...] | auth <login|setup|status|logout> [options]

Google Analytics, YouTube & Gmail Go SDK host for lmscript (on lmcode-go).

Options:
  --version                  Print version (lmgoogle X.Y.Z)
  --help, -h                 Print this usage and auth/safety overview
  --notices                  Print embedded third-party license notices
  --lmcode-schema [--json]   Print human-readable schema summary or raw JSON schema
  --lmcode-signatures        Print sorted canonical DSL signatures
  --lmcode FILE              Load, verify, and execute a compiled .dslb file
  auth login                 Run native OAuth 2.0 loopback flow (supports --link via lmctl upload)
  auth setup                 Generate/upload Google Cloud API & OAuth setup checklist HTML
  auth status                Show saved OAuth2 / ADC configuration (secrets masked)
  auth logout                Remove saved OAuth2 token bundle

Dry mode comes from the lmcode invocation manifest; there is no --dry-run flag.

Environment & Auth:
  LMGOOGLE_MAX_MEMORY_MB     Total memory budget in whole MiB (default: 4096; invalid = exit 2)
  LMGOOGLE_LIVE              Set to 1 to call live Google APIs (default: 0, in-process fake server)
  LMGOOGLE_ENDPOINT          Loopback HTTP emulator origin (mutually exclusive with LMGOOGLE_LIVE=1)
  LMGOOGLE_ACCESS_TOKEN      Optional dummy bearer token for external emulator
  LMGOOGLE_CLIENT_SECRET     Path to OAuth client_secret.json (default: ~/.config/lmgoogle/client_secret.json)
  LMGOOGLE_TOKEN_FILE        Path to saved OAuth token bundle (default: ~/.config/lmgoogle/token.json)
  LMGOOGLE_AUTH_MODE         auto (default: OAuth2 token then ADC), oauth2, or adc
  LMGOOGLE_READ_ONLY         Set to 1 to block all mutating API calls
  LMGOOGLE_ALLOW_DELETE      Set to 1 to allow deleting pre-existing resources not created in this run
  LMGOOGLE_AUTO_CLEANUP      Set to 1 to auto-delete run-created resources on host close

Linux Runtime Note (Rule 14):
  Built with CGO_ENABLED=0 (or dynamic glibc >= 2.35 on Linux); zero GPL/LGPL code linked.
`

func printSchemaSummary(reg *lmcode.Registry) {
	current := ""
	var order []string
	members := make(map[string][]string)
	for _, sig := range reg.Signatures() {
		if strings.HasPrefix(sig, "context ") || strings.HasPrefix(sig, "fn ") {
			fmt.Println(sig)
		} else if strings.HasPrefix(sig, "method ") {
			body := sig[7:]
			idx := strings.Index(body, ".")
			if idx > 0 {
				typeName := body[:idx]
				if typeName != current {
					current = typeName
					order = append(order, typeName)
				}
				members[typeName] = append(members[typeName], body)
			}
		}
	}
	for _, t := range order {
		fmt.Printf("%s:\n", t)
		for _, m := range members[t] {
			fmt.Printf("  %s\n", m)
		}
	}
}

func main() {
	os.Exit(realMain(os.Args))
}

func realMain(args []string) int {
	if _, err := membudget.InitFromEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, err.Error()))
	}

	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Print(usageText)
		return 0
	}

	if len(args) == 2 && args[1] == "--version" {
		v := strings.TrimSpace(rawVersion)
		if v == "" {
			v = "0.1.0"
		}
		fmt.Printf("lmgoogle %s\n", v)
		return 0
	}

	if len(args) == 2 && args[1] == "--notices" {
		fmt.Print(embeddedNotices)
		return 0
	}

	if len(args) >= 2 && args[1] == "auth" {
		return runAuthCommand(args[2:])
	}

	invocation := len(args) >= 2 && args[1] == "--lmcode"
	if len(args) < 2 || (args[1] != "--lmcode-schema" && args[1] != "--lmcode-signatures" && !invocation) {
		fmt.Fprint(os.Stderr, usageText)
		return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, usageText))
	}

	var results *lmcode.InvocationResults
	var order []int
	builderInvocation := false
	if invocation {
		parts := make([]string, 0, len(args)/2)
		files := make([]string, 0, len(args)/2)
		add := func(file string) bool {
			if file == "" || strings.HasPrefix(file, "--") {
				return false
			}
			parts = append(parts, file)
			files = append(files, file)
			return true
		}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--lmcode":
				if i+1 >= len(args) || !add(args[i+1]) {
					return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, usageText))
				}
				i++
			default:
				return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, usageText))
			}
		}
		if len(parts) == 0 {
			return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, usageText))
		}
		collected, err := lmcode.InvocationResultsFromCommand(files)
		if err != nil {
			return invoke.Failure("cli", err)
		}
		results = collected
		order = make([]int, len(parts))
		builderInvocation = len(parts) > 1
		if manifest := os.Getenv("LMCODE_INVOCATION_FILE"); manifest != "" {
			var meta struct {
				Mode string `json:"mode"`
			}
			raw, err := os.ReadFile(manifest)
			if err != nil || json.Unmarshal(raw, &meta) != nil || (meta.Mode != "single" && meta.Mode != "builder") {
				return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, "invalid validated invocation manifest"))
			}
			builderInvocation = meta.Mode == "builder"
		}
		for i := range parts {
			order[i] = i
		}
	}
	dryInvocation := results != nil && results.Dry()

	var h *host.Host
	var err error
	if args[1] == "--lmcode-schema" || args[1] == "--lmcode-signatures" {
		// Introspection must not require live credentials or touch network state.
		h = &host.Host{}
	} else {
		h, err = host.NewHost(dryInvocation)
		if err != nil {
			return invoke.Failure("app", lmcode.NewError(lmcode.APP_FAIL, "failed to initialize Google host: "+safety.RedactSecrets(err.Error())))
		}
		defer h.Close()
	}

	reg := lmcode.NewRegistry()
	if err := host.Register(reg, h); err != nil {
		return invoke.Failure("app", lmcode.NewError(lmcode.APP_FAIL, "failed to register host: "+err.Error()))
	}

	if args[1] == "--lmcode-schema" {
		if len(args) == 2 {
			printSchemaSummary(reg)
			return 0
		}
		if len(args) == 3 && args[2] == "--json" {
			if _, err := os.Stdout.Write(schema.EmbeddedSchema); err != nil {
				return 4
			}
			return 0
		}
		fmt.Fprint(os.Stderr, usageText)
		return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, usageText))
	}

	if len(args) == 3 && args[1] == "--lmcode-signatures" && args[2] == "--without-std" {
		for _, s := range reg.HostSignatures() {
			fmt.Println(s)
		}
		return 0
	}
	if args[1] == "--lmcode-signatures" && len(args) == 2 {
		for _, s := range reg.Signatures() {
			fmt.Println(s)
		}
		return 0
	}
	if !invocation {
		fmt.Fprint(os.Stderr, usageText)
		return invoke.Failure("cli", lmcode.NewError(lmcode.CLI_USAGE, usageText))
	}

	contract, err := lmcode.ResultContractFromSchema(schema.EmbeddedSchema)
	if err != nil {
		return invoke.Failure("load", err)
	}
	ctx := lmcode.NewContext()
	ctx.Set("google", h)
	ctx.SetDry(dryInvocation)

	var exitOnce sync.Once
	runner := invoke.Runner{
		Results:  results,
		Registry: reg,
		Schema:   schema.EmbeddedSchema,
		Contract: contract,
		Context:  ctx,
		Order:    order,
		Dry:      dryInvocation,
		Builder:  builderInvocation,
		Hooks: invoke.Hooks{
			BeforeRun: membudget.ResetRun,
			OnRunError: func(runErr error) {
				if strings.Contains(runErr.Error(), membudget.EnvVarName) {
					fmt.Fprintln(os.Stderr, runErr.Error())
				}
			},
			AuthError: h.AuthError,
			StartWatchdog: func(onError func(error)) func() {
				return membudget.StartWatchdog(func(watchdogErr error) {
					exitOnce.Do(func() { onError(watchdogErr) })
				})
			},
		},
	}
	return runner.Execute()
}
