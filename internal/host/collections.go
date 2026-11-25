// SPDX-License-Identifier: MIT

package host

import (
	"sort"

	"github.com/lmctl-ai/lmcode-go"
	"github.com/lmctl-ai/lmgoogle/internal/membudget"
	"github.com/lmctl-ai/lmgoogle/internal/safety"
)

func makeStrList(reg *lmcode.Registry, items []string) (lmcode.Value, error) {
	var totalBytes int64 = 64
	for _, s := range items {
		totalBytes += int64(len(s)) + 16
	}
	if err := membudget.Charge(totalBytes); err != nil {
		return lmcode.None(), err
	}
	mk := reg.FunctionBySignature("fn str_list() -> StrList")
	add := reg.MethodBySignature("method StrList.add(string) -> StrList")
	val, err := mk.Fn(nil)
	if err != nil {
		return lmcode.None(), err
	}
	for _, item := range items {
		val, err = add.Fn(val.Obj, []lmcode.Value{lmcode.Str(safety.RedactSecrets(item))})
		if err != nil {
			return lmcode.None(), err
		}
	}
	return val, nil
}

func makeStrMap(reg *lmcode.Registry, m map[string]string) (lmcode.Value, error) {
	var totalBytes int64 = 64
	keys := make([]string, 0, len(m))
	for k, v := range m {
		keys = append(keys, k)
		totalBytes += int64(len(k)+len(v)) + 32
	}
	sort.Strings(keys)
	if err := membudget.Charge(totalBytes); err != nil {
		return lmcode.None(), err
	}
	mk := reg.FunctionBySignature("fn str_map() -> StrMap")
	set := reg.MethodBySignature("method StrMap.set(string, string) -> StrMap")
	val, err := mk.Fn(nil)
	if err != nil {
		return lmcode.None(), err
	}
	for _, k := range keys {
		val, err = set.Fn(val.Obj, []lmcode.Value{
			lmcode.Str(k),
			lmcode.Str(safety.RedactSecrets(m[k])),
		})
		if err != nil {
			return lmcode.None(), err
		}
	}
	return val, nil
}

func makeJson(reg *lmcode.Registry, raw string) (lmcode.Value, error) {
	redacted := safety.RedactSecrets(raw)
	if err := membudget.Charge(int64(len(redacted)*2 + 64)); err != nil {
		return lmcode.None(), err
	}
	parse := reg.FunctionBySignature("fn json_parse(string) -> Json")
	return parse.Fn([]lmcode.Value{lmcode.Str(redacted)})
}

func extractStrList(v lmcode.Value) []string {
	if v.Kind != lmcode.KindObj || v.Obj == nil {
		return nil
	}
	if sl, ok := v.Obj.(*lmcode.StrList); ok && sl != nil {
		return sl.Values()
	}
	return nil
}
