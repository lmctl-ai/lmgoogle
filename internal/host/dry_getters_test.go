// SPDX-License-Identifier: MIT

package host

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lmctl-ai/lmcode-go"
)

func TestSnapshotGettersDryParity(t *testing.T) {
	samples := map[string]any{
		"GaAccount":         &GaAccount{},
		"GaProperty":        &GaProperty{},
		"GaDataStream":      &GaDataStream{},
		"GaCustomDimension": &GaCustomDimension{},
		"GaReport": &GaReport{
			DimensionHeadersVal: []string{"country"},
			MetricHeadersVal:    []string{"activeUsers"},
			DimensionRowsVal:    [][]string{{"US"}},
			MetricRowsVal:       [][]string{{"42"}},
		},
		"YtChannel":  &YtChannel{},
		"YtVideo":    &YtVideo{},
		"YtPlaylist": &YtPlaylist{},
		"YtAnalyticsReport": &YtAnalyticsReport{
			ColumnHeadersVal: []string{"day", "views"},
			RowsVal:          [][]string{{"2026-11-10", "100"}},
		},
		"GmailProfile": &GmailProfile{},
		"GmailMessage": &GmailMessage{
			LabelIDsVal:        []string{"INBOX"},
			HeadersVal:         map[string]string{"Subject": "Hello"},
			AttachmentNamesVal: []string{"file.txt"},
			AttachmentIDsVal:   []string{"att_1"},
			AttachmentMimesVal: []string{"text/plain"},
		},
	}
	for name, sample := range samples {
		if name == "GaReport" || name == "YtAnalyticsReport" || name == "GmailMessage" {
			continue
		}
		populateSnapshot(sample)
	}

	reg := lmcode.NewRegistry()
	h, err := NewHost(false)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := Register(reg, h); err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, signature := range reg.Signatures() {
		if !strings.HasPrefix(signature, "method ") {
			continue
		}
		body := strings.TrimPrefix(signature, "method ")
		dot := strings.IndexByte(body, '.')
		if dot < 0 {
			continue
		}
		typeName := body[:dot]
		self, ok := samples[typeName]
		if !ok {
			continue
		}
		entry := reg.MethodBySignature(signature)
		if entry == nil {
			t.Fatalf("missing method entry %s", signature)
		}
		if entry.DryFn == nil {
			t.Fatalf("snapshot getter lacks Dry callback: %s", signature)
		}
		var args []lmcode.Value
		for _, p := range entry.Sig.Params {
			switch p {
			case "string":
				args = append(args, lmcode.Str("k"))
			case "int":
				args = append(args, lmcode.Int(0))
			}
		}
		realValue, realErr := entry.Fn(self, args)
		dryValue, dryErr := entry.DryFn(self, args)
		if (realErr == nil) != (dryErr == nil) {
			t.Fatalf("%s error parity: real=%v dry=%v", signature, realErr, dryErr)
		}
		if realErr != nil {
			continue
		}
		if !sameValue(realValue, dryValue) {
			t.Fatalf("%s dry/real mismatch: real=%#v dry=%#v", signature, realValue, dryValue)
		}
		checked++
	}
	if checked < 60 {
		t.Fatalf("checked %d snapshot getters, want at least 60", checked)
	}
}

func populateSnapshot(value any) {
	rv := reflect.ValueOf(value).Elem()
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Field(i)
		if !field.CanSet() {
			continue
		}
		switch field.Kind() {
		case reflect.String:
			field.SetString("v")
		case reflect.Int64:
			field.SetInt(7)
		case reflect.Bool:
			field.SetBool(false)
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.String {
				slice := reflect.MakeSlice(field.Type(), 1, 1)
				slice.Index(0).SetString("v")
				field.Set(slice)
			}
		}
	}
}

func sameValue(a, b lmcode.Value) bool {
	if a.Kind != b.Kind || a.Type != b.Type {
		return false
	}
	switch a.Kind {
	case lmcode.KindVoid:
		return true
	case lmcode.KindInt:
		return a.Int == b.Int
	case lmcode.KindFloat:
		return a.Float == b.Float
	case lmcode.KindStr:
		return a.Str == b.Str
	case lmcode.KindBool:
		return a.Bool == b.Bool
	default:
		if ja, ok := a.Obj.(*lmcode.JSON); ok {
			jb, okb := b.Obj.(*lmcode.JSON)
			return okb && ja.ToJSON() == jb.ToJSON()
		}
		return reflect.DeepEqual(a.Obj, b.Obj)
	}
}
