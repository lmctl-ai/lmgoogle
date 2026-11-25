// SPDX-License-Identifier: MIT

package host

import (
	"github.com/lmctl-ai/lmcode-go"
	"github.com/lmctl-ai/lmgoogle/internal/membudget"
	"github.com/lmctl-ai/lmgoogle/internal/safety"
)

// Register installs the lmgoogle context, testing helpers, and all Google Analytics & YouTube methods on reg.
func Register(reg *lmcode.Registry, h *Host) error {
	if err := lmcode.RegisterTesting(reg, nil); err != nil {
		return err
	}
	if err := reg.Context("google", "Google"); err != nil {
		return err
	}

	m := func(typeName, name string, params []string, ret string, fn lmcode.Method) error {
		return reg.Method(typeName, name, params, ret, fn)
	}
	mSnapshot := func(typeName, name string, params []string, ret string, fn lmcode.Method) error {
		return reg.Method(typeName, name, params, ret, fn, lmcode.Dry(fn))
	}
	mLocal := func(typeName, name string, params []string, ret string, fn lmcode.Method) error {
		return reg.Method(typeName, name, params, ret, fn, lmcode.Dry(fn))
	}
	mSelf := func(typeName, name string, params []string, ret string, fn lmcode.Method) error {
		preview := lmcode.Method(func(self any, _ []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Object(typeName, self), nil
		})
		return reg.Method(typeName, name, params, ret, fn, lmcode.Dry(preview))
	}
	mRead := func(typeName, name string, params []string, ret string, fn lmcode.Method) error {
		dryFn := fn
		if zeroHostObject(ret) != nil {
			dryFn = func(self any, args []lmcode.Value) (lmcode.Value, error) {
				value, err := fn(self, args)
				if err != nil {
					return value, err
				}
				if value.Kind == lmcode.KindObj && value.Obj == nil {
					return lmcode.Object(ret, zeroHostObject(ret)), nil
				}
				return value, nil
			}
		}
		return reg.Method(typeName, name, params, ret, fn, lmcode.Dry(dryFn))
	}
	mPreview := func(typeName, name string, params []string, ret string, fn lmcode.Method) error {
		preview := lmcode.Method(func(any, []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Object(ret, zeroHostObject(ret)), nil
		})
		return reg.Method(typeName, name, params, ret, fn, lmcode.Dry(preview))
	}

	// --- Core & Safety on Google ---
	if err := mRead("Google", "is_live", nil, "bool", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).IsLive()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "auth_mode", nil, "string", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(self.(*Host).AuthMode()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "endpoint", nil, "string", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(self.(*Host).Endpoint()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "run_id", nil, "string", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(self.(*Host).RunID()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "readonly_mode", nil, "bool", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).ReadOnlyMode()), nil
	}); err != nil {
		return err
	}
	if err := mSelf("Google", "set_readonly_mode", []string{"bool"}, "Google", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		hs := self.(*Host)
		hs.SetReadOnlyMode(a[0].Bool)
		return lmcode.Obj("Google", hs), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "last_error", nil, "string", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(self.(*Host).LastError()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "redact", []string{"string"}, "string", func(_ any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(safety.RedactSecrets(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ledger_count", nil, "int", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(self.(*Host).LedgerCount()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ledger_entries", nil, "StrList", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).LedgerEntries())
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ledger_owns", []string{"string", "string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).LedgerOwns(a[0].Str, a[1].Str)), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "cleanup_ledger", nil, "int", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(self.(*Host).CleanupLedger()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "quota_used", nil, "int", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(self.(*Host).QuotaUsed()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "quota_limit", nil, "int", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(self.(*Host).QuotaLimit()), nil
	}); err != nil {
		return err
	}
	if err := mLocal("Google", "set_quota_limit", []string{"int"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).SetQuotaLimit(a[0].Int)), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "alloc_scratch_mb", []string{"int"}, "bool", func(_ any, a []lmcode.Value) (lmcode.Value, error) {
		if a[0].Int <= 0 {
			return lmcode.Bool(true), nil
		}
		if err := membudget.Charge(a[0].Int * 1024 * 1024); err != nil {
			return lmcode.None(), err
		}
		return lmcode.Bool(true), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "inventory_summary", nil, "StrMap", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrMap(reg, self.(*Host).InventorySummary())
	}); err != nil {
		return err
	}

	// --- 1. Google Analytics Admin API v1beta ---
	if err := mRead("Google", "ga_list_account_summaries", nil, "StrList", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GaListAccountSummaries())
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_list_accounts", nil, "StrList", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GaListAccounts())
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_get_account", []string{"string"}, "GaAccount", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaAccount", self.(*Host).GaGetAccount(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_list_properties", []string{"string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GaListProperties(a[0].Str))
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_get_property", []string{"string"}, "GaProperty", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaProperty", self.(*Host).GaGetProperty(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "ga_create_property", []string{"string", "string", "string", "string"}, "GaProperty", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaProperty", self.(*Host).GaCreateProperty(a[0].Str, a[1].Str, a[2].Str, a[3].Str)), nil
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "ga_update_property", []string{"string", "string", "string", "string"}, "GaProperty", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaProperty", self.(*Host).GaUpdateProperty(a[0].Str, a[1].Str, a[2].Str, a[3].Str)), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "ga_delete_property", []string{"string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).GaDeleteProperty(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_list_data_streams", []string{"string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GaListDataStreams(a[0].Str))
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_get_data_stream", []string{"string"}, "GaDataStream", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaDataStream", self.(*Host).GaGetDataStream(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "ga_create_web_stream", []string{"string", "string", "string"}, "GaDataStream", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaDataStream", self.(*Host).GaCreateWebStream(a[0].Str, a[1].Str, a[2].Str)), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "ga_delete_data_stream", []string{"string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).GaDeleteDataStream(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_list_custom_dimensions", []string{"string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GaListCustomDimensions(a[0].Str))
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "ga_create_custom_dimension", []string{"string", "string", "string", "string"}, "GaCustomDimension", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaCustomDimension", self.(*Host).GaCreateCustomDimension(a[0].Str, a[1].Str, a[2].Str, a[3].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_list_key_events", []string{"string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GaListKeyEvents(a[0].Str))
	}); err != nil {
		return err
	}
	if err := m("Google", "ga_create_key_event", []string{"string", "string", "string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).GaCreateKeyEvent(a[0].Str, a[1].Str, a[2].Str)), nil
	}); err != nil {
		return err
	}

	// GaAccount methods
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaAccount).NameVal), nil }},
		{"account_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaAccount).AccountIDVal), nil }},
		{"display_name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaAccount).DisplayNameVal), nil }},
		{"region_code", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaAccount).RegionCodeVal), nil }},
		{"deleted", "bool", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Bool(s.(*GaAccount).DeletedVal), nil }},
	} {
		if err := mSnapshot("GaAccount", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// GaProperty methods
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).NameVal), nil }},
		{"property_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).PropertyIDVal), nil }},
		{"display_name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).DisplayNameVal), nil }},
		{"property_type", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).PropertyTypeVal), nil }},
		{"time_zone", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).TimeZoneVal), nil }},
		{"currency_code", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).CurrencyCodeVal), nil }},
		{"industry_category", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Str(s.(*GaProperty).IndustryCategoryVal), nil
		}},
		{"service_level", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).ServiceLevelVal), nil }},
		{"parent", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaProperty).ParentVal), nil }},
	} {
		if err := mSnapshot("GaProperty", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// GaDataStream methods
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaDataStream).NameVal), nil }},
		{"stream_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaDataStream).StreamIDVal), nil }},
		{"type", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaDataStream).TypeVal), nil }},
		{"display_name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaDataStream).DisplayNameVal), nil }},
		{"measurement_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Str(s.(*GaDataStream).MeasurementIDVal), nil
		}},
		{"default_uri", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaDataStream).DefaultURIVal), nil }},
	} {
		if err := mSnapshot("GaDataStream", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// GaCustomDimension methods
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaCustomDimension).NameVal), nil }},
		{"parameter_name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Str(s.(*GaCustomDimension).ParameterNameVal), nil
		}},
		{"display_name", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Str(s.(*GaCustomDimension).DisplayNameVal), nil
		}},
		{"scope", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GaCustomDimension).ScopeVal), nil }},
		{"description", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Str(s.(*GaCustomDimension).DescriptionVal), nil
		}},
	} {
		if err := mSnapshot("GaCustomDimension", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// --- 2. Google Analytics Data API v1beta ---
	if err := mRead("Google", "ga_run_report", []string{"string", "string", "string", "StrList", "StrList"}, "GaReport", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaReport", self.(*Host).GaRunReport(a[0].Str, a[1].Str, a[2].Str, extractStrList(a[3]), extractStrList(a[4]), "", "", 0)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_run_report_filtered", []string{"string", "string", "string", "StrList", "StrList", "string", "string", "int"}, "GaReport", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaReport", self.(*Host).GaRunReport(a[0].Str, a[1].Str, a[2].Str, extractStrList(a[3]), extractStrList(a[4]), a[5].Str, a[6].Str, a[7].Int)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_run_realtime_report", []string{"string", "StrList", "StrList", "int"}, "GaReport", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GaReport", self.(*Host).GaRunRealtimeReport(a[0].Str, extractStrList(a[1]), extractStrList(a[2]), a[3].Int)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "ga_get_metadata", []string{"string"}, "StrMap", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrMap(reg, self.(*Host).GaGetMetadata(a[0].Str))
	}); err != nil {
		return err
	}

	// GaReport methods
	if err := mSnapshot("GaReport", "row_count", nil, "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(s.(*GaReport).RowCount()), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "dimension_headers", nil, "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, s.(*GaReport).DimensionHeadersVal)
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "metric_headers", nil, "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, s.(*GaReport).MetricHeadersVal)
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "dimension_value", []string{"int", "int"}, "string", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(s.(*GaReport).DimensionValue(a[0].Int, a[1].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "metric_value", []string{"int", "int"}, "string", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(s.(*GaReport).MetricValue(a[0].Int, a[1].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "metric_float", []string{"int", "int"}, "float", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Float(s.(*GaReport).MetricFloat(a[0].Int, a[1].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "metric_int", []string{"int", "int"}, "int", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(s.(*GaReport).MetricInt(a[0].Int, a[1].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "metric_sum", []string{"int"}, "float", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Float(s.(*GaReport).MetricSum(a[0].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "row_map", []string{"int"}, "StrMap", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrMap(reg, s.(*GaReport).RowMap(a[0].Int))
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "to_csv", nil, "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(s.(*GaReport).ToCSV()), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GaReport", "to_json", nil, "Json", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeJson(reg, s.(*GaReport).ToJSONString())
	}); err != nil {
		return err
	}

	// --- 3. YouTube Data API v3 ---
	if err := mRead("Google", "yt_my_channel", nil, "YtChannel", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtChannel", self.(*Host).YtMyChannel()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_get_channel", []string{"string"}, "YtChannel", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtChannel", self.(*Host).YtGetChannel(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_list_channels", []string{"string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).YtListChannels(a[0].Str))
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_search_videos", []string{"string", "int"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).YtSearchVideos(a[0].Str, a[1].Int))
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_search_videos_in_channel", []string{"string", "string", "int"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).YtSearchVideosInChannel(a[0].Str, a[1].Str, a[2].Int))
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_get_video", []string{"string"}, "YtVideo", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtVideo", self.(*Host).YtGetVideo(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "yt_upload_video", []string{"string", "string", "string", "StrList", "string", "string"}, "YtVideo", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtVideo", self.(*Host).YtUploadVideo(a[0].Str, a[1].Str, a[2].Str, extractStrList(a[3]), a[4].Str, a[5].Str)), nil
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "yt_update_video", []string{"string", "string", "string", "StrList", "string"}, "YtVideo", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtVideo", self.(*Host).YtUpdateVideo(a[0].Str, a[1].Str, a[2].Str, extractStrList(a[3]), a[4].Str)), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "yt_delete_video", []string{"string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).YtDeleteVideo(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_list_playlists", []string{"string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).YtListPlaylists(a[0].Str))
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_get_playlist", []string{"string"}, "YtPlaylist", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtPlaylist", self.(*Host).YtGetPlaylist(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "yt_create_playlist", []string{"string", "string", "string"}, "YtPlaylist", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtPlaylist", self.(*Host).YtCreatePlaylist(a[0].Str, a[1].Str, a[2].Str)), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "yt_delete_playlist", []string{"string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).YtDeletePlaylist(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_list_playlist_items", []string{"string", "int"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).YtListPlaylistItems(a[0].Str, a[1].Int))
	}); err != nil {
		return err
	}
	if err := m("Google", "yt_add_playlist_item", []string{"string", "string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).YtAddPlaylistItem(a[0].Str, a[1].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_list_comments", []string{"string", "int"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).YtListComments(a[0].Str, a[1].Int))
	}); err != nil {
		return err
	}
	if err := m("Google", "yt_post_comment", []string{"string", "string"}, "string", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(self.(*Host).YtPostComment(a[0].Str, a[1].Str)), nil
	}); err != nil {
		return err
	}

	// YtChannel methods
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtChannel).IDVal), nil }},
		{"title", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtChannel).TitleVal), nil }},
		{"description", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtChannel).DescriptionVal), nil }},
		{"custom_url", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtChannel).CustomURLVal), nil }},
		{"country", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtChannel).CountryVal), nil }},
		{"subscriber_count", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*YtChannel).SubscriberCountVal), nil }},
		{"view_count", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*YtChannel).ViewCountVal), nil }},
		{"video_count", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*YtChannel).VideoCountVal), nil }},
		{"uploads_playlist_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
			return lmcode.Str(s.(*YtChannel).UploadsPlaylistIDVal), nil
		}},
	} {
		if err := mSnapshot("YtChannel", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// YtVideo methods
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).IDVal), nil }},
		{"title", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).TitleVal), nil }},
		{"description", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).DescriptionVal), nil }},
		{"channel_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).ChannelIDVal), nil }},
		{"channel_title", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).ChannelTitleVal), nil }},
		{"published_at", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).PublishedAtVal), nil }},
		{"privacy_status", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).PrivacyStatusVal), nil }},
		{"category_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).CategoryIDVal), nil }},
		{"duration", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtVideo).DurationVal), nil }},
		{"tags", "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return makeStrList(reg, s.(*YtVideo).TagsVal) }},
		{"view_count", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*YtVideo).ViewCountVal), nil }},
		{"like_count", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*YtVideo).LikeCountVal), nil }},
		{"comment_count", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*YtVideo).CommentCountVal), nil }},
	} {
		if err := mSnapshot("YtVideo", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// YtPlaylist methods
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtPlaylist).IDVal), nil }},
		{"title", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtPlaylist).TitleVal), nil }},
		{"description", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtPlaylist).DescriptionVal), nil }},
		{"channel_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtPlaylist).ChannelIDVal), nil }},
		{"privacy_status", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*YtPlaylist).PrivacyStatusVal), nil }},
		{"item_count", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*YtPlaylist).ItemCountVal), nil }},
	} {
		if err := mSnapshot("YtPlaylist", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// --- 4. YouTube Analytics API v2 ---
	if err := mRead("Google", "yt_analytics_report", []string{"string", "string", "string", "StrList", "StrList"}, "YtAnalyticsReport", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtAnalyticsReport", self.(*Host).YtAnalyticsReport(a[0].Str, "", a[1].Str, a[2].Str, extractStrList(a[3]), extractStrList(a[4]))), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "yt_analytics_video_report", []string{"string", "string", "string", "string", "StrList", "StrList"}, "YtAnalyticsReport", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("YtAnalyticsReport", self.(*Host).YtAnalyticsReport(a[0].Str, a[1].Str, a[2].Str, a[3].Str, extractStrList(a[4]), extractStrList(a[5]))), nil
	}); err != nil {
		return err
	}

	// YtAnalyticsReport methods
	if err := mSnapshot("YtAnalyticsReport", "row_count", nil, "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(s.(*YtAnalyticsReport).RowCount()), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "column_headers", nil, "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, s.(*YtAnalyticsReport).ColumnHeadersVal)
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "cell_str", []string{"int", "int"}, "string", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(s.(*YtAnalyticsReport).CellStr(a[0].Int, a[1].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "cell_float", []string{"int", "int"}, "float", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Float(s.(*YtAnalyticsReport).CellFloat(a[0].Int, a[1].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "cell_int", []string{"int", "int"}, "int", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(s.(*YtAnalyticsReport).CellInt(a[0].Int, a[1].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "col_sum", []string{"int"}, "float", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Float(s.(*YtAnalyticsReport).ColSum(a[0].Int)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "row_map", []string{"int"}, "StrMap", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrMap(reg, s.(*YtAnalyticsReport).RowMap(a[0].Int))
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "to_csv", nil, "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(s.(*YtAnalyticsReport).ToCSV()), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("YtAnalyticsReport", "to_json", nil, "Json", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeJson(reg, s.(*YtAnalyticsReport).ToJSONString())
	}); err != nil {
		return err
	}

	// --- 5. Gmail API v1 ---
	if err := mRead("Google", "gmail_profile", nil, "GmailProfile", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GmailProfile", self.(*Host).GmailProfile()), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "gmail_list_labels", nil, "StrList", func(self any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GmailListLabels())
	}); err != nil {
		return err
	}
	if err := mRead("Google", "gmail_search_messages", []string{"string", "int"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GmailSearchMessages(a[0].Str, a[1].Int))
	}); err != nil {
		return err
	}
	if err := mRead("Google", "gmail_get_message", []string{"string"}, "GmailMessage", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GmailMessage", self.(*Host).GmailGetMessage(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mRead("Google", "gmail_get_thread", []string{"string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GmailGetThread(a[0].Str))
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "gmail_send_message", []string{"string", "string", "string"}, "GmailMessage", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GmailMessage", self.(*Host).GmailSendMessage(a[0].Str, a[1].Str, a[2].Str)), nil
	}); err != nil {
		return err
	}
	if err := mPreview("Google", "gmail_modify_labels", []string{"string", "StrList", "StrList"}, "GmailMessage", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Obj("GmailMessage", self.(*Host).GmailModifyLabels(a[0].Str, extractStrList(a[1]), extractStrList(a[2]))), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "gmail_trash_message", []string{"string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).GmailTrashMessage(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := m("Google", "gmail_delete_message", []string{"string"}, "bool", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(self.(*Host).GmailDeleteMessage(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := reg.Method("Google", "gmail_download_attachments", []string{"string", "string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GmailDownloadAttachments(a[0].Str, a[1].Str))
	}, lmcode.Dry(lmcode.Method(func(any, []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, nil)
	}))); err != nil {
		return err
	}
	if err := reg.Method("Google", "gmail_download_attachments_prefixed", []string{"string", "string", "string"}, "StrList", func(self any, a []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, self.(*Host).GmailDownloadAttachmentsPrefixed(a[0].Str, a[1].Str, a[2].Str))
	}, lmcode.Dry(lmcode.Method(func(any, []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, nil)
	}))); err != nil {
		return err
	}

	// GmailProfile getters
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"email_address", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailProfile).EmailAddressVal), nil }},
		{"messages_total", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*GmailProfile).MessagesTotalVal), nil }},
		{"threads_total", "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Int(s.(*GmailProfile).ThreadsTotalVal), nil }},
		{"history_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailProfile).HistoryIDVal), nil }},
	} {
		if err := mSnapshot("GmailProfile", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}

	// GmailMessage getters
	for _, fn := range []struct {
		name string
		ret  string
		fn   lmcode.Method
	}{
		{"id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).IDVal), nil }},
		{"thread_id", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).ThreadIDVal), nil }},
		{"subject", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).SubjectVal), nil }},
		{"from", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).FromVal), nil }},
		{"to", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).ToVal), nil }},
		{"date", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).DateVal), nil }},
		{"date_iso", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).DateISOVal), nil }},
		{"timestamp_slug", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).TimestampSlugVal), nil }},
		{"snippet", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).SnippetVal), nil }},
		{"body_text", "string", func(s any, _ []lmcode.Value) (lmcode.Value, error) { return lmcode.Str(s.(*GmailMessage).BodyTextVal), nil }},
	} {
		if err := mSnapshot("GmailMessage", fn.name, nil, fn.ret, fn.fn); err != nil {
			return err
		}
	}
	if err := mSnapshot("GmailMessage", "label_ids", nil, "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, s.(*GmailMessage).LabelIDsVal)
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "has_label", []string{"string"}, "bool", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Bool(s.(*GmailMessage).HasLabel(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "header", []string{"string"}, "string", func(s any, a []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Str(s.(*GmailMessage).Header(a[0].Str)), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "headers", nil, "StrMap", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrMap(reg, s.(*GmailMessage).HeadersVal)
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "to_json", nil, "Json", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeJson(reg, s.(*GmailMessage).ToJSONString())
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "attachment_count", nil, "int", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return lmcode.Int(s.(*GmailMessage).AttachmentCount()), nil
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "attachment_names", nil, "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, s.(*GmailMessage).AttachmentNamesVal)
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "attachment_ids", nil, "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, s.(*GmailMessage).AttachmentIDsVal)
	}); err != nil {
		return err
	}
	if err := mSnapshot("GmailMessage", "attachment_mimes", nil, "StrList", func(s any, _ []lmcode.Value) (lmcode.Value, error) {
		return makeStrList(reg, s.(*GmailMessage).AttachmentMimesVal)
	}); err != nil {
		return err
	}

	return nil
}

func zeroHostObject(typeName string) any {
	switch typeName {
	case "GaAccount":
		return &GaAccount{}
	case "GaProperty":
		return &GaProperty{}
	case "GaDataStream":
		return &GaDataStream{}
	case "GaCustomDimension":
		return &GaCustomDimension{}
	case "GaReport":
		return &GaReport{}
	case "YtChannel":
		return &YtChannel{}
	case "YtVideo":
		return &YtVideo{}
	case "YtPlaylist":
		return &YtPlaylist{}
	case "YtAnalyticsReport":
		return &YtAnalyticsReport{}
	case "GmailProfile":
		return &GmailProfile{}
	case "GmailMessage":
		return &GmailMessage{}
	default:
		return nil
	}
}
