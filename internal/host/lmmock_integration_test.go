// SPDX-License-Identifier: MIT

package host

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmctl-ai/lmgoogle/internal/fakegoogle"
)

func resolveSiblingBinary(t *testing.T, envKey, name string) string {
	t.Helper()
	if p := os.Getenv(envKey); p != "" {
		return p
	}
	sibling := filepath.Join("..", "..", "..", name, "build", name)
	if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
		abs, _ := filepath.Abs(sibling)
		return abs
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func startLmmockGoogleServer(t *testing.T, lmmockBin string) string {
	t.Helper()
	dir := t.TempDir()
	readyFile := filepath.Join(dir, "ready.json")
	cmd := exec.Command(lmmockBin, "serve",
		"--builtin", "google-apis",
		"--listen", "127.0.0.1:0",
		"--ready-file", readyFile,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lmmock: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			_, _ = cmd.Process.Wait()
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(readyFile); err == nil {
			var info struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(b, &info) == nil && info.URL != "" {
				return info.URL
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for lmmock ready-file %s", readyFile)
	return ""
}

// TestIntegrationWithLmmock runs full Google Analytics and YouTube workflows
// through lmgoogle's Host against an external lmmock (--builtin google-apis) process,
// and optionally verifies lminspect reverse-proxy recording in front of lmmock.
func TestIntegrationWithLmmock(t *testing.T) {
	lmmockBin := resolveSiblingBinary(t, "LMMOCK_BIN", "lmmock")
	if lmmockBin == "" {
		t.Skip("lmmock binary not found (set LMMOCK_BIN or build ../lmmock/build/lmmock)")
	}

	mockURL := startLmmockGoogleServer(t, lmmockBin)
	targetURL := mockURL

	// If lminspect is also available, sit lminspect in front of lmmock in reverse mode
	// to verify both live proxy forwarding and flow redaction/recording.
	var flowsFile string
	if inspector := resolveSiblingBinary(t, "LMINSPECT_BIN", "lminspect"); inspector != "" {
		flowsFile = filepath.Join(t.TempDir(), "integration-flows.jsonl")
		proxyPort := freeLoopbackPort(t)
		proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
		proxyCmd := exec.Command(inspector, "run",
			"--listen", proxyAddr,
			"--mode", "reverse:"+mockURL,
			"--record", flowsFile,
		)
		if err := proxyCmd.Start(); err != nil {
			t.Fatalf("start lminspect: %v", err)
		}
		t.Cleanup(func() {
			if proxyCmd.Process != nil {
				_ = proxyCmd.Process.Signal(os.Interrupt)
				_, _ = proxyCmd.Process.Wait()
			}
		})
		waitForTCP(t, proxyAddr, proxyCmd)
		targetURL = "http://" + proxyAddr
	}

	// 1. Verify bad Bearer token receives 401 from lmmock.
	t.Setenv("LMGOOGLE_LIVE", "0")
	t.Setenv("LMGOOGLE_ENDPOINT", targetURL)
	t.Setenv("LMGOOGLE_ACCESS_TOKEN", "wrong-token")
	badHost, err := NewHost(false)
	if err != nil {
		t.Fatalf("NewHost(wrong-token): %v", err)
	}
	_ = badHost.GaListAccounts()
	if !strings.Contains(badHost.LastError(), "401") && !strings.Contains(badHost.LastError(), "Invalid Credentials") {
		badHost.Close()
		t.Fatalf("expected 401/Invalid Credentials from lmmock on wrong token, got %q", badHost.LastError())
	}
	badHost.Close()

	// 2. Authenticate with fixture-token and run full GA4 + YouTube workflows.
	t.Setenv("LMGOOGLE_ACCESS_TOKEN", "fixture-token")
	h, err := NewHost(false)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	if h.AuthMode() != "external-emulator" {
		t.Fatalf("AuthMode = %q, want external-emulator", h.AuthMode())
	}

	if summaries := h.GaListAccountSummaries(); len(summaries) != 2 {
		t.Fatalf("expected 2 account summaries, got %v (err=%s)", summaries, h.LastError())
	}
	if props := h.GaListProperties("accounts/1001"); len(props) != 2 {
		t.Fatalf("expected 2 properties for accounts/1001, got %v", props)
	}

	createdProp := h.GaCreateProperty("accounts/1002", "lmmock Integration GA4", "America/Chicago", "USD")
	if createdProp.NameVal == "" {
		t.Fatalf("GaCreateProperty failed: %s", h.LastError())
	}
	updatedProp := h.GaUpdateProperty(createdProp.NameVal, "lmmock Integration GA4 v2", "", "")
	if updatedProp.DisplayNameVal != "lmmock Integration GA4 v2" {
		t.Fatalf("GaUpdateProperty DisplayName = %q", updatedProp.DisplayNameVal)
	}
	ds := h.GaCreateWebStream(createdProp.NameVal, "Integration Stream", "https://integration.example.test")
	if ds.MeasurementIDVal == "" {
		t.Fatalf("GaCreateWebStream failed: %s", h.LastError())
	}
	gotDS := h.GaGetDataStream(ds.NameVal)
	if gotDS.MeasurementIDVal != ds.MeasurementIDVal {
		t.Fatalf("GaGetDataStream mismatch: %+v vs %+v", gotDS, ds)
	}
	if !h.GaDeleteDataStream(ds.NameVal) {
		t.Fatalf("GaDeleteDataStream failed: %s", h.LastError())
	}
	if !h.GaDeleteProperty(createdProp.NameVal) {
		t.Fatalf("GaDeleteProperty failed: %s", h.LastError())
	}

	gaRep := h.GaRunReport("3001001", "7daysAgo", "today",
		[]string{"country", "sessionDefaultChannelGroup"},
		[]string{"activeUsers", "totalRevenue"},
		"country", "United States", 10,
	)
	if gaRep.RowCount() != 2 {
		t.Fatalf("filtered GA4 report RowCount = %d, want 2 (err=%s)", gaRep.RowCount(), h.LastError())
	}
	if rt := h.GaRunRealtimeReport("3001001", []string{"country"}, []string{"activeUsers"}, 5); rt.RowCount() != 5 {
		t.Fatalf("realtime RowCount = %d, want 5", rt.RowCount())
	}
	if meta := h.GaGetMetadata("3001001"); meta["dim:country"] == "" {
		t.Fatalf("expected dim:country metadata, got %+v", meta)
	}

	ch := h.YtMyChannel()
	if ch.IDVal != "UC_ACME_DEV" || ch.SubscriberCountVal != 48200 {
		t.Fatalf("unexpected YtMyChannel: %+v (err=%s)", ch, h.LastError())
	}
	mediaFile := filepath.Join(t.TempDir(), "integration.mp4")
	if err := os.WriteFile(mediaFile, []byte("integration-mp4-bytes"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	up := h.YtUploadVideo(mediaFile, "lmmock Upload", "Uploaded through lmmock", []string{"lmmock", "lmgoogle"}, "28", "private")
	if up.IDVal == "" || up.TitleVal != "lmmock Upload" {
		t.Fatalf("YtUploadVideo failed: %+v (err=%s)", up, h.LastError())
	}
	upd := h.YtUpdateVideo(up.IDVal, "lmmock Upload Ready", "Updated through lmmock", []string{"lmmock"}, "unlisted")
	if upd.TitleVal != "lmmock Upload Ready" || upd.PrivacyStatusVal != "unlisted" {
		t.Fatalf("YtUpdateVideo failed: %+v", upd)
	}
	pl := h.YtCreatePlaylist("lmmock Playlist", "Integration playlist", "private")
	if pl.IDVal == "" {
		t.Fatalf("YtCreatePlaylist failed: %s", h.LastError())
	}
	if !h.YtAddPlaylistItem(pl.IDVal, upd.IDVal) {
		t.Fatalf("YtAddPlaylistItem failed: %s", h.LastError())
	}
	if items := h.YtListPlaylistItems(pl.IDVal, 10); len(items) != 1 || items[0] != upd.IDVal {
		t.Fatalf("YtListPlaylistItems = %v, want [%s]", items, upd.IDVal)
	}
	if cid := h.YtPostComment(upd.IDVal, "Integration comment"); cid == "" {
		t.Fatalf("YtPostComment failed: %s", h.LastError())
	}
	if !h.YtDeletePlaylist(pl.IDVal) || !h.YtDeleteVideo(upd.IDVal) {
		t.Fatalf("cleanup delete playlist/video failed: %s", h.LastError())
	}

	ytRep := h.YtAnalyticsReport("MINE", "vid_launch_04", "2026-11-01", "2026-11-15", []string{"day", "video"}, []string{"views", "estimatedMinutesWatched"})
	if ytRep.RowCount() != 2 {
		t.Fatalf("YtAnalyticsReport RowCount = %d, want 2 (err=%s)", ytRep.RowCount(), h.LastError())
	}

	// 3. Verify Gmail search, get, thread, send, label modify, trash, delete against lmmock.
	prof := h.GmailProfile()
	if prof.EmailAddressVal != "engineering@acme.example" || prof.MessagesTotalVal < 4 {
		t.Fatalf("GmailProfile = %+v (err=%s)", prof, h.LastError())
	}
	unread := h.GmailSearchMessages("is:unread", 10)
	if len(unread) != 2 {
		t.Fatalf("GmailSearchMessages(is:unread) = %v, want 2 (err=%s)", unread, h.LastError())
	}
	gMsg := h.GmailGetMessage(unread[0])
	if gMsg.SubjectVal == "" || !strings.Contains(gMsg.BodyTextVal, "totalRevenue=$2940.00") {
		t.Fatalf("GmailGetMessage decoded = %+v (err=%s)", gMsg, h.LastError())
	}
	sentMail := h.GmailSendMessage("alex@acme.example", "lmmock Integration Email", "Gmail end-to-end via lmmock")
	if sentMail.IDVal == "" {
		t.Fatalf("GmailSendMessage failed: %s", h.LastError())
	}
	modMail := h.GmailModifyLabels(sentMail.IDVal, []string{"IMPORTANT"}, nil)
	if !modMail.HasLabel("IMPORTANT") {
		t.Fatalf("GmailModifyLabels = %+v", modMail.LabelIDsVal)
	}
	if !h.GmailTrashMessage(sentMail.IDVal) || !h.GmailDeleteMessage(sentMail.IDVal) {
		t.Fatalf("GmailTrashMessage/Delete failed: %s", h.LastError())
	}

	if h.LastError() != "" {
		t.Fatalf("unexpected LastError: %s", h.LastError())
	}

	if flowsFile != "" {
		time.Sleep(100 * time.Millisecond)
		rawFlows, err := os.ReadFile(flowsFile)
		if err != nil {
			t.Fatalf("read lminspect flows: %v", err)
		}
		textFlows := string(rawFlows)
		if !strings.Contains(textFlows, "/upload/youtube/v3/videos") || !strings.Contains(textFlows, ":runReport") || !strings.Contains(textFlows, "/gmail/v1/users/me/messages") {
			t.Fatalf("lminspect recording missing expected Google endpoints")
		}
		if strings.Contains(textFlows, "fixture-token") {
			t.Fatalf("lminspect recording leaked unredacted Bearer token")
		}
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForTCP(t *testing.T, addr string, proc *exec.Cmd) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if proc.ProcessState != nil && proc.ProcessState.Exited() {
			t.Fatalf("process exited early while waiting for %s", addr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", addr)
}

// TestCaptureFlowsWithLminspect starts fakegoogle behind an lminspect reverse proxy
// and exercises every Google Analytics and YouTube endpoint supported by lmgoogle
// so real wire traffic is recorded into JSONL flows for lmmock modeling.
//
// Run explicitly via:
//
//	LMINSPECT_BIN=/path/to/lminspect LMGOOGLE_CAPTURE_FLOWS=/path/to/flows.jsonl \
//	  go test -v ./internal/host -run TestCaptureFlowsWithLminspect
func TestCaptureFlowsWithLminspect(t *testing.T) {
	flowsOut := os.Getenv("LMGOOGLE_CAPTURE_FLOWS")
	inspector := os.Getenv("LMINSPECT_BIN")
	if flowsOut == "" || inspector == "" {
		t.Skip("set LMGOOGLE_CAPTURE_FLOWS and LMINSPECT_BIN to capture traffic through lminspect")
	}

	fake, err := fakegoogle.NewServer()
	if err != nil {
		t.Fatalf("fakegoogle.NewServer: %v", err)
	}
	defer fake.Close()

	if err := os.MkdirAll(filepath.Dir(flowsOut), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_ = os.Remove(flowsOut)

	proxyPort := freeLoopbackPort(t)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
	proxyCmd := exec.Command(inspector, "run",
		"--listen", proxyAddr,
		"--mode", "reverse:"+fake.URL(),
		"--record", flowsOut,
	)
	if err := proxyCmd.Start(); err != nil {
		t.Fatalf("start lminspect: %v", err)
	}
	defer func() {
		if proxyCmd.Process != nil {
			_ = proxyCmd.Process.Signal(os.Interrupt)
			_, _ = proxyCmd.Process.Wait()
		}
	}()
	waitForTCP(t, proxyAddr, proxyCmd)

	t.Setenv("LMGOOGLE_LIVE", "0")
	t.Setenv("LMGOOGLE_ENDPOINT", "http://"+proxyAddr)
	t.Setenv("LMGOOGLE_ACCESS_TOKEN", "fixture-token")

	h, err := NewHost(false)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	// Exercise all Google Analytics Admin & Data operations.
	_ = h.GaListAccountSummaries()
	_ = h.GaListAccounts()
	_ = h.GaGetAccount("accounts/1001")
	_ = h.GaListProperties("")
	_ = h.GaListProperties("accounts/1001")
	_ = h.GaGetProperty("3001001")
	prop := h.GaCreateProperty("accounts/1002", "Capture Staging GA4", "America/Chicago", "USD")
	_ = h.GaUpdateProperty(prop.NameVal, "Capture Staging GA4 v2", "", "")
	_ = h.GaListDataStreams("3001001")
	_ = h.GaGetDataStream("properties/3001001/dataStreams/5001")
	ds := h.GaCreateWebStream(prop.NameVal, "Staging Web", "https://staging.acme.example")
	_ = h.GaListCustomDimensions("3001001")
	_ = h.GaCreateCustomDimension(prop.NameVal, "build_sha", "Build SHA", "EVENT")
	_ = h.GaListKeyEvents("3001001")
	_ = h.GaCreateKeyEvent(prop.NameVal, "checkout_complete", "ONCE_PER_EVENT")
	_ = h.GaRunReport("3001001", "7daysAgo", "today", []string{"country", "deviceCategory"}, []string{"activeUsers", "sessions", "totalRevenue"}, "", "", 10)
	_ = h.GaRunReport("3001001", "7daysAgo", "today", []string{"country", "sessionDefaultChannelGroup"}, []string{"activeUsers", "conversions", "totalRevenue"}, "country", "United States", 5)
	_ = h.GaRunRealtimeReport("3001001", []string{"country", "deviceCategory"}, []string{"activeUsers", "screenPageViews"}, 5)
	_ = h.GaGetMetadata("3001001")
	_ = h.GaDeleteDataStream(ds.NameVal)
	_ = h.GaDeleteProperty(prop.NameVal)

	// Exercise all YouTube Data & Analytics operations.
	ch := h.YtMyChannel()
	_ = h.YtGetChannel(ch.IDVal)
	_ = h.YtListChannels("")
	_ = h.YtSearchVideos("Go", 5)
	_ = h.YtSearchVideosInChannel(ch.IDVal, "Go", 5)
	_ = h.YtGetVideo("vid_go_cli_01")

	mediaFile := filepath.Join(t.TempDir(), "upload.mp4")
	if err := os.WriteFile(mediaFile, []byte("demo-mp4-stream-payload"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	uploaded := h.YtUploadVideo(mediaFile, "Capture Upload", "Uploaded via lminspect", []string{"lmgoogle", "test"}, "28", "private")
	_ = h.YtUpdateVideo(uploaded.IDVal, "Capture Upload Updated", "Updated desc", []string{"lmgoogle"}, "unlisted")
	_ = h.YtListPlaylists("")
	_ = h.YtGetPlaylist("PL_ACME_TUTORIALS")
	pl := h.YtCreatePlaylist("Capture Playlist", "Playlist desc", "private")
	_ = h.YtAddPlaylistItem(pl.IDVal, uploaded.IDVal)
	_ = h.YtListPlaylistItems(pl.IDVal, 10)
	_ = h.YtListComments("vid_go_cli_01", 10)
	_ = h.YtPostComment(uploaded.IDVal, "Capture test comment")
	_ = h.YtDeletePlaylist(pl.IDVal)
	_ = h.YtDeleteVideo(uploaded.IDVal)

	_ = h.YtAnalyticsReport("MINE", "", "2026-11-01", "2026-11-15", []string{"day", "video"}, []string{"views", "estimatedMinutesWatched", "likes", "subscribersGained"})
	_ = h.YtAnalyticsReport("MINE", "vid_launch_04", "2026-11-01", "2026-11-15", []string{"day", "video"}, []string{"views", "estimatedMinutesWatched"})

	// Exercise all Gmail v1 operations.
	_ = h.GmailProfile()
	_ = h.GmailListLabels()
	_ = h.GmailSearchMessages("is:unread", 10)
	_ = h.GmailGetMessage("msg_1001")
	_ = h.GmailGetThread("thr_1001")
	sentMail := h.GmailSendMessage("alex@acme.example", "Capture Mail", "Captured via lminspect")
	_ = h.GmailModifyLabels(sentMail.IDVal, []string{"IMPORTANT"}, nil)
	_ = h.GmailTrashMessage(sentMail.IDVal)
	_ = h.GmailDeleteMessage(sentMail.IDVal)

	if h.LastError() != "" {
		t.Fatalf("unexpected LastError during capture: %s", h.LastError())
	}

	// Allow lminspect record addon a moment to flush the final JSONL line before SIGINT.
	time.Sleep(100 * time.Millisecond)
}
