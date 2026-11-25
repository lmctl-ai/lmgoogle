// SPDX-License-Identifier: MIT

package safety

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	DefaultQuotaLimit = int64(10000)
	MaxQuotaLimit     = int64(1000000)
)

var (
	reOAuthAccess  = regexp.MustCompile(`ya29\.[A-Za-z0-9._\-]+`)
	reRefreshToken = regexp.MustCompile(`1//[A-Za-z0-9._\-]+`)
	reClientSecret = regexp.MustCompile(`GOCSPX-[A-Za-z0-9._\-]+`)
	reAPIKey       = regexp.MustCompile(`AIza[0-9A-Za-z._\-]{10,}`)
	rePEMKey       = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	reJSONSecret   = regexp.MustCompile(`"(private_key|client_secret|refresh_token|access_token)"\s*:\s*"[^"]*"`)
	reKVSecret     = regexp.MustCompile(`(?i)(password|passwd|client_secret|access_token|refresh_token|private_key_id|session_token|code_verifier)\s*([=:])\s*([^\s,;"]+)`)
)

// RedactSecrets scrubs OAuth tokens, refresh tokens, client secrets, API keys, and PEM blocks.
func RedactSecrets(s string) string {
	if s == "" {
		return ""
	}
	out := rePEMKey.ReplaceAllString(s, "[REDACTED_PRIVATE_KEY]")
	out = reJSONSecret.ReplaceAllString(out, `"$1":"[REDACTED]"`)
	out = reOAuthAccess.ReplaceAllString(out, "ya29.[REDACTED]")
	out = reRefreshToken.ReplaceAllString(out, "1//[REDACTED]")
	out = reClientSecret.ReplaceAllString(out, "GOCSPX-[REDACTED]")
	out = reAPIKey.ReplaceAllString(out, "AIza[REDACTED]")
	out = reKVSecret.ReplaceAllString(out, "$1$2[REDACTED]")
	return out
}

// MaskIdentifier masks sensitive identifiers while retaining the trailing 4 characters.
func MaskIdentifier(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "****0000"
	}
	if len(trimmed) <= 4 {
		return "****" + trimmed
	}
	return "****" + trimmed[len(trimmed)-4:]
}

// NewRunID creates a unique run identifier for ledger tracking.
func NewRunID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("run-%d-%s", time.Now().Unix(), hex.EncodeToString(b[:]))
}

// LedgerEntry records a resource created during the current lmgoogle invocation.
type LedgerEntry struct {
	Kind       string
	Parent     string
	ID         string
	Name       string
	CreatedAt  int64
}

// RunLedger tracks resources created by the current run and API quota usage.
type RunLedger struct {
	mu         sync.Mutex
	runID      string
	entries    []LedgerEntry
	owned      map[string]bool
	quotaUsed  int64
	quotaLimit int64
}

// NewRunLedger creates a RunLedger with an optional run ID and default quota limit.
func NewRunLedger(runID string) *RunLedger {
	if strings.TrimSpace(runID) == "" {
		runID = NewRunID()
	}
	return &RunLedger{
		runID:      runID,
		owned:      make(map[string]bool),
		quotaLimit: DefaultQuotaLimit,
	}
}

func ledgerKey(kind, id string) string {
	return strings.ToLower(strings.TrimSpace(kind)) + ":" + strings.TrimSpace(id)
}

func (l *RunLedger) RunID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.runID
}

// ChargeQuota checks and records API quota units before performing a call.
func (l *RunLedger) ChargeQuota(units int64) error {
	if units <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.quotaUsed+units > l.quotaLimit {
		return fmt.Errorf("quota limit exceeded: next call requires %d units (used=%d, limit=%d)", units, l.quotaUsed, l.quotaLimit)
	}
	l.quotaUsed += units
	return nil
}

func (l *RunLedger) QuotaUsed() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.quotaUsed
}

func (l *RunLedger) QuotaLimit() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.quotaLimit
}

func (l *RunLedger) SetQuotaLimit(limit int64) bool {
	if limit <= 0 || limit > MaxQuotaLimit {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.quotaLimit = limit
	return true
}

// Record marks a resource as owned by the current run.
func (l *RunLedger) Record(kind, parent, id, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ledgerKey(kind, id)
	if l.owned[key] {
		return
	}
	l.owned[key] = true
	l.entries = append(l.entries, LedgerEntry{
		Kind:      kind,
		Parent:    parent,
		ID:        id,
		Name:      name,
		CreatedAt: time.Now().Unix(),
	})
}

func (l *RunLedger) Owns(kind, id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owned[ledgerKey(kind, id)]
}

func (l *RunLedger) Unrecord(kind, id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ledgerKey(kind, id)
	if !l.owned[key] {
		return
	}
	delete(l.owned, key)
	filtered := l.entries[:0]
	for _, e := range l.entries {
		if ledgerKey(e.Kind, e.ID) != key {
			filtered = append(filtered, e)
		}
	}
	l.entries = filtered
}

func (l *RunLedger) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *RunLedger) SnapshotLIFO() []LedgerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LedgerEntry, len(l.entries))
	for i := range l.entries {
		out[i] = l.entries[len(l.entries)-1-i]
	}
	return out
}

func (l *RunLedger) EntriesFormatted() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.entries))
	for i, e := range l.entries {
		out[i] = fmt.Sprintf("%s:%s (%s)", e.Kind, e.ID, e.Name)
	}
	return out
}
