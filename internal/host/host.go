// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lmctl-ai/lmgoogle/internal/auth"
	"github.com/lmctl-ai/lmgoogle/internal/fakegoogle"
	"github.com/lmctl-ai/lmgoogle/internal/safety"
	"google.golang.org/api/analyticsadmin/v1beta"
	"google.golang.org/api/analyticsdata/v1beta"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"google.golang.org/api/youtubeanalytics/v2"
)

// Host manages Google Analytics and YouTube Go SDK clients, authentication,
// quota/safety guardrails, run ledger, and fake/live transport.
type Host struct {
	mu           sync.Mutex
	liveMode     bool
	authMode     string
	endpointURL  string
	readOnly     bool
	autoCleanup  bool
	lastError    string
	lastAPIError *googleapi.Error
	authError    string
	ledger       *safety.RunLedger
	fakeSrv      *fakegoogle.Server

	gaAdminSvc     *analyticsadmin.Service
	gaDataSvc      *analyticsdata.Service
	ytSvc          *youtube.Service
	ytAnalyticsSvc *youtubeanalytics.Service
	gmailSvc       *gmail.Service
}

// NewHost initializes the lmgoogle Host.
// Unless LMGOOGLE_LIVE=1 is set, it starts an in-process fake Google API server
// (or connects to LMGOOGLE_ENDPOINT) and binds all four Google Go SDK clients.
func NewHost(dry bool) (*Host, error) {
	live := os.Getenv("LMGOOGLE_LIVE") == "1" && !dry
	readOnly := os.Getenv("LMGOOGLE_READ_ONLY") == "1"
	autoCleanup := os.Getenv("LMGOOGLE_AUTO_CLEANUP") == "1"

	h := &Host{
		liveMode:    live,
		readOnly:    readOnly,
		autoCleanup: autoCleanup,
		ledger:      safety.NewRunLedger(""),
	}

	ctx := context.Background()
	if endpoint := os.Getenv("LMGOOGLE_ENDPOINT"); endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "http" ||
			(u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			(u.Path != "" && u.Path != "/") || live {
			return nil, fmt.Errorf("LMGOOGLE_ENDPOINT requires a loopback HTTP origin and cannot be combined with LMGOOGLE_LIVE")
		}
		token := os.Getenv("LMGOOGLE_ACCESS_TOKEN")
		if strings.ContainsAny(token, "\r\n") {
			return nil, fmt.Errorf("invalid LMGOOGLE_ACCESS_TOKEN")
		}
		client := &http.Client{
			Transport: emulatorTransport{origin: u.Host, token: token},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		h.authMode = "external-emulator"
		h.endpointURL = strings.TrimRight(endpoint, "/")
		base := h.endpointURL + "/"
		if err := h.initClients(ctx, func(_ string) []option.ClientOption {
			return []option.ClientOption{
				option.WithEndpoint(base),
				option.WithHTTPClient(client),
				option.WithoutAuthentication(),
			}
		}); err != nil {
			return nil, err
		}
		return h, nil
	}

	if !live {
		srv, err := fakegoogle.NewServer()
		if err != nil {
			return nil, fmt.Errorf("failed to start in-process fake Google server: %w", err)
		}
		h.fakeSrv = srv
		h.authMode = "fake-emulator"
		h.endpointURL = srv.URL()
		if err := h.initClients(ctx, func(svc string) []option.ClientOption {
			return []option.ClientOption{
				option.WithEndpoint(srv.ServiceEndpoint(svc)),
				option.WithoutAuthentication(),
			}
		}); err != nil {
			srv.Close()
			return nil, err
		}
		return h, nil
	}

	mode, opts, err := auth.LiveClientOptions(ctx)
	if err != nil {
		return nil, err
	}
	h.authMode = mode
	h.endpointURL = "https://googleapis.com"
	if err := h.initClients(ctx, func(_ string) []option.ClientOption {
		return opts
	}); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Host) initClients(ctx context.Context, optsFor func(svc string) []option.ClientOption) error {
	var err error
	if h.gaAdminSvc, err = analyticsadmin.NewService(ctx, optsFor("analyticsadmin")...); err != nil {
		return err
	}
	if h.gaDataSvc, err = analyticsdata.NewService(ctx, optsFor("analyticsdata")...); err != nil {
		return err
	}
	if h.ytSvc, err = youtube.NewService(ctx, optsFor("youtube")...); err != nil {
		return err
	}
	if h.ytAnalyticsSvc, err = youtubeanalytics.NewService(ctx, optsFor("youtubeanalytics")...); err != nil {
		return err
	}
	if h.gmailSvc, err = gmail.NewService(ctx, optsFor("gmail")...); err != nil {
		return err
	}
	return nil
}

type emulatorTransport struct{ origin, token string }

func (t emulatorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "http" || req.URL.Host != t.origin {
		return nil, fmt.Errorf("emulator request escaped configured origin")
	}
	clone := req.Clone(req.Context())
	if t.token != "" {
		clone.Header.Set("Authorization", "Bearer "+t.token)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	return transport.RoundTrip(clone)
}

// Close shuts down any in-process fake server and cleans up ledger resources if configured.
func (h *Host) Close() {
	if h.autoCleanup {
		_ = h.CleanupLedger()
	}
	if h.fakeSrv != nil {
		h.fakeSrv.Close()
	}
}

func (h *Host) fail(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.lastError = ""
		h.lastAPIError = nil
		return
	}
	msg := safety.RedactSecrets(err.Error())
	if token := os.Getenv("LMGOOGLE_ACCESS_TOKEN"); token != "" {
		msg = strings.ReplaceAll(msg, token, "[REDACTED]")
	}
	h.lastError = msg
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		h.lastAPIError = apiErr
		if apiErr.Code == 401 || apiErr.Code == 403 {
			h.authError = msg
		}
	} else {
		h.lastAPIError = nil
	}
}

func (h *Host) clearErr() {
	h.mu.Lock()
	h.lastError = ""
	h.lastAPIError = nil
	h.mu.Unlock()
}

// AuthError retains authentication/permission failures (401/403).
func (h *Host) AuthError() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authError
}

func (h *Host) checkWriteAllowed(op string) bool {
	h.mu.Lock()
	ro := h.readOnly
	h.mu.Unlock()
	if ro {
		h.fail(fmt.Errorf("readonly mode blocks mutating operation %s", op))
		return false
	}
	return true
}

func (h *Host) chargeQuota(units int64) bool {
	if err := h.ledger.ChargeQuota(units); err != nil {
		h.fail(err)
		return false
	}
	return true
}

func opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// --- Core & Safety ---

func (h *Host) IsLive() bool     { return h.liveMode }
func (h *Host) AuthMode() string { return h.authMode }
func (h *Host) Endpoint() string { return h.endpointURL }
func (h *Host) RunID() string    { return h.ledger.RunID() }

func (h *Host) ReadOnlyMode() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.readOnly
}

func (h *Host) SetReadOnlyMode(v bool) {
	h.mu.Lock()
	h.readOnly = v
	h.mu.Unlock()
}

func (h *Host) LastError() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastError
}

func (h *Host) LedgerCount() int64          { return int64(h.ledger.Count()) }
func (h *Host) LedgerEntries() []string     { return h.ledger.EntriesFormatted() }
func (h *Host) LedgerOwns(k, id string) bool { return h.ledger.Owns(k, id) }
func (h *Host) QuotaUsed() int64            { return h.ledger.QuotaUsed() }
func (h *Host) QuotaLimit() int64           { return h.ledger.QuotaLimit() }
func (h *Host) SetQuotaLimit(v int64) bool  { return h.ledger.SetQuotaLimit(v) }

func (h *Host) CleanupLedger() int64 {
	entries := h.ledger.SnapshotLIFO()
	var deleted int64
	for _, e := range entries {
		var ok bool
		switch e.Kind {
		case "ga_data_stream":
			ok = h.GaDeleteDataStream(e.ID)
		case "ga_property":
			ok = h.GaDeleteProperty(e.ID)
		case "yt_playlist":
			ok = h.YtDeletePlaylist(e.ID)
		case "yt_video":
			ok = h.YtDeleteVideo(e.ID)
		case "gmail_message":
			ok = h.GmailDeleteMessage(e.ID)
		}
		if ok {
			deleted++
		}
	}
	return deleted
}

func (h *Host) InventorySummary() map[string]string {
	accs := h.GaListAccounts()
	props := h.GaListProperties("")
	streams := 0
	for _, p := range props {
		streams += len(h.GaListDataStreams(p))
	}
	chans := h.YtListChannels("")
	playlists := h.YtListPlaylists("")
	videos := h.YtSearchVideos("", 50)
	return map[string]string{
		"ga_accounts":     strconv.Itoa(len(accs)),
		"ga_properties":   strconv.Itoa(len(props)),
		"ga_data_streams": strconv.Itoa(streams),
		"yt_channels":     strconv.Itoa(len(chans)),
		"yt_playlists":    strconv.Itoa(len(playlists)),
		"yt_videos":       strconv.Itoa(len(videos)),
		"auth_mode":       h.authMode,
	}
}

// --- Google Analytics Admin API v1beta ---

func normalizeGaAccount(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "accounts/") {
		return id
	}
	return "accounts/" + id
}

func normalizeGaProperty(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "properties/") {
		return id
	}
	return "properties/" + id
}

func shortResourceID(res string) string {
	if idx := strings.LastIndex(res, "/"); idx >= 0 {
		return res[idx+1:]
	}
	return res
}

func (h *Host) GaListAccountSummaries() []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaAdminSvc.AccountSummaries.List().Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.AccountSummaries))
	for _, s := range resp.AccountSummaries {
		out = append(out, s.Account)
	}
	sort.Strings(out)
	return out
}

func (h *Host) GaListAccounts() []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaAdminSvc.Accounts.List().Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Accounts))
	for _, a := range resp.Accounts {
		out = append(out, a.Name)
	}
	sort.Strings(out)
	return out
}

func (h *Host) GaGetAccount(account string) *GaAccount {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &GaAccount{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	a, err := h.gaAdminSvc.Accounts.Get(normalizeGaAccount(account)).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaAccount{}
	}
	return &GaAccount{
		NameVal:        a.Name,
		AccountIDVal:   shortResourceID(a.Name),
		DisplayNameVal: a.DisplayName,
		RegionCodeVal:  a.RegionCode,
		DeletedVal:     a.Deleted,
	}
}

func (h *Host) GaListProperties(account string) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	call := h.gaAdminSvc.Properties.List().Context(ctx)
	if strings.TrimSpace(account) != "" {
		call = call.Filter("parent:" + normalizeGaAccount(account))
	} else {
		// In GA4 Admin API, filter is required in live calls unless iterating account summaries;
		// if empty in fake mode we list all, and in live mode fall back to accountSummaries when filter is empty.
		resp, err := call.Do()
		if err == nil {
			out := make([]string, 0, len(resp.Properties))
			for _, p := range resp.Properties {
				out = append(out, p.Name)
			}
			sort.Strings(out)
			return out
		}
		sumResp, sumErr := h.gaAdminSvc.AccountSummaries.List().Context(ctx).Do()
		if sumErr != nil {
			h.fail(err)
			return nil
		}
		var out []string
		for _, as := range sumResp.AccountSummaries {
			for _, ps := range as.PropertySummaries {
				out = append(out, ps.Property)
			}
		}
		sort.Strings(out)
		return out
	}
	resp, err := call.Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Properties))
	for _, p := range resp.Properties {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

func propertyToSnapshot(p *analyticsadmin.GoogleAnalyticsAdminV1betaProperty) *GaProperty {
	if p == nil {
		return &GaProperty{}
	}
	return &GaProperty{
		NameVal:             p.Name,
		PropertyIDVal:       shortResourceID(p.Name),
		DisplayNameVal:      p.DisplayName,
		PropertyTypeVal:     p.PropertyType,
		TimeZoneVal:         p.TimeZone,
		CurrencyCodeVal:     p.CurrencyCode,
		IndustryCategoryVal: p.IndustryCategory,
		ServiceLevelVal:     p.ServiceLevel,
		ParentVal:           p.Parent,
	}
}

func (h *Host) GaGetProperty(property string) *GaProperty {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &GaProperty{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	p, err := h.gaAdminSvc.Properties.Get(normalizeGaProperty(property)).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaProperty{}
	}
	return propertyToSnapshot(p)
}

func (h *Host) GaCreateProperty(account, displayName, timeZone, currencyCode string) *GaProperty {
	h.clearErr()
	if !h.checkWriteAllowed("ga_create_property") || !h.chargeQuota(5) {
		return &GaProperty{}
	}
	parent := normalizeGaAccount(account)
	ctx, cancel := opCtx()
	defer cancel()
	p, err := h.gaAdminSvc.Properties.Create(&analyticsadmin.GoogleAnalyticsAdminV1betaProperty{
		Parent:       parent,
		DisplayName:  displayName,
		TimeZone:     timeZone,
		CurrencyCode: currencyCode,
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaProperty{}
	}
	h.ledger.Record("ga_property", parent, p.Name, p.DisplayName)
	return propertyToSnapshot(p)
}

func (h *Host) GaUpdateProperty(property, displayName, timeZone, currencyCode string) *GaProperty {
	h.clearErr()
	if !h.checkWriteAllowed("ga_update_property") || !h.chargeQuota(5) {
		return &GaProperty{}
	}
	propName := normalizeGaProperty(property)
	var mask []string
	body := &analyticsadmin.GoogleAnalyticsAdminV1betaProperty{}
	if displayName != "" {
		body.DisplayName = displayName
		mask = append(mask, "displayName")
	}
	if timeZone != "" {
		body.TimeZone = timeZone
		mask = append(mask, "timeZone")
	}
	if currencyCode != "" {
		body.CurrencyCode = currencyCode
		mask = append(mask, "currencyCode")
	}
	if len(mask) == 0 {
		return h.GaGetProperty(propName)
	}
	ctx, cancel := opCtx()
	defer cancel()
	p, err := h.gaAdminSvc.Properties.Patch(propName, body).UpdateMask(strings.Join(mask, ",")).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaProperty{}
	}
	return propertyToSnapshot(p)
}

func (h *Host) GaDeleteProperty(property string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("ga_delete_property") || !h.chargeQuota(5) {
		return false
	}
	propName := normalizeGaProperty(property)
	if !h.ledger.Owns("ga_property", propName) && os.Getenv("LMGOOGLE_ALLOW_DELETE") != "1" {
		h.fail(fmt.Errorf("safety policy: refusing to delete pre-existing property %q not created in this run (set LMGOOGLE_ALLOW_DELETE=1 to override)", propName))
		return false
	}
	ctx, cancel := opCtx()
	defer cancel()
	if _, err := h.gaAdminSvc.Properties.Delete(propName).Context(ctx).Do(); err != nil {
		h.fail(err)
		return false
	}
	h.ledger.Unrecord("ga_property", propName)
	return true
}

func streamToSnapshot(ds *analyticsadmin.GoogleAnalyticsAdminV1betaDataStream) *GaDataStream {
	if ds == nil {
		return &GaDataStream{}
	}
	out := &GaDataStream{
		NameVal:        ds.Name,
		StreamIDVal:    shortResourceID(ds.Name),
		TypeVal:        ds.Type,
		DisplayNameVal: ds.DisplayName,
	}
	if ds.WebStreamData != nil {
		out.MeasurementIDVal = ds.WebStreamData.MeasurementId
		out.DefaultURIVal = ds.WebStreamData.DefaultUri
	}
	return out
}

func (h *Host) GaListDataStreams(property string) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaAdminSvc.Properties.DataStreams.List(normalizeGaProperty(property)).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.DataStreams))
	for _, s := range resp.DataStreams {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

func (h *Host) GaGetDataStream(streamName string) *GaDataStream {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &GaDataStream{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	ds, err := h.gaAdminSvc.Properties.DataStreams.Get(streamName).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaDataStream{}
	}
	return streamToSnapshot(ds)
}

func (h *Host) GaCreateWebStream(property, displayName, defaultURI string) *GaDataStream {
	h.clearErr()
	if !h.checkWriteAllowed("ga_create_web_stream") || !h.chargeQuota(5) {
		return &GaDataStream{}
	}
	parent := normalizeGaProperty(property)
	ctx, cancel := opCtx()
	defer cancel()
	ds, err := h.gaAdminSvc.Properties.DataStreams.Create(parent, &analyticsadmin.GoogleAnalyticsAdminV1betaDataStream{
		Type:        "WEB_DATA_STREAM",
		DisplayName: displayName,
		WebStreamData: &analyticsadmin.GoogleAnalyticsAdminV1betaDataStreamWebStreamData{
			DefaultUri: defaultURI,
		},
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaDataStream{}
	}
	h.ledger.Record("ga_data_stream", parent, ds.Name, ds.DisplayName)
	return streamToSnapshot(ds)
}

func (h *Host) GaDeleteDataStream(streamName string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("ga_delete_data_stream") || !h.chargeQuota(5) {
		return false
	}
	if !h.ledger.Owns("ga_data_stream", streamName) && os.Getenv("LMGOOGLE_ALLOW_DELETE") != "1" {
		h.fail(fmt.Errorf("safety policy: refusing to delete pre-existing data stream %q not created in this run", streamName))
		return false
	}
	ctx, cancel := opCtx()
	defer cancel()
	if _, err := h.gaAdminSvc.Properties.DataStreams.Delete(streamName).Context(ctx).Do(); err != nil {
		h.fail(err)
		return false
	}
	h.ledger.Unrecord("ga_data_stream", streamName)
	return true
}

func (h *Host) GaListCustomDimensions(property string) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaAdminSvc.Properties.CustomDimensions.List(normalizeGaProperty(property)).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.CustomDimensions))
	for _, cd := range resp.CustomDimensions {
		out = append(out, fmt.Sprintf("%s|%s|%s", cd.Name, cd.ParameterName, cd.Scope))
	}
	sort.Strings(out)
	return out
}

func (h *Host) GaCreateCustomDimension(property, parameterName, displayName, scope string) *GaCustomDimension {
	h.clearErr()
	if !h.checkWriteAllowed("ga_create_custom_dimension") || !h.chargeQuota(5) {
		return &GaCustomDimension{}
	}
	parent := normalizeGaProperty(property)
	ctx, cancel := opCtx()
	defer cancel()
	cd, err := h.gaAdminSvc.Properties.CustomDimensions.Create(parent, &analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension{
		ParameterName: parameterName,
		DisplayName:   displayName,
		Scope:         scope,
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaCustomDimension{}
	}
	return &GaCustomDimension{
		NameVal:          cd.Name,
		ParameterNameVal: cd.ParameterName,
		DisplayNameVal:   cd.DisplayName,
		ScopeVal:         cd.Scope,
		DescriptionVal:   cd.Description,
	}
}

func (h *Host) GaListKeyEvents(property string) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaAdminSvc.Properties.KeyEvents.List(normalizeGaProperty(property)).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.KeyEvents))
	for _, ke := range resp.KeyEvents {
		out = append(out, ke.EventName)
	}
	sort.Strings(out)
	return out
}

func (h *Host) GaCreateKeyEvent(property, eventName, countingMethod string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("ga_create_key_event") || !h.chargeQuota(5) {
		return false
	}
	parent := normalizeGaProperty(property)
	ctx, cancel := opCtx()
	defer cancel()
	_, err := h.gaAdminSvc.Properties.KeyEvents.Create(parent, &analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent{
		EventName:      eventName,
		CountingMethod: countingMethod,
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return false
	}
	return true
}

// --- Google Analytics Data API v1beta ---

func (h *Host) GaRunReport(property, startDate, endDate string, dimensions, metrics []string, filterField, filterValue string, limit int64) *GaReport {
	h.clearErr()
	if !h.chargeQuota(2) {
		return &GaReport{}
	}
	prop := normalizeGaProperty(property)
	req := &analyticsdata.RunReportRequest{
		DateRanges: []*analyticsdata.DateRange{{
			StartDate: defaultIfEmpty(startDate, "7daysAgo"),
			EndDate:   defaultIfEmpty(endDate, "today"),
		}},
	}
	for _, d := range dimensions {
		if strings.TrimSpace(d) != "" {
			req.Dimensions = append(req.Dimensions, &analyticsdata.Dimension{Name: strings.TrimSpace(d)})
		}
	}
	for _, m := range metrics {
		if strings.TrimSpace(m) != "" {
			req.Metrics = append(req.Metrics, &analyticsdata.Metric{Name: strings.TrimSpace(m)})
		}
	}
	if strings.TrimSpace(filterField) != "" && strings.TrimSpace(filterValue) != "" {
		req.DimensionFilter = &analyticsdata.FilterExpression{
			Filter: &analyticsdata.Filter{
				FieldName: strings.TrimSpace(filterField),
				StringFilter: &analyticsdata.StringFilter{
					MatchType: "EXACT",
					Value:     filterValue,
				},
			},
		}
	}
	if limit > 0 {
		req.Limit = limit
	}

	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaDataSvc.Properties.RunReport(prop, req).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaReport{}
	}
	return buildGaReport(resp.DimensionHeaders, resp.MetricHeaders, resp.Rows)
}

func (h *Host) GaRunRealtimeReport(property string, dimensions, metrics []string, limit int64) *GaReport {
	h.clearErr()
	if !h.chargeQuota(2) {
		return &GaReport{}
	}
	prop := normalizeGaProperty(property)
	req := &analyticsdata.RunRealtimeReportRequest{}
	for _, d := range dimensions {
		if strings.TrimSpace(d) != "" {
			req.Dimensions = append(req.Dimensions, &analyticsdata.Dimension{Name: strings.TrimSpace(d)})
		}
	}
	for _, m := range metrics {
		if strings.TrimSpace(m) != "" {
			req.Metrics = append(req.Metrics, &analyticsdata.Metric{Name: strings.TrimSpace(m)})
		}
	}
	if limit > 0 {
		req.Limit = limit
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaDataSvc.Properties.RunRealtimeReport(prop, req).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GaReport{}
	}
	return buildGaReport(resp.DimensionHeaders, resp.MetricHeaders, resp.Rows)
}

func (h *Host) GaGetMetadata(property string) map[string]string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return map[string]string{}
	}
	prop := normalizeGaProperty(property)
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gaDataSvc.Properties.GetMetadata(prop + "/metadata").Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return map[string]string{}
	}
	out := make(map[string]string, len(resp.Dimensions)+len(resp.Metrics))
	for _, d := range resp.Dimensions {
		out["dim:"+d.ApiName] = d.UiName
	}
	for _, m := range resp.Metrics {
		out["metric:"+m.ApiName] = m.UiName
	}
	return out
}

func buildGaReport(dHeaders []*analyticsdata.DimensionHeader, mHeaders []*analyticsdata.MetricHeader, rows []*analyticsdata.Row) *GaReport {
	rep := &GaReport{
		DimensionHeadersVal: make([]string, len(dHeaders)),
		MetricHeadersVal:    make([]string, len(mHeaders)),
		DimensionRowsVal:    make([][]string, len(rows)),
		MetricRowsVal:       make([][]string, len(rows)),
	}
	for i, h := range dHeaders {
		rep.DimensionHeadersVal[i] = h.Name
	}
	for i, h := range mHeaders {
		rep.MetricHeadersVal[i] = h.Name
	}
	for rIdx, row := range rows {
		dVals := make([]string, len(row.DimensionValues))
		for i, dv := range row.DimensionValues {
			dVals[i] = dv.Value
		}
		mVals := make([]string, len(row.MetricValues))
		for i, mv := range row.MetricValues {
			mVals[i] = mv.Value
		}
		rep.DimensionRowsVal[rIdx] = dVals
		rep.MetricRowsVal[rIdx] = mVals
	}
	return rep
}

// --- YouTube Data API v3 ---

func channelToSnapshot(ch *youtube.Channel) *YtChannel {
	if ch == nil {
		return &YtChannel{}
	}
	out := &YtChannel{IDVal: ch.Id}
	if ch.Snippet != nil {
		out.TitleVal = ch.Snippet.Title
		out.DescriptionVal = ch.Snippet.Description
		out.CustomURLVal = ch.Snippet.CustomUrl
		out.CountryVal = ch.Snippet.Country
	}
	if ch.Statistics != nil {
		out.SubscriberCountVal = int64(ch.Statistics.SubscriberCount)
		out.ViewCountVal = int64(ch.Statistics.ViewCount)
		out.VideoCountVal = int64(ch.Statistics.VideoCount)
	}
	if ch.ContentDetails != nil && ch.ContentDetails.RelatedPlaylists != nil {
		out.UploadsPlaylistIDVal = ch.ContentDetails.RelatedPlaylists.Uploads
	}
	return out
}

func (h *Host) YtMyChannel() *YtChannel {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &YtChannel{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.ytSvc.Channels.List([]string{"snippet", "statistics", "contentDetails"}).Mine(true).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &YtChannel{}
	}
	if len(resp.Items) == 0 {
		h.fail(fmt.Errorf("no authenticated YouTube channel found"))
		return &YtChannel{}
	}
	return channelToSnapshot(resp.Items[0])
}

func (h *Host) YtGetChannel(channelID string) *YtChannel {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &YtChannel{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.ytSvc.Channels.List([]string{"snippet", "statistics", "contentDetails"}).Id(channelID).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &YtChannel{}
	}
	if len(resp.Items) == 0 {
		h.fail(fmt.Errorf("YouTube channel not found: %s", channelID))
		return &YtChannel{}
	}
	return channelToSnapshot(resp.Items[0])
}

func (h *Host) YtListChannels(channelID string) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	call := h.ytSvc.Channels.List([]string{"snippet"}).Context(ctx)
	if strings.TrimSpace(channelID) != "" {
		call = call.Id(channelID)
	} else {
		call = call.Mine(true)
	}
	resp, err := call.Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Items))
	for _, ch := range resp.Items {
		out = append(out, ch.Id)
	}
	return out
}

func (h *Host) YtSearchVideos(query string, maxResults int64) []string {
	return h.YtSearchVideosInChannel("", query, maxResults)
}

func (h *Host) YtSearchVideosInChannel(channelID, query string, maxResults int64) []string {
	h.clearErr()
	if !h.chargeQuota(100) {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 50 {
		maxResults = 50
	}
	ctx, cancel := opCtx()
	defer cancel()
	call := h.ytSvc.Search.List([]string{"snippet"}).Type("video").MaxResults(maxResults).Context(ctx)
	if strings.TrimSpace(query) != "" {
		call = call.Q(query)
	}
	if strings.TrimSpace(channelID) != "" {
		call = call.ChannelId(channelID)
	}
	resp, err := call.Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		if item.Id != nil && item.Id.VideoId != "" {
			out = append(out, item.Id.VideoId)
		}
	}
	return out
}

func videoToSnapshot(v *youtube.Video) *YtVideo {
	if v == nil {
		return &YtVideo{}
	}
	out := &YtVideo{IDVal: v.Id}
	if v.Snippet != nil {
		out.TitleVal = v.Snippet.Title
		out.DescriptionVal = v.Snippet.Description
		out.ChannelIDVal = v.Snippet.ChannelId
		out.ChannelTitleVal = v.Snippet.ChannelTitle
		out.PublishedAtVal = v.Snippet.PublishedAt
		out.CategoryIDVal = v.Snippet.CategoryId
		out.TagsVal = append([]string(nil), v.Snippet.Tags...)
	}
	if v.Status != nil {
		out.PrivacyStatusVal = v.Status.PrivacyStatus
	}
	if v.ContentDetails != nil {
		out.DurationVal = v.ContentDetails.Duration
	}
	if v.Statistics != nil {
		out.ViewCountVal = int64(v.Statistics.ViewCount)
		out.LikeCountVal = int64(v.Statistics.LikeCount)
		out.CommentCountVal = int64(v.Statistics.CommentCount)
	}
	return out
}

func (h *Host) YtGetVideo(videoID string) *YtVideo {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &YtVideo{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.ytSvc.Videos.List([]string{"snippet", "statistics", "status", "contentDetails"}).Id(videoID).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &YtVideo{}
	}
	if len(resp.Items) == 0 {
		h.fail(fmt.Errorf("YouTube video not found: %s", videoID))
		return &YtVideo{}
	}
	return videoToSnapshot(resp.Items[0])
}

func (h *Host) YtUploadVideo(filePath, title, description string, tags []string, categoryID, privacyStatus string) *YtVideo {
	h.clearErr()
	if !h.checkWriteAllowed("yt_upload_video") || !h.chargeQuota(1600) {
		return &YtVideo{}
	}
	if strings.TrimSpace(title) == "" {
		h.fail(fmt.Errorf("yt_upload_video: title must not be empty"))
		return &YtVideo{}
	}
	if strings.TrimSpace(privacyStatus) == "" {
		privacyStatus = "private"
	}
	if strings.TrimSpace(categoryID) == "" {
		categoryID = "22"
	}

	video := &youtube.Video{
		Snippet: &youtube.VideoSnippet{
			Title:       title,
			Description: description,
			Tags:        tags,
			CategoryId:  categoryID,
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus: privacyStatus,
		},
	}

	ctx, cancel := opCtx()
	defer cancel()
	call := h.ytSvc.Videos.Insert([]string{"snippet", "status"}, video).Context(ctx)

	if strings.TrimSpace(filePath) != "" {
		f, err := os.Open(filePath)
		if err != nil {
			h.fail(fmt.Errorf("yt_upload_video: open media %q: %w", filePath, err))
			return &YtVideo{}
		}
		defer f.Close()
		call = call.Media(f)
	} else if h.liveMode {
		h.fail(fmt.Errorf("yt_upload_video: file_path is required in live mode"))
		return &YtVideo{}
	}

	created, err := call.Do()
	if err != nil {
		h.fail(err)
		return &YtVideo{}
	}
	h.ledger.Record("yt_video", "", created.Id, title)
	return videoToSnapshot(created)
}

func (h *Host) YtUpdateVideo(videoID, title, description string, tags []string, privacyStatus string) *YtVideo {
	h.clearErr()
	if !h.checkWriteAllowed("yt_update_video") || !h.chargeQuota(50) {
		return &YtVideo{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	curResp, err := h.ytSvc.Videos.List([]string{"snippet", "status"}).Id(videoID).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &YtVideo{}
	}
	if len(curResp.Items) == 0 {
		h.fail(fmt.Errorf("YouTube video not found: %s", videoID))
		return &YtVideo{}
	}
	v := curResp.Items[0]
	if v.Snippet == nil {
		v.Snippet = &youtube.VideoSnippet{}
	}
	if title != "" {
		v.Snippet.Title = title
	}
	if description != "" {
		v.Snippet.Description = description
	}
	if len(tags) > 0 {
		v.Snippet.Tags = tags
	}
	if v.Status == nil {
		v.Status = &youtube.VideoStatus{}
	}
	if privacyStatus != "" {
		v.Status.PrivacyStatus = privacyStatus
	}
	updated, err := h.ytSvc.Videos.Update([]string{"snippet", "status"}, v).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &YtVideo{}
	}
	return videoToSnapshot(updated)
}

func (h *Host) YtDeleteVideo(videoID string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("yt_delete_video") || !h.chargeQuota(50) {
		return false
	}
	if !h.ledger.Owns("yt_video", videoID) && os.Getenv("LMGOOGLE_ALLOW_DELETE") != "1" {
		h.fail(fmt.Errorf("safety policy: refusing to delete pre-existing video %q not created in this run (set LMGOOGLE_ALLOW_DELETE=1 to override)", videoID))
		return false
	}
	ctx, cancel := opCtx()
	defer cancel()
	if err := h.ytSvc.Videos.Delete(videoID).Context(ctx).Do(); err != nil {
		h.fail(err)
		return false
	}
	h.ledger.Unrecord("yt_video", videoID)
	return true
}

func playlistToSnapshot(pl *youtube.Playlist) *YtPlaylist {
	if pl == nil {
		return &YtPlaylist{}
	}
	out := &YtPlaylist{IDVal: pl.Id}
	if pl.Snippet != nil {
		out.TitleVal = pl.Snippet.Title
		out.DescriptionVal = pl.Snippet.Description
		out.ChannelIDVal = pl.Snippet.ChannelId
	}
	if pl.Status != nil {
		out.PrivacyStatusVal = pl.Status.PrivacyStatus
	}
	if pl.ContentDetails != nil {
		out.ItemCountVal = int64(pl.ContentDetails.ItemCount)
	}
	return out
}

func (h *Host) YtListPlaylists(channelID string) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	call := h.ytSvc.Playlists.List([]string{"snippet", "status", "contentDetails"}).MaxResults(50).Context(ctx)
	if strings.TrimSpace(channelID) != "" {
		call = call.ChannelId(channelID)
	} else {
		call = call.Mine(true)
	}
	resp, err := call.Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Items))
	for _, pl := range resp.Items {
		out = append(out, pl.Id)
	}
	return out
}

func (h *Host) YtGetPlaylist(playlistID string) *YtPlaylist {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &YtPlaylist{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.ytSvc.Playlists.List([]string{"snippet", "status", "contentDetails"}).Id(playlistID).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &YtPlaylist{}
	}
	if len(resp.Items) == 0 {
		h.fail(fmt.Errorf("YouTube playlist not found: %s", playlistID))
		return &YtPlaylist{}
	}
	return playlistToSnapshot(resp.Items[0])
}

func (h *Host) YtCreatePlaylist(title, description, privacyStatus string) *YtPlaylist {
	h.clearErr()
	if !h.checkWriteAllowed("yt_create_playlist") || !h.chargeQuota(50) {
		return &YtPlaylist{}
	}
	if strings.TrimSpace(privacyStatus) == "" {
		privacyStatus = "private"
	}
	ctx, cancel := opCtx()
	defer cancel()
	pl, err := h.ytSvc.Playlists.Insert([]string{"snippet", "status"}, &youtube.Playlist{
		Snippet: &youtube.PlaylistSnippet{
			Title:       title,
			Description: description,
		},
		Status: &youtube.PlaylistStatus{
			PrivacyStatus: privacyStatus,
		},
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &YtPlaylist{}
	}
	h.ledger.Record("yt_playlist", "", pl.Id, title)
	return playlistToSnapshot(pl)
}

func (h *Host) YtDeletePlaylist(playlistID string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("yt_delete_playlist") || !h.chargeQuota(50) {
		return false
	}
	if !h.ledger.Owns("yt_playlist", playlistID) && os.Getenv("LMGOOGLE_ALLOW_DELETE") != "1" {
		h.fail(fmt.Errorf("safety policy: refusing to delete pre-existing playlist %q not created in this run", playlistID))
		return false
	}
	ctx, cancel := opCtx()
	defer cancel()
	if err := h.ytSvc.Playlists.Delete(playlistID).Context(ctx).Do(); err != nil {
		h.fail(err)
		return false
	}
	h.ledger.Unrecord("yt_playlist", playlistID)
	return true
}

func (h *Host) YtListPlaylistItems(playlistID string, maxResults int64) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 25
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.ytSvc.PlaylistItems.List([]string{"snippet", "contentDetails"}).PlaylistId(playlistID).MaxResults(maxResults).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Items))
	for _, it := range resp.Items {
		if it.ContentDetails != nil && it.ContentDetails.VideoId != "" {
			out = append(out, it.ContentDetails.VideoId)
		} else if it.Snippet != nil && it.Snippet.ResourceId != nil {
			out = append(out, it.Snippet.ResourceId.VideoId)
		}
	}
	return out
}

func (h *Host) YtAddPlaylistItem(playlistID, videoID string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("yt_add_playlist_item") || !h.chargeQuota(50) {
		return false
	}
	ctx, cancel := opCtx()
	defer cancel()
	_, err := h.ytSvc.PlaylistItems.Insert([]string{"snippet"}, &youtube.PlaylistItem{
		Snippet: &youtube.PlaylistItemSnippet{
			PlaylistId: playlistID,
			ResourceId: &youtube.ResourceId{
				Kind:    "youtube#video",
				VideoId: videoID,
			},
		},
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return false
	}
	return true
}

func (h *Host) YtListComments(videoID string, maxResults int64) []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 20
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.ytSvc.CommentThreads.List([]string{"snippet"}).VideoId(videoID).MaxResults(maxResults).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Items))
	for _, ct := range resp.Items {
		if ct.Snippet != nil && ct.Snippet.TopLevelComment != nil && ct.Snippet.TopLevelComment.Snippet != nil {
			cs := ct.Snippet.TopLevelComment.Snippet
			out = append(out, fmt.Sprintf("%s: %s", cs.AuthorDisplayName, cs.TextDisplay))
		}
	}
	return out
}

func (h *Host) YtPostComment(videoID, text string) string {
	h.clearErr()
	if !h.checkWriteAllowed("yt_post_comment") || !h.chargeQuota(50) {
		return ""
	}
	ctx, cancel := opCtx()
	defer cancel()
	ct, err := h.ytSvc.CommentThreads.Insert([]string{"snippet"}, &youtube.CommentThread{
		Snippet: &youtube.CommentThreadSnippet{
			VideoId: videoID,
			TopLevelComment: &youtube.Comment{
				Snippet: &youtube.CommentSnippet{
					TextOriginal: text,
				},
			},
		},
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return ""
	}
	return ct.Id
}

// --- YouTube Analytics API v2 ---

func (h *Host) YtAnalyticsReport(channelID, videoID, startDate, endDate string, dimensions, metrics []string) *YtAnalyticsReport {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &YtAnalyticsReport{}
	}
	ids := "channel==MINE"
	if strings.TrimSpace(channelID) != "" && !strings.EqualFold(strings.TrimSpace(channelID), "MINE") {
		ids = "channel==" + strings.TrimSpace(channelID)
	}
	ctx, cancel := opCtx()
	defer cancel()
	call := h.ytAnalyticsSvc.Reports.Query().
		Ids(ids).
		StartDate(defaultIfEmpty(startDate, "2026-01-01")).
		EndDate(defaultIfEmpty(endDate, "2026-12-31")).
		Metrics(strings.Join(metrics, ",")).
		Context(ctx)
	if len(dimensions) > 0 {
		call = call.Dimensions(strings.Join(dimensions, ","))
	}
	if strings.TrimSpace(videoID) != "" {
		call = call.Filters("video==" + strings.TrimSpace(videoID))
	}
	resp, err := call.Do()
	if err != nil {
		h.fail(err)
		return &YtAnalyticsReport{}
	}
	headers := make([]string, len(resp.ColumnHeaders))
	for i, ch := range resp.ColumnHeaders {
		headers[i] = ch.Name
	}
	rows := make([][]string, len(resp.Rows))
	for i, rawRow := range resp.Rows {
		strRow := make([]string, len(rawRow))
		for j, cell := range rawRow {
			strRow[j] = fmt.Sprint(cell)
		}
		rows[i] = strRow
	}
	return &YtAnalyticsReport{
		ColumnHeadersVal: headers,
		RowsVal:          rows,
	}
}

func defaultIfEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

// --- Gmail API v1 ---

func gmailMessageToSnapshot(msg *gmail.Message) *GmailMessage {
	if msg == nil {
		return &GmailMessage{}
	}
	out := &GmailMessage{
		IDVal:       msg.Id,
		ThreadIDVal: msg.ThreadId,
		SnippetVal:  msg.Snippet,
		LabelIDsVal: append([]string(nil), msg.LabelIds...),
		HeadersVal:  make(map[string]string),
	}
	if msg.Payload != nil {
		for _, hdr := range msg.Payload.Headers {
			out.HeadersVal[hdr.Name] = hdr.Value
			switch strings.ToLower(hdr.Name) {
			case "subject":
				out.SubjectVal = hdr.Value
			case "from":
				out.FromVal = hdr.Value
			case "to":
				out.ToVal = hdr.Value
			case "date":
				out.DateVal = hdr.Value
			}
		}
		out.BodyTextVal = extractGmailPartText(msg.Payload)
		for _, p := range collectGmailAttachmentParts(msg.Payload) {
			name := p.Filename
			if name == "" {
				name = p.PartId
			}
			attID := ""
			if p.Body != nil {
				attID = p.Body.AttachmentId
			}
			out.AttachmentNamesVal = append(out.AttachmentNamesVal, name)
			out.AttachmentIDsVal = append(out.AttachmentIDsVal, attID)
			out.AttachmentMimesVal = append(out.AttachmentMimesVal, p.MimeType)
		}
	}
	if out.BodyTextVal == "" {
		out.BodyTextVal = msg.Snippet
	}
	if ts, ok := parseGmailTimestamp(out.DateVal, msg.InternalDate); ok {
		utc := ts.UTC()
		out.DateISOVal = utc.Format(time.RFC3339)
		out.TimestampSlugVal = utc.Format("20060102T150405Z")
	} else {
		out.TimestampSlugVal = "00000000T000000Z"
	}
	return out
}

func parseGmailTimestamp(dateHdr string, internalDateMs int64) (time.Time, bool) {
	if s := strings.TrimSpace(dateHdr); s != "" {
		if t, err := mail.ParseDate(s); err == nil {
			return t, true
		}
	}
	if internalDateMs > 0 {
		return time.UnixMilli(internalDateMs), true
	}
	return time.Time{}, false
}

func collectGmailAttachmentParts(part *gmail.MessagePart) []*gmail.MessagePart {
	if part == nil {
		return nil
	}
	var out []*gmail.MessagePart
	hasAttID := part.Body != nil && part.Body.AttachmentId != ""
	if part.Filename != "" || hasAttID {
		out = append(out, part)
	}
	for _, sub := range part.Parts {
		out = append(out, collectGmailAttachmentParts(sub)...)
	}
	return out
}

func decodeBase64URLBytes(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

func decodeBase64URL(s string) string {
	b, err := decodeBase64URLBytes(s)
	if err != nil {
		return ""
	}
	return string(b)
}

func findGmailPartByMime(part *gmail.MessagePart, mimePrefix string) string {
	if part == nil {
		return ""
	}
	mt := strings.ToLower(strings.TrimSpace(part.MimeType))
	if (mimePrefix == "" || strings.HasPrefix(mt, mimePrefix)) && part.Body != nil && part.Body.Data != "" {
		if txt := decodeBase64URL(part.Body.Data); txt != "" {
			return txt
		}
	}
	for _, sub := range part.Parts {
		if txt := findGmailPartByMime(sub, mimePrefix); txt != "" {
			return txt
		}
	}
	return ""
}

func extractGmailPartText(part *gmail.MessagePart) string {
	if part == nil {
		return ""
	}
	if txt := findGmailPartByMime(part, "text/plain"); txt != "" {
		return txt
	}
	if txt := findGmailPartByMime(part, "text/html"); txt != "" {
		return txt
	}
	return findGmailPartByMime(part, "")
}

func (h *Host) GmailProfile() *GmailProfile {
	h.clearErr()
	if !h.chargeQuota(1) {
		return &GmailProfile{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	p, err := h.gmailSvc.Users.GetProfile("me").Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GmailProfile{}
	}
	return &GmailProfile{
		EmailAddressVal:  p.EmailAddress,
		MessagesTotalVal: p.MessagesTotal,
		ThreadsTotalVal:  p.ThreadsTotal,
		HistoryIDVal:     strconv.FormatUint(p.HistoryId, 10),
	}
}

func (h *Host) GmailListLabels() []string {
	h.clearErr()
	if !h.chargeQuota(1) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	resp, err := h.gmailSvc.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(resp.Labels))
	for _, lbl := range resp.Labels {
		out = append(out, lbl.Id)
	}
	sort.Strings(out)
	return out
}

func (h *Host) GmailSearchMessages(query string, maxResults int64) []string {
	h.clearErr()
	if !h.chargeQuota(5) {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 25
	}
	ctx, cancel := opCtx()
	defer cancel()
	var out []string
	var pageToken string
	q := strings.TrimSpace(query)
	for int64(len(out)) < maxResults {
		pageSize := maxResults - int64(len(out))
		if pageSize > 500 {
			pageSize = 500
		}
		call := h.gmailSvc.Users.Messages.List("me").MaxResults(pageSize).Context(ctx)
		if q != "" {
			call = call.Q(q)
		}
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			h.fail(err)
			return nil
		}
		for _, m := range resp.Messages {
			out = append(out, m.Id)
			if int64(len(out)) >= maxResults {
				break
			}
		}
		if resp.NextPageToken == "" || len(resp.Messages) == 0 {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out
}

func (h *Host) GmailGetMessage(id string) *GmailMessage {
	h.clearErr()
	if !h.chargeQuota(5) {
		return &GmailMessage{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	msg, err := h.gmailSvc.Users.Messages.Get("me", strings.TrimSpace(id)).Format("full").Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GmailMessage{}
	}
	return gmailMessageToSnapshot(msg)
}

func (h *Host) GmailGetThread(threadID string) []string {
	h.clearErr()
	if !h.chargeQuota(5) {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	thr, err := h.gmailSvc.Users.Threads.Get("me", strings.TrimSpace(threadID)).Format("metadata").Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	out := make([]string, 0, len(thr.Messages))
	for _, m := range thr.Messages {
		out = append(out, m.Id)
	}
	return out
}

func (h *Host) GmailSendMessage(to, subject, bodyText string) *GmailMessage {
	h.clearErr()
	if !h.checkWriteAllowed("gmail_send_message") || !h.chargeQuota(100) {
		return &GmailMessage{}
	}
	cleanHeader := func(s string) string {
		return strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(s))
	}
	rawMsg := fmt.Sprintf("To: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		cleanHeader(to), cleanHeader(subject), bodyText)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(rawMsg))

	ctx, cancel := opCtx()
	defer cancel()
	sent, err := h.gmailSvc.Users.Messages.Send("me", &gmail.Message{Raw: encoded}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GmailMessage{}
	}
	if sent.Id != "" {
		h.ledger.Record("gmail_message", "", sent.Id, cleanHeader(subject))
	}
	return gmailMessageToSnapshot(sent)
}

func (h *Host) GmailModifyLabels(id string, addLabels, removeLabels []string) *GmailMessage {
	h.clearErr()
	if !h.checkWriteAllowed("gmail_modify_labels") || !h.chargeQuota(5) {
		return &GmailMessage{}
	}
	ctx, cancel := opCtx()
	defer cancel()
	mod, err := h.gmailSvc.Users.Messages.Modify("me", strings.TrimSpace(id), &gmail.ModifyMessageRequest{
		AddLabelIds:    addLabels,
		RemoveLabelIds: removeLabels,
	}).Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return &GmailMessage{}
	}
	return gmailMessageToSnapshot(mod)
}

func (h *Host) GmailTrashMessage(id string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("gmail_trash_message") || !h.chargeQuota(5) {
		return false
	}
	ctx, cancel := opCtx()
	defer cancel()
	if _, err := h.gmailSvc.Users.Messages.Trash("me", strings.TrimSpace(id)).Context(ctx).Do(); err != nil {
		h.fail(err)
		return false
	}
	return true
}

func (h *Host) GmailDeleteMessage(id string) bool {
	h.clearErr()
	if !h.checkWriteAllowed("gmail_delete_message") || !h.chargeQuota(10) {
		return false
	}
	msgID := strings.TrimSpace(id)
	if !h.ledger.Owns("gmail_message", msgID) && os.Getenv("LMGOOGLE_ALLOW_DELETE") != "1" {
		h.fail(fmt.Errorf("safety policy: refusing to delete pre-existing message %q not created in this run (set LMGOOGLE_ALLOW_DELETE=1 to override)", msgID))
		return false
	}
	ctx, cancel := opCtx()
	defer cancel()
	if err := h.gmailSvc.Users.Messages.Delete("me", msgID).Context(ctx).Do(); err != nil {
		h.fail(err)
		return false
	}
	h.ledger.Unrecord("gmail_message", msgID)
	return true
}

func sanitizeAttachmentName(raw string, index int) string {
	cleaned := strings.ReplaceAll(raw, `\`, `/`)
	base := filepath.Base(strings.TrimSpace(cleaned))
	base = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '\x00' {
			return '_'
		}
		return r
	}, base)
	if base == "" || base == "." || base == ".." {
		return fmt.Sprintf("attachment_%d.bin", index+1)
	}
	return base
}

func uniqueDestPath(outDir, name string) string {
	candidate := filepath.Join(outDir, name)
	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 2; n < 10000; n++ {
		alt := filepath.Join(outDir, fmt.Sprintf("%s_%d%s", stem, n, ext))
		if _, err := os.Stat(alt); errors.Is(err, os.ErrNotExist) {
			return alt
		}
	}
	return filepath.Join(outDir, fmt.Sprintf("%s_%d%s", stem, time.Now().UnixNano(), ext))
}

func sanitizeAttachmentPrefix(prefix string) string {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, p)
}

func (h *Host) GmailDownloadAttachments(msgID, outDir string) []string {
	return h.GmailDownloadAttachmentsPrefixed(msgID, outDir, "")
}

func (h *Host) GmailDownloadAttachmentsPrefixed(msgID, outDir, prefix string) []string {
	h.clearErr()
	if !h.chargeQuota(5) {
		return nil
	}
	id := strings.TrimSpace(msgID)
	dir := strings.TrimSpace(outDir)
	if id == "" || dir == "" {
		h.fail(fmt.Errorf("gmail_download_attachments requires non-empty message id and out_dir"))
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()

	msg, err := h.gmailSvc.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		h.fail(err)
		return nil
	}
	parts := collectGmailAttachmentParts(msg.Payload)
	if len(parts) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.fail(fmt.Errorf("create attachment output dir %q: %w", dir, err))
		return nil
	}

	safePrefix := sanitizeAttachmentPrefix(prefix)
	var saved []string
	for i, part := range parts {
		var data []byte
		if part.Body != nil && part.Body.AttachmentId != "" {
			att, err := h.gmailSvc.Users.Messages.Attachments.Get("me", id, part.Body.AttachmentId).Context(ctx).Do()
			if err != nil {
				h.fail(err)
				return saved
			}
			data, err = decodeBase64URLBytes(att.Data)
			if err != nil {
				h.fail(fmt.Errorf("decode attachment %q: %w", part.Filename, err))
				return saved
			}
		} else if part.Body != nil && part.Body.Data != "" {
			data, err = decodeBase64URLBytes(part.Body.Data)
			if err != nil {
				h.fail(fmt.Errorf("decode inline attachment %q: %w", part.Filename, err))
				return saved
			}
		}
		fileName := sanitizeAttachmentName(part.Filename, i)
		if safePrefix != "" {
			fileName = safePrefix + fileName
		}
		dest := uniqueDestPath(dir, fileName)
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			h.fail(fmt.Errorf("write attachment %q: %w", dest, err))
			return saved
		}
		saved = append(saved, dest)
	}
	return saved
}
