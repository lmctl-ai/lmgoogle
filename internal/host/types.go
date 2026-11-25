// SPDX-License-Identifier: MIT

package host

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
)

// GaAccount wraps a Google Analytics 4 account snapshot.
type GaAccount struct {
	NameVal        string
	AccountIDVal   string
	DisplayNameVal string
	RegionCodeVal  string
	DeletedVal     bool
}

// GaProperty wraps a Google Analytics 4 property snapshot.
type GaProperty struct {
	NameVal             string
	PropertyIDVal       string
	DisplayNameVal      string
	PropertyTypeVal     string
	TimeZoneVal         string
	CurrencyCodeVal     string
	IndustryCategoryVal string
	ServiceLevelVal     string
	ParentVal           string
}

// GaDataStream wraps a Google Analytics 4 data stream snapshot.
type GaDataStream struct {
	NameVal          string
	StreamIDVal      string
	TypeVal          string
	DisplayNameVal   string
	MeasurementIDVal string
	DefaultURIVal    string
}

// GaCustomDimension wraps a Google Analytics 4 custom dimension snapshot.
type GaCustomDimension struct {
	NameVal          string
	ParameterNameVal string
	DisplayNameVal   string
	ScopeVal         string
	DescriptionVal   string
}

// GaReport wraps a GA4 Data API RunReport or RunRealtimeReport result.
type GaReport struct {
	DimensionHeadersVal []string
	MetricHeadersVal    []string
	DimensionRowsVal    [][]string
	MetricRowsVal       [][]string
}

func (r *GaReport) RowCount() int64 {
	if r == nil {
		return 0
	}
	return int64(len(r.DimensionRowsVal))
}

func (r *GaReport) DimensionValue(rowIdx, dimIdx int64) string {
	if r == nil || rowIdx < 0 || int(rowIdx) >= len(r.DimensionRowsVal) {
		return ""
	}
	row := r.DimensionRowsVal[rowIdx]
	if dimIdx < 0 || int(dimIdx) >= len(row) {
		return ""
	}
	return row[dimIdx]
}

func (r *GaReport) MetricValue(rowIdx, metIdx int64) string {
	if r == nil || rowIdx < 0 || int(rowIdx) >= len(r.MetricRowsVal) {
		return ""
	}
	row := r.MetricRowsVal[rowIdx]
	if metIdx < 0 || int(metIdx) >= len(row) {
		return ""
	}
	return row[metIdx]
}

func (r *GaReport) MetricFloat(rowIdx, metIdx int64) float64 {
	s := strings.TrimSpace(r.MetricValue(rowIdx, metIdx))
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func (r *GaReport) MetricInt(rowIdx, metIdx int64) int64 {
	s := strings.TrimSpace(r.MetricValue(rowIdx, metIdx))
	if s == "" {
		return 0
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

func (r *GaReport) MetricSum(metIdx int64) float64 {
	if r == nil {
		return 0
	}
	var sum float64
	for i := range r.MetricRowsVal {
		sum += r.MetricFloat(int64(i), metIdx)
	}
	return sum
}

func (r *GaReport) RowMap(rowIdx int64) map[string]string {
	out := make(map[string]string)
	if r == nil || rowIdx < 0 || int(rowIdx) >= len(r.DimensionRowsVal) {
		return out
	}
	for i, h := range r.DimensionHeadersVal {
		out[h] = r.DimensionValue(rowIdx, int64(i))
	}
	for i, h := range r.MetricHeadersVal {
		out[h] = r.MetricValue(rowIdx, int64(i))
	}
	return out
}

func (r *GaReport) ToCSV() string {
	if r == nil {
		return ""
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	header := make([]string, 0, len(r.DimensionHeadersVal)+len(r.MetricHeadersVal))
	header = append(header, r.DimensionHeadersVal...)
	header = append(header, r.MetricHeadersVal...)
	if len(header) > 0 {
		_ = w.Write(header)
	}
	for i := range r.DimensionRowsVal {
		row := make([]string, 0, len(header))
		row = append(row, r.DimensionRowsVal[i]...)
		if i < len(r.MetricRowsVal) {
			row = append(row, r.MetricRowsVal[i]...)
		}
		_ = w.Write(row)
	}
	w.Flush()
	return buf.String()
}

func (r *GaReport) ToJSONString() string {
	if r == nil {
		return `{"dimensions":[],"metrics":[],"rows":[]}`
	}
	rows := make([]map[string]string, len(r.DimensionRowsVal))
	for i := range r.DimensionRowsVal {
		rows[i] = r.RowMap(int64(i))
	}
	payload := map[string]any{
		"dimensions": r.DimensionHeadersVal,
		"metrics":    r.MetricHeadersVal,
		"row_count":  len(rows),
		"rows":       rows,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// YtChannel wraps a YouTube Data API v3 channel snapshot.
type YtChannel struct {
	IDVal                string
	TitleVal             string
	DescriptionVal       string
	CustomURLVal         string
	CountryVal           string
	SubscriberCountVal   int64
	ViewCountVal         int64
	VideoCountVal        int64
	UploadsPlaylistIDVal string
}

// YtVideo wraps a YouTube Data API v3 video snapshot.
type YtVideo struct {
	IDVal            string
	TitleVal         string
	DescriptionVal   string
	ChannelIDVal     string
	ChannelTitleVal  string
	PublishedAtVal   string
	PrivacyStatusVal string
	CategoryIDVal    string
	DurationVal      string
	TagsVal          []string
	ViewCountVal     int64
	LikeCountVal     int64
	CommentCountVal  int64
}

// YtPlaylist wraps a YouTube Data API v3 playlist snapshot.
type YtPlaylist struct {
	IDVal            string
	TitleVal         string
	DescriptionVal   string
	ChannelIDVal     string
	PrivacyStatusVal string
	ItemCountVal     int64
}

// YtAnalyticsReport wraps a YouTube Analytics API v2 query result table.
type YtAnalyticsReport struct {
	ColumnHeadersVal []string
	RowsVal          [][]string
}

func (r *YtAnalyticsReport) RowCount() int64 {
	if r == nil {
		return 0
	}
	return int64(len(r.RowsVal))
}

func (r *YtAnalyticsReport) CellStr(rowIdx, colIdx int64) string {
	if r == nil || rowIdx < 0 || int(rowIdx) >= len(r.RowsVal) {
		return ""
	}
	row := r.RowsVal[rowIdx]
	if colIdx < 0 || int(colIdx) >= len(row) {
		return ""
	}
	return row[colIdx]
}

func (r *YtAnalyticsReport) CellFloat(rowIdx, colIdx int64) float64 {
	s := strings.TrimSpace(r.CellStr(rowIdx, colIdx))
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func (r *YtAnalyticsReport) CellInt(rowIdx, colIdx int64) int64 {
	s := strings.TrimSpace(r.CellStr(rowIdx, colIdx))
	if s == "" {
		return 0
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

func (r *YtAnalyticsReport) ColSum(colIdx int64) float64 {
	if r == nil {
		return 0
	}
	var sum float64
	for i := range r.RowsVal {
		sum += r.CellFloat(int64(i), colIdx)
	}
	return sum
}

func (r *YtAnalyticsReport) RowMap(rowIdx int64) map[string]string {
	out := make(map[string]string)
	if r == nil || rowIdx < 0 || int(rowIdx) >= len(r.RowsVal) {
		return out
	}
	for i, h := range r.ColumnHeadersVal {
		out[h] = r.CellStr(rowIdx, int64(i))
	}
	return out
}

func (r *YtAnalyticsReport) ToCSV() string {
	if r == nil {
		return ""
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if len(r.ColumnHeadersVal) > 0 {
		_ = w.Write(r.ColumnHeadersVal)
	}
	for _, row := range r.RowsVal {
		_ = w.Write(row)
	}
	w.Flush()
	return buf.String()
}

func (r *YtAnalyticsReport) ToJSONString() string {
	if r == nil {
		return `{"columns":[],"rows":[]}`
	}
	rows := make([]map[string]string, len(r.RowsVal))
	for i := range r.RowsVal {
		rows[i] = r.RowMap(int64(i))
	}
	payload := map[string]any{
		"columns":   r.ColumnHeadersVal,
		"row_count": len(rows),
		"rows":      rows,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// GmailProfile wraps a Gmail API v1 mailbox profile snapshot.
type GmailProfile struct {
	EmailAddressVal  string
	MessagesTotalVal int64
	ThreadsTotalVal  int64
	HistoryIDVal     string
}

// GmailMessage wraps a decoded Gmail API v1 message snapshot.
type GmailMessage struct {
	IDVal              string
	ThreadIDVal        string
	SubjectVal         string
	FromVal            string
	ToVal              string
	DateVal            string
	DateISOVal         string
	TimestampSlugVal   string
	SnippetVal         string
	BodyTextVal        string
	LabelIDsVal        []string
	HeadersVal         map[string]string
	AttachmentNamesVal []string
	AttachmentIDsVal   []string
	AttachmentMimesVal []string
}

func (m *GmailMessage) AttachmentCount() int64 {
	if m == nil {
		return 0
	}
	return int64(len(m.AttachmentNamesVal))
}

func (m *GmailMessage) HasLabel(label string) bool {
	if m == nil {
		return false
	}
	for _, l := range m.LabelIDsVal {
		if strings.EqualFold(l, label) {
			return true
		}
	}
	return false
}

func (m *GmailMessage) Header(name string) string {
	if m == nil || len(m.HeadersVal) == 0 {
		return ""
	}
	for k, v := range m.HeadersVal {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func (m *GmailMessage) ToJSONString() string {
	if m == nil {
		return `{}`
	}
	b, _ := json.Marshal(map[string]any{
		"id":               m.IDVal,
		"thread_id":        m.ThreadIDVal,
		"subject":          m.SubjectVal,
		"from":             m.FromVal,
		"to":               m.ToVal,
		"date":             m.DateVal,
		"date_iso":         m.DateISOVal,
		"timestamp_slug":   m.TimestampSlugVal,
		"snippet":          m.SnippetVal,
		"body_text":        m.BodyTextVal,
		"label_ids":        m.LabelIDsVal,
		"attachment_names": m.AttachmentNamesVal,
		"attachment_ids":   m.AttachmentIDsVal,
		"attachment_mimes": m.AttachmentMimesVal,
	})
	return string(b)
}
