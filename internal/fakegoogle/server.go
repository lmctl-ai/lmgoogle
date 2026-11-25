// SPDX-License-Identifier: MIT

package fakegoogle

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/api/analyticsadmin/v1beta"
	"google.golang.org/api/analyticsdata/v1beta"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/youtube/v3"
	"google.golang.org/api/youtubeanalytics/v2"
)

type gaSampleRow struct {
	Country         string
	Device          string
	Channel         string
	Date            string
	ActiveUsers     int64
	Sessions        int64
	ScreenPageViews int64
	Conversions     int64
	Revenue         float64
}

type ytAnalyticsRow struct {
	Day                     string
	VideoID                 string
	Country                 string
	Views                   int64
	EstimatedMinutesWatched int64
	AverageViewDuration     int64
	Likes                   int64
	Comments                int64
	SubscribersGained       int64
}

// Server is an in-process fake Google Analytics & YouTube REST server
// consumed directly by google.golang.org/api SDK clients.
type Server struct {
	mu       sync.RWMutex
	listener net.Listener
	httpSrv  *http.Server
	baseURL  string
	seq      int64

	accounts    map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaAccount
	properties  map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaProperty
	dataStreams map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaDataStream
	customDims  map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension
	keyEvents   map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent
	gaRows      map[string][]gaSampleRow

	myChannelID     string
	channels        map[string]*youtube.Channel
	videos          map[string]*youtube.Video
	playlists       map[string]*youtube.Playlist
	playlistItems   map[string][]string
	comments        map[string][]*youtube.CommentThread
	ytAnalyticsRows []ytAnalyticsRow

	gmailEmail       string
	gmailLabels      map[string]*gmail.Label
	gmailMessages    map[string]*gmail.Message
	gmailAttachments map[string][]byte
	gmailOrder       []string
}

// NewServer starts a local loopback fake Google API server.
func NewServer() (*Server, error) {
	s := &Server{
		accounts:         make(map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaAccount),
		properties:       make(map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaProperty),
		dataStreams:      make(map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaDataStream),
		customDims:       make(map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension),
		keyEvents:        make(map[string]*analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent),
		gaRows:           make(map[string][]gaSampleRow),
		channels:         make(map[string]*youtube.Channel),
		videos:           make(map[string]*youtube.Video),
		playlists:        make(map[string]*youtube.Playlist),
		playlistItems:    make(map[string][]string),
		comments:         make(map[string][]*youtube.CommentThread),
		gmailLabels:      make(map[string]*gmail.Label),
		gmailMessages:    make(map[string]*gmail.Message),
		gmailAttachments: make(map[string][]byte),
	}
	s.seed()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.listener = ln
	s.baseURL = "http://" + ln.Addr().String()
	s.httpSrv = &http.Server{Handler: http.HandlerFunc(s.serveHTTP)}
	go func() { _ = s.httpSrv.Serve(ln) }()
	return s, nil
}

// URL returns the loopback origin URL.
func (s *Server) URL() string { return s.baseURL }

// ServiceEndpoint returns the service base URL for option.WithEndpoint.
func (s *Server) ServiceEndpoint(_ string) string { return s.baseURL + "/" }

// Close shuts down the fake HTTP server.
func (s *Server) Close() {
	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
			"errors": []map[string]any{
				{"reason": reason, "message": msg},
			},
		},
	})
}

func (s *Server) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%d", prefix, 9000+s.seq)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/v1beta/accountSummaries"):
		s.handleAccountSummaries(w, r)
	case strings.HasPrefix(path, "/v1beta/accounts"):
		s.handleAccounts(w, r)
	case strings.HasPrefix(path, "/v1beta/properties"):
		s.handlePropertiesAndData(w, r)
	case strings.HasPrefix(path, "/upload/youtube/v3/videos"):
		s.handleYouTubeVideoUpload(w, r)
	case strings.HasPrefix(path, "/youtube/v3/channels"):
		s.handleYouTubeChannels(w, r)
	case strings.HasPrefix(path, "/youtube/v3/search"):
		s.handleYouTubeSearch(w, r)
	case strings.HasPrefix(path, "/youtube/v3/videos"):
		s.handleYouTubeVideos(w, r)
	case strings.HasPrefix(path, "/youtube/v3/playlists"):
		s.handleYouTubePlaylists(w, r)
	case strings.HasPrefix(path, "/youtube/v3/playlistItems"):
		s.handleYouTubePlaylistItems(w, r)
	case strings.HasPrefix(path, "/youtube/v3/commentThreads"):
		s.handleYouTubeComments(w, r)
	case strings.HasPrefix(path, "/v2/reports"):
		s.handleYouTubeAnalyticsReports(w, r)
	case strings.HasPrefix(path, "/gmail/v1/"):
		s.handleGmail(w, r)
	default:
		writeAPIError(w, http.StatusNotFound, "notFound", "unhandled fakegoogle route: "+r.Method+" "+path)
	}
}

// --- Google Analytics Admin & Data handlers ---

func (s *Server) handleAccountSummaries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "GET required")
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	accKeys := make([]string, 0, len(s.accounts))
	for k := range s.accounts {
		accKeys = append(accKeys, k)
	}
	sort.Strings(accKeys)

	propKeys := make([]string, 0, len(s.properties))
	for k := range s.properties {
		propKeys = append(propKeys, k)
	}
	sort.Strings(propKeys)

	summaries := make([]*analyticsadmin.GoogleAnalyticsAdminV1betaAccountSummary, 0, len(accKeys))
	for _, ak := range accKeys {
		acc := s.accounts[ak]
		var psums []*analyticsadmin.GoogleAnalyticsAdminV1betaPropertySummary
		for _, pk := range propKeys {
			p := s.properties[pk]
			if p.Parent == ak || p.Account == ak {
				psums = append(psums, &analyticsadmin.GoogleAnalyticsAdminV1betaPropertySummary{
					Property:     p.Name,
					DisplayName:  p.DisplayName,
					PropertyType: p.PropertyType,
					Parent:       p.Parent,
				})
			}
		}
		summaries = append(summaries, &analyticsadmin.GoogleAnalyticsAdminV1betaAccountSummary{
			Name:              strings.Replace(ak, "accounts/", "accountSummaries/", 1),
			Account:           ak,
			DisplayName:       acc.DisplayName,
			PropertySummaries: psums,
		})
	}
	writeJSON(w, http.StatusOK, &analyticsadmin.GoogleAnalyticsAdminV1betaListAccountSummariesResponse{
		AccountSummaries: summaries,
	})
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1beta/")
	if rest == "accounts" && r.Method == http.MethodGet {
		s.mu.RLock()
		defer s.mu.RUnlock()
		keys := make([]string, 0, len(s.accounts))
		for k := range s.accounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]*analyticsadmin.GoogleAnalyticsAdminV1betaAccount, 0, len(keys))
		for _, k := range keys {
			out = append(out, s.accounts[k])
		}
		writeJSON(w, http.StatusOK, &analyticsadmin.GoogleAnalyticsAdminV1betaListAccountsResponse{
			Accounts: out,
		})
		return
	}
	s.mu.RLock()
	acc, ok := s.accounts[rest]
	s.mu.RUnlock()
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound", "account not found: "+rest)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handlePropertiesAndData(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1beta/")

	// Collection: /v1beta/properties
	if rest == "properties" {
		switch r.Method {
		case http.MethodGet:
			filter := r.URL.Query().Get("filter")
			parentFilter := ""
			if strings.HasPrefix(filter, "parent:") {
				parentFilter = strings.TrimSpace(strings.TrimPrefix(filter, "parent:"))
			}
			s.mu.RLock()
			keys := make([]string, 0, len(s.properties))
			for k := range s.properties {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var out []*analyticsadmin.GoogleAnalyticsAdminV1betaProperty
			for _, k := range keys {
				p := s.properties[k]
				if parentFilter != "" && p.Parent != parentFilter && p.Account != parentFilter {
					continue
				}
				out = append(out, p)
			}
			s.mu.RUnlock()
			writeJSON(w, http.StatusOK, &analyticsadmin.GoogleAnalyticsAdminV1betaListPropertiesResponse{
				Properties: out,
			})
			return
		case http.MethodPost:
			var req analyticsadmin.GoogleAnalyticsAdminV1betaProperty
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalidArgument", err.Error())
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if req.Parent != "" {
				if _, ok := s.accounts[req.Parent]; !ok {
					writeAPIError(w, http.StatusNotFound, "notFound", "parent account not found: "+req.Parent)
					return
				}
			}
			id := s.nextID("39")
			id = strings.ReplaceAll(id, "_", "")
			name := "properties/" + id
			prop := &analyticsadmin.GoogleAnalyticsAdminV1betaProperty{
				Name:             name,
				Parent:           req.Parent,
				Account:          req.Parent,
				DisplayName:      req.DisplayName,
				PropertyType:     "PROPERTY_TYPE_ORDINARY",
				TimeZone:         defaultStr(req.TimeZone, "America/Chicago"),
				CurrencyCode:     defaultStr(req.CurrencyCode, "USD"),
				IndustryCategory: defaultStr(req.IndustryCategory, "TECHNOLOGY"),
				ServiceLevel:     "GOOGLE_ANALYTICS_STANDARD",
			}
			s.properties[name] = prop
			writeJSON(w, http.StatusOK, prop)
			return
		}
	}

	// Data API RPCs: properties/{id}:runReport, :runRealtimeReport
	if strings.HasSuffix(rest, ":runReport") {
		propName := strings.TrimSuffix(rest, ":runReport")
		s.handleRunReport(w, r, propName, false)
		return
	}
	if strings.HasSuffix(rest, ":runRealtimeReport") {
		propName := strings.TrimSuffix(rest, ":runRealtimeReport")
		s.handleRunReport(w, r, propName, true)
		return
	}
	if strings.HasSuffix(rest, "/metadata") && r.Method == http.MethodGet {
		propName := strings.TrimSuffix(rest, "/metadata")
		s.mu.RLock()
		_, ok := s.properties[propName]
		s.mu.RUnlock()
		if !ok {
			writeAPIError(w, http.StatusNotFound, "notFound", "property not found: "+propName)
			return
		}
		writeJSON(w, http.StatusOK, &analyticsdata.Metadata{
			Name: rest,
			Dimensions: []*analyticsdata.DimensionMetadata{
				{ApiName: "country", UiName: "Country", Description: "User country"},
				{ApiName: "deviceCategory", UiName: "Device category", Description: "desktop, mobile, tablet"},
				{ApiName: "sessionDefaultChannelGroup", UiName: "Session default channel group", Description: "Acquisition channel"},
				{ApiName: "date", UiName: "Date", Description: "YYYYMMDD date"},
			},
			Metrics: []*analyticsdata.MetricMetadata{
				{ApiName: "activeUsers", UiName: "Active users", Description: "Distinct active users"},
				{ApiName: "sessions", UiName: "Sessions", Description: "Engaged and total sessions"},
				{ApiName: "screenPageViews", UiName: "Views", Description: "Page or screen views"},
				{ApiName: "conversions", UiName: "Conversions", Description: "Key event conversions"},
				{ApiName: "totalRevenue", UiName: "Total revenue", Description: "Purchase revenue"},
			},
		})
		return
	}

	// Subresources: dataStreams, customDimensions, keyEvents
	if strings.Contains(rest, "/dataStreams") {
		s.handleDataStreams(w, r, rest)
		return
	}
	if strings.Contains(rest, "/customDimensions") {
		s.handleCustomDimensions(w, r, rest)
		return
	}
	if strings.Contains(rest, "/keyEvents") {
		s.handleKeyEvents(w, r, rest)
		return
	}

	// Single property GET/PATCH/DELETE: properties/{id}
	s.mu.Lock()
	defer s.mu.Unlock()
	prop, ok := s.properties[rest]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound", "property not found: "+rest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, prop)
	case http.MethodPatch:
		var patch analyticsadmin.GoogleAnalyticsAdminV1betaProperty
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalidArgument", err.Error())
			return
		}
		if patch.DisplayName != "" {
			prop.DisplayName = patch.DisplayName
		}
		if patch.TimeZone != "" {
			prop.TimeZone = patch.TimeZone
		}
		if patch.CurrencyCode != "" {
			prop.CurrencyCode = patch.CurrencyCode
		}
		writeJSON(w, http.StatusOK, prop)
	case http.MethodDelete:
		delete(s.properties, rest)
		writeJSON(w, http.StatusOK, prop)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
	}
}

func (s *Server) handleDataStreams(w http.ResponseWriter, r *http.Request, rest string) {
	if strings.HasSuffix(rest, "/dataStreams") {
		parent := strings.TrimSuffix(rest, "/dataStreams")
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.properties[parent]; !ok {
			writeAPIError(w, http.StatusNotFound, "notFound", "property not found: "+parent)
			return
		}
		switch r.Method {
		case http.MethodGet:
			keys := make([]string, 0)
			for k := range s.dataStreams {
				if strings.HasPrefix(k, parent+"/dataStreams/") {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			out := make([]*analyticsadmin.GoogleAnalyticsAdminV1betaDataStream, 0, len(keys))
			for _, k := range keys {
				out = append(out, s.dataStreams[k])
			}
			writeJSON(w, http.StatusOK, &analyticsadmin.GoogleAnalyticsAdminV1betaListDataStreamsResponse{DataStreams: out})
		case http.MethodPost:
			var req analyticsadmin.GoogleAnalyticsAdminV1betaDataStream
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalidArgument", err.Error())
				return
			}
			id := strings.ReplaceAll(s.nextID("7"), "_", "")
			name := parent + "/dataStreams/" + id
			ds := &analyticsadmin.GoogleAnalyticsAdminV1betaDataStream{
				Name:        name,
				Type:        defaultStr(req.Type, "WEB_DATA_STREAM"),
				DisplayName: req.DisplayName,
			}
			if ds.Type == "WEB_DATA_STREAM" {
				uri := ""
				if req.WebStreamData != nil {
					uri = req.WebStreamData.DefaultUri
				}
				ds.WebStreamData = &analyticsadmin.GoogleAnalyticsAdminV1betaDataStreamWebStreamData{
					MeasurementId: "G-LM" + id,
					DefaultUri:    uri,
				}
			}
			s.dataStreams[name] = ds
			writeJSON(w, http.StatusOK, ds)
		}
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ds, ok := s.dataStreams[rest]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound", "dataStream not found: "+rest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, ds)
	case http.MethodDelete:
		delete(s.dataStreams, rest)
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

func (s *Server) handleCustomDimensions(w http.ResponseWriter, r *http.Request, rest string) {
	if strings.HasSuffix(rest, "/customDimensions") {
		parent := strings.TrimSuffix(rest, "/customDimensions")
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.properties[parent]; !ok {
			writeAPIError(w, http.StatusNotFound, "notFound", "property not found: "+parent)
			return
		}
		switch r.Method {
		case http.MethodGet:
			keys := make([]string, 0)
			for k := range s.customDims {
				if strings.HasPrefix(k, parent+"/customDimensions/") {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			out := make([]*analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension, 0, len(keys))
			for _, k := range keys {
				out = append(out, s.customDims[k])
			}
			writeJSON(w, http.StatusOK, &analyticsadmin.GoogleAnalyticsAdminV1betaListCustomDimensionsResponse{CustomDimensions: out})
		case http.MethodPost:
			var req analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalidArgument", err.Error())
				return
			}
			id := strings.ReplaceAll(s.nextID("cd"), "_", "")
			name := parent + "/customDimensions/" + id
			cd := &analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension{
				Name:          name,
				ParameterName: req.ParameterName,
				DisplayName:   req.DisplayName,
				Scope:         defaultStr(req.Scope, "EVENT"),
				Description:   req.Description,
			}
			s.customDims[name] = cd
			writeJSON(w, http.StatusOK, cd)
		}
		return
	}
	s.mu.RLock()
	cd, ok := s.customDims[rest]
	s.mu.RUnlock()
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound", "customDimension not found: "+rest)
		return
	}
	writeJSON(w, http.StatusOK, cd)
}

func (s *Server) handleKeyEvents(w http.ResponseWriter, r *http.Request, rest string) {
	if strings.HasSuffix(rest, "/keyEvents") {
		parent := strings.TrimSuffix(rest, "/keyEvents")
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.properties[parent]; !ok {
			writeAPIError(w, http.StatusNotFound, "notFound", "property not found: "+parent)
			return
		}
		switch r.Method {
		case http.MethodGet:
			keys := make([]string, 0)
			for k := range s.keyEvents {
				if strings.HasPrefix(k, parent+"/keyEvents/") {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			out := make([]*analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent, 0, len(keys))
			for _, k := range keys {
				out = append(out, s.keyEvents[k])
			}
			writeJSON(w, http.StatusOK, &analyticsadmin.GoogleAnalyticsAdminV1betaListKeyEventsResponse{KeyEvents: out})
		case http.MethodPost:
			var req analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalidArgument", err.Error())
				return
			}
			name := parent + "/keyEvents/" + req.EventName
			ke := &analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent{
				Name:           name,
				EventName:      req.EventName,
				CountingMethod: defaultStr(req.CountingMethod, "ONCE_PER_EVENT"),
			}
			s.keyEvents[name] = ke
			writeJSON(w, http.StatusOK, ke)
		}
		return
	}
}

func (s *Server) handleRunReport(w http.ResponseWriter, r *http.Request, propName string, realtime bool) {
	var dims []string
	var mets []string
	var filterField, filterVal string
	var limit int64

	if realtime {
		var req analyticsdata.RunRealtimeReportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalidArgument", err.Error())
			return
		}
		for _, d := range req.Dimensions {
			dims = append(dims, d.Name)
		}
		for _, m := range req.Metrics {
			mets = append(mets, m.Name)
		}
		limit = req.Limit
	} else {
		var req analyticsdata.RunReportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalidArgument", err.Error())
			return
		}
		for _, d := range req.Dimensions {
			dims = append(dims, d.Name)
		}
		for _, m := range req.Metrics {
			mets = append(mets, m.Name)
		}
		if req.DimensionFilter != nil && req.DimensionFilter.Filter != nil {
			filterField = req.DimensionFilter.Filter.FieldName
			if req.DimensionFilter.Filter.StringFilter != nil {
				filterVal = req.DimensionFilter.Filter.StringFilter.Value
			}
		}
		limit = req.Limit
	}

	s.mu.RLock()
	_, ok := s.properties[propName]
	baseRows := s.gaRows[propName]
	s.mu.RUnlock()
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound", "property not found: "+propName)
		return
	}
	if len(baseRows) == 0 {
		baseRows = []gaSampleRow{
			{Country: "United States", Device: "desktop", Channel: "Direct", Date: "20261114", ActiveUsers: 120, Sessions: 150, ScreenPageViews: 410, Conversions: 5, Revenue: 250},
		}
	}

	dimVal := func(row gaSampleRow, d string) string {
		switch d {
		case "country":
			return row.Country
		case "deviceCategory":
			return row.Device
		case "sessionDefaultChannelGroup", "firstUserDefaultChannelGroup":
			return row.Channel
		case "date":
			return row.Date
		case "minutesAgo":
			return "01"
		default:
			return d + "_sample"
		}
	}
	metVal := func(row gaSampleRow, m string) string {
		scale := 1.0
		if realtime {
			scale = 0.05
		}
		switch m {
		case "activeUsers", "totalUsers":
			v := int64(float64(row.ActiveUsers) * scale)
			if v == 0 && realtime {
				v = 12
			}
			return strconv.FormatInt(v, 10)
		case "sessions":
			return strconv.FormatInt(int64(float64(row.Sessions)*scale), 10)
		case "screenPageViews", "eventCount":
			v := int64(float64(row.ScreenPageViews) * scale)
			if v == 0 && realtime {
				v = 34
			}
			return strconv.FormatInt(v, 10)
		case "conversions", "keyEvents":
			return strconv.FormatInt(int64(float64(row.Conversions)*scale), 10)
		case "totalRevenue":
			return fmt.Sprintf("%.2f", row.Revenue*scale)
		default:
			return "0"
		}
	}

	var rows []*analyticsdata.Row
	for _, sample := range baseRows {
		if filterField != "" && filterVal != "" && dimVal(sample, filterField) != filterVal {
			continue
		}
		dVals := make([]*analyticsdata.DimensionValue, len(dims))
		for i, d := range dims {
			dVals[i] = &analyticsdata.DimensionValue{Value: dimVal(sample, d)}
		}
		mVals := make([]*analyticsdata.MetricValue, len(mets))
		for i, m := range mets {
			mVals[i] = &analyticsdata.MetricValue{Value: metVal(sample, m)}
		}
		rows = append(rows, &analyticsdata.Row{
			DimensionValues: dVals,
			MetricValues:    mVals,
		})
		if limit > 0 && int64(len(rows)) >= limit {
			break
		}
	}

	dHeaders := make([]*analyticsdata.DimensionHeader, len(dims))
	for i, d := range dims {
		dHeaders[i] = &analyticsdata.DimensionHeader{Name: d}
	}
	mHeaders := make([]*analyticsdata.MetricHeader, len(mets))
	for i, m := range mets {
		typ := "TYPE_INTEGER"
		if m == "totalRevenue" {
			typ = "TYPE_CURRENCY"
		}
		mHeaders[i] = &analyticsdata.MetricHeader{Name: m, Type: typ}
	}

	if realtime {
		writeJSON(w, http.StatusOK, &analyticsdata.RunRealtimeReportResponse{
			DimensionHeaders: dHeaders,
			MetricHeaders:    mHeaders,
			Rows:             rows,
			RowCount:         int64(len(rows)),
		})
		return
	}
	writeJSON(w, http.StatusOK, &analyticsdata.RunReportResponse{
		DimensionHeaders: dHeaders,
		MetricHeaders:    mHeaders,
		Rows:             rows,
		RowCount:         int64(len(rows)),
	})
}

// --- YouTube Data API v3 handlers ---

func (s *Server) handleYouTubeChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "GET required")
		return
	}
	q := r.URL.Query()
	mine := q.Get("mine") == "true"
	id := q.Get("id")

	s.mu.RLock()
	defer s.mu.RUnlock()
	var items []*youtube.Channel
	if mine {
		if ch, ok := s.channels[s.myChannelID]; ok {
			items = append(items, ch)
		}
	} else if id != "" {
		for _, cid := range strings.Split(id, ",") {
			if ch, ok := s.channels[strings.TrimSpace(cid)]; ok {
				items = append(items, ch)
			}
		}
	} else {
		keys := make([]string, 0, len(s.channels))
		for k := range s.channels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			items = append(items, s.channels[k])
		}
	}
	writeJSON(w, http.StatusOK, &youtube.ChannelListResponse{
		Kind:  "youtube#channelListResponse",
		Items: items,
	})
}

func (s *Server) handleYouTubeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "GET required")
		return
	}
	q := r.URL.Query()
	query := strings.ToLower(strings.TrimSpace(q.Get("q")))
	channelID := strings.TrimSpace(q.Get("channelId"))
	maxRes, _ := strconv.Atoi(q.Get("maxResults"))
	if maxRes <= 0 {
		maxRes = 25
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.videos))
	for k := range s.videos {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var items []*youtube.SearchResult
	for _, k := range keys {
		v := s.videos[k]
		if channelID != "" && v.Snippet != nil && v.Snippet.ChannelId != channelID {
			continue
		}
		if query != "" && v.Snippet != nil {
			hay := strings.ToLower(v.Snippet.Title + " " + v.Snippet.Description + " " + strings.Join(v.Snippet.Tags, " "))
			if !strings.Contains(hay, query) {
				continue
			}
		}
		items = append(items, &youtube.SearchResult{
			Kind:    "youtube#searchResult",
			Id:      &youtube.ResourceId{Kind: "youtube#video", VideoId: v.Id},
			Snippet: &youtube.SearchResultSnippet{Title: v.Snippet.Title, Description: v.Snippet.Description, ChannelId: v.Snippet.ChannelId, ChannelTitle: v.Snippet.ChannelTitle, PublishedAt: v.Snippet.PublishedAt},
		})
		if len(items) >= maxRes {
			break
		}
	}
	writeJSON(w, http.StatusOK, &youtube.SearchListResponse{
		Kind:  "youtube#searchListResponse",
		Items: items,
	})
}

func (s *Server) handleYouTubeVideoUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "POST required")
		return
	}
	ct := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		s.handleYouTubeVideosInsertJSON(w, r.Body)
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	var metaPart []byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "badRequest", err.Error())
			return
		}
		b, _ := io.ReadAll(part)
		if strings.Contains(part.Header.Get("Content-Type"), "application/json") || metaPart == nil {
			if strings.HasPrefix(strings.TrimSpace(string(b)), "{") {
				metaPart = b
			}
		}
	}
	if len(metaPart) == 0 {
		writeAPIError(w, http.StatusBadRequest, "badRequest", "missing video metadata part")
		return
	}
	s.createYouTubeVideoFromJSON(w, metaPart)
}

func (s *Server) handleYouTubeVideosInsertJSON(w http.ResponseWriter, body io.Reader) {
	raw, err := io.ReadAll(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "badRequest", err.Error())
		return
	}
	s.createYouTubeVideoFromJSON(w, raw)
}

func (s *Server) createYouTubeVideoFromJSON(w http.ResponseWriter, raw []byte) {
	var req youtube.Video
	if err := json.Unmarshal(raw, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalidJson", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID("vid")
	if req.Snippet == nil {
		req.Snippet = &youtube.VideoSnippet{}
	}
	req.Kind = "youtube#video"
	req.Id = id
	req.Snippet.ChannelId = s.myChannelID
	if ch, ok := s.channels[s.myChannelID]; ok && ch.Snippet != nil {
		req.Snippet.ChannelTitle = ch.Snippet.Title
	}
	req.Snippet.PublishedAt = "2026-11-14T22:00:00Z"
	if req.Status == nil {
		req.Status = &youtube.VideoStatus{PrivacyStatus: "private"}
	}
	if req.Status.PrivacyStatus == "" {
		req.Status.PrivacyStatus = "private"
	}
	req.Status.UploadStatus = "uploaded"
	req.ContentDetails = &youtube.VideoContentDetails{Duration: "PT0M30S"}
	req.Statistics = &youtube.VideoStatistics{ViewCount: 0, LikeCount: 0, CommentCount: 0}
	s.videos[id] = &req
	s.playlistItems["UU_ACME_DEV"] = append([]string{id}, s.playlistItems["UU_ACME_DEV"]...)
	writeJSON(w, http.StatusOK, &req)
}

func (s *Server) handleYouTubeVideos(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ids := strings.Split(r.URL.Query().Get("id"), ",")
		s.mu.RLock()
		defer s.mu.RUnlock()
		var items []*youtube.Video
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if v, ok := s.videos[id]; ok {
				items = append(items, v)
			}
		}
		writeJSON(w, http.StatusOK, &youtube.VideoListResponse{
			Kind:  "youtube#videoListResponse",
			Items: items,
		})
	case http.MethodPost:
		s.handleYouTubeVideosInsertJSON(w, r.Body)
	case http.MethodPut:
		var req youtube.Video
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalidJson", err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		v, ok := s.videos[req.Id]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "videoNotFound", "video not found: "+req.Id)
			return
		}
		if req.Snippet != nil {
			if req.Snippet.Title != "" {
				v.Snippet.Title = req.Snippet.Title
			}
			if req.Snippet.Description != "" {
				v.Snippet.Description = req.Snippet.Description
			}
			if len(req.Snippet.Tags) > 0 {
				v.Snippet.Tags = append([]string(nil), req.Snippet.Tags...)
			}
		}
		if req.Status != nil && req.Status.PrivacyStatus != "" {
			v.Status.PrivacyStatus = req.Status.PrivacyStatus
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.videos[id]; !ok {
			writeAPIError(w, http.StatusNotFound, "videoNotFound", "video not found: "+id)
			return
		}
		delete(s.videos, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
	}
}

func (s *Server) handleYouTubePlaylists(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		id := strings.TrimSpace(q.Get("id"))
		chID := strings.TrimSpace(q.Get("channelId"))
		if q.Get("mine") == "true" {
			chID = s.myChannelID
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		keys := make([]string, 0, len(s.playlists))
		for k := range s.playlists {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var items []*youtube.Playlist
		for _, k := range keys {
			pl := s.playlists[k]
			if id != "" && pl.Id != id {
				continue
			}
			if chID != "" && pl.Snippet != nil && pl.Snippet.ChannelId != chID {
				continue
			}
			pl.ContentDetails = &youtube.PlaylistContentDetails{ItemCount: int64(len(s.playlistItems[pl.Id]))}
			items = append(items, pl)
		}
		writeJSON(w, http.StatusOK, &youtube.PlaylistListResponse{
			Kind:  "youtube#playlistListResponse",
			Items: items,
		})
	case http.MethodPost:
		var req youtube.Playlist
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalidJson", err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		id := s.nextID("PL")
		if req.Snippet == nil {
			req.Snippet = &youtube.PlaylistSnippet{}
		}
		req.Kind = "youtube#playlist"
		req.Id = id
		req.Snippet.ChannelId = s.myChannelID
		req.Snippet.PublishedAt = "2026-11-14T22:00:00Z"
		if req.Status == nil {
			req.Status = &youtube.PlaylistStatus{PrivacyStatus: "private"}
		}
		req.ContentDetails = &youtube.PlaylistContentDetails{ItemCount: 0}
		s.playlists[id] = &req
		writeJSON(w, http.StatusOK, &req)
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.playlists[id]; !ok {
			writeAPIError(w, http.StatusNotFound, "playlistNotFound", "playlist not found: "+id)
			return
		}
		delete(s.playlists, id)
		delete(s.playlistItems, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
	}
}

func (s *Server) handleYouTubePlaylistItems(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		plID := strings.TrimSpace(r.URL.Query().Get("playlistId"))
		maxRes, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
		if maxRes <= 0 {
			maxRes = 50
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		vids := s.playlistItems[plID]
		var items []*youtube.PlaylistItem
		for idx, vid := range vids {
			if idx >= maxRes {
				break
			}
			title := vid
			if v, ok := s.videos[vid]; ok && v.Snippet != nil {
				title = v.Snippet.Title
			}
			items = append(items, &youtube.PlaylistItem{
				Kind: "youtube#playlistItem",
				Id:   fmt.Sprintf("pli_%s_%d", plID, idx),
				Snippet: &youtube.PlaylistItemSnippet{
					PlaylistId: plID,
					Title:      title,
					ResourceId: &youtube.ResourceId{Kind: "youtube#video", VideoId: vid},
				},
				ContentDetails: &youtube.PlaylistItemContentDetails{VideoId: vid},
			})
		}
		writeJSON(w, http.StatusOK, &youtube.PlaylistItemListResponse{
			Kind:  "youtube#playlistItemListResponse",
			Items: items,
		})
	case http.MethodPost:
		var req youtube.PlaylistItem
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalidJson", err.Error())
			return
		}
		if req.Snippet == nil || req.Snippet.ResourceId == nil {
			writeAPIError(w, http.StatusBadRequest, "invalidRequest", "playlistId and videoId required")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		plID := req.Snippet.PlaylistId
		vid := req.Snippet.ResourceId.VideoId
		if _, ok := s.playlists[plID]; !ok {
			writeAPIError(w, http.StatusNotFound, "playlistNotFound", "playlist not found: "+plID)
			return
		}
		if _, ok := s.videos[vid]; !ok {
			writeAPIError(w, http.StatusNotFound, "videoNotFound", "video not found: "+vid)
			return
		}
		s.playlistItems[plID] = append(s.playlistItems[plID], vid)
		req.Id = s.nextID("pli")
		req.Kind = "youtube#playlistItem"
		writeJSON(w, http.StatusOK, &req)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
	}
}

func (s *Server) handleYouTubeComments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		vid := strings.TrimSpace(r.URL.Query().Get("videoId"))
		maxRes, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
		if maxRes <= 0 {
			maxRes = 20
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		list := s.comments[vid]
		if len(list) > maxRes {
			list = list[:maxRes]
		}
		writeJSON(w, http.StatusOK, &youtube.CommentThreadListResponse{
			Kind:  "youtube#commentThreadListResponse",
			Items: list,
		})
	case http.MethodPost:
		var req youtube.CommentThread
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalidJson", err.Error())
			return
		}
		if req.Snippet == nil || req.Snippet.TopLevelComment == nil || req.Snippet.TopLevelComment.Snippet == nil {
			writeAPIError(w, http.StatusBadRequest, "invalidRequest", "videoId and comment text required")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		vid := req.Snippet.VideoId
		if _, ok := s.videos[vid]; !ok {
			writeAPIError(w, http.StatusNotFound, "videoNotFound", "video not found: "+vid)
			return
		}
		id := s.nextID("ct")
		req.Id = id
		req.Kind = "youtube#commentThread"
		req.Snippet.TopLevelComment.Id = "c_" + id
		req.Snippet.TopLevelComment.Snippet.AuthorDisplayName = "Acme Engineering"
		req.Snippet.TopLevelComment.Snippet.PublishedAt = "2026-11-14T22:05:00Z"
		s.comments[vid] = append(s.comments[vid], &req)
		writeJSON(w, http.StatusOK, &req)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", r.Method)
	}
}

// --- YouTube Analytics v2 handler ---

func (s *Server) handleYouTubeAnalyticsReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "GET required")
		return
	}
	q := r.URL.Query()
	dims := splitCSV(q.Get("dimensions"))
	mets := splitCSV(q.Get("metrics"))
	filters := q.Get("filters")
	videoFilter := ""
	if strings.HasPrefix(filters, "video==") {
		videoFilter = strings.TrimPrefix(filters, "video==")
	}
	startDate := q.Get("startDate")
	endDate := q.Get("endDate")

	s.mu.RLock()
	defer s.mu.RUnlock()

	headers := make([]*youtubeanalytics.ResultTableColumnHeader, 0, len(dims)+len(mets))
	for _, d := range dims {
		headers = append(headers, &youtubeanalytics.ResultTableColumnHeader{
			Name:       d,
			ColumnType: "DIMENSION",
			DataType:   "STRING",
		})
	}
	for _, m := range mets {
		headers = append(headers, &youtubeanalytics.ResultTableColumnHeader{
			Name:       m,
			ColumnType: "METRIC",
			DataType:   "INTEGER",
		})
	}

	var rows [][]any
	for _, row := range s.ytAnalyticsRows {
		if videoFilter != "" && row.VideoID != videoFilter {
			continue
		}
		if startDate != "" && row.Day < startDate {
			continue
		}
		if endDate != "" && row.Day > endDate {
			continue
		}
		vals := make([]any, 0, len(dims)+len(mets))
		for _, d := range dims {
			switch d {
			case "day":
				vals = append(vals, row.Day)
			case "video":
				vals = append(vals, row.VideoID)
			case "country":
				vals = append(vals, row.Country)
			default:
				vals = append(vals, "unknown")
			}
		}
		for _, m := range mets {
			switch m {
			case "views":
				vals = append(vals, row.Views)
			case "estimatedMinutesWatched":
				vals = append(vals, row.EstimatedMinutesWatched)
			case "averageViewDuration":
				vals = append(vals, row.AverageViewDuration)
			case "likes":
				vals = append(vals, row.Likes)
			case "comments":
				vals = append(vals, row.Comments)
			case "subscribersGained":
				vals = append(vals, row.SubscribersGained)
			default:
				vals = append(vals, int64(0))
			}
		}
		rows = append(rows, vals)
	}

	writeJSON(w, http.StatusOK, &youtubeanalytics.QueryResponse{
		Kind:          "youtubeAnalytics#resultTable",
		ColumnHeaders: headers,
		Rows:          rows,
	})
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// --- Gmail API v1 handlers ---

func (s *Server) handleGmail(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/gmail/v1/")
	switch {
	case rest == "users/me/profile" && r.Method == http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		threads := make(map[string]struct{})
		for _, m := range s.gmailMessages {
			threads[m.ThreadId] = struct{}{}
		}
		writeJSON(w, http.StatusOK, &gmail.Profile{
			EmailAddress:  s.gmailEmail,
			MessagesTotal: int64(len(s.gmailMessages)),
			ThreadsTotal:  int64(len(threads)),
			HistoryId:     900100,
		})

	case rest == "users/me/labels" && r.Method == http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		keys := make([]string, 0, len(s.gmailLabels))
		for k := range s.gmailLabels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		labels := make([]*gmail.Label, 0, len(keys))
		for _, k := range keys {
			labels = append(labels, s.gmailLabels[k])
		}
		writeJSON(w, http.StatusOK, &gmail.ListLabelsResponse{Labels: labels})

	case rest == "users/me/messages" && r.Method == http.MethodGet:
		s.handleGmailListMessages(w, r)

	case rest == "users/me/messages/send" && r.Method == http.MethodPost:
		s.handleGmailSendMessage(w, r)

	case strings.HasPrefix(rest, "users/me/messages/"):
		sub := strings.TrimPrefix(rest, "users/me/messages/")
		if idx := strings.Index(sub, "/attachments/"); idx > 0 && r.Method == http.MethodGet {
			msgID := sub[:idx]
			attID := sub[idx+len("/attachments/"):]
			s.mu.RLock()
			data, ok := s.gmailAttachments[msgID+"/"+attID]
			s.mu.RUnlock()
			if !ok {
				writeAPIError(w, http.StatusNotFound, "notFound", "attachment not found: "+attID)
				return
			}
			writeJSON(w, http.StatusOK, &gmail.MessagePartBody{
				AttachmentId: attID,
				Size:         int64(len(data)),
				Data:         base64.RawURLEncoding.EncodeToString(data),
			})
			return
		}
		if strings.HasSuffix(sub, "/modify") && r.Method == http.MethodPost {
			id := strings.TrimSuffix(sub, "/modify")
			s.handleGmailModifyMessage(w, r, id)
			return
		}
		if strings.HasSuffix(sub, "/trash") && r.Method == http.MethodPost {
			id := strings.TrimSuffix(sub, "/trash")
			s.handleGmailTrashMessage(w, id)
			return
		}
		id := sub
		switch r.Method {
		case http.MethodGet:
			s.mu.RLock()
			msg, ok := s.gmailMessages[id]
			s.mu.RUnlock()
			if !ok {
				writeAPIError(w, http.StatusNotFound, "notFound", "message not found: "+id)
				return
			}
			writeJSON(w, http.StatusOK, msg)
		case http.MethodDelete:
			s.mu.Lock()
			_, ok := s.gmailMessages[id]
			if ok {
				delete(s.gmailMessages, id)
			}
			s.mu.Unlock()
			if !ok {
				writeAPIError(w, http.StatusNotFound, "notFound", "message not found: "+id)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeAPIError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "unsupported method on message")
		}

	case strings.HasPrefix(rest, "users/me/threads/") && r.Method == http.MethodGet:
		tid := strings.TrimPrefix(rest, "users/me/threads/")
		s.mu.RLock()
		defer s.mu.RUnlock()
		var msgs []*gmail.Message
		for _, id := range s.gmailOrder {
			if m, ok := s.gmailMessages[id]; ok && m.ThreadId == tid {
				msgs = append(msgs, m)
			}
		}
		if len(msgs) == 0 {
			writeAPIError(w, http.StatusNotFound, "notFound", "thread not found: "+tid)
			return
		}
		writeJSON(w, http.StatusOK, &gmail.Thread{
			Id:       tid,
			Messages: msgs,
		})

	default:
		writeAPIError(w, http.StatusNotFound, "notFound", "unhandled fakegoogle gmail route: "+r.Method+" "+r.URL.Path)
	}
}

func (s *Server) handleGmailListMessages(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	maxResults := int64(25)
	if mr, err := strconv.ParseInt(r.URL.Query().Get("maxResults"), 10, 64); err == nil && mr > 0 {
		maxResults = mr
	}
	labelFilter := r.URL.Query()["labelIds"]

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*gmail.Message
	for _, id := range s.gmailOrder {
		if int64(len(items)) >= maxResults {
			break
		}
		msg, ok := s.gmailMessages[id]
		if !ok {
			continue
		}
		if len(labelFilter) > 0 && !msgHasAllLabels(msg, labelFilter) {
			continue
		}
		if q != "" && !gmailMatchesQuery(msg, q) {
			continue
		}
		items = append(items, &gmail.Message{
			Id:       msg.Id,
			ThreadId: msg.ThreadId,
		})
	}
	writeJSON(w, http.StatusOK, &gmail.ListMessagesResponse{
		Messages:           items,
		ResultSizeEstimate: int64(len(items)),
	})
}

func msgHasAllLabels(m *gmail.Message, wanted []string) bool {
	for _, w := range wanted {
		if !msgHasLabel(m, w) {
			return false
		}
	}
	return true
}

func msgHasLabel(m *gmail.Message, want string) bool {
	for _, l := range m.LabelIds {
		if strings.EqualFold(l, want) {
			return true
		}
	}
	return false
}

func msgHeader(m *gmail.Message, name string) string {
	if m.Payload == nil {
		return ""
	}
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func msgPlainText(m *gmail.Message) string {
	if m.Payload == nil {
		return m.Snippet
	}
	if m.Payload.Body != nil && m.Payload.Body.Data != "" {
		if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(m.Payload.Body.Data, "=")); err == nil {
			return string(b)
		}
	}
	for _, p := range m.Payload.Parts {
		if p.Body != nil && p.Body.Data != "" {
			if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(p.Body.Data, "=")); err == nil {
				return string(b)
			}
		}
	}
	return m.Snippet
}

func gmailMatchesQuery(m *gmail.Message, q string) bool {
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return true
	}
	subj := strings.ToLower(msgHeader(m, "Subject"))
	from := strings.ToLower(msgHeader(m, "From"))
	to := strings.ToLower(msgHeader(m, "To"))
	body := strings.ToLower(msgPlainText(m))
	snip := strings.ToLower(m.Snippet)

	for _, tok := range tokens {
		lt := strings.ToLower(strings.TrimSpace(tok))
		switch {
		case lt == "is:unread":
			if !msgHasLabel(m, "UNREAD") {
				return false
			}
		case lt == "is:read":
			if msgHasLabel(m, "UNREAD") {
				return false
			}
		case lt == "is:starred":
			if !msgHasLabel(m, "STARRED") {
				return false
			}
		case strings.HasPrefix(lt, "from:"):
			v := strings.Trim(strings.TrimPrefix(lt, "from:"), `"'`)
			if !strings.Contains(from, v) {
				return false
			}
		case strings.HasPrefix(lt, "to:"):
			v := strings.Trim(strings.TrimPrefix(lt, "to:"), `"'`)
			if !strings.Contains(to, v) {
				return false
			}
		case strings.HasPrefix(lt, "subject:"):
			v := strings.Trim(strings.TrimPrefix(lt, "subject:"), `"'`)
			if !strings.Contains(subj, v) {
				return false
			}
		case strings.HasPrefix(lt, "label:"):
			v := strings.TrimPrefix(lt, "label:")
			if !msgHasLabel(m, v) {
				return false
			}
		case strings.HasPrefix(lt, "in:"):
			v := strings.TrimPrefix(lt, "in:")
			if !msgHasLabel(m, v) {
				return false
			}
		default:
			if !strings.Contains(subj, lt) && !strings.Contains(from, lt) && !strings.Contains(body, lt) && !strings.Contains(snip, lt) {
				return false
			}
		}
	}
	return true
}

func (s *Server) handleGmailSendMessage(w http.ResponseWriter, r *http.Request) {
	var in gmail.Message
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalidArgument", "invalid message JSON")
		return
	}
	rawBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(in.Raw, "="))
	if err != nil {
		if b2, err2 := base64.URLEncoding.DecodeString(in.Raw); err2 == nil {
			rawBytes = b2
		} else {
			writeAPIError(w, http.StatusBadRequest, "invalidArgument", "invalid base64url raw field")
			return
		}
	}
	toAddr, subj, bodyTxt := parseSimpleRFC2822(string(rawBytes))

	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID("msg")
	tid := defaultStr(in.ThreadId, "thr_"+id)
	snip := bodyTxt
	if len(snip) > 100 {
		snip = snip[:100]
	}
	msg := &gmail.Message{
		Id:       id,
		ThreadId: tid,
		LabelIds: []string{"SENT"},
		Snippet:  snip,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: s.gmailEmail},
				{Name: "To", Value: toAddr},
				{Name: "Subject", Value: subj},
				{Name: "Date", Value: "Mon, 23 Nov 2026 15:00:00 -0600"},
			},
			Body: &gmail.MessagePartBody{
				Data: base64.RawURLEncoding.EncodeToString([]byte(bodyTxt)),
			},
		},
	}
	s.gmailMessages[id] = msg
	s.gmailOrder = append(s.gmailOrder, id)
	writeJSON(w, http.StatusOK, msg)
}

func parseSimpleRFC2822(raw string) (toAddr, subject, body string) {
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	parts := strings.SplitN(norm, "\n\n", 2)
	hdrBlock := parts[0]
	if len(parts) == 2 {
		body = parts[1]
	}
	for _, line := range strings.Split(hdrBlock, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "to":
				toAddr = strings.TrimSpace(v)
			case "subject":
				subject = strings.TrimSpace(v)
			}
		}
	}
	return toAddr, subject, strings.TrimSpace(body)
}

func (s *Server) handleGmailModifyMessage(w http.ResponseWriter, r *http.Request, id string) {
	var req gmail.ModifyMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalidArgument", "invalid modify JSON")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.gmailMessages[id]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound", "message not found: "+id)
		return
	}
	removeSet := make(map[string]bool, len(req.RemoveLabelIds))
	for _, l := range req.RemoveLabelIds {
		removeSet[l] = true
	}
	var updated []string
	seen := make(map[string]bool)
	for _, l := range msg.LabelIds {
		if !removeSet[l] && !seen[l] {
			updated = append(updated, l)
			seen[l] = true
		}
	}
	for _, l := range req.AddLabelIds {
		if !seen[l] {
			updated = append(updated, l)
			seen[l] = true
		}
	}
	msg.LabelIds = updated
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleGmailTrashMessage(w http.ResponseWriter, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.gmailMessages[id]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound", "message not found: "+id)
		return
	}
	var updated []string
	hasTrash := false
	for _, l := range msg.LabelIds {
		if l == "INBOX" {
			continue
		}
		if l == "TRASH" {
			hasTrash = true
		}
		updated = append(updated, l)
	}
	if !hasTrash {
		updated = append(updated, "TRASH")
	}
	msg.LabelIds = updated
	writeJSON(w, http.StatusOK, msg)
}
