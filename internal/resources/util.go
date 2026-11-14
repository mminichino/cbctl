package resources

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/plan"
)

// Obj is a JSON-compatible mapping; it is the same type as plan.Obj and capellafile.Obj.
type Obj = plan.Obj

// norm converts decoded JSON into the engine's number convention: integral numbers become int64,
// other numbers stay float64. Maps and lists are converted in place.
func norm(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = norm(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = norm(e)
		}
		return x
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<62 {
			return int64(x)
		}
	}
	return v
}

func normObj(o Obj) Obj {
	if o == nil {
		return Obj{}
	}
	norm(o)
	return o
}

// strOf returns o[k] when it is a string, else "".
func strOf(o Obj, k string) string {
	s, _ := o[k].(string)
	return s
}

// clusterContentsUnavailable is true when Capella rejects reads of buckets, credentials, and
// scopes (turned off / turning off / turning on). Observe must return empty rather than fail the plan.
func clusterContentsUnavailable(cluster *plan.Action) bool {
	if cluster == nil {
		return false
	}
	state := ""
	if cluster.Observed != nil {
		state = strOf(cluster.Observed.Body, "currentState")
	}
	if state == "" {
		if s, ok := cluster.Data["state"].(string); ok {
			state = s
		}
	}
	return OffStates[state] || state == "turningOn"
}

// objOf returns v as a mapping, or nil.
func objOf(v any) Obj {
	o, _ := plan.AsObj(v)
	return o
}

// listOf returns v as a list, or nil.
func listOf(v any) []any {
	l, _ := plan.AsList(v)
	return l
}

// objsOf returns the mapping elements of a list.
func objsOf(v any) []Obj {
	var out []Obj
	for _, e := range listOf(v) {
		if o, ok := plan.AsObj(e); ok {
			out = append(out, o)
		}
	}
	return out
}

// stringsOf returns the string elements of a list.
func stringsOf(v any) []string {
	var out []string
	for _, e := range listOf(v) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// intOf returns v as an int64, truncating non-integral numbers; 0 when v is not a number.
func intOf(v any) int64 {
	if i, ok := plan.AsInt(v); ok {
		return i
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	}
	return 0
}

func isNumber(v any) bool {
	if _, ok := plan.AsInt(v); ok {
		return true
	}
	switch v.(type) {
	case float64, float32:
		return true
	}
	return false
}

// truthy follows Python truthiness for the value kinds a document holds.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	if isNumber(v) {
		return intOf(v) != 0 || math.Abs(toFloat(v)) > 0
	}
	return true
}

func toFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return float64(intOf(v))
}

// pyStr formats a value the way the capellactl plan text does: None for a missing value.
func pyStr(v any) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprint(v)
}

func has(o Obj, k string) bool {
	_, ok := o[k]
	return ok
}

func copyObj(o Obj) Obj {
	out := make(Obj, len(o))
	for k, v := range o {
		out[k] = v
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func plural(n int64, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// commas formats n with thousands separators.
func commas(n int64) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// projectID returns the id of the project the action sits under.
func projectID(a *plan.Action) string {
	if a == nil {
		return ""
	}
	if a.Kind == "project" {
		return a.ID()
	}
	if p := a.Ancestor("project"); p != nil {
		return p.ID()
	}
	return ""
}

// newAction builds an action of the given kind with its desired state, observed resource and parent.
func newAction(rec plan.Reconciler, kind, key string, t plan.ActionType, desired any, observed *plan.Observed, parent *plan.Action) *plan.Action {
	a := plan.NewAction(rec, kind, key, t)
	a.Desired, a.Observed, a.Parent = desired, observed, parent
	return a
}

// needIDs reports the missing-id problem of an action whose parent does not exist in Capella yet.
func needIDs(what string, ids ...string) error {
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("%s: the parent resource has no id yet", what)
		}
	}
	return nil
}
