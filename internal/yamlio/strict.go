package yamlio

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// checkShape walks the JSON document against the type v points to. It reports fields without a
// json tag and, unless unknownOnly is set, values whose JSON kind does not fit the Go type.
// Matching is by exact name (encoding/json would also accept other letter cases).
func checkShape(js []byte, lines LineIndex, v any, o *options, unknownOnly bool) ([]*FieldError, error) {
	t := reflect.TypeOf(v)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(v).IsNil() {
		return nil, fmt.Errorf("yamlio: decode target must be a non-nil pointer, got %T", v)
	}
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("yamlio: internal JSON: %w", err)
	}
	w := &walker{lines: lines, o: o, unknownOnly: unknownOnly}
	w.walk(doc, t.Elem(), "")
	return w.errs, nil
}

type walker struct {
	lines       LineIndex
	o           *options
	unknownOnly bool
	errs        []*FieldError
}

func (w *walker) add(ptr, msg, suggest string) {
	pos := w.lines.Of(ptr)
	w.errs = append(w.errs, &FieldError{Source: w.o.source, Pointer: ptr, Line: pos.Line, Column: pos.Column, Msg: msg, Suggest: suggest})
}

var (
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// opaque reports types that decode themselves or hold anything.
func opaque(t reflect.Type) bool {
	if t.Kind() == reflect.Interface {
		return true
	}
	pt := reflect.PointerTo(t)
	return pt.Implements(jsonUnmarshalerType) || pt.Implements(textUnmarshalerType)
}

func kindName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case bool:
		return "a boolean"
	}
	return "null"
}

func (w *walker) mismatch(ptr string, v any, want string) {
	if !w.unknownOnly {
		w.add(ptr, fmt.Sprintf("expected %s, got %s", want, kindName(v)), "")
	}
}

func (w *walker) walk(v any, t reflect.Type, ptr string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if v == nil || opaque(t) {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			w.mismatch(ptr, v, "an object")
			return
		}
		fields := fieldsOf(t)
		for _, k := range sortedKeys(obj) {
			child := ptr + "/" + escapePointer(k)
			f, known := fields.byName[k]
			if !known {
				w.add(child, "unknown field", ClosestKey(k, fields.names))
				continue
			}
			if !f.quoted {
				w.walk(obj[k], f.typ, child)
			}
		}
	case reflect.Map:
		obj, ok := v.(map[string]any)
		if !ok {
			w.mismatch(ptr, v, "an object")
			return
		}
		if t.Key().Kind() != reflect.String {
			return
		}
		for _, k := range sortedKeys(obj) {
			w.walk(obj[k], t.Elem(), ptr+"/"+escapePointer(k))
		}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			if _, ok := v.(string); !ok {
				w.mismatch(ptr, v, "a string")
			}
			return
		}
		list, ok := v.([]any)
		if !ok {
			w.mismatch(ptr, v, "a list")
			return
		}
		for i, item := range list {
			w.walk(item, t.Elem(), ptr+"/"+strconv.Itoa(i))
		}
	case reflect.String:
		if _, ok := v.(string); !ok {
			w.mismatch(ptr, v, "a string")
		}
	case reflect.Bool:
		if _, ok := v.(bool); !ok {
			w.mismatch(ptr, v, "a boolean")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := v.(json.Number)
		if !ok {
			w.mismatch(ptr, v, "an integer")
			return
		}
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil || reflect.Zero(t).OverflowInt(i) {
			w.mismatch(ptr, v, "an integer that fits "+t.String())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := v.(json.Number)
		if !ok {
			w.mismatch(ptr, v, "a non-negative integer")
			return
		}
		u, err := strconv.ParseUint(n.String(), 10, 64)
		if err != nil || reflect.Zero(t).OverflowUint(u) {
			w.mismatch(ptr, v, "a non-negative integer that fits "+t.String())
		}
	case reflect.Float32, reflect.Float64:
		if _, ok := v.(json.Number); !ok {
			w.mismatch(ptr, v, "a number")
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type fieldInfo struct {
	typ    reflect.Type
	quoted bool // `json:",string"`: the value is a string holding the real value
}

type structFields struct {
	byName map[string]fieldInfo
	names  []string
}

var fieldCache sync.Map // reflect.Type → *structFields

// fieldsOf lists the JSON names of struct type t, flattening embedded structs like encoding/json.
func fieldsOf(t reflect.Type) *structFields {
	if c, ok := fieldCache.Load(t); ok {
		return c.(*structFields)
	}
	sf := &structFields{byName: map[string]fieldInfo{}}
	collectFields(t, sf, map[reflect.Type]bool{})
	sort.Strings(sf.names)
	fieldCache.Store(t, sf)
	return sf
}

func collectFields(t reflect.Type, sf *structFields, visiting map[reflect.Type]bool) {
	if visiting[t] {
		return
	}
	visiting[t] = true
	defer delete(visiting, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, rest, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectFields(ft, sf, visiting)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if _, dup := sf.byName[name]; dup {
			continue
		}
		sf.byName[name] = fieldInfo{typ: f.Type, quoted: strings.Contains(","+rest+",", ",string,")}
		sf.names = append(sf.names, name)
	}
}

// ClosestKey returns the allowed key closest to key within edit distance 2 (case-insensitive),
// or "" when none is close.
func ClosestKey(key string, allowed []string) string {
	best, bestD := "", 3
	for _, a := range allowed {
		d := Levenshtein(strings.ToLower(key), strings.ToLower(a))
		if d < bestD {
			best, bestD = a, d
		}
	}
	return best
}

// Levenshtein is the edit distance between a and b, counted in runes.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
