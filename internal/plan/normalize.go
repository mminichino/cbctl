package plan

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
)

// IgnoredFields are never compared: Capella computes them.
var IgnoredFields = map[string]bool{
	"id": true, "audit": true, "stats": true, "connectionString": true,
	"currentState": true, "expressScaling": true, "appServiceId": true, "etag": true,
}

// PrivilegeAliases maps the short privilege names to the ones Capella returns.
var PrivilegeAliases = map[string]string{"read": "data_reader", "write": "data_writer"}

var caseInsensitivePaths = map[string]bool{"support.plan": true}

// GroupKey identifies a service group by its sorted services set, e.g. "data,index,query".
func GroupKey(group Obj) string {
	list, _ := AsList(group["services"])
	services := make([]string, 0, len(list))
	for _, s := range list {
		if str, ok := s.(string); ok {
			services = append(services, str)
		}
	}
	sort.Strings(services)
	return strings.Join(services, ",")
}

// CanonPrivileges applies the aliases, removes duplicates and sorts.
func CanonPrivileges(privs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(privs))
	for _, p := range privs {
		if alias, ok := PrivilegeAliases[p]; ok {
			p = alias
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// CanonFlush reads flushEnabled, falling back to the deprecated flush only when flushEnabled is absent.
func CanonFlush(body Obj) (value bool, ok bool) {
	if v, has := body["flushEnabled"]; has {
		return truthy(v), true
	}
	if v, has := body["flush"]; has {
		return truthy(v), true
	}
	return false, false
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	}
	if i, ok := AsInt(v); ok {
		return i != 0
	}
	if f, ok := toFloat(v); ok {
		return f != 0
	}
	return true
}

func stringList(v any) ([]string, bool) {
	list, ok := AsList(v)
	if !ok {
		return nil, false
	}
	out := make([]string, len(list))
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

func anyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// CanonValue puts a value in the form used for comparison: "support.plan" is lower-cased and
// paths ending "services" or "privileges" are sorted (privileges also aliased and de-duplicated).
func CanonValue(path string, v any) any {
	if caseInsensitivePaths[path] {
		if s, ok := v.(string); ok {
			return strings.ToLower(s)
		}
	}
	if strings.HasSuffix(path, "services") {
		if list, ok := stringList(v); ok {
			sorted := append([]string(nil), list...)
			sort.Strings(sorted)
			return anyStrings(sorted)
		}
	}
	if strings.HasSuffix(path, "privileges") {
		if list, ok := stringList(v); ok {
			return anyStrings(CanonPrivileges(list))
		}
	}
	return v
}

// FieldChanges lists the differences for fields present in desired only: a field the user omitted
// is never compared. Keys in skip (top level only) and IgnoredFields (at every level) are skipped.
// Leaves are compared after CanonValue; integral numbers compare as int64. Changes come out in
// sorted key order.
func FieldChanges(desired, observed any, prefix string, skip map[string]bool) []Change {
	if d, ok := AsObj(desired); ok {
		obs, _ := AsObj(observed)
		keys := make([]string, 0, len(d))
		for k := range d {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var changes []Change
		for _, k := range keys {
			if skip[k] || IgnoredFields[k] {
				continue
			}
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			changes = append(changes, FieldChanges(d[k], obs[k], path, nil)...)
		}
		return changes
	}
	if !ValuesEqual(CanonValue(prefix, desired), CanonValue(prefix, observed)) {
		return []Change{{Path: prefix, Before: observed, After: desired}}
	}
	return nil
}

// DeepMerge overlays desired on observed (architecture doc 6.4): maps merge key by key, any other
// overlay replaces the base unless it is nil, and fields the overlay omits keep their current values.
func DeepMerge(base, overlay any) any {
	b, bok := AsObj(base)
	o, ook := AsObj(overlay)
	if bok && ook {
		out := make(Obj, len(b)+len(o))
		for k, v := range b {
			out[k] = v
		}
		for k, v := range o {
			if bv, in := b[k]; in {
				out[k] = DeepMerge(bv, v)
			} else {
				out[k] = v
			}
		}
		return out
	}
	if overlay != nil {
		return overlay
	}
	return base
}

// AsObj returns v as a mapping when it is one with string keys.
func AsObj(v any) (Obj, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	out := make(Obj, rv.Len())
	for _, k := range rv.MapKeys() {
		out[k.String()] = rv.MapIndex(k).Interface()
	}
	return out, true
}

// AsList returns v as a []any when it is any slice or array other than bytes.
func AsList(v any) ([]any, bool) {
	if l, ok := v.([]any); ok {
		return l, true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, false
	}
	if rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

// AsString returns v when it is a string.
func AsString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// AsBool returns v when it is a bool.
func AsBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

// AsInt returns v as an int64 when it is an integer or an integral float.
func AsInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		if x > math.MaxInt64 {
			return 0, false
		}
		return int64(x), true
	case float32:
		return floatToInt(float64(x))
	case float64:
		return floatToInt(x)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil {
			return floatToInt(f)
		}
	}
	return 0, false
}

func floatToInt(f float64) (int64, bool) {
	if f != math.Trunc(f) || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) >= 1<<63 {
		return 0, false
	}
	return int64(f), true
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	if i, ok := AsInt(v); ok {
		return float64(i), true
	}
	return 0, false
}

func isNumber(v any) bool {
	_, ok := toFloat(v)
	return ok
}

// ValuesEqual compares two decoded values structurally. Numbers compare as int64 when both are
// integral and as float64 otherwise; lists and mappings compare element by element regardless of
// their concrete Go types.
func ValuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return isNil(a) && isNil(b)
	}
	if isNumber(a) && isNumber(b) {
		ai, aok := AsInt(a)
		bi, bok := AsInt(b)
		if aok && bok {
			return ai == bi
		}
		af, _ := toFloat(a)
		bf, _ := toFloat(b)
		return af == bf
	}
	if am, ok := AsObj(a); ok {
		bm, ok := AsObj(b)
		if !ok || len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			bv, in := bm[k]
			if !in || !ValuesEqual(av, bv) {
				return false
			}
		}
		return true
	}
	if al, ok := AsList(a); ok {
		bl, ok := AsList(b)
		if !ok || len(al) != len(bl) {
			return false
		}
		for i := range al {
			if !ValuesEqual(al[i], bl[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}
