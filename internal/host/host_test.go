// SPDX-License-Identifier: MIT

package host

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmctl-ai/lmcode-go"
	"google.golang.org/api/gmail/v1"
)

func TestHostSignatureParity(t *testing.T) {
	h, err := NewHost(false)
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	defer h.Close()

	reg := lmcode.NewRegistry()
	if err := Register(reg, h); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	lmscriptBin := os.Getenv("LMSCRIPT_BIN")
	if lmscriptBin == "" {
		lmscriptBin, err = exec.LookPath("lmscript")
		if err != nil {
			t.Skip("lmscript not on PATH, skipping signature parity check")
		}
	}

	schemaPath := filepath.Join("..", "..", "schema", "lmgoogle.dsl.json")
	out, err := exec.Command(lmscriptBin, "--lmcode-signatures", schemaPath).CombinedOutput()
	if err != nil {
		t.Fatalf("lmscript --lmcode-signatures failed: %v\n%s", err, string(out))
	}

	wantLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	gotLines := reg.Signatures()
	if len(gotLines) != len(wantLines) {
		t.Fatalf("signature count mismatch: got %d, want %d", len(gotLines), len(wantLines))
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf("signature[%d] = %q, want %q", i, gotLines[i], wantLines[i])
		}
	}
}

func TestGoogleAnalyticsEndToEnd(t *testing.T) {
	h, err := NewHost(false)
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	defer h.Close()

	if h.IsLive() || h.AuthMode() != "fake-emulator" {
		t.Fatalf("expected fake-emulator mode, got live=%v mode=%s", h.IsLive(), h.AuthMode())
	}

	summaries := h.GaListAccountSummaries()
	if len(summaries) != 2 {
		t.Fatalf("expected 2 account summaries, got %v", summaries)
	}
	acc := h.GaGetAccount("1001")
	if acc.DisplayNameVal != "Acme Digital Media" {
		t.Fatalf("unexpected account: %+v (err=%s)", acc, h.LastError())
	}

	props := h.GaListProperties("1001")
	if len(props) != 2 {
		t.Fatalf("expected 2 properties under accounts/1001, got %v", props)
	}
	prop := h.GaGetProperty("3001001")
	if prop.CurrencyCodeVal != "USD" || prop.DisplayNameVal != "Acme Web Storefront" {
		t.Fatalf("unexpected property: %+v", prop)
	}

	// Run GA4 standard & filtered reports
	rep := h.GaRunReport("3001001", "7daysAgo", "today", []string{"country", "deviceCategory"}, []string{"activeUsers", "totalRevenue"}, "", "", 0)
	if rep.RowCount() != 6 || rep.MetricInt(0, 0) != 4210 || rep.MetricSum(0) <= 0 {
		t.Fatalf("unexpected GA4 report: rows=%d err=%s", rep.RowCount(), h.LastError())
	}
	if !strings.Contains(rep.ToCSV(), "United States") {
		t.Fatalf("expected CSV to contain United States: %s", rep.ToCSV())
	}
	filtered := h.GaRunReport("3001001", "7daysAgo", "today", []string{"country"}, []string{"sessions"}, "country", "United States", 10)
	if filtered.RowCount() != 2 {
		t.Fatalf("expected 2 filtered rows, got %d", filtered.RowCount())
	}
	rt := h.GaRunRealtimeReport("3001001", []string{"country"}, []string{"activeUsers"}, 3)
	if rt.RowCount() != 3 {
		t.Fatalf("expected 3 realtime rows, got %d", rt.RowCount())
	}
	meta := h.GaGetMetadata("3001001")
	if meta["dim:country"] != "Country" || meta["metric:activeUsers"] != "Active users" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}

	// Admin mutations + safety delete protection + ledger cleanup
	if h.GaDeleteProperty("properties/3001001") {
		t.Fatal("safety violation: allowed deleting pre-existing GA4 property")
	}
	newProp := h.GaCreateProperty("accounts/1002", "Temp Test Property", "UTC", "USD")
	if newProp.NameVal == "" || !h.LedgerOwns("ga_property", newProp.NameVal) {
		t.Fatalf("GaCreateProperty failed: %+v err=%s", newProp, h.LastError())
	}
	updatedProp := h.GaUpdateProperty(newProp.NameVal, "Temp Test Updated", "", "")
	if updatedProp.DisplayNameVal != "Temp Test Updated" {
		t.Fatalf("GaUpdateProperty failed: %+v", updatedProp)
	}
	stream := h.GaCreateWebStream(newProp.NameVal, "Temp Web", "https://example.test")
	if stream.MeasurementIDVal == "" || !h.LedgerOwns("ga_data_stream", stream.NameVal) {
		t.Fatalf("GaCreateWebStream failed: %+v err=%s", stream, h.LastError())
	}
	cd := h.GaCreateCustomDimension(newProp.NameVal, "env_tier", "Env Tier", "EVENT")
	if cd.ParameterNameVal != "env_tier" {
		t.Fatalf("GaCreateCustomDimension failed: %+v err=%s", cd, h.LastError())
	}
	if !h.GaCreateKeyEvent(newProp.NameVal, "demo_signup", "ONCE_PER_EVENT") {
		t.Fatalf("GaCreateKeyEvent failed: %s", h.LastError())
	}

	cleaned := h.CleanupLedger()
	if cleaned != 2 || h.LedgerCount() != 0 {
		t.Fatalf("expected CleanupLedger to remove 2 items, cleaned=%d remaining=%d", cleaned, h.LedgerCount())
	}
}

func TestYouTubeEndToEndAndGuardrails(t *testing.T) {
	h, err := NewHost(false)
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	defer h.Close()

	ch := h.YtMyChannel()
	if ch.IDVal != "UC_ACME_DEV" || ch.SubscriberCountVal != 48200 {
		t.Fatalf("unexpected my channel: %+v (err=%s)", ch, h.LastError())
	}
	vids := h.YtSearchVideos("Go", 10)
	if len(vids) < 2 {
		t.Fatalf("expected search hits for Go, got %v", vids)
	}
	v := h.YtGetVideo("vid_go_cli_01")
	if v.ViewCountVal != 84500 || len(v.TagsVal) == 0 {
		t.Fatalf("unexpected video: %+v", v)
	}

	// Upload video with real temp file (multipart media upload)
	mediaPath := filepath.Join(t.TempDir(), "demo.mp4")
	if err := os.WriteFile(mediaPath, []byte("fake-mp4-bytes-for-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploaded := h.YtUploadVideo(mediaPath, "Test Upload", "Uploaded via lmgoogle", []string{"demo", "go"}, "28", "private")
	if uploaded.IDVal == "" || uploaded.PrivacyStatusVal != "private" || !h.LedgerOwns("yt_video", uploaded.IDVal) {
		t.Fatalf("YtUploadVideo failed: %+v err=%s", uploaded, h.LastError())
	}
	upd := h.YtUpdateVideo(uploaded.IDVal, "Test Upload Updated", "", nil, "unlisted")
	if upd.TitleVal != "Test Upload Updated" || upd.PrivacyStatusVal != "unlisted" {
		t.Fatalf("YtUpdateVideo failed: %+v", upd)
	}

	// Create playlist, add uploaded video, verify items
	pl := h.YtCreatePlaylist("Temp Playlist", "Demo", "private")
	if pl.IDVal == "" || !h.LedgerOwns("yt_playlist", pl.IDVal) {
		t.Fatalf("YtCreatePlaylist failed: %+v err=%s", pl, h.LastError())
	}
	if !h.YtAddPlaylistItem(pl.IDVal, uploaded.IDVal) {
		t.Fatalf("YtAddPlaylistItem failed: %s", h.LastError())
	}
	items := h.YtListPlaylistItems(pl.IDVal, 10)
	if len(items) != 1 || items[0] != uploaded.IDVal {
		t.Fatalf("unexpected playlist items: %v", items)
	}

	// Comments & YouTube Analytics
	comments := h.YtListComments("vid_go_cli_01", 10)
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %v", comments)
	}
	if cid := h.YtPostComment(uploaded.IDVal, "Automated note ya29.secretToken99"); cid == "" {
		t.Fatalf("YtPostComment failed: %s", h.LastError())
	}
	ytRep := h.YtAnalyticsReport("MINE", "", "2026-11-01", "2026-11-15", []string{"day", "video"}, []string{"views", "estimatedMinutesWatched"})
	if ytRep.RowCount() != 5 || ytRep.ColSum(2) <= 0 {
		t.Fatalf("unexpected YouTube Analytics report: rows=%d err=%s", ytRep.RowCount(), h.LastError())
	}

	// Read-only guardrail blocks mutations
	h.SetReadOnlyMode(true)
	if h.YtDeletePlaylist(pl.IDVal) {
		t.Fatal("expected readonly mode to block playlist deletion")
	}
	if !strings.Contains(h.LastError(), "readonly mode") {
		t.Fatalf("expected readonly error, got %q", h.LastError())
	}
	h.SetReadOnlyMode(false)

	// Quota guardrail
	if !h.SetQuotaLimit(h.QuotaUsed() + 10) {
		t.Fatal("SetQuotaLimit failed")
	}
	if got := h.YtSearchVideos("test", 5); len(got) != 0 || !strings.Contains(h.LastError(), "quota limit exceeded") {
		t.Fatalf("expected quota limit exceeded, got %v err=%q", got, h.LastError())
	}
	h.SetQuotaLimit(10000)

	// Safety: refuse deleting pre-existing video
	if h.YtDeleteVideo("vid_go_cli_01") {
		t.Fatal("safety violation: allowed deleting pre-existing video")
	}

	// Cleanup created video & playlist
	if deleted := h.CleanupLedger(); deleted != 2 || h.LedgerCount() != 0 {
		t.Fatalf("expected 2 cleaned items, got %d remaining=%d", deleted, h.LedgerCount())
	}
}

func TestGmailEndToEndAndGuardrails(t *testing.T) {
	t.Setenv("LMGOOGLE_LIVE", "0")
	t.Setenv("LMGOOGLE_ENDPOINT", "")
	t.Setenv("LMGOOGLE_READ_ONLY", "")
	t.Setenv("LMGOOGLE_ALLOW_DELETE", "")

	h, err := NewHost(false)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	prof := h.GmailProfile()
	if prof.EmailAddressVal != "engineering@acme.example" || prof.MessagesTotalVal < 4 {
		t.Fatalf("unexpected GmailProfile: %+v (err=%s)", prof, h.LastError())
	}
	if labels := h.GmailListLabels(); len(labels) < 5 {
		t.Fatalf("expected labels, got %v", labels)
	}

	unread := h.GmailSearchMessages("is:unread", 10)
	if len(unread) != 2 {
		t.Fatalf("expected 2 unread messages, got %v (err=%s)", unread, h.LastError())
	}
	msg := h.GmailGetMessage(unread[0])
	if msg.IDVal == "" || msg.SubjectVal == "" || msg.FromVal == "" || msg.BodyTextVal == "" {
		t.Fatalf("unexpected decoded GmailMessage: %+v (err=%s)", msg, h.LastError())
	}
	if thr := h.GmailGetThread("thr_1001"); len(thr) != 2 {
		t.Fatalf("expected 2 messages in thr_1001, got %v", thr)
	}

	// Safety policy blocks deleting pre-existing message not created in this run
	if h.GmailDeleteMessage("msg_1001") {
		t.Fatal("expected safety refusal deleting seeded msg_1001")
	}
	if !strings.Contains(h.LastError(), "safety policy") {
		t.Fatalf("expected safety policy error, got %q", h.LastError())
	}

	// Send -> modify -> trash -> delete -> RunLedger ownership
	sent := h.GmailSendMessage("alex@acme.example", "Test Gmail Support", "Hello from Go gmail/v1!")
	if sent.IDVal == "" || !h.LedgerOwns("gmail_message", sent.IDVal) {
		t.Fatalf("GmailSendMessage failed: %+v (err=%s)", sent, h.LastError())
	}
	mod := h.GmailModifyLabels(sent.IDVal, []string{"IMPORTANT"}, nil)
	if !mod.HasLabel("IMPORTANT") {
		t.Fatalf("GmailModifyLabels missing IMPORTANT: %+v", mod.LabelIDsVal)
	}
	if !h.GmailTrashMessage(sent.IDVal) {
		t.Fatalf("GmailTrashMessage failed: %s", h.LastError())
	}

	h.SetReadOnlyMode(true)
	if h.GmailSendMessage("alex@acme.example", "Blocked", "Nope").IDVal != "" {
		t.Fatal("expected readonly mode to block send")
	}
	h.SetReadOnlyMode(false)

	if cleaned := h.CleanupLedger(); cleaned != 1 || h.LedgerCount() != 0 {
		t.Fatalf("expected CleanupLedger()=1 remaining=0, got %d/%d (err=%s)", cleaned, h.LedgerCount(), h.LastError())
	}

	// Verify nested MIME decoding prefers text/plain even when text/html appears first
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	nested := gmailMessageToSnapshot(&gmail.Message{
		Id: "msg_nested",
		Payload: &gmail.MessagePart{
			MimeType: "multipart/alternative",
			Parts: []*gmail.MessagePart{
				{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: b64("<p>HTML first</p>")}},
				{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: b64("Plain preferred")}},
			},
		},
	})
	if nested.BodyTextVal != "Plain preferred" {
		t.Fatalf("expected text/plain preference, got %q", nested.BodyTextVal)
	}

	// Verify subject search and attachment download
	hits := h.GmailSearchMessages(`subject:"ORDER-1005"`, 10)
	if len(hits) != 1 {
		t.Fatalf("expected 1 subject hit for ORDER-1005, got %v (err=%s)", hits, h.LastError())
	}
	orderMsg := h.GmailGetMessage(hits[0])
	if orderMsg.AttachmentCount() != 1 || orderMsg.AttachmentNamesVal[0] != "receipt_ORDER-1005.txt" {
		t.Fatalf("unexpected orderMsg attachments: %+v", orderMsg)
	}
	if orderMsg.TimestampSlugVal != "20261123T173000Z" || orderMsg.DateISOVal != "2026-11-23T17:30:00Z" {
		t.Fatalf("unexpected timestamp fields: slug=%q iso=%q", orderMsg.TimestampSlugVal, orderMsg.DateISOVal)
	}
	attDir := filepath.Join(t.TempDir(), "attachments")
	saved := h.GmailDownloadAttachmentsPrefixed(hits[0], attDir, orderMsg.TimestampSlugVal+"_")
	if len(saved) != 1 || !strings.HasSuffix(saved[0], "20261123T173000Z_receipt_ORDER-1005.txt") {
		t.Fatalf("expected 1 prefixed downloaded attachment, got %v (err=%s)", saved, h.LastError())
	}
	rawAtt, err := os.ReadFile(saved[0])
	if err != nil || !strings.Contains(string(rawAtt), "ORDER-1005") {
		t.Fatalf("unexpected downloaded attachment content: %v %q", err, string(rawAtt))
	}
}
