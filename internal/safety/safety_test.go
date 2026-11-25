// SPDX-License-Identifier: MIT

package safety

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	in := `token=ya29.abc123XYZ bare=1//0eBareRefresh refresh_token=1//0eSecretRefresh client_secret=GOCSPX-mySecret99 key=AIzaSyDummyKey1234567`
	out := RedactSecrets(in)
	for _, secret := range []string{"abc123XYZ", "0eBareRefresh", "0eSecretRefresh", "mySecret99", "DummyKey1234567"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q survived redaction: %s", secret, out)
		}
	}
	for _, marker := range []string{"ya29.[REDACTED]", "1//[REDACTED]", "refresh_token=[REDACTED]", "AIza[REDACTED]"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("expected marker %q in output, got: %s", marker, out)
		}
	}
}

func TestRunLedgerAndQuota(t *testing.T) {
	l := NewRunLedger("run-1-abcdef123456")
	if err := l.ChargeQuota(100); err != nil {
		t.Fatalf("ChargeQuota failed: %v", err)
	}
	if l.QuotaUsed() != 100 {
		t.Fatalf("QuotaUsed = %d, want 100", l.QuotaUsed())
	}
	if !l.SetQuotaLimit(150) {
		t.Fatal("SetQuotaLimit failed")
	}
	if err := l.ChargeQuota(100); err == nil {
		t.Fatal("expected quota exceeded error")
	}
	l.Record("yt_playlist", "", "PL_1", "Demo Playlist")
	l.Record("ga_property", "accounts/1001", "properties/2001", "Demo GA4")
	if !l.Owns("yt_playlist", "PL_1") || l.Count() != 2 {
		t.Fatalf("unexpected ledger state: count=%d", l.Count())
	}
	lifo := l.SnapshotLIFO()
	if lifo[0].ID != "properties/2001" || lifo[1].ID != "PL_1" {
		t.Fatalf("expected LIFO order, got %+v", lifo)
	}
	l.Unrecord("yt_playlist", "PL_1")
	if l.Owns("yt_playlist", "PL_1") || l.Count() != 1 {
		t.Fatal("Unrecord failed")
	}
}
